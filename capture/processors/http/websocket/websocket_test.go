// Copyright 2025 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package websocket

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mintfog/sniffy/capture/types"
)

// Mock实现

// mockConnection 模拟连接
type mockConnection struct {
	conn   net.Conn
	reader *bufio.Reader
	writer *bufio.Writer
	server types.Server
}

func newMockConnection(conn net.Conn, server types.Server) *mockConnection {
	return &mockConnection{
		conn:   conn,
		reader: bufio.NewReader(conn),
		writer: bufio.NewWriter(conn),
		server: server,
	}
}

func (m *mockConnection) GetConn() net.Conn        { return m.conn }
func (m *mockConnection) SetConn(conn net.Conn)    { m.conn = conn }
func (m *mockConnection) GetReader() *bufio.Reader { return m.reader }
func (m *mockConnection) GetWriter() *bufio.Writer { return m.writer }
func (m *mockConnection) GetServer() types.Server  { return m.server }
func (m *mockConnection) Close() error             { return nil }

// mockServer 模拟服务器
type mockServer struct {
	logs []string
}

func newMockServer() *mockServer {
	return &mockServer{logs: make([]string, 0)}
}

func (m *mockServer) GetConfig() types.Config { return nil }
func (m *mockServer) LogInfo(msg string, args ...interface{}) {
	m.logs = append(m.logs, fmt.Sprintf("INFO: "+msg, args...))
}
func (m *mockServer) LogError(msg string, args ...interface{}) {
	m.logs = append(m.logs, fmt.Sprintf("ERROR: "+msg, args...))
}
func (m *mockServer) LogDebug(msg string, args ...interface{}) {
	m.logs = append(m.logs, fmt.Sprintf("DEBUG: "+msg, args...))
}
func (m *mockServer) FormatDataPreview(data []byte) string { return string(data) }

// mockConn 模拟网络连接
type mockConn struct {
	readBuffer  *bytes.Buffer
	writeBuffer *bytes.Buffer
	closed      bool
}

func newMockConn(data string) *mockConn {
	return &mockConn{
		readBuffer:  bytes.NewBufferString(data),
		writeBuffer: bytes.NewBuffer(nil),
		closed:      false,
	}
}

func (m *mockConn) Read(b []byte) (n int, err error) {
	if m.closed {
		return 0, io.EOF
	}
	return m.readBuffer.Read(b)
}

func (m *mockConn) Write(b []byte) (n int, err error) {
	if m.closed {
		return 0, io.ErrClosedPipe
	}
	return m.writeBuffer.Write(b)
}

func (m *mockConn) Close() error {
	m.closed = true
	return nil
}

func (m *mockConn) LocalAddr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8080}
}
func (m *mockConn) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 9090}
}
func (m *mockConn) SetDeadline(t time.Time) error      { return nil }
func (m *mockConn) SetReadDeadline(t time.Time) error  { return nil }
func (m *mockConn) SetWriteDeadline(t time.Time) error { return nil }

func (m *mockConn) WrittenData() string {
	return m.writeBuffer.String()
}

// 测试用例

func TestNew(t *testing.T) {
	mockConn := newMockConn("")
	mockServer := newMockServer()
	mockConnection := newMockConnection(mockConn, mockServer)

	request, _ := http.NewRequest("GET", "/ws", nil)
	request.Header.Set("Upgrade", "websocket")
	request.Header.Set("Connection", "Upgrade")

	processor := New(mockConnection, request, false)

	if processor == nil {
		t.Fatal("New() 应该返回非空的处理器")
	}

	if processor.conn != mockConnection {
		t.Error("处理器的连接设置不正确")
	}

	if processor.request != request {
		t.Error("处理器的请求设置不正确")
	}

	if processor.isHttps {
		t.Error("HTTP WebSocket的isHttps应该为false")
	}
}

func TestNew_HTTPS(t *testing.T) {
	mockConn := newMockConn("")
	mockServer := newMockServer()
	mockConnection := newMockConnection(mockConn, mockServer)

	request, _ := http.NewRequest("GET", "/ws", nil)
	request.Header.Set("Upgrade", "websocket")
	request.Header.Set("Connection", "Upgrade")

	processor := New(mockConnection, request, true)

	if !processor.isHttps {
		t.Error("HTTPS WebSocket的isHttps应该为true")
	}
}

