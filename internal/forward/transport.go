// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

// Package forward 提供「无侵入」的 HTTP/1.x 保真转发。
//
// 动机:Go 的 http.Transport 在写出请求时会把头按字母排序、规范化头名大小写、
// 强制 Host 在最前、并按需注入 User-Agent / Accept-Encoding —— 这些都是反爬 / 防篡改
// 系统会校验的指纹特征。stdlib 没有任何 hook 能保留原始顺序与大小写,故本包绕开
// http.Transport 的请求序列化,直接在自管连接上按「最终线缆头序列」(由 flow 层经 ctx
// 传入,保留客户端原始顺序+大小写)写线,从而做到 HTTP/1.x 请求无侵入透传。
//
// 适用边界:仅处理 HTTP/1.x。下列情形自动回退到注入的标准 http.RoundTripper(Fallback):
//   - ctx 中没有保真头序列(h2 入站、头块过大等);
//   - 目标经 ALPN 只会 h2(此时对 http/1.1 的 TLS 握手会失败,按回退处理);
//   - CONNECT / Upgrade 等非普通请求;
//   - 连接/握手等「尚未发出请求」的前置失败。
package forward

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mintfog/sniffy/internal/flow"
)

// Config 配置保真转发器。零值不可用,请用 New。
type Config struct {
	// Fallback 是无法保真时回退使用的标准 RoundTripper(通常为 *http.Transport)。必填。
	Fallback http.RoundTripper
	// Proxy 返回某请求应使用的上游代理(nil 表示直连)。可为 nil(恒直连)。
	Proxy func(*http.Request) (*url.URL, error)
	// TLSClientConfig 用于 https 目标(本包会克隆并强制 ALPN 为 http/1.1)。可为 nil。
	TLSClientConfig *tls.Config

	DialTimeout       time.Duration // 建连超时
	TLSTimeout        time.Duration // TLS 握手超时
	RespHeaderTimeout time.Duration // 等待响应头超时(读到头后清除,以支持流式 body)
	IdleConnTimeout   time.Duration // 空闲连接最长存活
	MaxIdlePerHost    int           // 每个 (scheme,host,proxy) 的最大空闲连接数

	// MaxFaithfulBody 是保真路径允许的最大请求体字节数。超过则回退到标准 Transport。
	// 原因:本包顺序「先写完整请求体、再读响应」,面对超大上传 + 服务端提前响应(如 413)
	// 时存在流控死锁风险;标准 Transport 以并发读写规避之。大体积上传通常不涉及头指纹,
	// 故以体积阈值换取健壮性。<=0 时取默认 1MiB。
	MaxFaithfulBody int

	// Disabled 为 true 时一律走 Fallback(运维兜底开关)。
	Disabled bool
}

// Transport 是保真 HTTP/1.x RoundTripper,内含简单的 keep-alive 连接池。
type Transport struct {
	cfg Config

	mu   sync.Mutex
	idle map[string][]*persistConn
}

// New 构造保真转发器并填充缺省超时。
func New(cfg Config) *Transport {
	if cfg.DialTimeout <= 0 {
		cfg.DialTimeout = 30 * time.Second
	}
	if cfg.TLSTimeout <= 0 {
		cfg.TLSTimeout = 30 * time.Second
	}
	if cfg.RespHeaderTimeout <= 0 {
		cfg.RespHeaderTimeout = 30 * time.Second
	}
	if cfg.IdleConnTimeout <= 0 {
		cfg.IdleConnTimeout = 90 * time.Second
	}
	if cfg.MaxIdlePerHost <= 0 {
		cfg.MaxIdlePerHost = 64
	}
	if cfg.MaxFaithfulBody <= 0 {
		cfg.MaxFaithfulBody = 1 << 20 // 1MiB
	}
	return &Transport{cfg: cfg, idle: make(map[string][]*persistConn)}
}

// ResolveProxy 返回对该请求会选用的上游代理(nil=直连),供引擎层切换代理与自检使用。
func (t *Transport) ResolveProxy(req *http.Request) (*url.URL, error) {
	if t.cfg.Proxy == nil {
		return nil, nil
	}
	return t.cfg.Proxy(req)
}

