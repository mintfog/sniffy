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
)

// 本文件对应 server.go 的装配与生命周期。这里是全包唯一能验证「authMiddleware 真的挂上了」
// 与「TLS 分支真的在跑 TLS」的层次。
//
// 本文件与 ws_hub_test.go 的用例绑真实端口、并用全进程 goroutine 快照做断言,
// 一律禁止 t.Parallel:并行会让两边互相看见对方的监听与 goroutine。

// startAPIServer 起一台真实监听的管理 API 服务器,返回它与实际绑定的地址。
// 直接绑 :0 再回读 listener 地址,避免「先探测端口、关掉、再重绑」那段窗口被别的进程抢走;
// t.Cleanup 里统一收口,任一步 t.Fatal 都不会留下在监听的服务器与 Hub goroutine。
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

// getWithToken 向真实监听的服务器发一条带凭证的 GET,轮询到服务器开始 accept 为止。
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

// TestListenAppliesAuthMiddleware server.go 里 Listen 是唯一装配 authMiddleware 的地方,而全部鉴权
// 用例都直调 s.authMiddleware(inner)。把 Handler 从 s.authMiddleware(mux) 写成 mux(加 CORS/日志
// 中间件时最易发生),整个管理 API 变成无认证 —— 包括 /api/config 与全部抓包内容 —— 而那些用例全绿。
func TestListenAppliesAuthMiddleware(t *testing.T) {
	s, addr := startAPIServer(t, nil)
	s.svc.RecordFlowCompleted(newFlowFixture("Flow-A"))
	client := &http.Client{Timeout: 3 * time.Second}
	base := "http://" + addr

	// 先确认服务器已在 accept:否则下面的 401 可能只是连接还没建起来。
	resp := getWithToken(t, client, base+"/api/status", "tok")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("带凭证的 GET /api/status = %d,响应 %s", resp.StatusCode, body)
	}
	var authorized struct {
		Data struct {
			Status string `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &authorized); err != nil || authorized.Data.Status != "running" {
		t.Errorf("响应体 = %s (err %v)", body, err)
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

	// 变更端点同样被挡在中间件之外,且没有产生副作用。
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

// TestListenServesHTTPSWithConfiguredCert server.go 的 TLSConfig 与 tls.NewListener 两行此前执行计数为 0,
// 全包测试没有 import crypto/tls。这两行被改坏时 Listen 依旧成功、日志照样打印 https://,
// 而端口实际是明文:管理 token 与全部抓包内容在网上裸奔,操作员没有任何可见信号。
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

	// 同一端口上的明文请求不能被当成 API 请求处理:存在明文降级就等于 TLS 白配。
	// crypto/tls 会对明文握手回一段 400 纯文本而不是断开连接,所以判据是「不是 200、也不是 API 信封」。
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

// TestSecondListenDoesNotOrphanRunningServer s.httpSrv 若在可能失败的 net.Listen 之前就被换成新对象,
// 第二次 Listen 报错后 Stop 关的是空壳:老服务器继续 accept、端口永不释放、广播循环继续跑,
// 而调用方拿到 nil 以为已优雅关闭,用户看到「停了还在监听、重启起不来」。
func TestSecondListenDoesNotOrphanRunningServer(t *testing.T) {
	s, addr := startAPIServer(t, nil)
	client := &http.Client{Timeout: 3 * time.Second}
	getWithToken(t, client, "http://"+addr+"/api/status", "tok").Body.Close()

	// 第二次 Listen 绑同一个地址,必定失败。
	s.addr = addr
	if err := s.Listen(); err == nil {
		t.Fatal("端口已被自己占用,第二次 Listen 应返回错误")
	}

	resp := getWithToken(t, client, "http://"+addr+"/api/status", "tok")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("失败的第二次 Listen 之后服务应照常工作,got %d", resp.StatusCode)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Stop(ctx); err != nil {
		t.Fatalf("Stop 失败: %v", err)
	}
	assertPortFree(t, addr)
}

// TestListenTLSFailureReleasesPort 证书加载失败时那个已经绑好的端口必须被释放。丢了这行:
// 证书路径写错一次,之后的重绑一直 EADDRINUSE,用户看到的是「改对了证书路径还是起不来」,
// 错误信息还指向端口占用。
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

// TestListenTLSCertErrorIsSynchronous 证书问题必须在 Listen 就暴露,而不是等到第一个请求进来 ——
// 否则「代理正常但管理 API 静默失效」的半启动状态没人察觉。
func TestListenTLSCertErrorIsSynchronous(t *testing.T) {
	s := New(nil, nil, nil, nil, "127.0.0.1:0", "tok")
	s.SetTLS("/nonexistent/cert.pem", "/nonexistent/key.pem")
	if err := s.Listen(); err == nil {
		t.Fatal("证书缺失时 Listen 应返回错误")
	}
}

// TestListenBindErrorIsSynchronous 端口被占用同理。
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

// TestServeBeforeListenFails Serve 依赖 Listen 建好的套接字,顺序颠倒必须报错而不是空转。
func TestServeBeforeListenFails(t *testing.T) {
	s := New(nil, nil, nil, nil, "127.0.0.1:0", "tok")
	if err := s.Serve(); err == nil {
		t.Fatal("未 Listen 就 Serve 应返回错误")
	}
}

// TestListenThenServeAndStop 关停一台确实在 accept 的服务器:Serve 必须以 http.ErrServerClosed 退出,
// 端口随之释放。旧写法在 Serve 与 Stop 之间没有就绪同步,Shutdown 先跑时 accept 循环一次都没进,
// 测试名承诺的「运行中的服务器被优雅关闭」在相当比例的执行里没有发生。
func TestListenThenServeAndStop(t *testing.T) {
	svc := service.New(nil, core.NewEventBus(), t.TempDir(), t.TempDir())
	s := New(svc, nil, nil, nil, "127.0.0.1:0", "tok")
	if err := s.Listen(); err != nil {
		t.Fatalf("Listen 失败: %v", err)
	}
	addr := s.listener.Addr().String()
	errc := make(chan error, 1)
	go func() { errc <- s.Serve() }()

	client := &http.Client{Timeout: 3 * time.Second}
	getWithToken(t, client, "http://"+addr+"/api/status", "tok").Body.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Stop(ctx); err != nil {
		t.Fatalf("Stop 失败: %v", err)
	}
	select {
	case err := <-errc:
		// 用哨兵而不是字符串比较:包一层 %w 或改文案都不该让这条误报,
		// 而 err == nil 更不能算通过(那表示 Serve 提前退出了)。
		if !errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("Serve 退出返回 %v,期望 http.ErrServerClosed", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Serve 未在关闭后退出")
	}
	assertPortFree(t, addr)
}

// TestStopWithCanceledContextReturnsPromptly 关机路径在 shutdownCtx 到期后仍会走到这里。
// 若 hub.stop 改成无条件等 h.stopped,SIGTERM 后进程直接挂死,用户只能 kill -9。
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
		// 被 hijack 的 WS 连接不在 http.Server 的活跃连接表内,首轮 closeIdleConns 即成功,
		// 所以这里拿不到 ctx.Err(),返回 nil 是正确结果。
		if err != nil {
			t.Errorf("Stop 返回 %v,期望 nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("已取消的 ctx 下 Stop 仍然挂住了")
	}

	select {
	case <-s.hub.done:
	default:
		t.Error("Stop 返回后 hub 的停止信号仍未发出")
	}

	// 幂等可续:重复 Stop 不 panic 也不改变结论。
	ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel2()
	if err := s.Stop(ctx2); err != nil {
		t.Errorf("重复 Stop 返回 %v,期望 nil", err)
	}
}

// TestStopWithoutListenReturnsNil 装配早期失败与桌面端提前退出都会调到它;守卫被重构掉后
// s.httpSrv.Shutdown 直接 nil 解引用,用户看到的是退出时的 panic 堆栈而不是正常退出码。
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

// TestListenSetsWebSocketFriendlyTimeouts WriteTimeout 必须为 0。有人以「防慢客户端」为由补上写超时后,
// 被 hijack 的 /api/ws 长连接会被固定时长写死,前端每隔 N 秒掉线重连并丢失这期间的 flow 事件 ——
// 功能测试完全不报错,只以「抓包列表偶尔断更」出现在用户面前。
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

// freeAddr 取一个当前空闲的具体地址(不是 :0)。用于需要在 Listen 失败后回头重绑同一地址的用例。
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
