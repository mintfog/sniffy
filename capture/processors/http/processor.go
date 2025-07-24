// Copyright 2025 The f-dong Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package http

import (
	"bufio"

	"github.com/f-dong/sniffy/capture/core"
)

// Processor HTTP协议处理器
type Processor struct {
	conn core.Connection
}

// New 创建新的HTTP处理器
func New(conn core.Connection) core.ProtocolProcessor {
	return &Processor{
		conn: conn,
	}
}

// Process 处理HTTP连接
func (p *Processor) Process() error {
	server := p.conn.GetServer()
	reader := p.conn.GetReader()
	writer := p.conn.GetWriter()

	// TODO: 实现HTTP协议处理逻辑
	server.LogInfo("Processing HTTP connection from %s", p.conn.GetConn().RemoteAddr().String())
	return p.handleHttpProtocol(server, reader, writer)
}

// GetProtocolName 获取协议名称
func (p *Processor) GetProtocolName() string {
	return "HTTP"
}

// handleHttpProtocol 处理HTTP协议逻辑
func (p *Processor) handleHttpProtocol(server core.Server, reader *bufio.Reader, writer *bufio.Writer) error {
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return err
		}

		if server.GetConfig().IsLoggingEnabled() {
			server.LogInfo("HTTP line: %q", line)
		}

		// 简单回复
		response := "HTTP/1.1 200 OK\r\nContent-Length: 13\r\n\r\nHello, World!"
		_, err = writer.WriteString(response)
		if err != nil {
			return err
		}
		return writer.Flush()
	}
}