// persistConn 是一条可复用的上游连接。
type persistConn struct {
	conn   net.Conn
	br     *bufio.Reader
	key    string
	idleAt time.Time
	// broken 标记连接已不可复用。可能由读写出错的读取方与 ctx 取消守护(connGuard)
	// 并发置位,故用原子量。
	broken    atomic.Bool
	closeOnce sync.Once
}

// close 关闭底层连接;可并发、可重复调用(connGuard 与读取方可能同时触发)。
func (pc *persistConn) close() {
	if pc == nil {
		return
	}
	pc.closeOnce.Do(func() {
		if pc.conn != nil {
			_ = pc.conn.Close()
		}
	})
}

// connGuard 在请求 ctx 取消时关闭其守护的连接,从而打断裸 conn 上阻塞的写 / 读
// (net/http 的 Transport 亦如此)。否则 http.Client.Timeout、客户端断开都无法中断
// 上游 I/O,导致读取该连接的 goroutine 与连接本身永久泄漏。
type connGuard struct {
	pc       *persistConn
	released atomic.Bool
	stop     chan struct{}
}

// newConnGuard 启动守护;ctx 不可取消时为轻量空守护(不起 goroutine)。
func newConnGuard(ctx context.Context, pc *persistConn) *connGuard {
	g := &connGuard{pc: pc, stop: make(chan struct{})}
	if ctx == nil || ctx.Done() == nil {
		return g
	}
	go func() {
		select {
		case <-ctx.Done():
			if g.released.CompareAndSwap(false, true) {
				pc.broken.Store(true)
				pc.close() // 打断阻塞中的 conn.Read / Write
			}
		case <-g.stop:
		}
	}()
	return g
}

// disarm 解除守护(连接已交还调用方或即将关闭)。返回 false 表示守护已抢先因 ctx
// 取消而关连接 —— 此时连接不可再复用。每个守护只调用一次。
func (g *connGuard) disarm() bool {
	won := g.released.CompareAndSwap(false, true)
	close(g.stop)
	return won
}

// RoundTrip 实现 http.RoundTripper。
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	ordered, ok := flow.OrderedHeadersFrom(req.Context())
	if t.cfg.Disabled || !ok || req.Method == http.MethodConnect || isUpgrade(req) {
		return t.fallback(req, nil)
	}
	// 读出 body(已是内存中的 identity 字节),以便回退 / 重试时可重发。
	var body []byte
	if req.Body != nil {
		body, _ = io.ReadAll(req.Body)
		_ = req.Body.Close()
	}

	if len(body) > t.cfg.MaxFaithfulBody {
		return t.fallback(req, body) // 超大请求体:交并发读写的标准 Transport 以防流控死锁
	}

	var proxyURL *url.URL
	if t.cfg.Proxy != nil {
		proxyURL, _ = t.cfg.Proxy(req)
	}
	absForm := proxyURL != nil && req.URL.Scheme == "http"

	// 最多两次:首次可能取到已被对端关闭的空闲连接,失败后用新连接重试一次。
	for attempt := 0; attempt < 2; attempt++ {
		pc, fromIdle, h2, err := t.acquire(req.Context(), req.URL, proxyURL)
		if err != nil {
			return t.fallback(req, body) // 建连/握手失败:请求尚未发出,可安全回退
		}
		if h2 {
			pc.close()
			return t.fallback(req, body) // 目标走 h2:本包只说 h1,交回退
		}

		// 裸 conn 不感知 ctx;装守护,在 ctx 取消(含 http.Client.Timeout、客户端断开)时
		// 关连接以打断阻塞的写 / 读头 / 读体。读头成功后守护移交响应体,继续覆盖读体阶段。
		guard := newConnGuard(req.Context(), pc)

		if err := writeFaithfulRequest(pc, req, ordered, body, absForm); err != nil {
			guard.disarm()
			pc.broken.Store(true)
			pc.close()
			if cerr := req.Context().Err(); cerr != nil {
				return nil, cerr // ctx 已取消:勿在死 ctx 上重试 / 回退
			}
			if fromIdle {
				continue // 空闲连接竞态:换新连接重试
			}
			return t.fallback(req, body) // 新连接写失败:对端未完整收到,回退重发一次
		}

		resp, err := readResponse(pc, req, t.cfg.RespHeaderTimeout)
		if err != nil {
			guard.disarm()
			pc.broken.Store(true)
			pc.close()
			if cerr := req.Context().Err(); cerr != nil {
				return nil, cerr // ctx 已取消:勿重试 / 回退
			}
			// 仅幂等方法可在「整请求已发出却未得响应」时安全重放;非幂等(POST 等)
			// 一旦完整发出就可能已被服务端处理,重发会造成重复提交,故直接返回错误。
			if fromIdle && idempotentMethod(req.Method) {
				continue // 空闲连接竞态:幂等请求换新连接重试
			}
			return nil, err
		}

		t.wrapBody(resp, pc, guard)
		return resp, nil
	}
	return t.fallback(req, body)
}

