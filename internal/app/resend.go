// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package app

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/mintfog/sniffy/ca"
	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/internal/truststore"
)

// maxComposeResponseBytes 是构造器一次性往返(重发 / GraphQL / SSE 退化)读取上游响应体的上限。
//
// 这条路径绕开了抓包侧的旁路与落盘(passthrough / bodycache):body 整块进 Flow,收尾时随
// 快照再复制一份,并长期留在会话存储里。没有上限时,重发一个下载链接就能在超时之前把进程
// 撑爆 —— 上游给多少就吃多少。构造器的响应是拿来在详情页逐字看的,32 MiB 远超这个用途;
// 真要抓大文件请让客户端走代理本身,那条路上才有边收边发与落盘。
//
// 是 var 而非 const 只为可测:让每个用例真发 32 MiB 才能覆盖到这条分支,代价不值。生产代码不改它。
var maxComposeResponseBytes int64 = 32 << 20

// errComposeResponseTooLarge 标记「上游响应体超过上限,已被主动收掉」。必须与「读到一半断了」
// 分开:两者都让 Flow.Body 不完整,但只有这一种能给用户一个可操作的原因。
var errComposeResponseTooLarge = errors.New("上游响应体超过上限")

func responseTooLargeError(limit int64) error {
	return fmt.Errorf("%w %d 字节,已中止(内容不完整)", errComposeResponseTooLarge, limit)
}

// composeSizeLimitError 把「超过 maxComposeResponseBytes」的两种成因翻成用户能看懂的原因,
// 不是超限则 ok 为假。
//
// 传输字节与解压后字节必须分开说:后者用户在响应头的 Content-Length 里根本看不出来 ——
// 半兆的 gzip 就能解出 512 MiB,只报「响应体超过上限」会让人以为是上限设错了。
func composeSizeLimitError(err error) (string, bool) {
	switch {
	case errors.Is(err, errComposeResponseTooLarge):
		return err.Error(), true
	case errors.Is(err, flow.ErrBodyTooLarge):
		return fmt.Sprintf("上游响应体解压后超过上限 %d 字节,已中止(内容不完整)", maxComposeResponseBytes), true
	}
	return "", false
}

// cappedBody 给上游响应体套一个字节上限,超出即报错中止。
//
// 不用 io.LimitReader:它到顶只给 EOF,读取方会把一条被腰斩的响应当成读完了,于是记成
// completed —— 界面上是绿的、内容却少了一截,正是这条路径最不能出的错。
type cappedBody struct {
	rc    io.ReadCloser
	limit int64
	read  int64
}

func (b *cappedBody) Read(p []byte) (int, error) {
	if b.read > b.limit {
		return 0, responseTooLargeError(b.limit)
	}
	// 最多再读到「上限 + 1」字节:多出的那一字节只用来把「恰好等于上限」和「超了」分开。
	if room := b.limit + 1 - b.read; int64(len(p)) > room {
		p = p[:room]
	}
	n, err := b.rc.Read(p)
	b.read += int64(n)
	// 判定必须就地做完,不能留到下一次 Read:带 Content-Length 的响应会把 EOF 和最后一批
	// 数据一起交出来,读取方拿到 EOF 就收工了,根本不会再问第二次。
	if b.read > b.limit {
		return n, responseTooLargeError(b.limit)
	}
	return n, err
}

func (b *cappedBody) Close() error { return b.rc.Close() }

// capResponseBody 就地给 resp.Body 套上字节上限(limit<=0 时不设限)。
func capResponseBody(resp *http.Response, limit int64) {
	if resp.Body == nil || limit <= 0 {
		return
	}
	resp.Body = &cappedBody{rc: resp.Body, limit: limit}
}

type invalidCAImportError struct {
	err error
}

func (e *invalidCAImportError) Error() string      { return e.err.Error() }
func (e *invalidCAImportError) Unwrap() error      { return e.err }
func (e *invalidCAImportError) InvalidInput() bool { return true }

func invalidCAImport(err error) error {
	return &invalidCAImportError{err: err}
}

