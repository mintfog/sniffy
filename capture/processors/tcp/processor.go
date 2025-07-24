// Copyright 2025 The f-dong Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package tcp

import (
	"bufio"
	"fmt"

	"github.com/f-dong/sniffy/capture/core"
)

// Processor 默认TCP协议处理器
type Processor struct {
	Conn core.Connection
}

// New 创建新的TCP处理器
func New(conn core.Connection) core.ProtocolProcessor {
	return &Processor{
		Conn: conn,
	}
}

// Process 处理TCP连接
func (p *Processor) Process() error {
	server := p.Conn.GetServer()
	reader := p.Conn.GetReader()
	writer := p.Conn.GetWriter()

	// TODO: 实现通用TCP协议处理逻辑
	server.LogInfo("Processing TCP connection from %s", p.Conn.GetConn().RemoteAddr().String())
	return p.handleTcpProtocol(server, reader, writer)
}

// GetProtocolName 获取协议名称
func (p *Processor) GetProtocolName() string {
	return "TCP"
}

// handleTcpProtocol 处理TCP协议逻辑
func (p *Processor) handleTcpProtocol(server core.Server, reader *bufio.Reader, writer *bufio.Writer) error {
	// 处理原始TCP数据
	buffer := make([]byte, server.GetConfig().GetBufferSize())

	for {
		n, err := reader.Read(buffer)
		if err != nil {
			return err
		}

		if n > 0 {
			if server.GetConfig().IsLoggingEnabled() {
				server.LogInfo("TCP data: %d bytes - %s", n, server.FormatDataPreview(buffer[:n]))
			}

			// Echo back the data
			response := fmt.Sprintf("TCP Echo: %d bytes received\n", n)
			_, err = writer.WriteString(response)
			if err != nil {
				return err
			}
			err = writer.Flush()
			if err != nil {
				return err
			}
		}
	}
}