// fallback 重置 body 后委托标准 RoundTripper。
func (t *Transport) fallback(req *http.Request, body []byte) (*http.Response, error) {
	if body != nil {
		req.Body = io.NopCloser(bytes.NewReader(body))
		req.ContentLength = int64(len(body))
	}
	return t.cfg.Fallback.RoundTrip(req)
}

// acquire 取一条可用连接:优先复用空闲连接,否则新建。
// 返回 (连接, 是否来自空闲池, 是否协商成 h2, err)。
func (t *Transport) acquire(ctx context.Context, u *url.URL, proxyURL *url.URL) (*persistConn, bool, bool, error) {
	key := connKey(u, proxyURL)
	if pc := t.getIdle(key); pc != nil {
		return pc, true, false, nil
	}
	pc, h2, err := t.dial(ctx, u, proxyURL, key)
	return pc, false, h2, err
}

// dial 新建一条到目标的连接(按需经上游代理、按需 TLS)。
func (t *Transport) dial(ctx context.Context, u *url.URL, proxyURL *url.URL, key string) (*persistConn, bool, error) {
	d := &net.Dialer{Timeout: t.cfg.DialTimeout}
	target := canonicalAddr(u)

	if proxyURL == nil {
		raw, err := d.DialContext(ctx, "tcp", target)
		if err != nil {
			return nil, false, err
		}
		if u.Scheme == "https" {
			tc, h2, err := t.tlsHandshake(ctx, raw, hostname(u))
			if err != nil || h2 {
				return nil, h2, err
			}
			return newPersistConn(tc, key), false, nil
		}
		return newPersistConn(raw, key), false, nil
	}

	// 经上游代理。
	praw, err := d.DialContext(ctx, "tcp", canonicalAddr(proxyURL))
	if err != nil {
		return nil, false, err
	}
	if u.Scheme == "http" {
		// 明文 http:用绝对形式请求行,直接走代理,无需 CONNECT / TLS。
		return newPersistConn(praw, key), false, nil
	}
	// https:先 CONNECT 隧道,再在隧道上 TLS。
	if err := proxyConnect(praw, target, proxyURL, t.cfg.DialTimeout); err != nil {
		_ = praw.Close()
		return nil, false, err
	}
	tc, h2, err := t.tlsHandshake(ctx, praw, hostname(u))
	if err != nil || h2 {
		return nil, h2, err
	}
	return newPersistConn(tc, key), false, nil
}

// tlsHandshake 在原始连接上完成 TLS 握手,只通告 http/1.1。
// 若对端协商成 h2(仅当其忽略 ALPN 时)则返回 h2=true,交由上层回退。
// 对 h2-only 源站,只通告 http/1.1 的握手会失败,同样按回退处理。
func (t *Transport) tlsHandshake(ctx context.Context, raw net.Conn, serverName string) (net.Conn, bool, error) {
	var cfg *tls.Config
	if t.cfg.TLSClientConfig != nil {
		cfg = t.cfg.TLSClientConfig.Clone()
	} else {
		cfg = &tls.Config{}
	}
	cfg.ServerName = serverName
	cfg.NextProtos = []string{"http/1.1"}

	tc := tls.Client(raw, cfg)
	_ = tc.SetDeadline(time.Now().Add(t.cfg.TLSTimeout))
	if err := tc.HandshakeContext(ctx); err != nil {
		_ = raw.Close()
		return nil, false, err
	}
	_ = tc.SetDeadline(time.Time{})
	if tc.ConnectionState().NegotiatedProtocol == "h2" {
		_ = tc.Close()
		return nil, true, nil
	}
	return tc, false, nil
}