// SendRequest 按 spec 发起一次请求,作为一条新 flow 记录并广播,返回新 flow 的 ID。
// URL 无法解析或协议不受支持时返回错误,此时不会产生任何 flow。
func (a *App) SendRequest(spec flow.RequestSpec) (string, error) {
	method := strings.ToUpper(strings.TrimSpace(spec.Method))
	if method == "" {
		method = http.MethodGet
	}

	raw := strings.TrimSpace(spec.URL)
	if raw == "" {
		return "", errors.New("请求 URL 为空")
	}
	// 用户多半直接粘贴 host/path,补默认协议而不是报错。选 https 而非 http:
	// 猜错时握手立刻失败可见,反过来猜 http 会把本该加密的请求明文发出去。
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("URL 无法解析: %w", err)
	}
	if u.Host == "" {
		return "", fmt.Errorf("URL 缺少主机名: %s", spec.URL)
	}
	proto := flow.ProtoHTTPS
	switch u.Scheme {
	case "https":
	case "http":
		proto = flow.ProtoHTTP
	case "ws", "wss":
		return "", errors.New("WebSocket 请用 OpenWebSocket 建立连接")
	default:
		return "", fmt.Errorf("不支持的协议: %s", u.Scheme)
	}

	// 构造器的头是原样写线的(见 splitComposedHeaders),含 CR/LF 就能拼出额外的头乃至
	// 第二个请求。在入口拦下,让用户当场看到原因,而不是留一条 errored 的 flow。
	if err := flow.ValidateHeaderPairs(spec.Headers); err != nil {
		return "", err
	}
	if len(spec.Body) > flow.MaxComposeBodyBytes {
		return "", fmt.Errorf("请求体 %d 字节超过上限 %d", len(spec.Body), flow.MaxComposeBodyBytes)
	}

	host, header, rawHeaders := splitComposedHeaders(spec.Headers, u.Host)
	nf := flow.New(proto)
	nf.Request = &flow.Request{
		Method:     method,
		URL:        u.String(),
		Host:       host,
		Path:       u.Path,
		Proto:      "HTTP/1.1",
		Header:     header,
		Body:       []byte(spec.Body),
		RawHeaders: rawHeaders,
	}
	nf.Tags = append(nf.Tags, "composed")
	if spec.FromID != "" {
		nf.Tags = append(nf.Tags, "resent")
		nf.Metadata["resentFrom"] = spec.FromID
	}

	switch spec.Kind {
	case flow.SpecKindSSE:
		nf.Tags = append(nf.Tags, "sse")
		nf.Metadata["stream"] = flow.StreamSSE
		// 在这里建 ctx 并同步占名额:登记若挪进 goroutine,上限检查就成了 check-then-act。
		// 超限时还没广播过 flow,直接报错返回即可,不留下任何记录。
		ctx, cancel := context.WithCancel(context.Background())
		if err := a.outStreams.add(nf.ID, cancel); err != nil {
			cancel()
			return "", err
		}
		a.Service.ImportFlowStarted(nf.Clone())
		go a.runComposeSSE(ctx, cancel, nf, spec.ViaPipeline)
	case flow.SpecKindGraphQL:
		// query/variables/operationName 已由前端合成为 JSON body,后端与 HTTP 同路。
		nf.Tags = append(nf.Tags, "graphql")
		a.Service.ImportFlowStarted(nf.Clone())
		go a.runResend(nf, spec.ViaPipeline)
	default:
		// 空 Kind 与 SpecKindHTTP 等价:不认识 Kind 的旧客户端照旧走一次性往返。
		a.Service.ImportFlowStarted(nf.Clone())
		go a.runResend(nf, spec.ViaPipeline)
	}
	return nf.ID, nil
}

