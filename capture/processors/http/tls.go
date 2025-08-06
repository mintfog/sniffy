// Copyright 2025 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package http

import (
	"bufio"
	"crypto/tls"
	"net"
	"time"

	"github.com/mintfog/sniffy/capture/types"
)

// readerConn 包装连接，确保TLS可以从bufio.Reader读取数据
type readerConn struct {
	net.Conn
	reader *bufio.Reader
}

// Read 从bufio.Reader读取数据
func (rc *readerConn) Read(b []byte) (n int, err error) {
	return rc.reader.Read(b)
}

// TLSHandler TLS处理器
type TLSHandler struct {
	processor *Processor
}

// newTLSHandler 创建新的TLS处理器
func newTLSHandler(processor *Processor) *TLSHandler {
	return &TLSHandler{
		processor: processor,
	}
}

// handleTlsHandshake 处理TLS握手
func (t *TLSHandler) handleTlsHandshake(server types.Server, reader *bufio.Reader) error {
	server.LogDebug("开始TLS握手")

	// 创建包装连接，让TLS能够从bufio.Reader读取数据
	conn := &readerConn{
		Conn:   t.processor.conn.GetConn(),
		reader: reader,
	}

	// 生成自签名证书
	cert, err := selfCA.IssueCert(t.processor.request.Host)
	if err != nil {
		server.LogError("生成证书失败: %v", err)
		return err
	}

	// 设置TLS握手超时
	if err := conn.SetDeadline(time.Now().Add(TLSHandshakeTimeout)); err != nil {
		server.LogError("设置连接超时失败: %v", err)
		return err
	}

	// 创建TLS连接
	connSsl := tls.Server(conn, &tls.Config{
		Certificates: []tls.Certificate{*cert},
	})

	// 执行TLS握手
	if err := connSsl.Handshake(); err != nil {
		server.LogError("TLS握手失败: %v", err)
		return err
	}

	server.LogDebug("TLS握手成功")

	// 设置连接超时
	_ = connSsl.SetDeadline(time.Now().Add(TLSConnectionTimeout))
	t.processor.conn.SetConn(connSsl)

	// 清空请求，避免重复处理，等待新的HTTPS请求
	t.processor.request = nil

	// 递归调用handleHttpProtocol处理后续的HTTPS请求
	return t.processor.handleHttpProtocol(server, t.processor.conn.GetReader(), t.processor.conn.GetWriter())
}
