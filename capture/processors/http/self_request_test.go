// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package http

import (
	"bufio"
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/internal/outboundtls"
	"github.com/mintfog/sniffy/internal/pipeline"
)

func TestFlowPipelineSelfTargetDecisions(t *testing.T) {
	const self = "http://127.0.0.1:8080/"
	const origin = "http://example.test/rewritten"
	for _, tc := range []struct {
		name, target, rewrite string
		decision              flow.Decision
		wantForward           string
		wantState             flow.FlowState
	}{
		{"Mock自身目标", self, "", flow.MockDecision("mock"), "", flow.StateMocked},
		{"改写自身目标到源站", self, origin, flow.ContinueDecision(), origin, flow.StateCompleted},
		{"改写源站到自身目标", origin, self, flow.ContinueDecision(), "", flow.StateBlocked},
	} {
		t.Run(tc.name, func(t *testing.T) {
			preserveHTTPGlobals(t)
			pipe := pipeline.New(nil, nil)
			pipe.Register(&flowDecisionHook{onRequest: func(f *flow.Flow) flow.Decision {
				f.Response = &flow.Response{Status: http.StatusOK, Body: []byte("mocked")}
				if tc.rewrite != "" {
					f.Request.URL = tc.rewrite
				}
				return tc.decision
			}})
			activePipeline = pipe
			sink := &collectingSink{}
			flowSink, processResolver = sink, nil
			var forwarded string
			transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
				forwarded = r.URL.String()
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: http.NoBody, Request: r}, nil
			})
			sharedHttpClient = &http.Client{Transport: outboundtls.NewTransport(nil, transport, nil)}
			request := httptest.NewRequest(http.MethodGet, tc.target, nil)
			local := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8080}
			if err := runFlowPipeline(silentServer{}, request, flow.ProtoHTTP, nil, local, &branchResponder{}); err != nil {
				t.Fatal(err)
			}
			if f := sink.last(); f == nil || f.State != tc.wantState || forwarded != tc.wantForward {
				t.Fatalf("记录=%v forward=%q，期望状态 %s、forward=%q", f, forwarded, tc.wantState, tc.wantForward)
			}
		})
	}
}

func TestHTTPProcessorRejectsSelfRequest(t *testing.T) {
	preserveHTTPGlobals(t)
	restoreScopeGlobals(t)
	SetDecryptScope(false, "all", nil, nil)
	SetUpstreamProxyURL(nil)
	activePipeline = pipeline.New(nil, nil)
	var forwarded int
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		forwarded++
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: http.NoBody}, nil
	})
	sharedHttpClient = &http.Client{Transport: outboundtls.NewTransport(nil, transport, nil)}

	for _, tc := range []struct {
		name, raw  string
		wantStatus int
		wantState  flow.FlowState
	}{
		{"代理自身目标", "GET http://127.0.0.1:8080/self-loop HTTP/1.1\r\nHost: 127.0.0.1:8080\r\n\r\n", http.StatusLoopDetected, flow.StateBlocked},
		{"CONNECT", "CONNECT 127.0.0.1:8080 HTTP/1.1\r\nHost: 127.0.0.1:8080\r\n\r\n", http.StatusLoopDetected, ""},
		{"WebSocket", "GET / HTTP/1.1\r\nHost: 127.0.0.1:8080\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n", http.StatusLoopDetected, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sink := &collectingSink{}
			flowSink = sink
			c := newMockConn(tc.raw)
			p := New(newMockConnection(c, newMockServer())).(*Processor)
			if err := p.Process(); err != nil {
				t.Fatal(err)
			}
			reader := bufio.NewReader(strings.NewReader(c.WrittenData()))
			response, err := http.ReadResponse(reader, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != tc.wantStatus || forwarded != 0 || !p.closeAfterResponse {
				t.Fatalf("自转发: status=%d, forwarded=%d, flow=%v, close=%v", response.StatusCode, forwarded, sink.last(), p.closeAfterResponse)
			}
			var state flow.FlowState
			if f := sink.last(); f != nil {
				state = f.State
			}
			if state != tc.wantState {
				t.Fatalf("记录状态=%q，期望 %q", state, tc.wantState)
			}
		})
	}
}

func TestGRPCPipelineChecksRewrittenSelfTarget(t *testing.T) {
	preserveHTTPGlobals(t)
	pipe := pipeline.New(nil, nil)
	pipe.Register(&flowDecisionHook{onRequest: func(f *flow.Flow) flow.Decision {
		f.Request.URL = "http://127.0.0.1:8080/rewritten"
		return flow.ContinueDecision()
	}})
	activePipeline = pipe
	sink := &collectingSink{}
	flowSink, processResolver, streamSink = sink, nil, nil
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Error("被拦截的请求到达了上游")
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: http.NoBody, Request: r}, nil
	})
	SetUpstreamClient(&http.Client{Transport: outboundtls.NewTransport(nil, transport, nil)})
	request := httptest.NewRequest(http.MethodPost, "http://example.test/", http.NoBody)
	request.ProtoMajor = 2
	request.Header.Set("Content-Type", "application/grpc")
	local := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8080}
	r := &branchResponder{streamOK: true, stream: &captureStreamWriter{}}
	if err := runFlowPipeline(silentServer{}, request, flow.ProtoHTTP, nil, local, r); err != nil {
		t.Fatal(err)
	}
	if r.aborted == nil || r.aborted.StatusOnAbort != http.StatusLoopDetected || !r.reuseDisabled {
		t.Fatalf("abort=%v disableReuse=%v，期望返回 508 并关闭连接", r.aborted, r.reuseDisabled)
	}
	if f := sink.last(); f == nil || f.State != flow.StateBlocked {
		t.Fatalf("完成记录=%v，期望 blocked", f)
	}
}

