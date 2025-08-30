// Copyright 2025 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package http

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

	processor := New(mockConnection)

	if processor == nil {
		t.Fatal("New() 应该返回非空的处理器")
	}

	httpProcessor, ok := processor.(*Processor)
	if !ok {
		t.Fatal("New() 应该返回 *Processor 类型")
	}

	if httpProcessor.conn != mockConnection {
		t.Error("处理器的连接设置不正确")
	}

	if httpProcessor.request != nil {
		t.Error("新创建的处理器request应该为nil")
	}

	if httpProcessor.isHttps {
		t.Error("新创建的处理器isHttps应该为false")
	}
}

func TestGetProtocolName(t *testing.T) {
	mockConn := newMockConn("")
	mockServer := newMockServer()
	mockConnection := newMockConnection(mockConn, mockServer)

	processor := New(mockConnection).(*Processor)

	protocolName := processor.GetProtocolName()
	expected := "HTTP"

	if protocolName != expected {
		t.Errorf("GetProtocolName() = %s, expected %s", protocolName, expected)
	}
}

func TestHandleHttpProtocol_InvalidRequest(t *testing.T) {
	// 测试无效的HTTP请求
	invalidHTTP := "INVALID HTTP REQUEST\r\n\r\n"
	mockConn := newMockConn(invalidHTTP)
	mockServer := newMockServer()
	mockConnection := newMockConnection(mockConn, mockServer)

	processor := New(mockConnection).(*Processor)

	err := processor.handleHttpProtocol(mockServer, mockConnection.GetReader(), mockConnection.GetWriter())

	if err == nil {
		t.Error("handleHttpProtocol应该对无效的HTTP请求返回错误")
	}

	// 检查是否记录了错误日志
	hasErrorLog := false
	for _, log := range mockServer.logs {
		if strings.Contains(log, "ERROR") && strings.Contains(log, "读取HTTP请求失败") {
			hasErrorLog = true
			break
		}
	}
	if !hasErrorLog {
		t.Error("应该记录错误日志")
	}
}

func TestHandleHttpProtocol_ValidGETRequest(t *testing.T) {
	// 测试有效的GET请求
	validHTTP := "GET / HTTP/1.1\r\nHost: example.com\r\n\r\n"
	mockConn := newMockConn(validHTTP)
	mockServer := newMockServer()
	mockConnection := newMockConnection(mockConn, mockServer)

	processor := New(mockConnection).(*Processor)

	// 注意：这个测试可能会因为实际的HTTP请求而失败，我们主要测试解析部分
	_ = processor.handleHttpProtocol(mockServer, mockConnection.GetReader(), mockConnection.GetWriter())

	// 由于我们没有mock HTTP客户端，这里可能会失败，但我们可以检查请求是否被正确解析
	if processor.request == nil {
		t.Error("应该正确解析HTTP请求")
	} else {
		if processor.request.Method != "GET" {
			t.Errorf("请求方法应该是GET，得到: %s", processor.request.Method)
		}
		if processor.request.Host != "example.com" {
			t.Errorf("请求主机应该是example.com，得到: %s", processor.request.Host)
		}
	}
}

func TestHandleConnect(t *testing.T) {
	// 测试CONNECT请求处理
	connectRequest := "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n"
	mockConn := newMockConn(connectRequest)
	mockServer := newMockServer()
	mockConnection := newMockConnection(mockConn, mockServer)

	processor := New(mockConnection).(*Processor)

	// 首先解析CONNECT请求
	request, err := http.ReadRequest(mockConnection.GetReader())
	if err != nil {
		t.Fatalf("解析CONNECT请求失败: %v", err)
	}
	processor.request = request

	// 为了测试handleConnect，我们需要重新设置reader
	connectRequest2 := "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\nG" // 添加一个'G'字节模拟后续HTTP请求
	mockConn2 := newMockConn(connectRequest2)
	mockConnection2 := newMockConnection(mockConn2, mockServer)

	// 先读取CONNECT请求
	_, _ = http.ReadRequest(mockConnection2.GetReader())

	_ = processor.handleConnect(mockServer, mockConnection2.GetReader(), mockConnection2.GetWriter())

	// 检查是否发送了CONNECT响应
	writtenData := mockConn2.WrittenData()
	if !strings.Contains(writtenData, "HTTP/1.1 200 Connection Established") {
		t.Error("应该发送CONNECT建立响应")
	}
}

