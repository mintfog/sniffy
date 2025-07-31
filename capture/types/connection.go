// Copyright 2025 The f-dong Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package types

import (
	"bufio"
	"net"
)

// DefaultConnection 默认连接实现
type DefaultConnection struct {
	conn   net.Conn
	reader *bufio.Reader
	writer *bufio.Writer
	server Server
}

// NewConnection 创建新的连接实例
func NewConnection(conn net.Conn, server Server) Connection {
	return &DefaultConnection{
		conn:   conn,
		reader: bufio.NewReader(conn),
		writer: bufio.NewWriter(conn),
		server: server,
	}
}

// GetConn 获取原始网络连接
func (c *DefaultConnection) GetConn() net.Conn {
	return c.conn
}

// SetConn 设置原始网络连接
func (c *DefaultConnection) SetConn(conn net.Conn) {
	c.conn = conn
	c.reader = bufio.NewReader(conn)
	c.writer = bufio.NewWriter(conn)
}

// GetReader 获取缓冲读取器
func (c *DefaultConnection) GetReader() *bufio.Reader {
	return c.reader
}

// GetWriter 获取缓冲写入器
func (c *DefaultConnection) GetWriter() *bufio.Writer {
	return c.writer
}

// GetServer 获取服务器实例
func (c *DefaultConnection) GetServer() Server {
	return c.server
}

// Close 关闭连接
func (c *DefaultConnection) Close() error {
	if c.writer != nil {
		c.writer.Flush()
	}
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}
