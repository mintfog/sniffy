// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package api

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mintfog/sniffy/internal/core"
	"github.com/mintfog/sniffy/internal/service"
	"github.com/mintfog/sniffy/internal/version"
)

// 本文件覆盖 server.go 的装配与生命周期，验证鉴权中间件、TLS 监听和关停流程。
//
// 本文件与 ws_hub_test.go 使用真实端口和全进程 goroutine 快照，因此用例串行执行。

// startAPIServer 启动真实监听的管理 API 服务器并返回实际地址；清理函数负责关闭服务器和 Hub。
func startAPIServer(t *testing.T, prepare func(*Server)) (*Server, string) {
	t.Helper()
	svc := service.New(nil, core.NewEventBus(), t.TempDir(), t.TempDir())
	s := New(svc, nil, nil, nil, "127.0.0.1:0", "tok")
	if prepare != nil {
		prepare(s)
	}
	if err := s.Listen(); err != nil {
		t.Fatalf("Listen 失败: %v", err)
	}
	addr := s.listener.Addr().String()
	go func() { _ = s.Serve() }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.Stop(ctx)
	})
	return s, addr
}

// getWithToken 向真实监听服务器发送带凭证的 GET，轮询直到服务器开始 accept。
func getWithToken(t *testing.T, client *http.Client, url, token string) *http.Response {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			t.Fatalf("构造请求: %v", err)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(req)
		if err == nil {
			return resp
		}
		if time.Now().After(deadline) {
			t.Fatalf("请求 %s 失败: %v", url, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestListenAppliesAuthMiddleware 验证 Listen 装配的 Handler 对读写端点统一执行鉴权。
func TestListenAppliesAuthMiddleware(t *testing.T) {
	s, addr := startAPIServer(t, nil)
	s.svc.RecordFlowCompleted(newFlowFixture("Flow-A"))
	client := &http.Client{Timeout: 3 * time.Second}
	base := "http://" + addr

	// 先确认服务器已开始 accept，再检查未认证响应。
	resp := getWithToken(t, client, base+"/api/status", "tok")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("带凭证的 GET /api/status = %d,响应 %s", resp.StatusCode, body)
	}
	var authorized struct {
		Data struct {
			Status  string `json:"status"`
			Version string `json:"version"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &authorized); err != nil {
		t.Fatalf("解析 /api/status 响应失败: %v，响应体 = %s", err, body)
	}
	if authorized.Data.Status != "running" {
		t.Errorf("/api/status 的 status = %q，期望 running", authorized.Data.Status)
	}
	if want := version.Get(); authorized.Data.Version != want {
		t.Errorf("/api/status 的 version = %q,期望 %q", authorized.Data.Version, want)
	}

	resp = getWithToken(t, client, base+"/api/status", "")
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("无凭证的 GET /api/status = %d,期望 401", resp.StatusCode)
	}
	var rejected struct {
		Success bool            `json:"success"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &rejected); err != nil {
		t.Fatalf("401 响应体不是 JSON: %s", body)
	}
	if rejected.Success || len(rejected.Data) != 0 {
		t.Errorf("401 响应不应带 data: %s", body)
	}

	// 变更端点同样需要凭证，未认证请求不改变会话存储。
	req, _ := http.NewRequest(http.MethodPost, base+"/api/sessions/clear", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /api/sessions/clear: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("无凭证的清空 = %d,期望 401", resp.StatusCode)
	}
	if _, total := s.svc.Sessions(1, 50); total != 1 {
		t.Errorf("未认证的清空动了会话存储,剩余 %d 条", total)
	}
}

// TestListenServesHTTPSWithConfiguredCert 验证配置证书用于 TLS 握手，明文请求不会降级为 API 响应。
func TestListenServesHTTPSWithConfiguredCert(t *testing.T) {
	dir := t.TempDir()
	certPEM, keyPEM := newSelfSignedPEM(t, "sniffy-api", nil, []net.IP{net.ParseIP("127.0.0.1")})
	certFile := filepath.Join(dir, "cert.pem")
	keyFile := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certFile, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}

	_, addr := startAPIServer(t, func(s *Server) { s.SetTLS(certFile, keyFile) })

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(certPEM) {
		t.Fatal("测试证书无法加入信任池")
	}
	client := &http.Client{
		Timeout:   3 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}},
	}
	resp := getWithToken(t, client, "https://"+addr+"/api/status", "tok")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("HTTPS GET /api/status = %d", resp.StatusCode)
	}
	if resp.TLS == nil || len(resp.TLS.PeerCertificates) == 0 {
		t.Fatal("连接没有 TLS 信息,说明端口其实是明文")
	}
	block, _ := pem.Decode(certPEM)
	want, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("解析测试证书: %v", err)
	}
	if !resp.TLS.PeerCertificates[0].Equal(want) {
		t.Error("握手用的不是 SetTLS 传入的那份证书")
	}

	// 同一端口上的明文请求返回 TLS 层错误，不进入 API Handler。
	plain := &http.Client{Timeout: 2 * time.Second}
	plainResp, err := plain.Get("http://" + addr + "/api/status")
	if err != nil {
		return // 连接直接被拒,同样满足「无明文降级」
	}
	defer plainResp.Body.Close()
	if plainResp.StatusCode == http.StatusOK {
		t.Fatalf("明文请求拿到了 200,TLS 监听存在降级")
	}
	plainBody, _ := io.ReadAll(plainResp.Body)
	var envelope struct {
		Success bool `json:"success"`
	}
	if json.Unmarshal(plainBody, &envelope) == nil && envelope.Success {
		t.Errorf("明文请求拿到了一份成功的 API 响应: %s", plainBody)
	}
}

