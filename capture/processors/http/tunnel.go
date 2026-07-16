// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package http

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/mintfog/sniffy/capture/types"
)

// tunnelUpstream 持有当前上游代理地址(nil = 直连),供直通隧道使用。
// 与引擎自建上游客户端各自独立:MITM 转发经客户端的 Proxy 闭包,直通隧道经此裸 URL 建 CONNECT。
// 由 SetUpstreamProxyURL 原子写入,与引擎的 SetUpstreamProxy 保持同步。
var tunnelUpstream atomic.Pointer[url.URL]

// SetUpstreamProxyURL 下发直通隧道使用的上游代理地址(nil 表示直连)。
func SetUpstreamProxyURL(u *url.URL) {
	if u == nil {
		tunnelUpstream.Store(nil)
		return
	}
	cp := *u
	tunnelUpstream.Store(&cp)
}

// tunnel 对不在解密范围的 CONNECT 目标做盲转发:不终止 TLS、不抓包,原样在客户端与源站
// 之间双向复制字节。配置了上游代理时经其 CONNECT 建立隧道,否则直连源站。
func (p *Processor) tunnel(server types.Server, reader *bufio.Reader) error {
	host := p.request.Host
	origin, err := dialTunnelTarget(host)
	if err != nil {
		server.LogError("直通隧道建立失败 %s: %v", host, err)
		return err
	}
	defer origin.Close()

	client := p.conn.GetConn()
	// 直通隧道可为长连接,清除握手期可能残留的读写超时。
	_ = client.SetDeadline(time.Time{})
	_ = origin.SetDeadline(time.Time{})

	errc := make(chan error, 2)
	// 客户端 → 源站:从 reader 读,先排空 bufio 缓冲(可能已含首个 TLS 记录)再读裸连接。
	go func() { _, e := io.Copy(origin, reader); errc <- e }()
	// 源站 → 客户端:直接写裸连接(200 响应此前已由 bufio writer 刷出,其缓冲为空)。
	go func() { _, e := io.Copy(client, origin); errc <- e }()

	// 任一方向结束即关闭双方,唤醒另一方向阻塞中的 Copy;等其退出后收尾。
	<-errc
	_ = origin.Close()
	_ = client.Close()
	<-errc
	return nil
}

// dialTunnelTarget 为直通隧道建立到 host(host:port)的连接:直连或经上游代理 CONNECT。
func dialTunnelTarget(host string) (net.Conn, error) {
	up := tunnelUpstream.Load()
	if up == nil {
		return net.DialTimeout("tcp", host, TLSHandshakeTimeout)
	}
	praw, err := net.DialTimeout("tcp", proxyDialAddr(up), TLSHandshakeTimeout)
	if err != nil {
		return nil, err
	}
	if err := tunnelViaProxy(praw, host, up, TLSHandshakeTimeout); err != nil {
		_ = praw.Close()
		return nil, err
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

// tunnelViaProxy 在已建立的上游代理连接上发起 CONNECT,建成到 target 的隧道。
func tunnelViaProxy(conn net.Conn, target string, proxyURL *url.URL, timeout time.Duration) error {
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
		return err
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("上游代理 CONNECT 失败: %s", resp.Status)
	}
	return nil
}
