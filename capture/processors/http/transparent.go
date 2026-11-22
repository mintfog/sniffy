// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package http

import (
	"bufio"
	"bytes"
	"cmp"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mintfog/sniffy/capture/types"
)

// transparentSniffTimeout 是等透明连接首批字节的时限。SMTP、SSH、MySQL 这类协议由服务端
// 先说话，客户端不会发首字节；到点仍无数据即按盲转发处理，这段等待就是它们多出的建连延迟。
const transparentSniffTimeout = 300 * time.Millisecond

// maxHTTPMethodLen 覆盖 IANA 登记的最长方法名（UPDATEREDIRECTREF）。
const maxHTTPMethodLen = 17

// ServeTransparent 处理一条未经代理握手、直接交来的连接，阻塞到连接结束。连接的 RemoteAddr
// 须是应用的原始源地址，LocalAddr 须是它要连的原始目标地址。
//
// 按首批字节分流：TLS 交给 serveTransparentTLS，明文 HTTP 进请求循环，其余盲转发到原始目标。
func ServeTransparent(conn types.Connection) error {
	client, server, reader := conn.GetConn(), conn.GetServer(), conn.GetReader()
	p := &Processor{
		conn: conn,
		// 透明连接上的请求和 CONNECT 隧道内的一样，不经代理认证，也不会是在访问入口页。
		proxyTunnel: true,
		originalDst: client.LocalAddr().String(),
	}

	_ = client.SetReadDeadline(time.Now().Add(transparentSniffTimeout))
	first, err := reader.Peek(1)
	switch {
	case errors.Is(err, io.EOF), isTimeout(err):
		// 应用在等服务端先说话（EOF 是它没发数据就关了写端，响应仍要送回去）：盲转发。
	case err != nil:
		return err
	case first[0] == TLSHandshakeRecordType && sniffClientHello(reader):
		return p.serveTransparentTLS(server, reader)
	case sniffHTTPRequest(reader) == sniffedHTTP:
		server.LogDebug("透明连接 %s：明文 HTTP", p.originalDst)
		_ = client.SetReadDeadline(time.Time{})
		return p.handleHttpProtocol(server, reader, conn.GetWriter())
	}
	server.LogDebug("透明连接 %s：直通转发", p.originalDst)
	return p.tunnel(server, p.originalDst)
}

// serveTransparentTLS 从 ClientHello 取出域名后决定解密还是盲转发。透明连接只知道目标 IP，
// 解密范围判断与签发证书都要用域名；没有 SNI 时按 IP 处理。
func (p *Processor) serveTransparentTLS(server types.Server, reader *bufio.Reader) error {
	if transparentTLSBlind.Load() {
		server.LogDebug("透明连接 %s：TLS 一律直通转发", p.originalDst)
		return p.tunnel(server, p.originalDst)
	}
	client := p.conn.GetConn()
	_ = client.SetReadDeadline(time.Now().Add(TLSHandshakeTimeout))
	hello := peekClientHello(client, reader)
	_ = client.SetReadDeadline(time.Time{})

	host := p.originalDst
	if hello != nil && hello.ServerName != "" {
		_, port, _ := net.SplitHostPort(p.originalDst)
		host = net.JoinHostPort(strings.ToLower(hello.ServerName), port)
	}
	decrypt := hello != nil && offersHTTP(hello.SupportedProtos) && shouldDecrypt(host) && !mitmBypass.active(host)
	if !decrypt {
		server.LogDebug("透明连接 %s（%s）：TLS 直通转发", p.originalDst, host)
		return p.tunnel(server, p.originalDst)
	}

	server.LogDebug("透明连接 %s（%s）：TLS 解密", p.originalDst, host)
	p.isHttps = true
	// handleTlsHandshake 从 p.request.Host 取握手所用的主机：补一个与 CONNECT 同形的请求。
	p.request = &http.Request{Method: http.MethodConnect, Host: host}
	return p.handleTlsHandshake(server, reader)
}

// awaitFirstBytes 在透明 TLS 握手成功后等应用发出首批应用层字节，最多等 window，返回应用是否
// 在这段时间里一直没出声。
//
// 握手成功不等于应用接受了证书：OkHttp 的 CertificatePinner 一类在握手完成后才校验，不通过
// 就一个字节不发地关闭连接。window 内这样关闭的记一次失败；收到字节清掉未生效的失败计数。
// 到点仍没出声的不记失败（浏览器的预连接会长时间空闲），之后发出字节时由调用方补记成功。
// gen 是签发证书时的代次。
func (p *Processor) awaitFirstBytes(host string, window time.Duration, gen uint64) (silent bool, err error) {
	_ = p.conn.GetConn().SetReadDeadline(time.Now().Add(window))
	_, err = p.conn.GetReader().Peek(1)
	switch {
	case err == nil:
		mitmBypass.noteSuccess(host, gen)
	case isTimeout(err):
		return true, nil
	default:
		mitmBypass.addCertRejection(host, bypassClosedAfterHandshake, 1, gen)
	}
	return false, err
}

