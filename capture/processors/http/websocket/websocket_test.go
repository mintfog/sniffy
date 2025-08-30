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
	"golang.org/x/net/websocket"
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

func TestGetOrigin(t *testing.T) {
	tests := []struct {
		name         string
		originHeader string
		host         string
		isHttps      bool
		expected     string
	}{
		{
			name:         "With Origin header",
			originHeader: "https://example.com",
			host:         "api.example.com",
			isHttps:      true,
			expected:     "https://example.com",
		},
		{
			name:         "No Origin header - HTTP",
			originHeader: "",
			host:         "example.com",
			isHttps:      false,
			expected:     "http://example.com",
		},
		{
			name:         "No Origin header - HTTPS",
			originHeader: "",
			host:         "example.com",
			isHttps:      true,
			expected:     "https://example.com",
		},
		{
			name:         "With port in host",
			originHeader: "",
			host:         "localhost:8080",
			isHttps:      false,
			expected:     "http://localhost:8080",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockConn := newMockConn("")
			mockServer := newMockServer()
			mockConnection := newMockConnection(mockConn, mockServer)

			request, _ := http.NewRequest("GET", "/ws", nil)
			request.Host = tt.host
			if tt.originHeader != "" {
				request.Header.Set("Origin", tt.originHeader)
			}

			processor := New(mockConnection, request, tt.isHttps)
			result := processor.getOrigin()

			if result != tt.expected {
				t.Errorf("getOrigin() = %s, expected %s", result, tt.expected)
			}
		})
	}
}