// TestSecondListenDoesNotOrphanRunningServer 失败的 Listen 保留正在运行的服务器和其监听资源。
func TestSecondListenDoesNotOrphanRunningServer(t *testing.T) {
	s, addr := startAPIServer(t, nil)
	client := &http.Client{Timeout: 3 * time.Second}
	getWithToken(t, client, "http://"+addr+"/api/status", "tok").Body.Close()

	// 第二次 Listen 绑定同一地址，验证失败路径。
	s.addr = addr
	if err := s.Listen(); err == nil {
		t.Fatal("端口已被自己占用,第二次 Listen 应返回错误")
	}

	resp := getWithToken(t, client, "http://"+addr+"/api/status", "tok")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("失败的第二次 Listen 之后服务应照常工作,got %d", resp.StatusCode)
	}

	// TLS 证书加载失败同样保留原服务器。
	s.addr = freeAddr(t)
	s.SetTLS("/nonexistent/cert.pem", "/nonexistent/key.pem")
	if err := s.Listen(); err == nil {
		t.Fatal("证书缺失时 Listen 应返回错误")
	}
	resp = getWithToken(t, client, "http://"+addr+"/api/status", "tok")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("失败的 TLS 装配之后服务应照常工作,got %d", resp.StatusCode)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Stop(ctx); err != nil {
		t.Fatalf("Stop 失败: %v", err)
	}
	assertPortFree(t, addr)
}

// TestListenTLSFailureReleasesPort 证书加载失败时释放已绑定的端口，后续可立即重试。
func TestListenTLSFailureReleasesPort(t *testing.T) {
	addr := freeAddr(t)
	s := New(service.New(nil, core.NewEventBus(), "", ""), nil, nil, nil, addr, "tok")
	s.SetTLS("/nonexistent/cert.pem", "/nonexistent/key.pem")

	err := s.Listen()
	if err == nil {
		t.Fatal("证书缺失时 Listen 应返回错误")
	}
	if !strings.Contains(err.Error(), "加载管理 API TLS 证书") {
		t.Errorf("错误文案 = %q,期望点名 TLS 证书加载", err)
	}
	assertPortFree(t, addr)
}

// TestListenTLSCertErrorIsSynchronous 证书问题在 Listen 阶段同步返回。
func TestListenTLSCertErrorIsSynchronous(t *testing.T) {
	s := New(nil, nil, nil, nil, "127.0.0.1:0", "tok")
	s.SetTLS("/nonexistent/cert.pem", "/nonexistent/key.pem")
	if err := s.Listen(); err == nil {
		t.Fatal("证书缺失时 Listen 应返回错误")
	}
}

// TestListenBindErrorIsSynchronous 端口占用在 Listen 阶段同步返回。
func TestListenBindErrorIsSynchronous(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	s := New(nil, nil, nil, nil, ln.Addr().String(), "tok")
	if err := s.Listen(); err == nil {
		t.Fatal("端口被占用时 Listen 应返回错误")
	}
}