// relayUnlessHTTP 处理握手时没有协商出 ALPN 的透明 TLS 连接：IMAPS、SMTPS、MQTT over TLS
// 都不带 ALPN，HTTP 解析读不了它们。clientSilent 是应用在 transparentSniffTimeout 内是否一直
// 没出声。解密后的首批字节是 HTTP 请求时返回 false，由调用方继续按 HTTP 处理；否则另建一条
// 到原始目标的 TLS 连接，把明文原样中继过去，返回 true。
//
// 能确认不是 HTTP 的把主机记入直通名单，此后到它的连接直接盲转发；无从判断的只中继这一条。
func (p *Processor) relayUnlessHTTP(server types.Server, host string, clientSilent bool) (relayed bool, err error) {
	client, reader := p.conn.GetConn(), p.conn.GetReader()
	verdict := sniffUndecided
	if !clientSilent {
		verdict = sniffHTTPRequest(reader)
	}
	if verdict == sniffedHTTP {
		return false, nil
	}

	server.LogDebug("透明连接 %s（%s）：解密后不是 HTTP，中继到源站", p.originalDst, host)
	origin, err := p.dialRelayOrigin(server, host)
	if err != nil {
		return true, err
	}
	originReader := bufio.NewReader(origin)

	// 应用先发了非 HTTP 的字节即可确认。请求行只到了一半的仍存疑：可能是分片到达的 HTTP 请求。
	if verdict == sniffedOther {
		mitmBypass.add(host, bypassNotHTTP, mitmBypassStrikes)
	}
	if clientSilent {
		// 应用没出声就看源站：它先开口即可确认；双方都沉默的可能是预先建好的空闲 HTTP 连接。
		// 源站索要客户端证书时，TLS 1.3 的握手照常返回成功，拒绝要到这次读取才以 alert 的
		// 形式收到：同样记入名单，之后由应用自己与源站握手。
		_ = origin.SetReadDeadline(time.Now().Add(transparentSniffTimeout))
		switch _, originErr := originReader.Peek(1); {
		case originErr == nil:
			mitmBypass.add(host, bypassNotHTTP, mitmBypassStrikes)
		case isTLSAlert(originErr):
			mitmBypass.add(host, bypassOriginRejected, mitmBypassStrikes)
		}
	}
	relay(client, reader, origin, originReader, halfClosedIdleTimeout)
	return true, nil
}

// relayIfProxyRequest 处理协商出 http/1.1 的透明 TLS 连接：首个请求是 CONNECT 或
// absolute-form 说明原始目标是应用自己配置的 HTTPS 代理，按代理语义替它转发会绕过那台代理
// 连同它的认证。此时另建一条到原始目标的 TLS 连接，把明文原样中继过去，返回 true，主机记入
// 直通名单；否则返回 false，由调用方继续按 HTTP 处理。
func (p *Processor) relayIfProxyRequest(server types.Server, host string) (relayed bool, err error) {
	client, reader := p.conn.GetConn(), p.conn.GetReader()
	// 等首个请求行用请求循环的读期限。armReadDeadline 在未配置 ReadTimeout 时什么都不设，
	// 握手期留下的期限要先清掉。
	_ = client.SetReadDeadline(time.Time{})
	p.armReadDeadline(server)
	if !startsWithProxyRequest(reader) {
		return false, nil
	}

	server.LogDebug("透明连接 %s（%s）：原始目标是 HTTPS 代理，中继到它", p.originalDst, host)
	origin, err := p.dialRelayOrigin(server, host, "http/1.1")
	if err != nil {
		return true, err
	}
	mitmBypass.add(host, bypassHTTPSProxy, mitmBypassStrikes)
	relay(client, reader, origin, origin, halfClosedIdleTimeout)
	return true, nil
}

