// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package core

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// proxyURLFor 返回上游客户端 Transport 对给定目标会选用的代理 URL(nil=直连)。
func proxyURLFor(t *testing.T, c *http.Client, target string) string {
	t.Helper()
	tr, ok := c.Transport.(*http.Transport)
	if !ok || tr.Proxy == nil {
		t.Fatalf("upstream client 缺少可配置的 Transport.Proxy")
	}
	u, err := tr.Proxy(httptest.NewRequest(http.MethodGet, target, nil))
	if err != nil {
		t.Fatalf("Proxy 闭包返回错误: %v", err)
	}
	if u == nil {
		return ""
	}
	return u.String()
}

// TestSetUpstreamProxy 校验上游代理在运行时即时切换:默认直连、设置后生效、
// 无 scheme 时补 http://、清空后恢复直连。
func TestSetUpstreamProxy(t *testing.T) {
	e := &Engine{}
	e.upstream = e.buildUpstreamClient()
	const target = "http://example.com/x"

	if got := proxyURLFor(t, e.upstream, target); got != "" {
		t.Fatalf("默认应直连, 却选用了代理 %q", got)
	}

	if err := e.SetUpstreamProxy("http://127.0.0.1:7777"); err != nil {
		t.Fatalf("SetUpstreamProxy: %v", err)
	}
	if got := proxyURLFor(t, e.upstream, target); got != "http://127.0.0.1:7777" {
		t.Fatalf("设置后代理 = %q, 期望 http://127.0.0.1:7777", got)
	}

	// 不含 scheme 时按 http:// 解析。
	if err := e.SetUpstreamProxy("127.0.0.1:8888"); err != nil {
		t.Fatalf("SetUpstreamProxy(无 scheme): %v", err)
	}
	if got := proxyURLFor(t, e.upstream, target); got != "http://127.0.0.1:8888" {
		t.Fatalf("无 scheme 地址 = %q, 期望 http://127.0.0.1:8888", got)
	}

	// 空地址恢复直连。
	if err := e.SetUpstreamProxy("   "); err != nil {
		t.Fatalf("SetUpstreamProxy(空): %v", err)
	}
	if got := proxyURLFor(t, e.upstream, target); got != "" {
		t.Fatalf("清空后应直连, 却选用了代理 %q", got)
	}
}

// TestUpstreamProxyEndToEnd 用一个假上游代理验证请求确实经代理转发:
// 目标域名不可解析,若没走代理必然失败,从而证明代理真实生效。
func TestUpstreamProxyEndToEnd(t *testing.T) {
	var hits int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		// 转发代理收到的是绝对 URI 请求行;这里直接应答以证明流量到达。
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("via-proxy"))
	}))
	defer proxy.Close()

	e := &Engine{}
	e.upstream = e.buildUpstreamClient()
	if err := e.SetUpstreamProxy(proxy.URL); err != nil {
		t.Fatalf("SetUpstreamProxy: %v", err)
	}

	resp, err := e.upstream.Get("http://nonexistent.invalid/")
	if err != nil {
		t.Fatalf("经代理请求失败(代理未生效?): %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d, 期望 200", resp.StatusCode)
	}
	if atomic.LoadInt32(&hits) == 0 {
		t.Fatal("请求未经过上游代理")
	}
}
