// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package http

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mintfog/sniffy/capture/processors/http/websocket"
	"github.com/mintfog/sniffy/capture/types"
)

// tunnelUpstream 持有当前上游代理(nil = 直连),供直通隧道使用。
// 与引擎自建上游客户端各自独立:MITM 转发经客户端的 Proxy 闭包,直通隧道经此建 CONNECT。
// 由 SetUpstreamProxy 原子写入,与引擎的 SetUpstreamProxy 保持同步。
var tunnelUpstream atomic.Pointer[UpstreamProxy]

type dialContextFunc func(context.Context, string, string) (net.Conn, error)

var tunnelDialContext atomic.Pointer[dialContextFunc]

// SetDialContext 为 CONNECT 和 WebSocket 注入统一的上游拨号检查；nil 恢复默认拨号。
func SetDialContext(dial func(context.Context, string, string) (net.Conn, error)) {
	if dial == nil {
		tunnelDialContext.Store(nil)
	} else {
		fn := dialContextFunc(dial)
		tunnelDialContext.Store(&fn)
	}
	websocket.SetDialContext(dial)
}

// SetUpstreamProxy 下发直通隧道使用的上游代理(nil 表示直连)。
func SetUpstreamProxy(p *UpstreamProxy) { tunnelUpstream.Store(p) }

// SetUpstreamProxyURL 按地址下发直通隧道使用的上游代理(nil 表示直连)。
func SetUpstreamProxyURL(u *url.URL) { SetUpstreamProxy(NewUpstreamProxy(u)) }

// UpstreamProxyURL 返回直通隧道当前配置的上游代理地址的副本(nil 表示直连)。
func UpstreamProxyURL() *url.URL { return tunnelUpstream.Load().URL() }

// tunnel 把连接盲转发到 host(host:port):不终止 TLS、不抓包,原样在客户端与源站
// 之间双向复制字节。配置了上游代理时经其 CONNECT 建立隧道,否则直连源站。
//
// 转发结束后交还 p.conn 的读缓冲（见 types.ReleaseReader）：调用方此后不能再读它，也不能
// 在读缓冲已交给别的 goroutine 读取的连接上调用。
func (p *Processor) tunnel(server types.Server, host string) error {
	origin, err := dialTunnelTarget(host, p.originalDst != "")
	if err != nil {
		server.LogError("直通隧道建立失败 %s: %v", host, err)
		return err
	}
	relay(p.conn.GetConn(), p.conn.GetReader(), origin, origin, halfClosedIdleTimeout)
	types.ReleaseReader(p.conn)
	return nil
}

// halfClosedIdleTimeout 是中继的一个方向结束后,检查另一方向是否还有数据的周期:一个周期里
// 一个字节都没有即断开,故从最后一个字节算起,实际在 1～2 个周期之间断开。对端关了写端却
// 迟迟不关连接时(应用整体退出而源站不理会 FIN、应用留着已被源站关闭的空闲连接),靠它释放
// 处理协程与两端连接。
const halfClosedIdleTimeout = time.Minute

// relay 在 client 与 origin 之间原样双向复制字节,两个方向都结束后关闭这两条连接。两个
// reader 是各自连接的读取侧:带缓冲时先排空缓冲(可能已含对端发来的首批字节)再读连接。
func relay(client net.Conn, clientReader io.Reader, origin net.Conn, originReader io.Reader, idle time.Duration) {
	// 中继可为长连接,清除握手期可能残留的读写超时。
	_ = client.SetDeadline(time.Time{})
	_ = origin.SetDeadline(time.Time{})

	var wg sync.WaitGroup
	wg.Go(func() { relayHalf(origin, clientReader, client, idle) })
	wg.Go(func() { relayHalf(client, originReader, origin, idle) })
	wg.Wait()
	_ = origin.Close()
	_ = client.Close()
}

// relayBufs 是中继复制用的缓冲,每块 64KB:透明连接的一端是用户态协议栈,它与网卡以最大
// 64KB 的分段卸载大段交换数据,缓冲小于一个大段就要多读写一轮。
var relayBufs = sync.Pool{New: func() any {
	b := make([]byte, 64<<10)
	return &b
}}