// startsWithProxyRequest 报告 reader 中待读的首行是否为发给代理的 HTTP/1.x 请求（目标是
// authority-form 或 absolute-form），不消费字节。首行读不出来时返回 false，交给 HTTP 处理去报错。
func startsWithProxyRequest(reader *bufio.Reader) bool {
	for {
		buf, _ := reader.Peek(reader.Buffered())
		if end := bytes.IndexByte(buf, '\n'); end >= 0 {
			fields := bytes.Fields(buf[:end]) // 方法、目标、协议版本
			isHTTP := len(fields) == 3 && bytes.HasPrefix(fields[2], []byte("HTTP/1."))
			return isHTTP && fields[1][0] != '/' && fields[1][0] != '*'
		}
		if _, err := reader.Peek(len(buf) + 1); err != nil {
			return false
		}
	}
}

// dialRelayOrigin 为明文中继建立到原始目标的 TLS 连接，按 host 校验证书，alpn 是向源站通告
// 的协议。源站不接受 sniffy 代为握手（证书不被信任、握手期就要求客户端证书）时把主机记入
// 直通名单，之后由应用自己与它握手；超时、连接被重置这类传输层失败可能只是网络抖动，只让
// 这一条连接失败。
func (p *Processor) dialRelayOrigin(server types.Server, host string, alpn ...string) (net.Conn, error) {
	raw, err := dialTunnelTarget(p.originalDst, true)
	if err != nil {
		server.LogError("连接源站 %s 失败: %v", host, err)
		return nil, err
	}
	config := outboundTLSPolicy.Load().ConfigForHost(hostOnly(host))
	config.NextProtos = alpn
	origin := tls.Client(raw, config)
	_ = origin.SetDeadline(time.Now().Add(TLSHandshakeTimeout))
	if err := origin.Handshake(); err != nil {
		_ = raw.Close()
		server.LogError("与源站 %s 的 TLS 握手失败: %v", host, err)
		if !isTransportFailure(err) {
			mitmBypass.add(host, bypassOriginRejected, mitmBypassStrikes)
		}
		return nil, err
	}
	return origin, nil
}

// sniffClientHello 核对首字节为 0x16 的连接是否以 TLS ClientHello 开头（记录头的主版本号与
// 握手类型），不消费字节。别的协议也可能以 0x16 开头，等它凑出一份完整的 ClientHello 要等到
// 握手超时。
func sniffClientHello(reader *bufio.Reader) bool {
	const clientHelloType = 0x01
	buf, err := reader.Peek(6)
	return err == nil && buf[1] == 0x03 && buf[5] == clientHelloType
}

type sniffVerdict int

const (
	// sniffUndecided 表示已到的字节还不足以判断，再等就超过了读期限。
	sniffUndecided sniffVerdict = iota
	sniffedHTTP
	sniffedOther
)

// sniffHTTPRequest 判断 reader 中待读的字节是否以 origin-form 的 HTTP/1.x 请求行开头
// （「方法 空格 /… 空格 HTTP/1.x」，目标也可以是 *），不消费字节。要看到行尾的协议版本
// 才认：Redis inline 命令（GET /session）这类文本协议的开头与请求行相同。
// 已到的字节与请求行不矛盾、但整行在读期限内没到齐时返回 sniffUndecided。
//
// 只认 origin-form：authority-form（CONNECT）与 absolute-form 说明对端是应用自己配置的
// HTTP 代理，按代理语义替它转发会绕过那台代理。
func sniffHTTPRequest(reader *bufio.Reader) sniffVerdict {
	for {
		buf, _ := reader.Peek(reader.Buffered())
		if !startsLikeRequestLine(buf) {
			return sniffedOther
		}
		if end := bytes.IndexByte(buf, '\n'); end >= 0 {
			line := bytes.TrimSuffix(buf[:end], []byte("\r"))
			if bytes.HasSuffix(line, []byte(" HTTP/1.1")) || bytes.HasSuffix(line, []byte(" HTTP/1.0")) {
				return sniffedHTTP
			}
			return sniffedOther
		}
		// 行还没到齐：等下一批字节，到读期限或缓冲装满为止。
		if _, err := reader.Peek(len(buf) + 1); err != nil {
			return sniffUndecided
		}
	}
}

// startsLikeRequestLine 报告 buf 是否与 origin-form 请求行的开头不矛盾：大写方法名，
// 一个空格，再是 / 或 *。buf 可以在其中任何位置截断。
func startsLikeRequestLine(buf []byte) bool {
	for i, c := range buf {
		if c == ' ' {
			return i > 0 && (len(buf) == i+1 || buf[i+1] == '/' || buf[i+1] == '*')
		}
		if i >= maxHTTPMethodLen || ((c < 'A' || c > 'Z') && c != '-') {
			return false
		}
	}
	return true
}

