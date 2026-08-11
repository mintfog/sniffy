// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0

package capture

import (
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mintfog/sniffy/plugins"
)

func newTestTCPListener(cfg testConfig) *TCPListener {
	ctx, cancel := context.WithCancel(context.Background())
	return &TCPListener{
		config:  cfg,
		handler: NewDefaultPacketHandler(cfg),
		ctx:     ctx,
		cancel:  cancel,
		conns:   make(map[net.Conn]struct{}),
	}
}

// newConnectionHookExecutor 构造一个只挂载 hook 的插件管理器:插件目录用临时目录,
// 避免扫描到用户真实插件。
func newConnectionHookExecutor(t *testing.T, logger Logger, hook *recordingConnectionPlugin) *plugins.HookExecutor {
	t.Helper()
	manager := plugins.NewPluginManager(nil, logger, plugins.ManagerConfig{
		PluginsDir:  t.TempDir(),
		ConfigDir:   t.TempDir(),
		LoadTimeout: 5 * time.Second,
	})
	manager.RegisterFactory(hook.GetInfo().Name, func(plugins.PluginAPI) plugins.Plugin { return hook })
	if err := manager.LoadPlugins(); err != nil {
		t.Fatalf("load connection hook plugin: %v", err)
	}
	return plugins.NewHookExecutor(manager, logger)
}

func TestNewTCPListener(t *testing.T) {
	cfg := defaultTestConfig()
	tl := NewTCPListener(cfg)
	defer tl.cancel()

	if tl.config != cfg || tl.handler == nil || tl.ctx == nil || tl.cancel == nil {
		t.Fatal("NewTCPListener did not initialize its dependencies")
	}
	if tl.conns == nil || tl.IsRunning() {
		t.Fatal("NewTCPListener has invalid initial state")
	}
}

func TestTCPListenerLifecycleAndAccept(t *testing.T) {
	cfg := defaultTestConfig()
	tl := newTestTCPListener(cfg)
	logger := &recordingLogger{}
	tl.SetLogger(logger)
	hook := &recordingConnectionPlugin{}
	tl.SetHookExecutor(newConnectionHookExecutor(t, logger, hook))

	if tl.GetConfig() != cfg || tl.GetHandler() == nil {
		t.Fatal("listener accessors did not return configured values")
	}
	if got := tl.GetAddress(); got != "127.0.0.1:0" {
		t.Fatalf("address before Start = %q", got)
	}
	if err := tl.Start(); err != nil {
		t.Fatalf("Start returned %v", err)
	}
	if !tl.IsRunning() {
		t.Fatal("listener should be running")
	}
	if err := tl.Start(); err == nil {
		t.Fatal("second Start should fail")
	}

	conn, err := net.DialTimeout("tcp", tl.GetAddress(), time.Second)
	if err != nil {
		t.Fatalf("dial listener: %v", err)
	}
	if _, err := conn.Write([]byte{0x10}); err != nil {
		t.Fatalf("write protocol byte: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	// 必须是 EOF:读超时同样返回非 nil error,只判 err != nil 无法区分
	// "处理完毕后关闭"与"处理卡住"。
	if _, err := conn.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("handler should close the processed connection, read error = %v", err)
	}
	_ = conn.Close()

	if err := tl.Stop(); err != nil {
		t.Fatalf("Stop returned %v", err)
	}
	if tl.IsRunning() {
		t.Fatal("listener should be stopped")
	}
	if err := tl.Stop(); err != nil {
		t.Fatalf("idempotent Stop returned %v", err)
	}
	for _, fragment := range []string{"TCP listener started", "TCP listener stopped", "处理新连接"} {
		if !logger.contains(fragment) {
			t.Errorf("missing lifecycle log containing %q", fragment)
		}
	}

	started, ended, duration := hook.calls()
	if started != 1 || ended != 1 {
		t.Fatalf("connection hooks = %d start, %d end, want 1 each", started, ended)
	}
	if duration <= 0 {
		t.Fatalf("connection end hook duration = %v, want a positive value", duration)
	}
}

func TestTCPListenerStartReportsAddressConflict(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	defer occupied.Close()

	_, portText, err := net.SplitHostPort(occupied.Addr().String())
	if err != nil {
		t.Fatalf("split reserved address: %v", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("parse reserved port: %v", err)
	}
	cfg := defaultTestConfig()
	cfg.port = port
	tl := newTestTCPListener(cfg)
	defer tl.cancel()
	if err := tl.Start(); err == nil || !strings.Contains(err.Error(), "failed to start TCP listener") {
		t.Fatalf("Start error = %v", err)
	}
}

func TestTCPListenerConnectionTracking(t *testing.T) {
	tl := newTestTCPListener(defaultTestConfig())
	server, client := net.Pipe()
	defer client.Close()

	if !tl.trackConn(server) {
		t.Fatal("new connection should be tracked")
	}
	tl.untrackConn(server)
	if len(tl.conns) != 0 {
		t.Fatal("untrackConn did not remove connection")
	}
	if !tl.trackConn(server) {
		t.Fatal("connection should be trackable before shutdown")
	}
	tl.closeAllConns()
	_ = client.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := client.Read(make([]byte, 1)); err == nil {
		t.Fatal("closeAllConns did not close tracked connection")
	}
	if tl.trackConn(newMemoryConn(nil)) {
		t.Fatal("connection should not be tracked while closing")
	}
}

func TestTCPListenerErrorAndLogDelegation(t *testing.T) {
	tl := newTestTCPListener(defaultTestConfig())
	handler := &stubPacketHandler{}
	tl.handler = handler
	tl.handleError(errors.New("accept failed"), "accept")
	if len(handler.errors) != 1 || !strings.Contains(handler.errors[0], "accept failed") {
		t.Fatalf("handler errors = %#v", handler.errors)
	}

	logger := &recordingLogger{}
	tl.handler = nil
	tl.logger = logger
	tl.handleError(errors.New("fallback"), "listener")
	tl.logInfo("info %d", 1)
	tl.logDebug("debug %d", 2)
	tl.logError("error %d", 3)
	for _, fragment := range []string{"fallback", "info 1", "debug 2", "error 3"} {
		if !logger.contains(fragment) {
			t.Errorf("missing delegated log containing %q", fragment)
		}
	}

	output := captureStandardLog(t)
	tl.logger = nil
	tl.logInfo("dropped info")
	tl.logDebug("dropped debug")
	if output.Len() != 0 {
		t.Fatalf("logging disabled, but the standard logger received %q", output.String())
	}

	tl.config = testConfig{logging: true}
	tl.logInfo("fallback info")
	tl.logError("fallback error")
	for _, fragment := range []string{"fallback info", "ERROR: fallback error"} {
		if !strings.Contains(output.String(), fragment) {
			t.Errorf("missing fallback log %q in %q", fragment, output.String())
		}
	}
}