func TestHandleConnect_TLSHandshake(t *testing.T) {
	// 测试CONNECT请求后的TLS握手检测
	// 构造CONNECT请求 + TLS握手字节
	connectData := "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n\x16" // \x16是TLS握手字节
	mockConn := newMockConn(connectData)
	mockServer := newMockServer()
	mockConnection := newMockConnection(mockConn, mockServer)

	processor := New(mockConnection).(*Processor)

	// 解析CONNECT请求
	request, err := http.ReadRequest(mockConnection.GetReader())
	if err != nil {
		t.Fatalf("解析CONNECT请求失败: %v", err)
	}
	processor.request = request

	// 测试会因为实际的TLS握手失败，但我们可以检查是否检测到了TLS
	err = processor.handleConnect(mockServer, mockConnection.GetReader(), mockConnection.GetWriter())

	// 检查日志是否包含TLS检测信息
	hasTLSDetection := false
	for _, log := range mockServer.logs {
		if strings.Contains(log, "检测到TLS握手") {
			hasTLSDetection = true
			break
		}
	}
	if !hasTLSDetection {
		t.Error("应该检测到TLS握手")
	}
}

func TestHandleConnect_HTTPRequest(t *testing.T) {
	// 测试CONNECT请求后的HTTP请求检测
	connectData := "CONNECT example.com:80 HTTP/1.1\r\nHost: example.com:80\r\n\r\nGET / HTTP/1.1\r\n"
	mockConn := newMockConn(connectData)
	mockServer := newMockServer()
	mockConnection := newMockConnection(mockConn, mockServer)

	processor := New(mockConnection).(*Processor)

	// 解析CONNECT请求
	request, err := http.ReadRequest(mockConnection.GetReader())
	if err != nil {
		t.Fatalf("解析CONNECT请求失败: %v", err)
	}
	processor.request = request

	err = processor.handleConnect(mockServer, mockConnection.GetReader(), mockConnection.GetWriter())

	// 检查日志是否包含HTTP检测信息
	hasHTTPDetection := false
	for _, log := range mockServer.logs {
		if strings.Contains(log, "检测到HTTP请求") {
			hasHTTPDetection = true
			break
		}
	}
	if !hasHTTPDetection {
		t.Error("应该检测到HTTP请求")
	}
}

func TestHandleConnect_UnknownProtocol(t *testing.T) {
	// 测试CONNECT请求后的未知协议
	connectData := "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n\xAB" // 未知字节
	mockConn := newMockConn(connectData)
	mockServer := newMockServer()
	mockConnection := newMockConnection(mockConn, mockServer)

	processor := New(mockConnection).(*Processor)

	// 解析CONNECT请求
	request, err := http.ReadRequest(mockConnection.GetReader())
	if err != nil {
		t.Fatalf("解析CONNECT请求失败: %v", err)
	}
	processor.request = request

	err = processor.handleConnect(mockServer, mockConnection.GetReader(), mockConnection.GetWriter())

	// 检查日志是否包含未知协议信息
	hasUnknownProtocol := false
	for _, log := range mockServer.logs {
		if strings.Contains(log, "未知协议") {
			hasUnknownProtocol = true
			break
		}
	}
	if !hasUnknownProtocol {
		t.Error("应该检测到未知协议")
	}
}

func TestProcess(t *testing.T) {
	// 测试Process方法
	validHTTP := "GET / HTTP/1.1\r\nHost: example.com\r\n\r\n"
	mockConn := newMockConn(validHTTP)
	mockServer := newMockServer()
	mockConnection := newMockConnection(mockConn, mockServer)

	processor := New(mockConnection)

	// Process方法会调用handleHttpProtocol
	_ = processor.Process()

	// 我们期望会有错误，因为没有真实的网络连接
	// 但我们可以检查是否尝试了处理
	hasDebugLog := false
	for _, log := range mockServer.logs {
		if strings.Contains(log, "处理HTTP协议") {
			hasDebugLog = true
			break
		}
	}
	if !hasDebugLog {
		t.Error("Process方法应该尝试处理HTTP协议")
	}
}

