// Copyright 2025 The f-dong Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package http

import (
	"bufio"
	"crypto/tls"
	"io"
	"net/http"
	"time"

	"github.com/f-dong/sniffy/ca"
	"github.com/f-dong/sniffy/capture/types"
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
			MaxIdleConns:        1000,             // 最大空闲连接数
			MaxIdleConnsPerHost: 100,              // 每个主机的最大空闲连接数
			MaxConnsPerHost:     500,              // 每个主机的最大连接数
			IdleConnTimeout:     90 * time.Second, // 空闲连接超时时间
			DisableKeepAlives:   false,            // 启用keep-alive
			// TCP连接配置
			ResponseHeaderTimeout: 30 * time.Second, // 响应头超时
			ExpectContinueTimeout: 1 * time.Second,  // 100-continue超时
		},
		Timeout: 10 * time.Minute,
	}
}

// Processor HTTP协议处理器
type Processor struct {
	conn    types.Connection
	request *http.Request
	isHttps bool
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

	// 如果是CONNECT请求，则处理HTTPS代理
	if p.request.Method == http.MethodConnect {
		p.isHttps = true
		server.LogDebug("处理HTTPS代理请求")
		return p.handleHttpsProxy(server, writer)
	}

	if p.request.Header.Get("Upgrade") == "websocket" && p.request.Header.Get("Connection") == "Upgrade" {
		// websocket
	}

	return p.handleRequest(server, writer)
}

// https握手
func (p *Processor) handleHttpsProxy(server types.Server, writer *bufio.Writer) error {

	// 直接返回连接成功
	const response = "HTTP/1.1 200 Connection Established\r\n\r\n"
	_, err := writer.WriteString(response)
	if err != nil {
		return err
	}

	// 伪造tls握手
	cert, err := selfCA.IssueCert(p.request.Host)
	if err != nil {
		return err
	}

	connSsl := tls.Server(p.conn.GetConn(), &tls.Config{
		Certificates: []tls.Certificate{*cert},
	})

	err = connSsl.Handshake()
	if err != nil {
		// 正常情况
		if err == io.EOF && err != io.ErrClosedPipe {
			server.LogError("TLS握手失败: %v", err)
			return err
		}
	}

	_ = connSsl.SetDeadline(time.Now().Add(time.Second * 60))
	p.conn.SetConn(connSsl)

	return p.handleRequest(server, writer)
}

// 转发请求
func (p *Processor) handleRequest(server types.Server, writer *bufio.Writer) error {

	request := p.request

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

	// 发起请求 (使用共享连接池)
	resp, err := sharedHttpClient.Do(request)
	if err != nil {
		server.LogError("请求失败: %v", err)
		// 返回502错误
		errorResp := "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 15\r\n\r\n502 Bad Gateway"
		_, _ = writer.WriteString(errorResp)
		return writer.Flush()
	}
	defer resp.Body.Close()

	// 获取原始连接，直接写入响应
	err = resp.Write(p.conn.GetConn())
	if err != nil {
		server.LogError("写入响应失败: %v", err)
		return err
	}

	return nil
}