// offersHTTP 报告客户端的 ALPN 列表是否容得下 HTTP。带了 ALPN 却不含 h2 / http/1.1（如
// imap、xmpp-client、dot）的握手必然协商失败，不必尝试解密；没带 ALPN 的先解密，握手后
// 由 relayUnlessHTTP 按明文内容再判断。
func offersHTTP(protos []string) bool {
	return len(protos) == 0 || slices.Contains(protos, "h2") || slices.Contains(protos, "http/1.1")
}

var errClientHelloPeeked = errors.New("ClientHello 已取得")

// peekClientHello 解析 reader 中待读的 ClientHello，不消费字节；不是完整合法的 ClientHello
// 时返回 nil。解析借用 crypto/tls 的服务端握手，拿到 ClientHello 即中止。
func peekClientHello(conn net.Conn, reader *bufio.Reader) *tls.ClientHelloInfo {
	var hello *tls.ClientHelloInfo
	_ = tls.Server(&peekOnlyConn{Conn: conn, reader: reader}, &tls.Config{
		GetConfigForClient: func(info *tls.ClientHelloInfo) (*tls.Config, error) {
			hello = info
			return nil, errClientHelloPeeked
		},
	}).Handshake()
	return hello
}

// peekOnlyConn 把 reader 中待读的字节供出去而不消费它们，并吞掉一切写入：中止握手时
// crypto/tls 会写一条 alert，不能让它到达客户端。
type peekOnlyConn struct {
	net.Conn
	reader *bufio.Reader
	offset int
}

func (c *peekOnlyConn) Read(b []byte) (int, error) {
	if c.reader.Buffered() <= c.offset {
		if _, err := c.reader.Peek(c.offset + 1); err != nil {
			return 0, err
		}
	}
	buf, _ := c.reader.Peek(c.reader.Buffered())
	n := copy(b, buf[c.offset:])
	c.offset += n
	return n, nil
}

func (c *peekOnlyConn) Write(b []byte) (int, error) { return len(b), nil }

// withOriginalDst 给透明连接上读到的请求标上原始目标：出站拨号据此连应用原本连的地址，
// 自转发与入口页的判定据此跳过。
//
// HTTP/1.0 请求可以不带 Host，此时出站 URL 的主机用原始目标补上。
func withOriginalDst(request *http.Request, isHTTPS bool, originalDst string) *http.Request {
	if request.URL.Host == "" && request.Host == "" {
		request.URL.Host = originalDst
	}
	target := url.URL{Host: cmp.Or(request.URL.Host, request.Host)}
	port := target.Port()
	if port == "" {
		port = "80"
		if isHTTPS {
			port = "443"
		}
	}
	authority := net.JoinHostPort(target.Hostname(), port)
	return request.WithContext(types.WithOriginalDst(request.Context(), authority, originalDst))
}

// postHandshakeWindow 是 h2 / http/1.1 连接握手成功后等首批应用层字节的时限，见 awaitFirstBytes。
// 正常客户端握手后立即发出 connection preface 或请求，远用不到这么久。
const postHandshakeWindow = time.Second

// transparentTLSBlind 打开时透明连接上的 TLS 一律盲转发，见 SetTransparentCATrusted。
var transparentTLSBlind atomic.Bool

// SetTransparentCATrusted 按根证书是否被系统信任设置透明连接上的 TLS：未信任时一律盲转发、不解密，
// 明文 HTTP 不受影响，否则解密只会让每个 HTTPS 主机的首次连接因证书不受信任失败一次。只影响此后的
// 新连接。
//
// 由未信任变为信任时清除直通名单里应用拒绝证书的记录：它们可能是在未信任期间、发现之前记下的，
// 原因是证书不受信任而不是应用做了证书固定。
func SetTransparentCATrusted(trusted bool) {
	if wasBlind := transparentTLSBlind.Swap(!trusted); wasBlind && trusted {
		ForgetCertRejections()
	}
}

const (
	// mitmBypassStrikes 是连续失败多少次后转为盲转发。客户端只是断开连接时原因不明
	// （也可能是它自己取消了连接），要再失败一次才算数。
	mitmBypassStrikes = 2
	// mitmBypassStrikeWindow 是未生效的失败计数的保留时长，从最近一次失败算起：隔得太久的
	// 两次断开多半各有各的原因，不该凑成一条长期记录。
	mitmBypassStrikeWindow = 30 * time.Minute
	mitmBypassMaxHosts     = 4096
)

