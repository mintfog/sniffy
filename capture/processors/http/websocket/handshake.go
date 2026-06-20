// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package websocket

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mintfog/sniffy/internal/flow"
)

const wsDialTimeout = 30 * time.Second

// dialUpstreamFaithful 连接上游并以「保真」方式转发客户端的 WebSocket 握手请求,
// 返回:上游连接、其读缓冲、上游握手响应的原始字节、状态码。
func (p *Processor) dialUpstreamFaithful() (net.Conn, *bufio.Reader, []byte, int, error) {
	host := p.request.Host
	if host == "" {
		host = p.request.URL.Host
	}
	raw, err := (&net.Dialer{Timeout: wsDialTimeout}).Dial("tcp", wsHostPort(host, p.isHttps))
	if err != nil {
		return nil, nil, nil, 0, err
	}

	conn := net.Conn(raw)
	if p.isHttps {
		tc := tls.Client(raw, &tls.Config{
			ServerName:         hostnameOnly(host),
			InsecureSkipVerify: true,
			NextProtos:         []string{"http/1.1"}, // WebSocket 走 http/1.1
		})
		_ = tc.SetDeadline(time.Now().Add(wsDialTimeout))
		if err := tc.Handshake(); err != nil {
			_ = raw.Close()
			return nil, nil, nil, 0, err
		}
		_ = tc.SetDeadline(time.Time{})
		conn = tc
	}

	if err := writeFaithfulHandshake(conn, p.request); err != nil {
		_ = conn.Close()
		return nil, nil, nil, 0, err
	}

	br := bufio.NewReaderSize(conn, 64*1024)
	respBytes, status, err := readHandshakeResponse(br)
	if err != nil {
		_ = conn.Close()
		return nil, nil, nil, 0, err
	}
	return conn, br, respBytes, status, nil
}

// writeFaithfulHandshake 把客户端的 WebSocket 升级请求按原样(顺序+大小写)写到上游。
// 优先用读取侧抓到的原始头序列 —— 它含客户端自己的 Sec-WebSocket-Key,使上游回的
// Sec-WebSocket-Accept 与客户端预期一致(我们随后把该 101 原样转回客户端,校验自洽)。
// 缺失原始序列时回退按 Header map 重建(顺序不保真,但功能可用)。
func writeFaithfulHandshake(w io.Writer, req *http.Request) error {
	var b strings.Builder
	b.WriteString(req.Method)
	b.WriteByte(' ')
	b.WriteString(req.URL.RequestURI()) // origin-form:/path?query
	b.WriteString(" HTTP/1.1\r\n")

	if raw, ok := flow.RawHeadersFrom(req.Context()); ok {
		for _, kv := range raw {
			b.WriteString(kv[0])
			b.WriteString(": ")
			b.WriteString(kv[1])
			b.WriteString("\r\n")
		}
	} else {
		// 回退:Host 优先(http.ReadRequest 把它从 Header 挪到 req.Host),其余按 map(无序)。
		if req.Host != "" {
			b.WriteString("Host: ")
			b.WriteString(req.Host)
			b.WriteString("\r\n")
		}
		for k, vs := range req.Header {
			for _, v := range vs {
				b.WriteString(k)
				b.WriteString(": ")
				b.WriteString(v)
				b.WriteString("\r\n")
			}
		}
	}
	b.WriteString("\r\n")
	_, err := io.WriteString(w, b.String())
	return err
}

// readHandshakeResponse 读出上游握手响应的完整头块原始字节,并解析状态码。
// 101 无 body;非 101 仅返回头块,其 body 由调用方另行透传。
func readHandshakeResponse(br *bufio.Reader) ([]byte, int, error) {
	var buf []byte
	status, first := 0, true
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return nil, 0, err
		}
		buf = append(buf, line...)
		if first {
			first = false
			status = parseStatusCode(line)
		}
		if line == "\r\n" || line == "\n" {
			return buf, status, nil
		}
		if len(buf) > 64*1024 {
			return nil, 0, fmt.Errorf("websocket: 上游握手响应头过大")
		}
	}
}

// parseStatusCode 从状态行(如 "HTTP/1.1 101 Switching Protocols")解析状态码。
func parseStatusCode(statusLine string) int {
	f := strings.Fields(statusLine)
	if len(f) < 2 {
		return 0
	}
	n, _ := strconv.Atoi(f[1])
	return n
}

// wsHostPort 把 host 补全为 host:port(ws→80, wss→443)。
func wsHostPort(host string, secure bool) string {
	if _, _, err := net.SplitHostPort(host); err == nil {
		return host
	}
	if secure {
		return net.JoinHostPort(host, "443")
	}
	return net.JoinHostPort(host, "80")
}

// hostnameOnly 去掉 host 的端口部分(用作 TLS ServerName)。
func hostnameOnly(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return host
}
