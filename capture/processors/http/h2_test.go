// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package http

import (
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/http2"

	"github.com/mintfog/sniffy/internal/flow"
)

// collectingSink 是一个最小 FlowSink,收集已完成的 Flow 供断言。
type collectingSink struct {
	mu        sync.Mutex
	completed []*flow.Flow
}

func (s *collectingSink) RecordFlowStarted(*flow.Flow) {}
func (s *collectingSink) RecordFlowUpdated(*flow.Flow) {}
func (s *collectingSink) RecordFlowCompleted(f *flow.Flow) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.completed = append(s.completed, f.Clone())
}

func (s *collectingSink) last() *flow.Flow {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.completed) == 0 {
		return nil
	}
	return s.completed[len(s.completed)-1]
}

// TestHTTP2EndToEnd 端到端验证 h2 抓包链路:
// 客户端以 ALPN h2 连到 MITM(serveHTTP2)→ flow 管道 → 上游也走 h2 → 响应/尾部捕获并写回。
func TestHTTP2EndToEnd(t *testing.T) {
	// 1) 上游:一个支持 h2 的源站,返回已知 body 并发送一个响应尾部(模拟 gRPC 的 grpc-status)。
	upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Trailer", "Grpc-Status")
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "hello-h2:"+r.Proto)
		w.Header().Set("Grpc-Status", "0") // body 之后设置 → 作为尾部发送
	}))
	upstream.EnableHTTP2 = true
	upstream.StartTLS()
	defer upstream.Close()
	upstreamAuthority := upstream.Listener.Addr().String()

	// 2) 把代理的上游客户端指向源站,并开启对上游的 h2 协商。保存/恢复包级状态。
	defer func(c *http.Client, s FlowSink) { sharedHttpClient = c; flowSink = s }(sharedHttpClient, flowSink)
	sharedHttpClient = &http.Client{
		Transport: &http.Transport{
			TLSClientConfig:   &tls.Config{InsecureSkipVerify: true},
			ForceAttemptHTTP2: true,
		},
		Timeout: 10 * time.Second,
	}
	sink := &collectingSink{}
	flowSink = sink

	// 3) MITM 监听:接受一条连接,用本地 CA 颁发的证书 + ALPN h2 终止 TLS,然后 serveHTTP2。
	cert, err := currentCA().IssueCert("127.0.0.1")
	if err != nil {
		t.Fatalf("颁发证书失败: %v", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	defer ln.Close()

	srvErr := make(chan error, 1)
	go func() {
		raw, aerr := ln.Accept()
		if aerr != nil {
			srvErr <- aerr
			return
		}
		tlsConn := tls.Server(raw, &tls.Config{
			Certificates: []tls.Certificate{*cert},
			NextProtos:   []string{"h2", "http/1.1"},
		})
		if herr := tlsConn.Handshake(); herr != nil {
			srvErr <- herr
			return
		}
		if np := tlsConn.ConnectionState().NegotiatedProtocol; np != "h2" {
			srvErr <- &net.OpError{Op: "alpn", Err: errString("expected h2, got " + np)}
			return
		}
		srvErr <- serveHTTP2(newMockServer(), tlsConn)
	}()

	// 4) 客户端:以 ALPN h2 直连 MITM,在该单连接上跑一个 h2 ClientConn。
	clientTLS, err := tls.Dial("tcp", ln.Addr().String(), &tls.Config{
		InsecureSkipVerify: true,
		NextProtos:         []string{"h2"},
	})
	if err != nil {
		t.Fatalf("客户端 TLS 拨号失败: %v", err)
	}
	defer clientTLS.Close()
	if np := clientTLS.ConnectionState().NegotiatedProtocol; np != "h2" {
		t.Fatalf("客户端侧 ALPN 期望 h2,实得 %q", np)
	}

	tr := &http2.Transport{}
	cc, err := tr.NewClientConn(clientTLS)
	if err != nil {
		t.Fatalf("建立 h2 ClientConn 失败: %v", err)
	}

	// :authority 指向上游源站地址,使 MITM 把请求转发到真实源站(而非回环到自身)。
	req, err := http.NewRequest(http.MethodGet, "https://"+upstreamAuthority+"/echo", nil)
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	resp, err := cc.RoundTrip(req)
	if err != nil {
		t.Fatalf("h2 RoundTrip 失败: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	// 5) 断言客户端经代理收到的响应。
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码期望 200,实得 %d", resp.StatusCode)
	}
	if got := string(body); got != "hello-h2:HTTP/2.0" {
		t.Fatalf("响应体期望 %q,实得 %q", "hello-h2:HTTP/2.0", got)
	}
	// 客户端应收到经代理透传的尾部。
	if got := resp.Trailer.Get("Grpc-Status"); got != "0" {
		t.Fatalf("客户端侧尾部 Grpc-Status 期望 0,实得 %q(全部尾部: %v)", got, resp.Trailer)
	}

	// 等待 serveHTTP2 端因连接关闭而返回(关闭客户端连接触发)。
	_ = clientTLS.Close()
	select {
	case <-srvErr:
	case <-time.After(3 * time.Second):
	}

	// 6) 断言 Flow 被正确捕获:协议为 h2、记录了响应尾部。
	f := sink.last()
	if f == nil {
		t.Fatal("未捕获到任何已完成 Flow")
	}
	if f.Request == nil || f.Request.Proto != "HTTP/2.0" {
		t.Fatalf("捕获的请求 Proto 期望 HTTP/2.0,实得 %+v", f.Request)
	}
	if f.Response == nil {
		t.Fatal("捕获的 Flow 缺少响应")
	}
	if got := f.Response.Trailer["Grpc-Status"]; len(got) == 0 || got[0] != "0" {
		t.Fatalf("捕获的响应尾部 Grpc-Status 期望 [0],实得 %v", f.Response.Trailer)
	}
	if string(f.Response.Body) != "hello-h2:HTTP/2.0" {
		t.Fatalf("捕获的响应体不符: %q", string(f.Response.Body))
	}
}

type errString string

func (e errString) Error() string { return string(e) }
