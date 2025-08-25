// Copyright 2025 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package http

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"net/http"

	"github.com/mintfog/sniffy/ca"
	"github.com/mintfog/sniffy/capture/processors/http/websocket"
	"github.com/mintfog/sniffy/capture/types"
	"github.com/mintfog/sniffy/plugins"
)

var selfCA ca.CA
var sharedHttpClient *http.Client

func init() {
	var err error
	selfCA, err = ca.NewSelfSignedCA()
	if err != nil {
		panic(err)
	}

	// 初始化共享的HTTP客户端，配置连接池
	sharedHttpClient = &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true, // 忽略HTTPS证书
			},
			// 连接池配置
			MaxIdleConns:        MaxIdleConns,
			MaxIdleConnsPerHost: MaxIdleConnsPerHost,
			MaxConnsPerHost:     MaxConnsPerHost,
			IdleConnTimeout:     IdleConnTimeout,
			DisableKeepAlives:   false, // 启用keep-alive
			// TCP连接配置
			ResponseHeaderTimeout: ResponseHeaderTimeout,
			ExpectContinueTimeout: ExpectContinueTimeout,
		},
		Timeout: ClientTimeout,
	}
}

// Processor HTTP协议处理器
type Processor struct {
	conn         types.Connection
	request      *http.Request
	isHttps      bool
	interceptor  *RequestInterceptor
}

// New 创建新的HTTP处理器
func New(conn types.Connection) types.ProtocolProcessor {
	return &Processor{
		conn: conn,
	}
}

// GetProtocolName 返回协议名称
func (p *Processor) GetProtocolName() string {
	return "HTTP"
}

// SetHookExecutor 设置插件钩子执行器
func (p *Processor) SetHookExecutor(hookExecutor *plugins.HookExecutor) {
	if hookExecutor != nil {
		server := p.conn.GetServer()
		logger := &LoggerAdapter{server: server}
		p.interceptor = NewRequestInterceptor(hookExecutor, logger)
	}
}

// LoggerAdapter 适配器，将types.Server转换为types.Logger
type LoggerAdapter struct {
	server types.Server
}

func (la *LoggerAdapter) Info(msg string, args ...interface{}) {
	la.server.LogInfo(msg, args...)
}

func (la *LoggerAdapter) Error(msg string, args ...interface{}) {
	la.server.LogError(msg, args...)
}

func (la *LoggerAdapter) Debug(msg string, args ...interface{}) {
	la.server.LogDebug(msg, args...)
}

func (la *LoggerAdapter) Warn(msg string, args ...interface{}) {
	la.server.LogInfo("[WARN] "+msg, args...)
}

// Process 处理HTTP协议
func (p *Processor) Process() error {
	server := p.conn.GetServer()
	reader := p.conn.GetReader()
	writer := p.conn.GetWriter()

	// 执行具体的HTTP协议处理逻辑
	return p.handleHttpProtocol(server, reader, writer)
}

// handleHttpProtocol 处理HTTP协议的具体逻辑
func (p *Processor) handleHttpProtocol(server types.Server, reader *bufio.Reader, writer *bufio.Writer) error {
	// HTTP协议处理逻辑
	server.LogDebug("处理HTTP协议...")

	request, err := http.ReadRequest(reader)
	if err != nil {
		server.LogError("读取HTTP请求失败: %v", err)
		return err
	}

	p.request = request
	server.LogDebug("请求的域名是：" + request.Host)

	// 如果是CONNECT请求，交给专门的处理方法
	if p.request.Method == http.MethodConnect {
		server.LogDebug("处理CONNECT请求")
		return p.handleConnect(server, reader, writer)
	}

	// 检查是否为WebSocket请求
	if websocket.IsWebSocketRequest(p.request) {
		server.LogDebug("处理WebSocket请求")
		return p.handleWebSocket(server)
	}

	// 处理普通HTTP请求
	return p.handleRequest(server)
}