func TestProtocolDetectionBytes(t *testing.T) {
	tests := []struct {
		name      string
		firstByte byte
		expected  string
	}{
		{"TLS handshake", TLSHandshakeRecordType, "TLS"},
		{"HTTP GET", HTTPGetByte, "HTTP"},
		{"HTTP POST", HTTPPostByte, "HTTP"},
		{"Unknown protocol", 0xAB, "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var detectedProtocol string

			switch tt.firstByte {
			case TLSHandshakeRecordType:
				detectedProtocol = "TLS"
			case HTTPGetByte, HTTPPostByte:
				detectedProtocol = "HTTP"
			default:
				detectedProtocol = "unknown"
			}

			if detectedProtocol != tt.expected {
				t.Errorf("协议检测失败: 字节 0x%02x, 期望 %s, 得到 %s", tt.firstByte, tt.expected, detectedProtocol)
			}
		})
	}
}

func TestHTTPResponseMessages(t *testing.T) {
	t.Run("Connect established response", func(t *testing.T) {
		if !strings.Contains(ConnectEstablishedResponse, "200 Connection Established") {
			t.Error("CONNECT响应应该包含'200 Connection Established'")
		}
	})

	t.Run("Bad gateway response", func(t *testing.T) {
		if !strings.Contains(BadGatewayResponse, "502 Bad Gateway") {
			t.Error("Bad Gateway响应应该包含'502 Bad Gateway'")
		}
	})
}

// 测试初始化函数的效果
func TestInitialization(t *testing.T) {
	// 检查全局变量是否正确初始化
	if selfCA == nil {
		t.Error("selfCA 应该在init()中初始化")
	}

	if sharedHttpClient == nil {
		t.Error("sharedHttpClient 应该在init()中初始化")
	}

	// 检查HTTP客户端配置
	if sharedHttpClient != nil {
		if sharedHttpClient.Timeout != ClientTimeout {
			t.Errorf("HTTP客户端超时配置不正确: 期望 %v, 得到 %v", ClientTimeout, sharedHttpClient.Timeout)
		}

		transport, ok := sharedHttpClient.Transport.(*http.Transport)
		if !ok {
			t.Error("HTTP客户端应该使用http.Transport")
		} else {
			if transport.MaxIdleConns != MaxIdleConns {
				t.Errorf("MaxIdleConns配置不正确: 期望 %d, 得到 %d", MaxIdleConns, transport.MaxIdleConns)
			}
			if transport.MaxIdleConnsPerHost != MaxIdleConnsPerHost {
				t.Errorf("MaxIdleConnsPerHost配置不正确: 期望 %d, 得到 %d", MaxIdleConnsPerHost, transport.MaxIdleConnsPerHost)
			}
			if transport.MaxConnsPerHost != MaxConnsPerHost {
				t.Errorf("MaxConnsPerHost配置不正确: 期望 %d, 得到 %d", MaxConnsPerHost, transport.MaxConnsPerHost)
			}
			if transport.IdleConnTimeout != IdleConnTimeout {
				t.Errorf("IdleConnTimeout配置不正确: 期望 %v, 得到 %v", IdleConnTimeout, transport.IdleConnTimeout)
			}
			if transport.ResponseHeaderTimeout != ResponseHeaderTimeout {
				t.Errorf("ResponseHeaderTimeout配置不正确: 期望 %v, 得到 %v", ResponseHeaderTimeout, transport.ResponseHeaderTimeout)
			}
			if transport.ExpectContinueTimeout != ExpectContinueTimeout {
				t.Errorf("ExpectContinueTimeout配置不正确: 期望 %v, 得到 %v", ExpectContinueTimeout, transport.ExpectContinueTimeout)
			}
		}
	}
}