// relayHalf 把 src(srcConn 的读取侧)复制到 dst,直到这一方向结束。
//
// 正常读完只是对端关了写端,它可能还在等响应:把半关闭传给 dst,对向继续。对向读的正是
// dst,从此给它带上读期限,以 idle 为周期检查它是否还有数据。出错(含 dst 不支持半关闭)则
// 整条连接作废,关闭双方以唤醒对向阻塞中的复制。
//
// srcConn 的读期限到点时,这一轮复制里有数据流过就再续 idle,一个字节都没有才算出错:
// 半关闭之后陆续到达的长响应照常送完。首轮的字节数从中继开始算起,半关闭之前流过数据的
// 连接至少续一次。
func relayHalf(dst net.Conn, src io.Reader, srcConn net.Conn, idle time.Duration) {
	buf := relayBufs.Get().(*[]byte)
	defer relayBufs.Put(buf)
	// 两端各包一层以绕开 ReadFrom / WriteTo,让复制用上 buf。中继的入站一端总带着限速包装或
	// TLS,不是裸的内核 socket,用不上 splice,不绕开它们也只会退回 io.Copy 另分配一块 32KB 的缓冲。
	w, r := struct{ io.Writer }{dst}, struct{ io.Reader }{src}
	n, err := io.CopyBuffer(w, r, *buf)
	for n > 0 && isTimeout(err) {
		_ = srcConn.SetReadDeadline(time.Now().Add(idle))
		n, err = io.CopyBuffer(w, r, *buf)
	}
	if err == nil {
		err = types.CloseWrite(dst)
	}
	if err != nil {
		_ = dst.Close()
		_ = srcConn.Close()
		return
	}
	_ = dst.SetReadDeadline(time.Now().Add(idle))
}

// dialTunnelTarget 为直通隧道建立到 host(host:port)的连接:直连或经上游代理 CONNECT。
// transparent 表示连接经虚拟网卡而来,上游在本机时直连(见 UpstreamProxy.For)。
func dialTunnelTarget(host string, transparent bool) (net.Conn, error) {
	dial := (&net.Dialer{Timeout: TLSHandshakeTimeout}).DialContext
	if fn := tunnelDialContext.Load(); fn != nil {
		dial = *fn
	}
	up := tunnelUpstream.Load().For(transparent)
	if up == nil {
		return dial(context.Background(), "tcp", host)
	}
	praw, err := dial(context.Background(), "tcp", proxyDialAddr(up))
	if err != nil {
		return nil, err
	}
	br, err := tunnelViaProxy(praw, host, up, TLSHandshakeTimeout)
	if err != nil {
		_ = praw.Close()
		return nil, err
	}
	// 服务端先说话的协议(SMTP、SSH)下,代理可能把 banner 与 CONNECT 应答放在同一批发来,
	// 这些字节已经读进 br,后续读取要先从它取。
	if br.Buffered() > 0 {
		return &readerConn{Conn: praw, reader: br}, nil
	}
	return praw, nil
}

// proxyDialAddr 返回上游代理的拨号地址,缺省端口按 scheme 补全。
func proxyDialAddr(u *url.URL) string {
	if u.Port() != "" {
		return u.Host
	}
	if u.Scheme == "https" {
		return net.JoinHostPort(u.Hostname(), "443")
	}
	return net.JoinHostPort(u.Hostname(), "80")
}

// tunnelViaProxy 在已建立的上游代理连接上发起 CONNECT,建成到 target 的隧道。返回读应答用的
// 缓冲读取器:应答之后的字节属于隧道,可能已有一部分被读进它。
func tunnelViaProxy(conn net.Conn, target string, proxyURL *url.URL, timeout time.Duration) (*bufio.Reader, error) {
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
			auth := proxyURL.User.Username() + ":" + pw
			req.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(auth)))
		}
	}
	if err := req.Write(conn); err != nil {
		return nil, err
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("上游代理 CONNECT 失败: %s", resp.Status)
	}
	return br, nil
}
