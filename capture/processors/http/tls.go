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

	// 生成自签名证书(并发安全地取当前 CA,兼容运行时重新生成)
	host := t.processor.request.Host
	cert, err := currentCA().IssueCert(host)
	if err != nil {
		server.LogError("生成证书失败: %v", err)
		t.processor.recordTLSFailure(host, err)
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
		// 向客户端通告 ALPN:优先 h2,回退 http/1.1。不通告时,所有支持 h2 的客户端
		// 都会被静默降级到 HTTP/1.1(历史行为),h2 流量无从抓起。
		NextProtos: []string{"h2", "http/1.1"},
	})

	// 执行TLS握手
	if err := connSsl.Handshake(); err != nil {
		server.LogError("TLS握手失败: %v", err)
		t.processor.recordTLSFailure(host, err)
		return err
	}

	server.LogDebug("TLS握手成功")
	t.processor.conn.SetConn(connSsl)

	// 按 ALPN 协商结果分流:协商为 h2 时交给 HTTP/2 服务端处理,其余按 HTTP/1.1 处理。
	if connSsl.ConnectionState().NegotiatedProtocol == "h2" {
		server.LogDebug("ALPN 协商为 h2,启用 HTTP/2 处理")
		_ = connSsl.SetDeadline(time.Time{}) // h2 为长连接,清除握手期设置的绝对超时
		return serveHTTP2(server, connSsl)
	}

	// 设置连接超时(HTTP/1.1)
	_ = connSsl.SetDeadline(time.Now().Add(TLSConnectionTimeout))

	// 清空请求，避免重复处理，等待新的HTTPS请求
	t.processor.request = nil

	// 递归调用handleHttpProtocol处理后续的HTTPS请求
	return t.processor.handleHttpProtocol(server, t.processor.conn.GetReader(), t.processor.conn.GetWriter())
}
