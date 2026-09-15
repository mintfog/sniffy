// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package outboundtls

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"strings"
)

type http1OnlyContextKey struct{}

// 与 net/http 的 requiresHTTP1 使用相同的首个头值和 ASCII 边界,
// 使拨号约束与 Transport 的 onlyH1 连接池键一致。
func requiresHTTP1(req *http.Request) bool {
	if !matchesLowerASCII(req.Header.Get("Upgrade"), "websocket") {
		return false
	}
	for token := range strings.FieldsFuncSeq(req.Header.Get("Connection"), func(r rune) bool {
		return r == ' ' || r == '\t' || r == ','
	}) {
		if matchesLowerASCII(token, "upgrade") {
			return true
		}
	}
	return false
}

func matchesLowerASCII(value, lower string) bool {
	if len(value) != len(lower) {
		return false
	}
	for i := 0; i < len(value); i++ {
		b := value[i]
		if b >= 'A' && b <= 'Z' {
			b += 'a' - 'A'
		}
		if b != lower[i] {
			return false
		}
	}
	return true
}

// ConfigureInsecureHTTPTransport 只能用于 Transport 的例外连接池,且必须在首次请求前调用。
// net/http 把同一 TLS 配置用于源站和 HTTPS 代理;首跳独立拨号保留真实目标名,
// 而 CONNECT 后的 TLS 由精确主机分流器保证仅应用到已允许的源站。
func (p *Policy) ConfigureInsecureHTTPTransport(tr *http.Transport) {
	connectConfig := tr.TLSClientConfig
	if connectConfig == nil {
		connectConfig = &tls.Config{RootCAs: p.rootCAs()}
	} else {
		connectConfig = connectConfig.Clone()
	}
	connectConfig.ServerName = ""
	connectConfig.InsecureSkipVerify = true
	connectConfig.VerifyConnection = func(state tls.ConnectionState) error {
		// IP 的空 SNI 只会出现在 CONNECT 后;首跳始终使用下面的按地址验证。
		if state.ServerName == "" {
			return nil
		}
		return p.verifyConnection(connectConfig, state, state.ServerName)
	}
	tr.TLSClientConfig = connectConfig
	tr.DialTLSContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		var raw net.Conn
		if tr.DialContext != nil {
			raw, err = tr.DialContext(ctx, network, addr)
		} else if tr.Dial != nil {
			raw, err = tr.Dial(network, addr)
		} else {
			raw, err = (&net.Dialer{}).DialContext(ctx, network, addr)
		}
		if err != nil {
			return nil, err
		}
		firstHopConfig := tr.TLSClientConfig.Clone()
		if onlyH1, _ := ctx.Value(http1OnlyContextKey{}).(bool); onlyH1 {
			// 自定义拨号绕过 net/http 的 onlyH1 ALPN 处理,需恢复同样约束。
			firstHopConfig.NextProtos = nil
		}
		firstHopConfig.ServerName = host
		firstHopConfig.InsecureSkipVerify = p.AllowsInsecure(host)
		firstHopConfig.VerifyConnection = nil
		if firstHopConfig.InsecureSkipVerify {
			firstHopConfig.VerifyConnection = func(state tls.ConnectionState) error {
				return p.verifyConnection(firstHopConfig, state, host)
			}
		}
		tlsConn := tls.Client(raw, firstHopConfig)
		handshakeContext := ctx
		if tr.TLSHandshakeTimeout > 0 {
			var cancel context.CancelFunc
			handshakeContext, cancel = context.WithTimeout(ctx, tr.TLSHandshakeTimeout)
			defer cancel()
		}
		if err := tlsConn.HandshakeContext(handshakeContext); err != nil {
			_ = raw.Close()
			return nil, err
		}
		return tlsConn, nil
	}
}
