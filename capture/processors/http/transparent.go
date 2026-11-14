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
	return p.tunnel(server, reader, p.originalDst)
}

// serveTransparentTLS 从 ClientHello 取出域名后决定解密还是盲转发。透明连接只知道目标 IP，
// 解密范围判断与签发证书都要用域名；没有 SNI 时按 IP 处理。
func (p *Processor) serveTransparentTLS(server types.Server, reader *bufio.Reader) error {
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
		return p.tunnel(server, reader, p.originalDst)
	}

	server.LogDebug("透明连接 %s（%s）：TLS 解密", p.originalDst, host)
	p.isHttps = true
	// handleTlsHandshake 从 p.request.Host 取握手所用的主机：补一个与 CONNECT 同形的请求。
	p.request = &http.Request{Method: http.MethodConnect, Host: host}
	return p.handleTlsHandshake(server, reader)
}

// relayUnlessHTTP 处理握手时没有协商出 ALPN 的透明 TLS 连接：IMAPS、SMTPS、MQTT over TLS
// 都不带 ALPN，HTTP 解析读不了它们。解密后的首批字节是 HTTP 请求时返回 false，由调用方继续
// 按 HTTP 处理；否则另建一条到原始目标的 TLS 连接，把明文原样中继过去，返回 true。
//
// 能确认不是 HTTP 的把主机记入直通名单，此后到它的连接直接盲转发；无从判断的只中继这一条。
func (p *Processor) relayUnlessHTTP(server types.Server, host string) (relayed bool, err error) {
	client, reader := p.conn.GetConn(), p.conn.GetReader()
	_ = client.SetReadDeadline(time.Now().Add(transparentSniffTimeout))
	_, peekErr := reader.Peek(1)
	clientSilent := isTimeout(peekErr)
	if peekErr != nil && !clientSilent {
		return true, peekErr
	}
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
	bypass := verdict == sniffedOther
	if clientSilent {
		// 应用没出声就看源站：它先开口即可确认；双方都沉默的可能是预先建好的空闲 HTTP 连接。
		// 源站索要客户端证书时，TLS 1.3 的握手照常返回成功，拒绝要到这次读取才以 alert 的
		// 形式收到：同样记入名单，之后由应用自己与源站握手。
		_ = origin.SetReadDeadline(time.Now().Add(transparentSniffTimeout))
		_, originErr := originReader.Peek(1)
		bypass = originErr == nil || isTLSAlert(originErr)
	}
	if bypass {
		mitmBypass.add(host, mitmBypassStrikes)
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
	mitmBypass.add(host, mitmBypassStrikes)
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
// 直通名单，之后由应用自己与它握手。
func (p *Processor) dialRelayOrigin(server types.Server, host string, alpn ...string) (net.Conn, error) {
	raw, err := dialTunnelTarget(p.originalDst)
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
		mitmBypass.add(host, mitmBypassStrikes)
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

const (
	// mitmBypassTTL 到期后重新尝试解密，用户可能已经装好了根证书。
	mitmBypassTTL = 30 * time.Minute
	// mitmBypassStrikes 是握手连续失败多少次后转为盲转发。客户端只是断开连接时原因不明
	// （也可能是它自己取消了连接），要再失败一次才算数。
	mitmBypassStrikes  = 2
	mitmBypassMaxHosts = 4096
)

// mitmBypass 是直通名单，记住透明连接上不该解密的主机，此后到它们的透明连接直接盲转发：
// 做了证书固定或不认系统根证书的程序会拒绝 sniffy 签发的证书，继续解密只会让它们一直
// 连不上；TLS 里跑的不是 HTTP 的，解密了也无从抓包。
//
// 键是小写的 host:port。同一主机常在 443 上跑 HTTPS、在别的端口上跑其它协议（邮件服务器的
// 网页端与 IMAPS），带上端口才不会因为后者停掉前者的抓包。
var mitmBypass = &bypassList{hosts: make(map[string]bypassEntry)}

type bypassEntry struct {
	strikes int
	expires time.Time
}

type bypassList struct {
	mu    sync.Mutex
	hosts map[string]bypassEntry
}

func (b *bypassList) active(host string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	entry, ok := b.hosts[host]
	if !ok {
		return false
	}
	if time.Now().After(entry.expires) {
		delete(b.hosts, host)
		return false
	}
	return entry.strikes >= mitmBypassStrikes
}

// noteFailure 记一次握手失败。客户端回了 TLS alert 说明它明确拒绝了证书，一次即生效。
func (b *bypassList) noteFailure(host string, cause error) {
	strikes := 1
	if isTLSAlert(cause) {
		strikes = mitmBypassStrikes
	}
	b.add(host, strikes)
}

// isTLSAlert 报告 err 是否为对端发来的 TLS alert。
func isTLSAlert(err error) bool {
	var remote *net.OpError
	return errors.As(err, &remote) && remote.Op == "remote error"
}

// add 给 host 累加 strikes 次失败，并把有效期续到 mitmBypassTTL 之后。
func (b *bypassList) add(host string, strikes int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	entry, ok := b.hosts[host]
	if now.After(entry.expires) {
		entry = bypassEntry{}
	}
	if !ok && len(b.hosts) >= mitmBypassMaxHosts {
		clear(b.hosts)
	}
	entry.strikes += strikes
	entry.expires = now.Add(mitmBypassTTL)
	b.hosts[host] = entry
}

func (b *bypassList) noteSuccess(host string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.hosts, host)
}