func newPersistConn(conn net.Conn, key string) *persistConn {
	// 64KB 读缓冲:容纳常见(乃至偏大)的响应头块,使 readResponse 能在 http.ReadResponse
	// 之前 Peek 出完整头块、抓取响应头原始顺序/大小写(写回客户端时保真)。
	return &persistConn{conn: conn, br: bufio.NewReaderSize(conn, 64*1024), key: key}
}

// writeFaithfulRequest 按 ordered 的顺序/大小写把请求写线;帧头(Content-Length /
// Transfer-Encoding)由本函数据 body 自洽化,避免 ordered 与实际 body 不一致。
func writeFaithfulRequest(pc *persistConn, req *http.Request, ordered [][2]string, body []byte, absForm bool) error {
	w := bufio.NewWriter(pc.conn)

	target := req.URL.RequestURI() // origin-form:path?query,RawQuery 逐字 → 参数顺序保真
	if absForm {
		target = req.URL.String() // 明文 http 经代理:绝对形式
	}
	// 保真起见沿用客户端的 HTTP 版本(仅 1.0/1.1;其余按 1.1)。1.0 的响应不会进连接池
	//(wrapBody 的复用判定走 resp.ProtoAtLeast(1,1)),故对连接复用自洽、安全。
	proto := "HTTP/1.1"
	if req.ProtoMajor == 1 && req.ProtoMinor == 0 {
		proto = "HTTP/1.0"
	}
	if _, err := fmt.Fprintf(w, "%s %s %s\r\n", req.Method, target, proto); err != nil {
		return err
	}

	for _, kv := range normalizeFraming(ordered, len(body)) {
		if _, err := w.WriteString(kv[0]); err != nil {
			return err
		}
		if _, err := w.WriteString(": "); err != nil {
			return err
		}
		if _, err := w.WriteString(kv[1]); err != nil {
			return err
		}
		if _, err := w.WriteString("\r\n"); err != nil {
			return err
		}
	}
	if _, err := w.WriteString("\r\n"); err != nil {
		return err
	}
	if len(body) > 0 {
		if _, err := w.Write(body); err != nil {
			return err
		}
	}
	return w.Flush()
}

// normalizeFraming 保证 ordered 的帧头与 body 自洽:
//   - 正常情形(恰有一个值正确的 Content-Length 且无 Transfer-Encoding)原样返回,保留位置;
//   - 否则剔除所有 Content-Length / Transfer-Encoding,并在 body 非空时于末尾补一个正确的
//     Content-Length(防止合成请求 / 异常输入造成帧错乱)。
func normalizeFraming(ordered [][2]string, bodyLen int) [][2]string {
	clCount, teCount, clOK := 0, 0, false
	want := strconv.Itoa(bodyLen)
	for _, kv := range ordered {
		switch textproto.CanonicalMIMEHeaderKey(kv[0]) {
		case "Content-Length":
			clCount++
			if strings.TrimSpace(kv[1]) == want {
				clOK = true
			}
		case "Transfer-Encoding":
			teCount++
		}
	}
	if teCount == 0 && ((bodyLen == 0 && clCount == 0) || (clCount == 1 && clOK)) {
		return ordered // 已自洽,保留原位
	}
	out := make([][2]string, 0, len(ordered)+1)
	for _, kv := range ordered {
		switch textproto.CanonicalMIMEHeaderKey(kv[0]) {
		case "Content-Length", "Transfer-Encoding":
			continue // 剔除,稍后据 body 重建
		}
		out = append(out, kv)
	}
	if bodyLen > 0 {
		out = append(out, [2]string{"Content-Length", want})
	}
	return out
}

