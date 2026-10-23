// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package core

import (
	"bufio"
	"bytes"
	"context"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mintfog/sniffy/ca"
	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/internal/netinfo"
	"github.com/mintfog/sniffy/internal/pipeline"
)

func TestEnginePortalOnActualListenerAddress(t *testing.T) {
	for _, tc := range []struct {
		name, bind, host, localIP string
	}{
		{"IPv4动态端口", "127.0.0.1", "127.0.0.1", "127.0.0.1"},
		{"localhost", "127.0.0.1", "localhost", "127.0.0.1"},
		{"通配监听", "0.0.0.0", "127.0.0.1", "127.0.0.1"},
		{"IPv6动态端口", "[::1]", "::1", "::1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.host == "::1" {
				listener, err := net.Listen("tcp6", "[::1]:0")
				if err != nil {
					t.Skipf("当前环境没有 IPv6 回环监听: %v", err)
				}
				_ = listener.Close()
			}
			engine := newProbeEngine(t)
			engine.config.(*coreTestConfig).address = tc.bind
			proxyClient := startProbeEngine(t, engine)
			_, port, err := net.SplitHostPort(engine.Listener().GetAddress())
			if err != nil || port == "0" {
				t.Fatalf("实际监听端口无效: %q, %v", engine.Listener().GetAddress(), err)
			}
			proxyAddress := &url.URL{Scheme: "http", Host: net.JoinHostPort(tc.localIP, port)}
			proxyClient.Transport.(*http.Transport).Proxy = http.ProxyURL(proxyAddress)
			target := "http://" + net.JoinHostPort(tc.host, port) + "/"
			for route, client := range map[string]*http.Client{"直连": newPortalClient(t), "代理": proxyClient} {
				response, body := fetch(t, client, target, route)
				if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/html") {
					t.Fatalf("%s入口页响应: status=%d type=%q body=%q", route, response.StatusCode, response.Header.Get("Content-Type"), body)
				}
				if !bytes.Contains(body, []byte("Sniffy")) || !bytes.Contains(body, []byte(`<code id="proxy-port">`+port+`</code>`)) {
					t.Fatalf("%s入口页缺少 Sniffy 名称或实际监听端口 %s", route, port)
				}
				if tc.bind != "0.0.0.0" && !bytes.Contains(body, []byte(`id="proxy-host">`+tc.localIP+`</code>`)) {
					t.Fatalf("%s入口页未显示绑定地址 %s", route, tc.localIP)
				}
			}
		})
	}
}

func TestEngineProxyPortalAcrossListenerAddresses(t *testing.T) {
	addresses := netinfo.LANIPs()
	if len(addresses) == 0 {
		t.Skip("当前环境没有可用的局域网 IPv4 地址")
	}
	lanIP := addresses[0].IP
	for _, tc := range []struct {
		name, proxy, target string
		pipeline            bool
	}{
		{"回环代理访问局域网", "127.0.0.1", lanIP, false},
		{"局域网代理访问回环", lanIP, "127.0.0.1", false},
		{"管道回环代理访问局域网", "127.0.0.1", lanIP, true},
		{"管道局域网代理访问回环", lanIP, "127.0.0.1", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine := newProbeEngine(t)
			engine.config.(*coreTestConfig).address = "0.0.0.0"
			if tc.pipeline {
				engine.SetPipeline(pipeline.New(nil, nil))
			}
			client := startProbeEngine(t, engine)
			_, port, err := net.SplitHostPort(engine.Listener().GetAddress())
			if err != nil {
				t.Fatal(err)
			}
			proxy := &url.URL{Scheme: "http", Host: net.JoinHostPort(tc.proxy, port)}
			client.Transport.(*http.Transport).Proxy = http.ProxyURL(proxy)
			base := "http://" + net.JoinHostPort(tc.target, port)
			for _, path := range []string{"/", "/sniffy-ca.crt", "/sniffy.mobileconfig", "/sniffy-qr.png?host=" + lanIP} {
				response, body := fetch(t, client, base+path, tc.name)
				if response.StatusCode != http.StatusOK || len(body) == 0 {
					t.Errorf("%s: status=%d body=%q，期望 200 和非空响应", path, response.StatusCode, body)
				}
			}
		})
	}
}