// bypassReason 是主机进直通名单的原因。
type bypassReason string

const (
	// bypassCertRejected：应用在握手期拒绝了 sniffy 签发的证书。
	bypassCertRejected bypassReason = "cert_rejected"
	// bypassClosedAfterHandshake：握手成功后应用一个字节不发就关闭连接，是握手后才校验证书
	// 的程序拒绝证书时的表现。
	bypassClosedAfterHandshake bypassReason = "closed_after_handshake"
	// bypassNotHTTP：TLS 里跑的不是 HTTP。
	bypassNotHTTP bypassReason = "not_http"
	// bypassOriginRejected：源站不接受 sniffy 代为握手（证书不被信任、要求客户端证书）。
	bypassOriginRejected bypassReason = "origin_rejected"
	// bypassHTTPSProxy：原始目标是应用自己配置的 HTTPS 代理。
	bypassHTTPSProxy bypassReason = "https_proxy"
)

// mitmBypass 是直通名单，记住透明连接上不该解密的主机，此后到它们的透明连接直接盲转发：
// 做了证书固定或不认系统根证书的程序会拒绝 sniffy 签发的证书，继续解密只会让它们一直
// 连不上；TLS 里跑的不是 HTTP 的，解密了也无从抓包；源站只认应用自己握手的（要求客户端证书），
// 或原始目标是应用自己配置的 HTTPS 代理的，解密了也没法替它转发。
//
// 这些原因都不随时间改变，记录生效后在进程生命周期内一直有效。应用拒绝证书的记录是针对
// 当时的根证书、它的信任状态与导入的服务器证书得出的：换根证书、经 sniffy 把根证书装入信任库、
// 导入的服务器证书有变动时清除（见 ForgetCertRejections）。
//
// 键是小写的 host:port。同一主机常在 443 上跑 HTTPS、在别的端口上跑其它协议（邮件服务器的
// 网页端与 IMAPS），带上端口才不会因为后者停掉前者的抓包。
var mitmBypass = &bypassList{hosts: make(map[string]bypassEntry)}

type bypassEntry struct {
	strikes int
	// reason 是最近一次失败的原因，记录生效后不再变。
	reason     bypassReason
	lastStrike time.Time
}

type bypassList struct {
	mu    sync.Mutex
	hosts map[string]bypassEntry
	// certGen 是证书代次，ForgetCertRejections 时递增。应用接受或拒绝证书的结论只对签发时的
	// 证书成立：清除之前就签发证书的连接，之后报出的拒绝与成功都不再计入。
	certGen uint64
}

// certGeneration 返回当前证书代次。须在取根证书签发之前读：反过来读，旧证书可能被标成新代次。
func (b *bypassList) certGeneration() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.certGen
}

func (b *bypassList) active(host string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.hosts[host].strikes >= mitmBypassStrikes
}

// noteFailure 记一次握手失败，gen 是签发证书时的代次。客户端回了 TLS alert 说明它明确拒绝了
// 证书，一次即生效。
func (b *bypassList) noteFailure(host string, cause error, gen uint64) {
	strikes := 1
	if isTLSAlert(cause) {
		strikes = mitmBypassStrikes
	}
	b.addCertRejection(host, bypassCertRejected, strikes, gen)
}

// addCertRejection 与 add 相同，但签发证书时的代次 gen 已过期时丢弃这次结果。
func (b *bypassList) addCertRejection(host string, reason bypassReason, strikes int, gen uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if gen == b.certGen {
		b.addLocked(host, reason, strikes)
	}
}

// isTLSAlert 报告 err 是否为对端发来的 TLS alert。
func isTLSAlert(err error) bool {
	var remote *net.OpError
	return errors.As(err, &remote) && remote.Op == "remote error"
}

// isTransportFailure 报告 TLS 握手错误 err 是否出自传输层（超时、连接被关闭或重置），而不是
// TLS 协议层面的拒绝（对端的 alert、证书校验不通过、协商不出共同参数）。
func isTransportFailure(err error) bool {
	// crypto/tls 把收到与发出的 alert 包成 Op 为 remote error / local error 的 net.OpError。
	var opErr *net.OpError
	return isTimeout(err) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		(errors.As(err, &opErr) && opErr.Op != "remote error" && opErr.Op != "local error")
}

// add 以 reason 给 host 累加 strikes 次失败。已生效的记录保持原样。
func (b *bypassList) add(host string, reason bypassReason, strikes int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.addLocked(host, reason, strikes)
}