// readResponse 读取上游响应头(受 RespHeaderTimeout 约束),读到后清除截止时间以支持流式 body。
// 解析前先 Peek 出响应头块原始顺序/大小写与状态行,经 ctx 回填给 flow.ResponseCapture
// （供响应写回客户端时保真);Peek 失败不阻断正常解析。
func readResponse(pc *persistConn, req *http.Request, respHeaderTimeout time.Duration) (*http.Response, error) {
	if respHeaderTimeout > 0 {
		_ = pc.conn.SetReadDeadline(time.Now().Add(respHeaderTimeout))
	}
	statusLine, rawHdr := peekResponseHead(pc.br)
	resp, err := http.ReadResponse(pc.br, req)
	if err != nil {
		return nil, err
	}
	_ = pc.conn.SetReadDeadline(time.Time{})
	if len(rawHdr) > 0 {
		if rc, ok := flow.ResponseCaptureFrom(req.Context()); ok {
			rc.StatusLine = statusLine
			rc.Headers = rawHdr
		}
	}
	return resp, nil
}

// peekResponseHead 在 http.ReadResponse 之前增量 Peek 出完整响应头块(直到 CRLFCRLF),
// 解析出原始状态行与头序列(顺序+大小写)。Peek 不推进读指针;头块超缓冲 / 出错时返回空。
func peekResponseHead(br *bufio.Reader) (string, [][2]string) {
	const maxPeek = 64 * 1024
	for {
		if n := br.Buffered(); n > 0 {
			b, _ := br.Peek(n)
			if end := headerBlockEnd(b); end >= 0 {
				return parseResponseHead(b[:end])
			}
			if n >= maxPeek {
				return "", nil
			}
		}
		if _, err := br.Peek(br.Buffered() + 1); err != nil {
			return "", nil
		}
	}
}

// headerBlockEnd 返回头块结束位置:CRLFCRLF 中第一个 CRLF 之后的下标(含末头尾随 CRLF、
// 不含终止空行)。未结束返回 -1。容忍极少数仅用 LF 的实现。
func headerBlockEnd(b []byte) int {
	if i := bytes.Index(b, []byte("\r\n\r\n")); i >= 0 {
		return i + 2
	}
	if i := bytes.Index(b, []byte("\n\n")); i >= 0 {
		return i + 1
	}
	return -1
}

// parseResponseHead 把响应头块解析成(状态行, 头序列)。保留顺序与原始大小写,含重复头;
// 对 obs-fold 续行并入上一个值。
func parseResponseHead(head []byte) (string, [][2]string) {
	lines := strings.Split(string(head), "\n")
	status := ""
	out := make([][2]string, 0, len(lines))
	first := true
	for _, ln := range lines {
		ln = strings.TrimSuffix(ln, "\r")
		if ln == "" {
			continue
		}
		if first {
			first = false
			status = ln // 状态行
			continue
		}
		if ln[0] == ' ' || ln[0] == '\t' {
			if len(out) > 0 {
				out[len(out)-1][1] += " " + strings.TrimSpace(ln)
			}
			continue
		}
		c := strings.IndexByte(ln, ':')
		if c < 0 {
			continue
		}
		out = append(out, [2]string{ln[:c], strings.TrimLeft(ln[c+1:], " \t")})
	}
	return status, out
}

// wrapBody 用可回收连接的包装替换 resp.Body:body 读尽且可复用时把连接放回池,否则关闭。
// guard 在读体阶段继续守护 ctx 取消,由 body.Close 解除。
func (t *Transport) wrapBody(resp *http.Response, pc *persistConn, guard *connGuard) {
	reusable := !resp.Close && resp.ProtoAtLeast(1, 1)
	resp.Body = &pooledBody{rc: resp.Body, pc: pc, t: t, guard: guard, reusable: reusable}
}

// pooledBody 包装响应体,在关闭时解除 ctx 取消守护并决定连接回收或关闭。
type pooledBody struct {
	rc       io.ReadCloser
	pc       *persistConn
	t        *Transport
	guard    *connGuard
	reusable bool
	eof      bool
	closed   bool
}

func (b *pooledBody) Read(p []byte) (int, error) {
	n, err := b.rc.Read(p)
	if err == io.EOF {
		b.eof = true
	} else if err != nil {
		b.pc.broken.Store(true)
	}
	return n, err
}

func (b *pooledBody) Close() error {
	if b.closed {
		return nil
	}
	b.closed = true
	won := b.guard.disarm() // 解除守护;false 表示 ctx 取消已抢先关连接,连接不可复用
	err := b.rc.Close()
	if won && b.reusable && b.eof && !b.pc.broken.Load() {
		b.t.putIdle(b.pc)
	} else {
		b.pc.close()
	}
	return err
}

