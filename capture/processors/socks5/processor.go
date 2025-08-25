// Copyright 2025 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package socks5

import (
	"bufio"

	"github.com/mintfog/sniffy/capture/types"
)

// Processor SOCKS5协议处理器
type Processor struct {
	conn types.Connection
}

// New 创建新的SOCKS5处理器
func New(conn types.Connection) types.ProtocolProcessor {
	return &Processor{
		conn: conn,
	}
}

// GetProtocolName 返回协议名称
func (p *Processor) GetProtocolName() string {
	return "SOCKS5"
}

// Process 处理SOCKS5协议
func (p *Processor) Process() error {
	server := p.conn.GetServer()
	reader := p.conn.GetReader()
	writer := p.conn.GetWriter()

	server.LogInfo("开始处理SOCKS5连接")

	// 执行具体的SOCKS5协议处理逻辑
	return p.handleSocks5Protocol(server, reader, writer)
}

// handleSocks5Protocol 处理SOCKS5协议的具体逻辑
func (p *Processor) handleSocks5Protocol(server types.Server, reader *bufio.Reader, writer *bufio.Writer) error {
	// SOCKS5协议处理逻辑
	server.LogInfo("处理SOCKS5协议...")

	// 这里应该实现实际的SOCKS5协议处理逻辑
	// 例如：SOCKS5握手、身份验证、连接建立等

	return nil
}