func TestEnginePortalDownloadsCurrentCA(t *testing.T) {
	engine := newProbeEngine(t)
	proxyClient := startProbeEngine(t, engine)
	clients := map[string]*http.Client{"直连": newPortalClient(t), "代理": proxyClient}
	base := "http://" + engine.Listener().GetAddress()
	for _, stage := range []string{"初始证书", "替换证书"} {
		if stage == "替换证书" {
			if err := engine.SetCA(newCoreTestCA(t)); err != nil {
				t.Fatal(err)
			}
		}
		cert := engine.CA().GetCA()
		for route, client := range clients {
			response, body := fetch(t, client, base+"/sniffy-ca.crt", stage+"/"+route)
			block, rest := pem.Decode(body)
			if response.StatusCode != http.StatusOK || block == nil || block.Type != "CERTIFICATE" || len(bytes.TrimSpace(rest)) != 0 {
				t.Fatalf("%s/%s: 证书下载不是单个 PEM 证书: status=%d body=%q", stage, route, response.StatusCode, body)
			}
			if !bytes.Equal(block.Bytes, cert.Raw) {
				t.Fatalf("%s/%s: 下载内容与引擎当前 CA 不一致", stage, route)
			}
			response, body = fetch(t, client, base+"/sniffy.mobileconfig", stage+"/"+route)
			if response.StatusCode != http.StatusOK || !bytes.Equal(body, ca.Mobileconfig(cert)) {
				t.Fatalf("%s/%s: 描述文件与引擎当前 CA 不一致: status=%d", stage, route, response.StatusCode)
			}
		}
	}
}