func TestCopyWebSocketHeaders(t *testing.T) {
	mockConn := newMockConn("")
	mockServer := newMockServer()
	mockConnection := newMockConnection(mockConn, mockServer)

	request, _ := http.NewRequest("GET", "/ws", nil)
	request.Host = "example.com"

	// 设置各种头部
	request.Header.Set("Sec-WebSocket-Protocol", "chat, superchat")
	request.Header.Set("Sec-WebSocket-Extensions", "deflate-frame")
	request.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	request.Header.Set("Sec-WebSocket-Version", "13")
	request.Header.Set("Host", "example.com")
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Upgrade", "websocket")
	request.Header.Set("User-Agent", "Test Client")
	request.Header.Set("Authorization", "Bearer token123")

	processor := New(mockConnection, request, false)

	// 创建WebSocket配置
	config, err := websocket.NewConfig("ws://example.com/ws", "http://example.com")
	if err != nil {
		t.Fatalf("创建WebSocket配置失败: %v", err)
	}

	// 复制头部
	processor.copyWebSocketHeaders(config)

	// 检查协议头是否被正确设置
	if len(config.Protocol) != 1 || config.Protocol[0] != "chat, superchat" {
		t.Errorf("Sec-WebSocket-Protocol头设置不正确: %v", config.Protocol)
	}

	// 检查其他头部是否被转发
	userAgent := config.Header.Get("User-Agent")
	if userAgent != "Test Client" {
		t.Errorf("User-Agent头应该被转发: 期望 'Test Client', 得到 '%s'", userAgent)
	}

	auth := config.Header.Get("Authorization")
	if auth != "Bearer token123" {
		t.Errorf("Authorization头应该被转发: 期望 'Bearer token123', 得到 '%s'", auth)
	}

	// 检查被过滤的头部（这些头部不应该从原始请求中复制过来）
	// 注意：websocket包可能会自动设置某些头部，我们检查的是我们没有从原始请求复制这些头部
	filteredHeaders := []string{"Host", "Connection", "Upgrade"}
	for _, header := range filteredHeaders {
		// 检查原始请求中的值是否被复制到config中
		originalValue := request.Header.Get(header)
		configValue := config.Header.Get(header)
		if originalValue != "" && configValue == originalValue {
			t.Errorf("头部 %s 不应该从原始请求复制，但值匹配: %s", header, configValue)
		}
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

func TestFakeResponseWriter(t *testing.T) {
	mockConn := newMockConn("")
	writer := &fakeResponseWriter{conn: mockConn}

	t.Run("Header", func(t *testing.T) {
		header := writer.Header()
		if header == nil {
			t.Error("Header() 应该返回非空的http.Header")
		}

		// 测试可以设置头部
		header.Set("Test-Header", "test-value")
		if header.Get("Test-Header") != "test-value" {
			t.Error("应该能够设置和获取头部")
		}
	})

	t.Run("Write", func(t *testing.T) {
		testData := []byte("test response data")
		n, err := writer.Write(testData)

		if err != nil {
			t.Errorf("Write() 返回错误: %v", err)
		}
		if n != len(testData) {
			t.Errorf("Write() 返回字节数不正确: 期望 %d, 得到 %d", len(testData), n)
		}

		written := mockConn.WrittenData()
		if written != string(testData) {
			t.Errorf("写入数据不正确: 期望 '%s', 得到 '%s'", string(testData), written)
		}
	})

	t.Run("WriteHeader", func(t *testing.T) {
		// WriteHeader方法应该什么都不做
		writer.WriteHeader(200)
		writer.WriteHeader(404)
		// 如果没有panic就成功了
	})

	t.Run("Hijack", func(t *testing.T) {
		conn, rw, err := writer.Hijack()

		if err != nil {
			t.Errorf("Hijack() 返回错误: %v", err)
		}
		if conn != mockConn {
			t.Error("Hijack() 应该返回原始连接")
		}
		if rw == nil {
			t.Error("Hijack() 应该返回非空的ReadWriter")
		}

		// 测试ReadWriter功能
		testData := "hijack test"
		_, err = rw.WriteString(testData)
		if err != nil {
			t.Errorf("ReadWriter写入失败: %v", err)
		}

		err = rw.Flush()
		if err != nil {
			t.Errorf("ReadWriter刷新失败: %v", err)
		}
	})
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

func TestHeaderFiltering(t *testing.T) {
	// 测试头部过滤功能的详细行为
	mockConn := newMockConn("")
	mockServer := newMockServer()
	mockConnection := newMockConnection(mockConn, mockServer)

	request, _ := http.NewRequest("GET", "/ws", nil)

	// 设置各种类型的头部
	headersToFilter := map[string]string{
		"Sec-WebSocket-Extensions": "permessage-deflate",
		"Sec-WebSocket-Key":        "SGVsbG8gV29ybGQ=",
		"Sec-WebSocket-Version":    "13",
		"Host":                     "example.com",
		"Connection":               "Upgrade",
		"Upgrade":                  "websocket",
	}

	headersToKeep := map[string]string{
		"User-Agent":      "TestClient/1.0",
		"Authorization":   "Bearer token",
		"Custom-Header":   "custom-value",
		"X-Forwarded-For": "192.168.1.1",
	}

	// 设置所有头部
	for key, value := range headersToFilter {
		request.Header.Set(key, value)
	}
	for key, value := range headersToKeep {
		request.Header.Set(key, value)
	}

	processor := New(mockConnection, request, false)

	config, _ := websocket.NewConfig("ws://example.com/ws", "http://example.com")
	processor.copyWebSocketHeaders(config)

	// 验证被过滤的头部不是从原始请求复制的
	// 注意：websocket包可能会自动设置某些头部，我们只验证没有从原始请求复制
	filteredHeaders := []string{"Host", "Connection", "Upgrade"}
	for _, key := range filteredHeaders {
		originalValue := headersToFilter[key]
		configValue := config.Header.Get(key)
		if originalValue != "" && configValue == originalValue {
			t.Errorf("头部 %s 不应该从原始请求复制", key)
		}
	}

	// 验证保留的头部存在
	for key, expectedValue := range headersToKeep {
		actualValue := config.Header.Get(key)
		if actualValue != expectedValue {
			t.Errorf("头部 %s: 期望 '%s', 得到 '%s'", key, expectedValue, actualValue)
		}
	}
}
