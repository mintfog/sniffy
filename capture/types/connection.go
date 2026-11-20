// Copyright 2025 The f-dong Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package types

import (
	"bufio"
	"errors"
	"net"
	"sync"
)

// connReadBufferSize 是连接读缓冲大小。设为 64KB 以容纳常见(乃至偏大)的请求头块,
// 使读取侧能在 http.ReadRequest 之前 Peek 出完整头块、抓取头部原始顺序与大小写
// (无侵入转发所需)。头块超过该大小时退化为标准转发。
const connReadBufferSize = 64 * 1024

// connReaders 复用连接读缓冲（见 ReleaseReader）：盲转发的连接因此不必各自分配并清零 64KB，
// 再交给 GC 回收。
var connReaders = sync.Pool{New: func() any { return bufio.NewReaderSize(nil, connReadBufferSize) }}

func newConnReader(conn net.Conn) *bufio.Reader {
	reader := connReaders.Get().(*bufio.Reader)
	reader.Reset(conn)
	return reader
}

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
		reader: newConnReader(conn),
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
	c.reader = newConnReader(conn)
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

// CloseWrite 关闭 conn 的写端，conn 不支持半关闭时返回 errors.ErrUnsupported。
// 嵌入 net.Conn 接口的包装类型要显式实现 CloseWrite 并转给底层连接，才算支持。
func CloseWrite(conn net.Conn) error {
	if half, ok := conn.(interface{ CloseWrite() error }); ok {
		return half.CloseWrite()
	}
	return errors.ErrUnsupported
}

// ReleaseReader 把 conn 的读缓冲交给之后的连接复用，此后 conn.GetReader 返回 nil。conn 不是
// NewConnection 创建的时什么都不做。
//
// 只能在确定再没有谁会读它时调用：读缓冲会被别的连接拿去，残留的读取者读到的就是别的连接
// 的数据。读缓冲交给过自起 goroutine 读取的组件（如 HTTP/2 服务端的读帧循环）的连接不满足
// 这一点。
func ReleaseReader(conn Connection) {
	c, ok := conn.(*DefaultConnection)
	if !ok || c.reader == nil {
		return
	}
	c.reader.Reset(nil)
	connReaders.Put(c.reader)
	c.reader = nil
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