// handleConnect 专门处理CONNECT请求
func (p *Processor) handleConnect(server types.Server, reader *bufio.Reader, writer *bufio.Writer) error {
	server.LogDebug("处理CONNECT请求，目标地址：%s", p.request.Host)
	fmt.Println(p.request.Header)

	// 发送CONNECT响应，告诉客户端连接已建立
	if _, err := writer.WriteString(ConnectEstablishedResponse); err != nil {
		server.LogError("发送CONNECT响应失败: %v", err)
		return err
	}
	if err := writer.Flush(); err != nil {
		server.LogError("刷新CONNECT响应失败: %v", err)
		return err
	}

	// 读取下一个字节来判断后续的协议类型
	firstByte, err := reader.Peek(1)
	if err != nil {
		server.LogError("读取第一个字节失败: %v", err)
		return err
	}

	// 根据第一个字节判断协议类型
	switch firstByte[0] {
	case TLSHandshakeRecordType: // TLS握手记录类型
		server.LogDebug("检测到TLS握手：0x%02x", firstByte[0])
		p.isHttps = true
		return p.handleTlsHandshake(server, reader)
	case HTTPGetByte, HTTPPostByte: // 'G' (GET) 或 'P' (POST) - HTTP请求
		server.LogDebug("检测到HTTP请求：0x%02x", firstByte[0])
		p.isHttps = false
		// 递归调用handleHttpProtocol处理HTTP请求
		return p.handleHttpProtocol(server, reader, writer)
	default:
		server.LogDebug("未知协议，第一个字节：0x%02x", firstByte[0])
		// 默认尝试作为TLS处理
		p.isHttps = true
		return p.handleTlsHandshake(server, reader)
	}
}

func (p *Processor) handleWebSocket(server types.Server) error {
	// 创建WebSocket处理器并委托处理
	wsProcessor := websocket.New(p.conn, p.request, p.isHttps)
	
	// 如果有拦截器，设置钩子执行器  
	if p.interceptor != nil {
		// 通过反射或者添加getter方法来获取hookExecutor
		// 这里我们需要为RequestInterceptor添加一个获取hookExecutor的方法
		if hookExecutor := p.interceptor.GetHookExecutor(); hookExecutor != nil {
			wsProcessor.SetHookExecutor(hookExecutor)
		}
	}
	
	return wsProcessor.Process(server)
}

// handleTlsHandshake 处理TLS握手
func (p *Processor) handleTlsHandshake(server types.Server, reader *bufio.Reader) error {
	tlsHandler := newTLSHandler(p)
	return tlsHandler.handleTlsHandshake(server, reader)
}

// 转发请求
func (p *Processor) handleRequest(server types.Server) error {

	var request *http.Request
	if p.request == nil {
		var err error
		request, err = http.ReadRequest(p.conn.GetReader())
		if err != nil {
			server.LogError("读取HTTP请求失败: %v", err)
			return err
		}
	} else {
		request = p.request
	}

	// 构建完整的URL
	if request.URL.Scheme == "" {
		if p.isHttps {
			request.URL.Scheme = "https"
		} else {
			request.URL.Scheme = "http"
		}
	}
	if request.URL.Host == "" {
		request.URL.Host = request.Host
	}

	// 清空RequestURI，避免客户端请求错误
	request.RequestURI = ""

	// 调用插件请求拦截器
	if p.interceptor != nil {
		interceptedRequest, err := p.interceptor.InterceptRequest(request, p.conn)
		if err != nil {
			if _, ok := err.(*InterceptError); ok {
				server.LogInfo("请求被插件拦截: %v", err)
				// 返回插件定制的响应或默认响应
				writer := p.conn.GetWriter()
				_, _ = writer.WriteString("HTTP/1.1 403 Forbidden\r\nContent-Type: text/plain\r\n\r\nRequest blocked by plugin\r\n")
				return writer.Flush()
			}
			server.LogError("请求拦截器错误: %v", err)
		}
		if interceptedRequest != nil {
			request = interceptedRequest
		}
	}

	// 发起请求 (使用共享连接池)
	resp, err := sharedHttpClient.Do(request)
	if err != nil {
		server.LogError("请求失败: %v", err)
		// 返回502错误
		writer := p.conn.GetWriter()
		_, _ = writer.WriteString(BadGatewayResponse)
		return writer.Flush()
	}
	defer resp.Body.Close()

	// 调用插件响应拦截器
	if p.interceptor != nil {
		interceptedResponse, err := p.interceptor.InterceptResponse(resp, request, p.conn)
		if err != nil {
			if _, ok := err.(*InterceptError); ok {
				server.LogInfo("响应被插件拦截: %v", err)
				// 返回插件定制的响应或默认响应
				writer := p.conn.GetWriter()
				_, _ = writer.WriteString("HTTP/1.1 502 Bad Gateway\r\nContent-Type: text/plain\r\n\r\nResponse blocked by plugin\r\n")
				return writer.Flush()
			}
			server.LogError("响应拦截器错误: %v", err)
		}
		if interceptedResponse != nil {
			resp = interceptedResponse
		}
	}

	// 获取原始连接，直接写入响应
	err = resp.Write(p.conn.GetConn())
	if err != nil {
		server.LogError("写入响应失败: %v", err)
		return err
	}

	return nil
}