func TestFlowPipelineUsesStreamClientProxyRoute(t *testing.T) {
	for _, kind := range []string{"SSE", "gRPC"} {
		t.Run(kind, func(t *testing.T) {
			preserveHTTPGlobals(t)
			activePipeline, flowSink, processResolver, streamSink = nil, nil, nil, nil
			contentType, payload := "text/event-stream", []byte("data: ok\n\n")
			if kind == "gRPC" {
				contentType, payload = "application/grpc", grpcFrameBytes([]byte("ok"), false)
			}
			received := make(chan string, 1)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				received <- r.URL.String()
				w.Header().Set("Content-Type", contentType)
				_, _ = w.Write(payload)
			}))
			t.Cleanup(upstream.Close)
			proxyURL, err := url.Parse(upstream.URL)
			if err != nil {
				t.Fatal(err)
			}
			transport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
			t.Cleanup(transport.CloseIdleConnections)
			sharedHttpClient = &http.Client{Transport: &http.Transport{}}
			sharedStreamClient = &http.Client{Transport: transport, Timeout: 3 * time.Second}
			const target = "http://127.0.0.1:8080/remote-service"
			request := httptest.NewRequest(http.MethodPost, target, http.NoBody)
			request.ProtoMajor = 2
			request.Header.Set("Accept", "text/event-stream")
			if kind == "gRPC" {
				request.Header.Set("Content-Type", "application/grpc")
			}
			local := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8080}
			sw := &captureStreamWriter{}
			r := &fakeResponder{sw: sw}
			if err := runFlowPipeline(silentServer{}, request, flow.ProtoHTTP, nil, local, r); err != nil {
				t.Fatal(err)
			}
			if r.aborted != nil || sw.status != http.StatusOK || string(sw.body()) != string(payload) {
				t.Fatalf("abort=%v status=%d body=%q，期望上游的 200 响应", r.aborted, sw.status, sw.body())
			}
			select {
			case got := <-received:
				if got != target {
					t.Fatalf("上游收到目标 %q，期望 %q", got, target)
				}
			default:
				t.Fatal("请求未到达上游代理")
			}
		})
	}
}

func TestSelfRequestTarget(t *testing.T) {
	for _, tc := range []struct {
		name, target, local string
		isHTTPS, want       bool
	}{
		{"IPv4", "http://127.0.0.1:8080/", "127.0.0.1:8080", false, true},
		{"IPv6", "http://[::1]:8080/", "[::1]:8080", false, true},
		{"IPv4映射", "http://[::ffff:127.0.0.1]:8080/", "127.0.0.1:8080", false, true},
		{"默认HTTP端口", "http://127.0.0.1/", "127.0.0.1:80", false, true},
		{"默认HTTPS端口", "https://127.0.0.1/", "127.0.0.1:443", false, true},
		{"隧道内HTTPS", "//127.0.0.1/", "127.0.0.1:443", true, true},
		{"显式HTTP地址", "http://127.0.0.1/", "127.0.0.1:443", true, false},
		{"同机其他端口", "http://127.0.0.1:8081/", "127.0.0.1:8080", false, false},
		{"同端口其他主机", "http://192.0.2.2:8080/", "192.0.2.1:8080", false, false},
		{"同端口其他域名", "http://example.test:8080/", "127.0.0.1:8080", false, false},
		{"localhost实际端点待定", "http://localhost:8080/", "127.0.0.1:8080", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := http.NewRequest(http.MethodGet, tc.target, nil)
			if err != nil {
				t.Fatal(err)
			}
			local, err := net.ResolveTCPAddr("tcp", tc.local)
			if err != nil {
				t.Fatal(err)
			}
			if got := isSelfRequest(r, local, tc.isHTTPS); got != tc.want {
				t.Fatalf("自转发判定 = %v, want %v", got, tc.want)
			}
		})
	}
}

func BenchmarkSelfRequestCheck(b *testing.B) {
	local := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8080}
	for _, target := range []string{"http://example.com/", "https://example.com/", "http://example.com:8080/", "http://127.0.0.1:8080/", "http://localhost:8080/"} {
		b.Run(target, func(b *testing.B) {
			r, err := http.NewRequest(http.MethodGet, target, nil)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				isSelfRequest(r, local, false)
			}
		})
	}
}

func TestTunnelUsesCheckedDialContext(t *testing.T) {
	previousDial, previousUpstream := tunnelDialContext.Load(), tunnelUpstream.Load()
	t.Cleanup(func() {
		if previousDial == nil {
			SetDialContext(nil)
		} else {
			SetDialContext(*previousDial)
		}
		tunnelUpstream.Store(previousUpstream)
	})
	blocked := errors.New("测试拨号已被拦截")
	for _, tc := range []struct {
		name, wantAddress string
		upstream          *url.URL
	}{
		{"直连目标", "origin.test:443", nil},
		{"上游代理首跳", "proxy.test:8080", &url.URL{Scheme: "http", Host: "proxy.test:8080"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			SetDialContext(func(_ context.Context, network, address string) (net.Conn, error) {
				if network != "tcp" || address != tc.wantAddress {
					t.Fatalf("network=%q address=%q，期望 tcp %s", network, address, tc.wantAddress)
				}
				return nil, blocked
			})
			SetUpstreamProxyURL(tc.upstream)
			conn, err := dialTunnelTarget("origin.test:443")
			if conn != nil || !errors.Is(err, blocked) {
				t.Fatalf("conn=%v err=%v，期望拨号检查拦截连接", conn, err)
			}
		})
	}
}
