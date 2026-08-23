// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package api

import (
	"context"
	"net"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/mintfog/sniffy/internal/core"
	"github.com/mintfog/sniffy/internal/service"
)

// startWSServer 起一个真实监听的 API 服务器,返回其地址。
func startWSServer(t *testing.T, bus *core.EventBus) (*Server, string) {
	t.Helper()
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := probe.Addr().String()
	_ = probe.Close()

	s := New(service.New(nil, bus, t.TempDir(), t.TempDir()), nil, nil, nil, addr, "tok")
	if err := s.Listen(); err != nil {
		t.Fatalf("Listen 失败: %v", err)
	}
	go func() { _ = s.Serve() }()
	return s, addr
}

func dialWS(t *testing.T, addr string) *websocket.Conn {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		conn, _, err := websocket.DefaultDialer.Dial("ws://"+addr+"/api/ws?token=tok", nil)
		if err == nil {
			return conn
		}
		if time.Now().After(deadline) {
			t.Fatalf("连接 WebSocket 失败: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestStopClosesHubAndUpgradedConns 覆盖:http.Server.Shutdown 不会关闭被 hijack 的
// WebSocket 连接,也不会让广播循环退出——这些必须由 Stop 自己收口。
func TestStopClosesHubAndUpgradedConns(t *testing.T) {
	bus := core.NewEventBus()
	s, addr := startWSServer(t, bus)
	conn := dialWS(t, addr)
	defer conn.Close()

	// 先确认链路是通的,否则后面的断言可能只是"本来就没收到"。
	waitForEvent(t, bus, conn)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Stop(ctx); err != nil {
		t.Fatalf("Stop 失败: %v", err)
	}

	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("Stop 后已升级的 WebSocket 连接仍可读,应已断开")
	} else if !websocket.IsCloseError(err,
		websocket.CloseNormalClosure, websocket.CloseNoStatusReceived, websocket.CloseAbnormalClosure) {
		t.Fatalf("期望连接被关闭,实际错误: %v", err)
	}

	// Stop 返回时 run 已退出(事件总线订阅随之取消);两个 pump 紧随其后。
	if dump := goroutineDump(); strings.Contains(dump, "api.(*Hub).run") {
		t.Error("Stop 后广播循环 goroutine 仍存活,事件总线订阅未取消")
	}
	assertGoroutineGone(t, "api.(*Hub).readPump")
	assertGoroutineGone(t, "api.(*Hub).writePump")
}

// TestStopIsIdempotent 确保重复 Stop 不会 panic(close of closed channel)。
func TestStopIsIdempotent(t *testing.T) {
	bus := core.NewEventBus()
	s, addr := startWSServer(t, bus)
	conn := dialWS(t, addr)
	defer conn.Close()
	waitForEvent(t, bus, conn)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for range 3 {
		if err := s.Stop(ctx); err != nil {
			t.Fatalf("重复 Stop 失败: %v", err)
		}
	}
}

// TestStopWithoutServeDoesNotBlock 覆盖 Listen 后未 Serve 就 Stop:
// 广播循环从未启动,stop 不该干等到 ctx 超时。
func TestStopWithoutServeDoesNotBlock(t *testing.T) {
	s := New(service.New(nil, core.NewEventBus(), t.TempDir(), t.TempDir()), nil, nil, nil, "127.0.0.1:0", "tok")
	if err := s.Listen(); err != nil {
		t.Fatalf("Listen 失败: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- s.Stop(ctx) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Stop 失败: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("未 Serve 时 Stop 阻塞了")
	}
}

// waitForEvent 发事件直到客户端收到,确认广播链路已就绪
// (register 是异步的,升级成功不代表已入册)。
func waitForEvent(t *testing.T, bus *core.EventBus, conn *websocket.Conn) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		bus.Emit(core.EventFlowStarted, map[string]any{"probe": true})
		_ = conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		if _, _, err := conn.ReadMessage(); err == nil {
			return
		}
	}
	t.Fatal("广播链路未就绪:客户端收不到事件")
}

func goroutineDump() string {
	buf := make([]byte, 1<<20)
	return string(buf[:runtime.Stack(buf, true)])
}

func assertGoroutineGone(t *testing.T, fn string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !strings.Contains(goroutineDump(), fn) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("Stop 后 goroutine 仍存活: %s", fn)
}
