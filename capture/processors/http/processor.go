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

func init() {
	var err error
	selfCA, err = ca.NewSelfSignedCA()
	if err != nil {
		panic(err)
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

	p.handleHttp(server, writer)

	response := "HTTP/1.1 200 OK\r\nContent-Length: 13\r\n\r\nHello, World!"
	_, _ = writer.WriteString(response)
	return writer.Flush()
}

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

	return nil
}

func (p *Processor) handleHttp(server types.Server, writer *bufio.Writer) error {

}