// TestServeBeforeListenFails Serve 依赖 Listen 创建的套接字，调用顺序错误返回错误。
func TestServeBeforeListenFails(t *testing.T) {
	s := New(nil, nil, nil, nil, "127.0.0.1:0", "tok")
	if err := s.Serve(); err == nil {
		t.Fatal("未 Listen 就 Serve 应返回错误")
	}
}

// TestListenThenServeAndStop 服务器完成一次请求后优雅关停，Serve 返回 http.ErrServerClosed，端口随之释放。
func TestListenThenServeAndStop(t *testing.T) {
	svc := service.New(nil, core.NewEventBus(), t.TempDir(), t.TempDir())
	s := New(svc, nil, nil, nil, "127.0.0.1:0", "tok")
	if err := s.Listen(); err != nil {
		t.Fatalf("Listen 失败: %v", err)
	}
	addr := s.listener.Addr().String()
	errc := make(chan error, 1)
	go func() { errc <- s.Serve() }()
	// 清理函数覆盖中途失败路径，避免监听和 goroutine 泄漏。
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.Stop(ctx)
	})

	client := &http.Client{Timeout: 3 * time.Second}
	getWithToken(t, client, "http://"+addr+"/api/status", "tok").Body.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Stop(ctx); err != nil {
		t.Fatalf("Stop 失败: %v", err)
	}
	select {
	case err := <-errc:
		// 用 errors.Is 识别关闭哨兵，保留错误包装语义。
		if !errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("Serve 退出返回 %v,期望 http.ErrServerClosed", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Serve 未在关闭后退出")
	}
	assertPortFree(t, addr)
}

// TestStopWithCanceledContextReturnsPromptly 取消上下文时 Stop 及时返回，并完成 Hub 收口。
func TestStopWithCanceledContextReturnsPromptly(t *testing.T) {
	s, addr := startAPIServer(t, nil)
	client := &http.Client{Timeout: 3 * time.Second}
	getWithToken(t, client, "http://"+addr+"/api/status", "tok").Body.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan error, 1)
	go func() { done <- s.Stop(ctx) }()
	select {
	case err := <-done:
		// Shutdown 可能返回 context.Canceled；两种结果都表示已及时收口，其他错误才需报告。
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("Stop 返回 %v,期望 nil 或 context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("已取消的 ctx 下 Stop 仍然挂住了")
	}

	select {
	case <-s.hub.done:
	default:
		t.Error("Stop 返回后 hub 的停止信号仍未发出")
	}

	// 重复 Stop 保持幂等。
	ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel2()
	if err := s.Stop(ctx2); err != nil {
		t.Errorf("重复 Stop 返回 %v,期望 nil", err)
	}
}

// TestStopWithoutListenReturnsNil 未完成 Listen 的服务器可安全执行 Stop，并返回 nil。
func TestStopWithoutListenReturnsNil(t *testing.T) {
	s := New(service.New(nil, core.NewEventBus(), "", ""), nil, nil, nil, "127.0.0.1:0", "tok")
	done := make(chan error, 1)
	go func() { done <- s.Stop(context.Background()) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("未 Listen 时 Stop 返回 %v,期望 nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("未 Listen 时 Stop 阻塞了")
	}
}

// TestListenSetsWebSocketFriendlyTimeouts 管理 API 使用适合 WebSocket 长连接的读写超时配置。
func TestListenSetsWebSocketFriendlyTimeouts(t *testing.T) {
	s := New(service.New(nil, core.NewEventBus(), "", ""), nil, nil, nil, "127.0.0.1:0", "tok")
	if err := s.Listen(); err != nil {
		t.Fatalf("Listen 失败: %v", err)
	}
	t.Cleanup(func() { _ = s.listener.Close() })

	if got := s.httpSrv.ReadTimeout; got != 15*time.Second {
		t.Errorf("ReadTimeout = %v,期望 15s", got)
	}
	if got := s.httpSrv.WriteTimeout; got != 0 {
		t.Errorf("WriteTimeout = %v,必须为 0 —— /api/ws 是长连接,写超时会把它定时掐断", got)
	}
	if got := s.httpSrv.IdleTimeout; got != 60*time.Second {
		t.Errorf("IdleTimeout = %v,期望 60s", got)
	}
}

// freeAddr 取一个当前空闲的具体地址，供端口释放测试重绑。
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}

// assertPortFree 断言地址已可被重新绑定。
func assertPortFree(t *testing.T, addr string) {
	t.Helper()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Errorf("端口 %s 未被释放: %v", addr, err)
		return
	}
	_ = ln.Close()
}