// splitComposedHeaders 把构造器给出的有序头拆成三份:出站 Host、供插件/规则读写的
// 规范化 map,以及写线用的原始序列。
//
// Host 必须单独拎出来:net/http 的出站请求从 req.Host 而非 Header 取 Host,
// 留在 map 里会被静默忽略(见 flow.ApplyRequestToHTTP)。用户没写 Host 时按 URL 补一条
// 并置于首位——HTTP/1.1 惯例如此,若交给 reconcileOrderedHeaders 兜底会被排到末尾。
func splitComposedHeaders(pairs [][2]string, urlHost string) (string, map[string][]string, [][2]string) {
	host := urlHost
	header := make(map[string][]string, len(pairs))
	rawHeaders := make([][2]string, 0, len(pairs)+1)
	hasHost := false

	for _, kv := range pairs {
		name := strings.TrimSpace(kv[0])
		if name == "" {
			continue
		}
		rawHeaders = append(rawHeaders, [2]string{name, kv[1]})
		if textproto.CanonicalMIMEHeaderKey(name) == "Host" {
			hasHost = true
			if v := strings.TrimSpace(kv[1]); v != "" {
				host = v
			}
			continue
		}
		ck := textproto.CanonicalMIMEHeaderKey(name)
		header[ck] = append(header[ck], kv[1])
	}

	// 一条头都没有时不进保真写线路径,交给 net/http 按标准姿势拼,避免只写一行 Host 的怪报文。
	if len(rawHeaders) == 0 {
		return host, header, nil
	}
	if !hasHost {
		rawHeaders = append([][2]string{{"Host", host}}, rawHeaders...)
	}
	return host, header, rawHeaders
}

// ResendFlow 以一条已捕获 flow 的请求为蓝本重新发起请求,作为一条新 flow 记录并广播。
// 重发会完整走插件/规则/断点管道。返回是否找到了原始 flow。
func (a *App) ResendFlow(id string) bool {
	orig, ok := a.Service.RawFlow(id)
	if !ok || orig.Request == nil {
		return false
	}

	nf := flow.New(orig.Protocol)
	nf.ConnID = orig.ConnID
	src := orig.Request

	hdr := make(map[string][]string, len(src.Header))
	for k, v := range src.Header {
		cp := make([]string, len(v))
		copy(cp, v)
		hdr[k] = cp
	}
	body := make([]byte, len(src.Body))
	copy(body, src.Body)

	nf.Request = &flow.Request{
		Method:   src.Method,
		URL:      src.URL,
		Host:     src.Host,
		Path:     src.Path,
		Proto:    src.Proto,
		Header:   hdr,
		Body:     body,
		ClientIP: src.ClientIP,
	}
	nf.Tags = append(nf.Tags, "resent")
	if nf.Metadata == nil {
		nf.Metadata = map[string]any{}
	}
	nf.Metadata["resentFrom"] = id

	// 存入会话存储的是快照副本:runResend 在私有的 nf 上就地改写(含规则引擎对
	// Header map 的写入),存储里始终是不可变快照,从而消除与 UI 读取(SessionDTO)的竞态。
	a.Service.ImportFlowStarted(nf.Clone())
	go a.runResend(nf, true)
	return true
}

// runResend 在后台执行一次重发的完整往返(请求管道 → 转发/mock/abort → 响应管道)。
// viaPipeline 为假时跳过两侧管道,请求原样出站、响应原样记录。
func (a *App) runResend(nf *flow.Flow, viaPipeline bool) {
	ctx := context.Background()

	if viaPipeline {
		switch d := a.Pipeline.OnRequest(ctx, nf); d.Kind {
		case flow.Abort:
			nf.State = flow.StateBlocked
			nf.Error = d.Reason
			a.finishResend(nf)
			return
		case flow.Mock:
			nf.State = flow.StateMocked
			nf.Timing.ResponseAt = time.Now()
			a.Pipeline.OnResponse(ctx, nf)
			a.finishResend(nf)
			return
		}
	}

	req, err := http.NewRequest(nf.Request.Method, nf.Request.URL, bytes.NewReader(nf.Request.Body))
	if err != nil {
		nf.State = flow.StateErrored
		nf.Error = err.Error()
		a.finishResend(nf)
		return
	}
	req = flow.ApplyRequestToHTTP(nf, req)

	nf.State = flow.StateAwaitingResponse
	resp, err := a.Engine.UpstreamClient().Do(req)
	if err != nil {
		nf.State = flow.StateErrored
		nf.Error = err.Error()
		a.finishResend(nf)
		return
	}
	defer resp.Body.Close()

	nf.Timing.ResponseAt = time.Now()
	// 读到一半断了、或响应体超过上限,都是 errored:此时 Flow.Body 是截断的,记成 completed
	// 等于告诉用户「这就是上游的完整响应」,而重发页面正是拿它去比对的。
	capResponseBody(resp, maxComposeResponseBytes)
	readErr := flow.CaptureResponseToFlowLimit(nf, resp, maxComposeResponseBytes)
	switch msg, limited := composeSizeLimitError(readErr); {
	case readErr == nil:
		nf.State = flow.StateCompleted
	case limited:
		nf.State = flow.StateErrored
		nf.Error = msg
	default:
		nf.State = flow.StateErrored
		nf.Error = fmt.Sprintf("响应体读取失败(内容不完整): %v", readErr)
	}
	// 响应阶段管道
	if viaPipeline {
		if d2 := a.Pipeline.OnResponse(ctx, nf); d2.Kind == flow.Abort {
			nf.State = flow.StateBlocked
			if d2.Reason != "" {
				nf.Error = d2.Reason
			}
		}
	}
	a.finishResend(nf)
}

