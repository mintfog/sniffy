// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package http

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/mintfog/sniffy/internal/flow"
)

// proxyAuthorizationHeader 是客户端向 Sniffy 出示凭据的头名。它是逐跳头:
// 只属于「客户端 → Sniffy」这一跳,校验后必须就地剔除,不得继续外传。
const proxyAuthorizationHeader = "Proxy-Authorization"

// proxyAuthConfig 是 Sniffy 监听端自己要求的凭据,与上游代理凭据无关:前者认的是连到
// Sniffy 的客户端,后者认的是 Sniffy 连上游时的身份。
type proxyAuthConfig struct {
	enabled  bool
	username string
	password string
}

var listenerProxyAuth atomic.Pointer[proxyAuthConfig]

func init() {
	listenerProxyAuth.Store(&proxyAuthConfig{})
}

// SetProxyAuth 更新监听端要求的凭据。是否强制认证只看 enabled:开着而账号或密码为空
// 时一律拒绝,不能让半配置好的开关把监听端静默地敞开。
func SetProxyAuth(enabled bool, username, password string) {
	listenerProxyAuth.Store(&proxyAuthConfig{enabled: enabled, username: username, password: password})
}

// checkProxyAuthorization 校验 Proxy-Authorization,只接受 Basic,解析后定长比较两侧,
// 避免把畸形的 scheme 或凭据当成通过。
func checkProxyAuthorization(r *http.Request) bool {
	c := listenerProxyAuth.Load()
	if c == nil || !c.enabled {
		return true
	}
	// 凭据不全时一律拒绝:否则任何人都能用空密码猜中(`Basic dXNlcjo=`)。
	if c.username == "" || c.password == "" {
		return false
	}
	raw := strings.TrimSpace(r.Header.Get(proxyAuthorizationHeader))
	if len(raw) < len("Basic ") || !strings.EqualFold(raw[:len("Basic ")], "Basic ") {
		return false
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(raw[len("Basic "):]))
	if err != nil {
		return false
	}
	parts := strings.SplitN(string(decoded), ":", 2)
	if len(parts) != 2 {
		return false
	}
	// 两侧都先算完再合并:用 && 短路会因「是否继续比较密码」而泄漏用户名是否命中。
	userOK := constantTimeEqualString(parts[0], c.username)
	passOK := constantTimeEqualString(parts[1], c.password)
	return userOK && passOK
}

// constantTimeEqualString 先摘要再定长比较。直接对原文调用 ConstantTimeCompare
// 会在长度不等时立刻返回,把凭据长度暴露给计时观测;摘要把两侧统一成 32 字节。
func constantTimeEqualString(got, want string) bool {
	g := sha256.Sum256([]byte(got))
	w := sha256.Sum256([]byte(want))
	return subtle.ConstantTimeCompare(g[:], w[:]) == 1
}

// stripProxyAuthorization 剔除客户端出示给 Sniffy 的代理凭据,无论认证是否开启 ——
// 没开时客户端也可能带着上一跳代理的凭据,同样不该外传。头表与 ctx 里的原始头序列
// 两处都要清:前者决定抓包记录与普通转发,后者会被 WebSocket 保真握手写给源站。
//
// 返回值可能是带新 ctx 的副本,调用方必须改用它。
func stripProxyAuthorization(req *http.Request) *http.Request {
	if req == nil {
		return req
	}
	req.Header.Del(proxyAuthorizationHeader)

	raw, ok := flow.RawHeadersFrom(req.Context())
	if !ok {
		return req
	}
	// 绝大多数请求不带该头,先探测再决定是否复制,避免热路径上无谓的分配。
	hit := false
	for _, kv := range raw {
		if strings.EqualFold(kv[0], proxyAuthorizationHeader) {
			hit = true
			break
		}
	}
	if !hit {
		return req
	}
	filtered := make([][2]string, 0, len(raw)-1)
	for _, kv := range raw {
		if strings.EqualFold(kv[0], proxyAuthorizationHeader) {
			continue
		}
		filtered = append(filtered, kv)
	}
	return req.WithContext(flow.WithRawHeaders(req.Context(), filtered))
}

// writeProxyAuthChallenge 在记录 flow、转发上游之前挡下未认证的客户端。连带关闭连接,
// 免得客户端在一次失败认证之后继续在同一条连接上夹带请求。
func writeProxyAuthChallenge(w interface {
	WriteString(string) (int, error)
	Flush() error
}) error {
	_, err := w.WriteString("HTTP/1.1 407 Proxy Authentication Required\r\n" +
		"Proxy-Authenticate: Basic realm=\"Sniffy\"\r\n" +
		"Connection: close\r\nContent-Length: 0\r\n\r\n")
	if err != nil {
		return err
	}
	return w.Flush()
}
