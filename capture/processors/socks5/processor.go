// Copyright 2025 The f-dong Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package socks5

import (
	"bufio"
	"fmt"

	"github.com/f-dong/sniffy/capture/core"
)

// Processor SOCKS5协议处理器
type Processor struct {
	conn core.Connection
}

// New 创建新的SOCKS5处理器
func New(conn core.Connection) core.ProtocolProcessor {
	return &Processor{
		conn: conn,
	}
}

// Process 处理SOCKS5连接
func (p *Processor) Process() error {
	server := p.conn.GetServer()
	reader := p.conn.GetReader()
	writer := p.conn.GetWriter()

	// TODO: 实现SOCKS5协议处理逻辑
	server.LogInfo("Processing SOCKS5 connection from %s", p.conn.GetConn().RemoteAddr().String())
	return p.handleSocks5Protocol(server, reader, writer)
}

// GetProtocolName 获取协议名称
func (p *Processor) GetProtocolName() string {
	return "SOCKS5"
}

// handleSocks5Protocol 处理SOCKS5协议逻辑
func (p *Processor) handleSocks5Protocol(server core.Server, reader *bufio.Reader, writer *bufio.Writer) error {
	// 读取版本和认证方法数量
	version, err := reader.ReadByte()
	if err != nil {
		return err
	}

	if version != 0x05 {
		return fmt.Errorf("unsupported SOCKS version: %d", version)
	}

	server.LogInfo("SOCKS5 version: %d", version)

	// 简单回复，拒绝连接
	_, err = writer.Write([]byte{0x05, 0xFF}) // 无可接受的方法
	if err != nil {
		return err
	}
	return writer.Flush()
}