func TestIsWebSocketRequest(t *testing.T) {
	tests := []struct {
		name     string
		headers  map[string]string
		expected bool
	}{
		{
			name: "Valid WebSocket request",
			headers: map[string]string{
				"Upgrade":    "websocket",
				"Connection": "Upgrade",
			},
			expected: true,
		},
		{
			name: "Missing Upgrade header",
			headers: map[string]string{
				"Connection": "Upgrade",
			},
			expected: false,
		},
		{
			name: "Missing Connection header",
			headers: map[string]string{
				"Upgrade": "websocket",
			},
			expected: false,
		},
		{
			name: "Wrong Upgrade value",
			headers: map[string]string{
				"Upgrade":    "http2",
				"Connection": "Upgrade",
			},
			expected: false,
		},
		{
			name: "Wrong Connection value",
			headers: map[string]string{
				"Upgrade":    "websocket",
				"Connection": "close",
			},
			expected: false,
		},
		{
			name: "Case sensitive test",
			headers: map[string]string{
				"Upgrade":    "WebSocket", // 大小写不同
				"Connection": "Upgrade",
			},
			expected: false,
		},
		{
			name:     "No headers",
			headers:  map[string]string{},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request, _ := http.NewRequest("GET", "/ws", nil)
			for key, value := range tt.headers {
				request.Header.Set(key, value)
			}

			result := IsWebSocketRequest(request)
			if result != tt.expected {
				t.Errorf("IsWebSocketRequest() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestBuildWebSocketURL(t *testing.T) {
	tests := []struct {
		name     string
		host     string
		path     string
		isHttps  bool
		expected string
	}{
		{
			name:     "HTTP WebSocket",
			host:     "example.com",
			path:     "/ws",
			isHttps:  false,
			expected: "ws://example.com/ws",
		},
		{
			name:     "HTTPS WebSocket",
			host:     "example.com",
			path:     "/ws",
			isHttps:  true,
			expected: "wss://example.com/ws",
		},
		{
			name:     "Root path",
			host:     "localhost:8080",
			path:     "/",
			isHttps:  false,
			expected: "ws://localhost:8080/",
		},
		{
			name:     "Complex path",
			host:     "api.example.com",
			path:     "/v1/websocket/chat",
			isHttps:  true,
			expected: "wss://api.example.com/v1/websocket/chat",
		},
		{
			name:     "Path with query string",
			host:     "example.com",
			path:     "/socket.io/?EIO=4&transport=websocket",
			isHttps:  true,
			expected: "wss://example.com/socket.io/?EIO=4&transport=websocket",
		},
		{
			name:     "Query with auth token",
			host:     "api.example.com",
			path:     "/ws?token=abc123",
			isHttps:  false,
			expected: "ws://api.example.com/ws?token=abc123",
		},
		{
			name:     "Empty path",
			host:     "example.com",
			path:     "",
			isHttps:  false,
			expected: "ws://example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockConn := newMockConn("")
			mockServer := newMockServer()
			mockConnection := newMockConnection(mockConn, mockServer)

			request, _ := http.NewRequest("GET", tt.path, nil)
			request.Host = tt.host

			processor := New(mockConnection, request, tt.isHttps)
			result := processor.buildWebSocketURL()

			if result != tt.expected {
				t.Errorf("buildWebSocketURL() = %s, expected %s", result, tt.expected)
			}
		})
	}
}

func TestSendWebSocketError(t *testing.T) {
	mockConn := newMockConn("")
	mockServer := newMockServer()
	mockConnection := newMockConnection(mockConn, mockServer)

	request, _ := http.NewRequest("GET", "/ws", nil)
	processor := New(mockConnection, request, false)

	err := processor.sendWebSocketError()
	if err != nil {
		t.Errorf("sendWebSocketError() 返回错误: %v", err)
	}

	// 检查写入的响应
	written := mockConn.WrittenData()
	expectedParts := []string{
		"HTTP/1.1 502 Bad Gateway",
		"Content-Type: text/plain",
		"Content-Length: 28",
		"WebSocket connection failed",
	}

	for _, part := range expectedParts {
		if !strings.Contains(written, part) {
			t.Errorf("错误响应应该包含 '%s'，但实际响应为: %s", part, written)
		}
	}
}

func TestProcessor_Process_InvalidConnection(t *testing.T) {
	// 测试无效连接的处理
	mockConn := newMockConn("")
	mockServer := newMockServer()
	mockConnection := newMockConnection(mockConn, mockServer)

	request, _ := http.NewRequest("GET", "/ws", nil)
	request.Header.Set("Upgrade", "websocket")
	request.Header.Set("Connection", "Upgrade")
	request.Host = "nonexistent.example.com"

	processor := New(mockConnection, request, false)

	// Process会失败，因为无法连接到目标服务器
	err := processor.Process(mockServer)

	// 检查是否记录了适当的日志
	hasDebugLog := false
	hasErrorLog := false
	for _, log := range mockServer.logs {
		if strings.Contains(log, "开始处理WebSocket连接") {
			hasDebugLog = true
		}
		if strings.Contains(log, "ERROR") {
			hasErrorLog = true
		}
	}

	if !hasDebugLog {
		t.Error("应该记录开始处理WebSocket连接的日志")
	}

	// 由于连接会失败，应该有错误日志或者错误返回
	if err == nil && !hasErrorLog {
		t.Error("无效连接应该产生错误")
	}
}

func TestWebSocketURLSchemes(t *testing.T) {
	tests := []struct {
		name    string
		isHttps bool
		scheme  string
	}{
		{"HTTP to WS", false, "ws"},
		{"HTTPS to WSS", true, "wss"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockConn := newMockConn("")
			mockServer := newMockServer()
			mockConnection := newMockConnection(mockConn, mockServer)

			request, _ := http.NewRequest("GET", "/test", nil)
			request.Host = "example.com"

			processor := New(mockConnection, request, tt.isHttps)
			url := processor.buildWebSocketURL()

			expectedPrefix := tt.scheme + "://"
			if !strings.HasPrefix(url, expectedPrefix) {
				t.Errorf("WebSocket URL应该以 %s 开头，得到: %s", expectedPrefix, url)
			}
		})
	}
}
