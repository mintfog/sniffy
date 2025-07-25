// Copyright 2025 The f-dong Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package tcp

import (
	"bufio"

	"github.com/f-dong/sniffy/capture/types"
)

// Processor TCP协议处理器
type Processor struct {
	Conn types.Connection
}

// New 创建新的TCP处理器
func New(conn types.Connection) types.ProtocolProcessor {
	return &Processor{
		Conn: conn,
	}
}

// GetProtocolName 返回协议名称
func (p *Processor) GetProtocolName() string {
	return "TCP"
}

// Process 处理TCP协议
func (p *Processor) Process() error {
	server := p.Conn.GetServer()
	reader := p.Conn.GetReader()
	writer := p.Conn.GetWriter()

	server.LogInfo("开始处理TCP连接")

	// 执行具体的TCP协议处理逻辑
	return p.handleTcpProtocol(server, reader, writer)
}

// handleTcpProtocol 处理TCP协议的具体逻辑
func (p *Processor) handleTcpProtocol(server types.Server, reader *bufio.Reader, writer *bufio.Writer) error {
	// TCP协议处理逻辑
	server.LogInfo("处理TCP协议...")

	// 这里应该实现实际的TCP协议处理逻辑
	// 例如：数据中继、流量监控等

	return nil
}