func TestEngineProxyPortalHead(t *testing.T) {
	engine := newProbeEngine(t)
	engine.SetPipeline(pipeline.New(nil, nil))
	startProbeEngine(t, engine)
	address := engine.Listener().GetAddress()
	for path, content := range map[string][]byte{
		"/sniffy-ca.crt":       ca.ExportRootCertPEM(engine.CA()),
		"/sniffy.mobileconfig": ca.Mobileconfig(engine.CA().GetCA()),
	} {
		t.Run(path, func(t *testing.T) {
			conn, err := net.DialTimeout("tcp", address, 5*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
				t.Fatal(err)
			}
			request, err := http.NewRequest(http.MethodHead, "http://"+address+path, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := request.WriteProxy(conn); err != nil {
				t.Fatal(err)
			}
			reader := bufio.NewReader(conn)
			response, err := http.ReadResponse(reader, request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK || response.ContentLength != int64(len(content)) || response.Close {
				t.Fatalf("HEAD 响应: status=%d length=%d close=%v，期望 200、%d 和可复用连接", response.StatusCode, response.ContentLength, response.Close, len(content))
			}
			getRequest := request.Clone(request.Context())
			getRequest.Method = http.MethodGet
			if err := getRequest.WriteProxy(conn); err != nil {
				t.Fatal(err)
			}
			next, err := http.ReadResponse(reader, getRequest)
			if err != nil {
				t.Fatalf("HEAD 后同连接 GET 失败: %v", err)
			}
			defer next.Body.Close()
			body, err := io.ReadAll(next.Body)
			if err != nil || next.StatusCode != http.StatusOK || !bytes.Equal(body, content) {
				t.Fatalf("HEAD 后同连接 GET 响应: status=%d body=%q err=%v", next.StatusCode, body, err)
			}
		})
	}
}

type portalFlowSink struct{ calls atomic.Int32 }

func (s *portalFlowSink) RecordFlowStarted(*flow.Flow)   { s.calls.Add(1) }
func (s *portalFlowSink) RecordFlowUpdated(*flow.Flow)   { s.calls.Add(1) }
func (s *portalFlowSink) RecordFlowCompleted(*flow.Flow) { s.calls.Add(1) }

func TestEnginePortalAndProxyAuthentication(t *testing.T) {
	engine := newProbeEngine(t)
	t.Cleanup(func() { _ = engine.SetProxyAuth(false, "", "") })
	if err := engine.SetProxyAuth(true, "sniffy", "test-password"); err != nil {
		t.Fatal(err)
	}
	hook := &probeRequestHook{}
	pipe := pipeline.New(nil, nil)
	pipe.Register(hook)
	engine.SetPipeline(pipe)
	sink := &portalFlowSink{}
	engine.SetFlowSink(sink)
	proxyClient := startProbeEngine(t, engine)
	directClient := newPortalClient(t)
	base := "http://" + engine.Listener().GetAddress()
	for _, path := range []string{"/", "/sniffy-ca.crt", "/sniffy.mobileconfig"} {
		response, body := fetch(t, directClient, base+path, "公开证书入口")
		if response.StatusCode != http.StatusOK {
			t.Fatalf("直接访问 %s: status=%d body=%q，期望 200", path, response.StatusCode, body)
		}
	}
	for _, target := range []string{base + "/", "http://example.test/", "http://cert.sniffy/"} {
		response, _ := fetch(t, proxyClient, target, "代理认证")
		if response.StatusCode != http.StatusProxyAuthRequired {
			t.Fatalf("未认证代理请求 %s: status=%d，期望 407", target, response.StatusCode)
		}
	}
	if len(hook.seen()) != 0 || sink.calls.Load() != 0 {
		t.Fatalf("公开入口或未认证请求进入了抓包管道: hooks=%v flowCalls=%d", hook.seen(), sink.calls.Load())
	}

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "authenticated-origin")
	}))
	t.Cleanup(origin.Close)
	proxyURL, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	proxyURL.User = url.UserPassword("sniffy", "test-password")
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
	t.Cleanup(transport.CloseIdleConnections)
	authenticatedClient := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	for _, path := range []string{"/", "/sniffy-ca.crt", "/sniffy.mobileconfig"} {
		response, body := fetch(t, authenticatedClient, base+path, "通过认证的连接指南")
		if response.StatusCode != http.StatusOK || len(body) == 0 {
			t.Fatalf("认证后访问 %s: status=%d body=%q，期望 200", path, response.StatusCode, body)
		}
	}
	response, body := fetch(t, authenticatedClient, origin.URL, "通过认证的代理请求")
	if response.StatusCode != http.StatusOK || string(body) != "authenticated-origin" || len(hook.seen()) != 4 || sink.calls.Load() == 0 {
		t.Fatalf("认证后代理路径异常: status=%d body=%q hooks=%v flowCalls=%d", response.StatusCode, body, hook.seen(), sink.calls.Load())
	}
}

type portalRequestHook struct {
	probeRequestHook
	onRequest func(*flow.Flow) flow.Decision
}

func (h *portalRequestHook) OnRequest(ctx context.Context, f *flow.Flow) flow.Decision {
	h.probeRequestHook.OnRequest(ctx, f)
	return h.onRequest(f)
}

func TestEngineProxyPortalRespectsPluginDecisions(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "rewritten-origin")
	}))
	t.Cleanup(origin.Close)
	engine := newProbeEngine(t)
	pipe := pipeline.New(nil, nil)
	engine.SetPipeline(pipe)
	sink := newProbeFlowSink()
	engine.SetFlowSink(sink)
	client := startProbeEngine(t, engine)
	self := "http://" + engine.Listener().GetAddress() + "/"

	for _, tc := range []struct {
		name, target, rewrite, wantBody string
		decision                        flow.Decision
		wantStatus                      int
		wantState                       flow.FlowState
	}{
		{name: "连接指南", target: self, rewrite: self, wantBody: "Sniffy", wantStatus: http.StatusOK, wantState: flow.StateCompleted},
		{name: "阻断", target: self, rewrite: self, decision: flow.AbortDecision(http.StatusForbidden, "blocked-self"), wantBody: "blocked-self", wantStatus: http.StatusForbidden, wantState: flow.StateBlocked},
		{name: "自身改写到源站", target: self, rewrite: origin.URL, wantBody: "rewritten-origin", wantStatus: http.StatusOK, wantState: flow.StateCompleted},
		{name: "源站改写到自身", target: origin.URL, rewrite: self, wantBody: "代理监听端口", wantStatus: http.StatusLoopDetected, wantState: flow.StateBlocked},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hook := &portalRequestHook{onRequest: func(f *flow.Flow) flow.Decision {
				f.Request.URL = tc.rewrite
				return tc.decision
			}}
			pipe.Register(hook)
			t.Cleanup(pipe.Clear)
			response, body := fetch(t, client, tc.target, tc.name)
			f := sink.take(t, tc.name)
			if response.StatusCode != tc.wantStatus || !bytes.Contains(body, []byte(tc.wantBody)) || len(hook.seen()) != 1 {
				t.Fatalf("status=%d body=%q hooks=%v，期望 %d、%q 和一次请求钩子", response.StatusCode, body, hook.seen(), tc.wantStatus, tc.wantBody)
			}
			if f.State != tc.wantState {
				t.Fatalf("记录状态=%s，期望 %s", f.State, tc.wantState)
			}
		})
	}
}