func (a *App) finishResend(nf *flow.Flow) {
	if nf.Timing.CompletedAt.IsZero() {
		nf.Timing.CompletedAt = time.Now()
	}
	nf.Timing.DurationMs = time.Since(nf.Timing.RequestAt).Milliseconds()
	a.Service.ImportFlowCompleted(nf.Clone())
}

// RegenerateCA 重新生成根 CA(覆盖磁盘),刷新 service 的证书导出,返回新证书 PEM。
func (a *App) RegenerateCA() (string, error) {
	a.caMu.Lock()
	defer a.caMu.Unlock()

	newCA, err := ca.RegenerateCA(a.CertDir)
	if err != nil {
		return "", err
	}
	if err := a.Engine.SetCA(newCA); err != nil {
		return "", err
	}
	a.Service.SetCA(newCA)
	return string(a.Service.CertificatePEM()), nil
}

// InstallCAToSystem 把当前根 CA 装入本机信任库,授权对话框由平台实现触发。
// macOS 走用户级(登录钥匙串 + user 域,支持 Touch ID);Windows/Linux 见对应实现。
func (a *App) InstallCAToSystem() error {
	pem := a.Service.CertificatePEM()
	if len(pem) == 0 {
		return errors.New("根证书尚未就绪")
	}
	return truststore.Install(pem)
}

// ExportCAAs 按格式导出根证书内容(不落盘,交由 Bridge 处理文件对话框)。
func (a *App) ExportCAAs(format, password string) ([]byte, string, error) {
	return a.Service.CertificateExportAs(format, password)
}

// ImportCAFromFile 读取给定路径的证书文件,尝试按 PKCS12 / PEM Bundle 解析并热切换根 CA。
// password 只用于 PKCS12(PEM 分支忽略);解析成功后同步刷新 service 的证书导出。
// 返回新根的 PEM,便于前端直接更新展示。
func (a *App) ImportCAFromFile(path, password string) (string, error) {
	if path == "" {
		return "", errors.New("未选择文件")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return a.ImportCA(data, password)
}

// ImportCA 从客户端提供的字节导入 PKCS12 或 PEM Bundle,持久化后热切换根 CA。
// 它是桌面文件导入与 headless HTTP 上传共用的无运输层入口。
func (a *App) ImportCA(data []byte, password string) (string, error) {
	if len(data) == 0 {
		return "", invalidCAImport(errors.New("导入数据为空"))
	}
	a.caMu.Lock()
	defer a.caMu.Unlock()

	var (
		cert *x509.Certificate
		key  any
		err  error
	)
	// 先尝试 PEM Bundle,不要求文件以 PEM 头开头:OpenSSL 导出的 bundle 可能带
	// Bag Attributes 等前言。TrimPrefix 剥 UTF-8 BOM/前导空白,兼容 Windows 编辑器保存的 PEM;
	// 只有未识别到任何 PEM 块时才回退到 PKCS12;否则保留 PEM 的明确错误
	// (例如 bundle 仅含证书时的“未找到匹配的私钥”)。
	probe := bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	probe = bytes.TrimLeft(probe, " \r\n\t")
	cert, key, err = ca.ImportFromPEMBundle(probe)
	if err != nil {
		block, _ := pem.Decode(probe)
		if block == nil {
			cert, key, err = ca.ImportFromPKCS12(data, password)
		}
	}
	if err != nil {
		return "", invalidCAImport(err)
	}
	newCA, err := ca.ImportCA(cert, key, a.CertDir)
	if err != nil {
		return "", err
	}
	if err := a.Engine.SetCA(newCA); err != nil {
		return "", err
	}
	a.Service.SetCA(newCA)
	return string(a.Service.CertificatePEM()), nil
}
