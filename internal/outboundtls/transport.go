// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package outboundtls

import (
	"context"
	"net/http"
	"net/url"
)

// Transport 隔离严格与例外连接池,防止撤销例外后复用未经校验的旧连接。
type Transport struct {
	policy   *Policy
	secure   http.RoundTripper
	insecure http.RoundTripper
}

// NewTransport 按策略分流;secure 为 nil 时使用 http.DefaultTransport,insecure 为 nil 时复用 secure。
// 启用证书例外时,调用方须提供独立的严格与例外连接池。
func NewTransport(policy *Policy, secure, insecure http.RoundTripper) *Transport {
	if secure == nil {
		secure = http.DefaultTransport
	}
	if insecure == nil {
		insecure = secure
	}
	return &Transport{policy: policy, secure: secure, insecure: insecure}
}

func (t *Transport) selectTransport(req *http.Request) (transport http.RoundTripper, insecure bool) {
	if req != nil && req.URL != nil && t.policy.AllowsInsecure(req.URL.Hostname()) {
		return t.insecure, true
	}
	return t.secure, false
}

func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	tr, insecure := t.selectTransport(req)
	if insecure && requiresHTTP1(req) {
		req = req.WithContext(context.WithValue(req.Context(), http1OnlyContextKey{}, true))
	}
	return tr.RoundTrip(req)
}

func (t *Transport) CloseIdleConnections() {
	for _, tr := range []http.RoundTripper{t.secure, t.insecure} {
		if closer, ok := tr.(interface{ CloseIdleConnections() }); ok {
			closer.CloseIdleConnections()
		}
	}
}

// ResolveProxy 保留底层转发器的代理自检能力,并使用与 RoundTrip 相同的目标选择。
func (t *Transport) ResolveProxy(req *http.Request) (*url.URL, error) {
	tr, _ := t.selectTransport(req)
	if resolver, ok := tr.(interface {
		ResolveProxy(*http.Request) (*url.URL, error)
	}); ok {
		return resolver.ResolveProxy(req)
	}
	if transport, ok := tr.(*http.Transport); ok && transport.Proxy != nil {
		return transport.Proxy(req)
	}
	return nil, nil
}