func TestEngineProxyPortalMock(t *testing.T) {
	engine := newProbeEngine(t)
	hook := &portalRequestHook{onRequest: func(f *flow.Flow) flow.Decision {
		f.Response = &flow.Response{Status: http.StatusOK, Body: []byte("mocked-self")}
		return flow.MockDecision("mock")
	}}
	pipe := pipeline.New(nil, nil)
	pipe.Register(hook)
	engine.SetPipeline(pipe)
	sink := newProbeFlowSink()
	engine.SetFlowSink(sink)
	client := startProbeEngine(t, engine)
	target := "http://" + engine.Listener().GetAddress() + "/"
	response, body := fetch(t, client, target, "Mock")
	if response.StatusCode != http.StatusOK || string(body) != "mocked-self" || len(hook.seen()) != 1 {
		t.Fatalf("status=%d body=%q hooks=%v，期望 200、mocked-self 和一次请求钩子", response.StatusCode, body, hook.seen())
	}
	if f := sink.take(t, "Mock"); f.State != flow.StateMocked {
		t.Fatalf("记录状态=%s，期望 %s", f.State, flow.StateMocked)
	}
}

func newPortalClient(t *testing.T) *http.Client {
	t.Helper()
	transport := &http.Transport{}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport, Timeout: 5 * time.Second}
}

func TestEnginePortalInstallThroughDirectAndProxyRoutes(t *testing.T) {
	engine := newProbeEngine(t)
	proxyClient := startProbeEngine(t, engine)
	var requests atomic.Int32
	engine.SetPortalInstallHandler(func() { requests.Add(1) })
	t.Cleanup(func() { engine.SetPortalInstallHandler(nil) })
	base := "http://" + engine.Listener().GetAddress()
	for route, client := range map[string]*http.Client{"直连": newPortalClient(t), "代理": proxyClient} {
		t.Run(route, func(t *testing.T) {
			_, body := fetch(t, client, base+"/", route)
			marker := []byte(`data-install-token="`)
			_, rest, ok := bytes.Cut(body, marker)
			if !ok {
				t.Fatal("本机页面缺少安装入口")
			}
			token, _, ok := bytes.Cut(rest, []byte(`"`))
			if !ok {
				t.Fatal("本机页面缺少安装令牌")
			}
			request, err := http.NewRequest(http.MethodPost, base+"/sniffy/install-ca", nil)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Origin", base)
			request.Header.Set("X-Sniffy-Install", string(token))
			before := requests.Load()
			response, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusAccepted || requests.Load() != before+1 {
				t.Fatalf("安装确认请求失败: status=%d，触发次数=%d", response.StatusCode, requests.Load()-before)
			}
			response, script := fetch(t, client, base+"/sniffy-portal.js", route)
			if response.StatusCode != http.StatusOK || !bytes.Contains(script, []byte("X-Sniffy-Install")) {
				t.Fatalf("脚本资源不可用: status=%d", response.StatusCode)
			}
		})
	}
}
