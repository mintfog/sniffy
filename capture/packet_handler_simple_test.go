// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0

package capture

import (
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/mintfog/sniffy/capture/types"
	"github.com/mintfog/sniffy/plugins"
)

func TestSimplePacketHandlerConfigurationAndLogging(t *testing.T) {
	cfg := defaultTestConfig()
	h := NewDefaultPacketHandler(cfg)
	if h.GetConfig() != cfg {
		t.Fatal("GetConfig did not return the configured value")
	}

	logger := &recordingLogger{}
	h.SetLogger(logger)
	h.LogInfo("listening on %d", 8080)
	h.LogError("failed: %s", "boom")
	h.LogDebug("accepted %s", "client")
	h.HandleError(errors.New("bad packet"), "read")

	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	if err := h.OnConnectionStart(server); err != nil {
		t.Fatalf("OnConnectionStart returned %v", err)
	}
	h.OnConnectionEnd(server, time.Millisecond)

	for _, fragment := range []string{"listening on 8080", "failed: boom", "accepted client", "错误 [read]", "连接开始", "连接结束"} {
		if !logger.contains(fragment) {
			t.Errorf("missing log entry containing %q", fragment)
		}
	}

	hookExecutor := &plugins.HookExecutor{}
	h.SetHookExecutor(hookExecutor)
	if h.hookExecutor != hookExecutor {
		t.Fatal("SetHookExecutor did not retain the executor")
	}
}

func TestSimplePacketHandlerDataPreview(t *testing.T) {
	h := NewDefaultPacketHandler(defaultTestConfig())
	if got := h.FormatDataPreview([]byte{0x01, 0xab}); got != "01ab" {
		t.Fatalf("short preview = %q", got)
	}

	data := []byte(strings.Repeat("x", 65))
	got := h.FormatDataPreview(data)
	if !strings.Contains(got, "truncated, total: 65 bytes") || strings.Contains(got, strings.Repeat("78", 65)) {
		t.Fatalf("long preview was not truncated correctly: %q", got)
	}
}

func TestSimplePacketHandlerProcessesDetectedProtocol(t *testing.T) {
	h := NewDefaultPacketHandler(defaultTestConfig())
	logger := &recordingLogger{}
	h.SetLogger(logger)

	wantErr := errors.New("processor failed")
	processor := &stubProcessor{protocol: "HTTP", err: wantErr}
	h.registry.Register("HTTP", func(types.Connection) types.ProtocolProcessor { return processor })
	conn := newMemoryConn([]byte("GET / HTTP/1.1\r\n\r\n"))
	h.HandleConnection(conn, &types.ConnectionInfo{LocalAddr: conn.LocalAddr(), RemoteAddr: conn.RemoteAddr()})

	if !processor.called {
		t.Fatal("detected protocol processor was not called")
	}
	if !conn.isClosed() {
		t.Fatal("connection was not closed after processing")
	}
	if !logger.contains(wantErr.Error()) {
		t.Fatal("processor error was not logged")
	}
}

func TestSimplePacketHandlerHandlesNilProcessor(t *testing.T) {
	h := NewDefaultPacketHandler(defaultTestConfig())
	logger := &recordingLogger{}
	h.SetLogger(logger)
	h.registry.Register("HTTP", func(types.Connection) types.ProtocolProcessor { return nil })

	h.HandleConnection(newMemoryConn([]byte("GET / HTTP/1.1\r\n\r\n")), &types.ConnectionInfo{
		LocalAddr:  testAddr("local"),
		RemoteAddr: testAddr("remote"),
	})
	if !logger.contains("无法获取协议处理器: HTTP") {
		t.Fatal("nil processor was not reported")
	}
}

func TestSimplePacketHandlerFallbackLogging(t *testing.T) {
	output := captureStandardLog(t)
	h := NewDefaultPacketHandler(defaultTestConfig())
	h.LogInfo("info %d", 1)
	h.LogError("error %d", 2)
	h.LogDebug("debug %d", 3)

	for _, fragment := range []string{"[INFO] info 1", "[ERROR] error 2", "[DEBUG] debug 3"} {
		if !strings.Contains(output.String(), fragment) {
			t.Errorf("missing fallback log %q in %q", fragment, output.String())
		}
	}
}