// getIdle 取一条未过期的空闲连接(LIFO),过期的就地关闭。
func (t *Transport) getIdle(key string) *persistConn {
	t.mu.Lock()
	defer t.mu.Unlock()
	list := t.idle[key]
	for len(list) > 0 {
		pc := list[len(list)-1]
		list = list[:len(list)-1]
		if time.Since(pc.idleAt) > t.cfg.IdleConnTimeout {
			pc.close()
			continue
		}
		t.idle[key] = list
		return pc
	}
	delete(t.idle, key)
	return nil
}

// putIdle 把连接放回空闲池;超过每主机上限时丢弃最旧的一条。
func (t *Transport) putIdle(pc *persistConn) {
	if pc.broken.Load() {
		pc.close()
		return
	}
	pc.idleAt = time.Now()
	t.mu.Lock()
	list := t.idle[pc.key]
	list = append(list, pc)
	for len(list) > t.cfg.MaxIdlePerHost {
		old := list[0]
		list = list[1:]
		old.close()
	}
	t.idle[pc.key] = list
	t.mu.Unlock()
}

// CloseIdleConnections 关闭并清空全部空闲连接(切换上游代理时调用)。
func (t *Transport) CloseIdleConnections() {
	t.mu.Lock()
	idle := t.idle
	t.idle = make(map[string][]*persistConn)
	t.mu.Unlock()
	for _, list := range idle {
		for _, pc := range list {
			pc.close()
		}
	}
	if c, ok := t.cfg.Fallback.(interface{ CloseIdleConnections() }); ok {
		c.CloseIdleConnections()
	}
}

// proxyConnect 经上游代理建立到 target 的 CONNECT 隧道。
func proxyConnect(conn net.Conn, target string, proxyURL *url.URL, timeout time.Duration) error {
	_ = conn.SetDeadline(time.Now().Add(timeout))
	defer conn.SetDeadline(time.Time{})

	req := &http.Request{
		Method: http.MethodConnect,
		URL:    &url.URL{Opaque: target},
		Host:   target,
		Header: make(http.Header),
	}
	if proxyURL.User != nil {
		if pw, ok := proxyURL.User.Password(); ok {
			req.Header.Set("Proxy-Authorization", "Basic "+basicAuth(proxyURL.User.Username(), pw))
		}
	}
	if err := req.Write(conn); err != nil {
		return err
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("forward: 上游代理 CONNECT 失败: %s", resp.Status)
	}
	return nil
}

// idempotentMethod 判断方法是否可安全重放。与 net/http.Transport 的 isReplayable 保持一致,
// 只取 GET/HEAD/OPTIONS/TRACE —— PUT/DELETE 虽 RFC 幂等,但实践中盲目重发有风险,故不纳入,
// 以免「请求已发出、响应丢失」时对上游造成重复副作用。
func idempotentMethod(m string) bool {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	default:
		return false
	}
}

// isUpgrade 判断是否为协议升级(如 WebSocket)请求 —— 这类不走保真转发。
func isUpgrade(req *http.Request) bool {
	return strings.EqualFold(req.Header.Get("Upgrade"), "websocket") ||
		tokenListContains(req.Header.Get("Connection"), "upgrade")
}

func tokenListContains(v, token string) bool {
	for _, p := range strings.Split(v, ",") {
		if strings.EqualFold(strings.TrimSpace(p), token) {
			return true
		}
	}
	return false
}

// connKey 是连接池键:scheme + 目标地址 + 代理。
func connKey(u *url.URL, proxyURL *url.URL) string {
	p := ""
	if proxyURL != nil {
		p = proxyURL.String()
	}
	return u.Scheme + "\x00" + canonicalAddr(u) + "\x00" + p
}

// canonicalAddr 返回 host:port(补默认端口)。用 Hostname/Port 以正确处理 IPv6(避免重复方括号)。
func canonicalAddr(u *url.URL) string {
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return net.JoinHostPort(u.Hostname(), port)
}

// hostname 返回不含端口的主机名(用作 TLS ServerName)。
func hostname(u *url.URL) string {
	return u.Hostname()
}

func basicAuth(user, pass string) string {
	return base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
}