func (b *bypassList) addLocked(host string, reason bypassReason, strikes int) {
	entry, ok := b.hosts[host]
	if entry.strikes >= mitmBypassStrikes {
		return
	}
	if !ok && len(b.hosts) >= mitmBypassMaxHosts {
		clear(b.hosts)
	}
	now := time.Now()
	if now.Sub(entry.lastStrike) > mitmBypassStrikeWindow {
		entry.strikes = 0
	}
	entry.strikes += strikes
	entry.reason = reason
	entry.lastStrike = now
	b.hosts[host] = entry
}

// noteSuccess 清掉 host 尚未生效的失败计数，gen 是签发证书时的代次，已过期时什么都不做。
// 已生效的记录保留：应用发出字节只说明它接受了 sniffy 的证书，而记录可能出自别的原因（源站
// 拒绝代为握手、跑的不是 HTTP），这条连接又可能是在记录生效之前就开始的。
func (b *bypassList) noteSuccess(host string, gen uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if gen == b.certGen && b.hosts[host].strikes < mitmBypassStrikes {
		delete(b.hosts, host)
	}
}

// ForgetCertRejections 清除直通名单里应用拒绝证书的记录，含未生效的计数，并让证书代次前进。
// 其余原因与根证书无关，保留。
func ForgetCertRejections() {
	b := mitmBypass
	b.mu.Lock()
	defer b.mu.Unlock()
	b.certGen++
	for host, entry := range b.hosts {
		if entry.reason == bypassCertRejected || entry.reason == bypassClosedAfterHandshake {
			delete(b.hosts, host)
		}
	}
}

// BypassHost 是直通名单里一条已生效的记录。
type BypassHost struct {
	// Host 是小写的 host:port。
	Host string
	// Reason 是 bypassReason 的取值。
	Reason string
	// Since 是记录生效的时间。
	Since time.Time
}

// BypassHosts 返回直通名单里已生效的记录，按主机排序。
func BypassHosts() []BypassHost {
	b := mitmBypass
	b.mu.Lock()
	list := make([]BypassHost, 0, len(b.hosts))
	for host, entry := range b.hosts {
		if entry.strikes >= mitmBypassStrikes {
			list = append(list, BypassHost{Host: host, Reason: string(entry.reason), Since: entry.lastStrike})
		}
	}
	// 每条新的透明连接都要取这把锁，排序放到锁外。
	b.mu.Unlock()
	slices.SortFunc(list, func(a, b BypassHost) int { return strings.Compare(a.Host, b.Host) })
	return list
}

// ForgetBypass 删掉 host（host:port）在直通名单里的记录，含未生效的失败计数，此后到它的透明
// 连接重新尝试解密。
func ForgetBypass(host string) {
	b := mitmBypass
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.hosts, strings.ToLower(host))
}

// ClearBypass 清空直通名单，含未生效的失败计数。
func ClearBypass() {
	b := mitmBypass
	b.mu.Lock()
	defer b.mu.Unlock()
	clear(b.hosts)
}

// noteOriginRejection 在 request 出站失败、写回错误响应之前调用。源站经 bypassRejectingOrigin
// 记入直通名单时，让 r 在这次响应后断开客户端连接：这条连接已经解密，留着只会让后续请求
// 继续失败，应用重连后即为盲转发。
func noteOriginRejection(server types.Server, request *http.Request, cause error, r clientResponder) {
	if request.URL.Scheme == "https" && bypassRejectingOrigin(server, request, cause) {
		r.closeAfterResponse()
	}
}

// bypassRejectingOrigin 在透明连接上的 request 出站失败后调用，调用方须确认出站走的是 TLS。
// 失败原因是源站发来的 TLS alert 时，说明它不接受 sniffy 代为握手（常见于要求客户端证书），
// 之后要由应用自己与它握手：把请求所连的主机记入直通名单并返回 true。
func bypassRejectingOrigin(server types.Server, request *http.Request, cause error) bool {
	if !types.IsTransparent(request.Context()) || !isTLSAlert(cause) {
		return false
	}
	target := url.URL{Host: cmp.Or(request.URL.Host, request.Host)}
	host := net.JoinHostPort(strings.ToLower(target.Hostname()), cmp.Or(target.Port(), "443"))
	server.LogDebug("透明连接：源站 %s 以 TLS alert 拒绝了请求，转为盲转发", host)
	mitmBypass.add(host, bypassOriginRejected, mitmBypassStrikes)
	return true
}
