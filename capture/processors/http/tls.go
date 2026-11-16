// Copyright 2025 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package http

import (
	"bufio"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"time"

	"github.com/mintfog/sniffy/capture/types"
)

// readerConn 包装连接，读取改从 bufio.Reader 取：reader 里可能已有预读进来的字节。
type readerConn struct {
	net.Conn
	reader *bufio.Reader
}

// CloseWrite 把半关闭转给底层连接。
func (rc *readerConn) CloseWrite() error {
	return types.CloseWrite(rc.Conn)
}

// Read 从bufio.Reader读取数据
func (rc *readerConn) Read(b []byte) (n int, err error) {
	return rc.reader.Read(b)
}

// peekedTLSConn 先供出 reader 里已预读的字节，读完后直接读 TLS 连接。嵌入 *tls.Conn 是为了
// 留住 ConnectionState：http2 服务端经它得知连接走的是 TLS，并据此给请求填 r.TLS。
// onData 非 nil 时在首次读到字节后调用一次。不支持并发 Read。
type peekedTLSConn struct {
	*tls.Conn
	reader *bufio.Reader
	onData func()
}

func (c *peekedTLSConn) Read(b []byte) (n int, err error) {
	if c.reader != nil && c.reader.Buffered() > 0 {
		n, err = c.reader.Read(b)
	} else {
		c.reader = nil
		n, err = c.Conn.Read(b)
	}
	if n > 0 && c.onData != nil {
		c.onData()
		c.onData = nil
	}
	return n, err
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

	host := t.processor.request.Host
	transparent := t.processor.originalDst != ""
	certGen := mitmBypass.certGeneration() // 须在取证书之前读，见 certGeneration
	// 优先使用用户为该主机导入的真实服务端证书(应对固定证书场景);未命中再回退到 CA 现签的
	// 伪造证书(并发安全地取当前 CA,兼容运行时重新生成)。
	cert := importedServerCertFor(host)
	if cert == nil {
		rootCA := currentCA()
		if rootCA == nil {
			err := errors.New("根证书尚未配置")
			server.LogError("生成证书失败: %v", err)
			t.processor.recordTLSFailure(host, err)
			return err
		}
		issued, err := rootCA.IssueCert(host)
		if err != nil {
			server.LogError("生成证书失败: %v", err)
			t.processor.recordTLSFailure(host, err)
			return err
		}
		cert = issued
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
		if transparent {
			mitmBypass.noteFailure(host, err, certGen)
		}
		return err
	}

	server.LogDebug("TLS握手成功")
	t.processor.conn.SetConn(connSsl)

	alpn := connSsl.ConnectionState().NegotiatedProtocol
	var clientSilent bool
	if transparent {
		// 没协商出 ALPN 的多半不是 HTTP，等不了 postHandshakeWindow 那么久：服务端先说话的
		// 协议（IMAPS、SMTPS）要靠这段沉默判断出来。
		window := postHandshakeWindow
		if alpn == "" {
			window = transparentSniffTimeout
		}
		var err error
		clientSilent, err = t.processor.awaitFirstBytes(host, window, certGen)
		if errors.Is(err, io.EOF) {
			return nil // 应用没发数据就正常关闭，awaitFirstBytes 已记下这次失败
		}
		if err != nil {
			return err
		}
	}

	// 按 ALPN 协商结果分流:协商为 h2 时交给 HTTP/2 服务端处理,其余按 HTTP/1.1 处理。
	if alpn == "h2" {
		server.LogDebug("ALPN 协商为 h2,启用 HTTP/2 处理")
		_ = connSsl.SetDeadline(time.Time{}) // h2 为长连接,清除握手期设置的绝对超时
		// awaitFirstBytes 预读的字节留在 reader 里，HTTP/2 服务端要先读到它们。
		conn := &peekedTLSConn{Conn: connSsl, reader: t.processor.conn.GetReader()}
		if clientSilent {
			conn.onData = func() { mitmBypass.noteSuccess(host, certGen) }
		}
		return serveHTTP2(server, conn, t.processor.originalDst, host)
	}

	if transparent {
		// 透明连接解密后未必是发给源站的 HTTP：没协商出 ALPN 的可能是别的协议，协商出
		// http/1.1 的对端也可能是应用自己配置的 HTTPS 代理。这两种都原样中继到原始目标。
		var relayed bool
		var err error
		if alpn == "" {
			relayed, err = t.processor.relayUnlessHTTP(server, host, clientSilent)
		} else {
			relayed, err = t.processor.relayIfProxyRequest(server, host)
		}
		if relayed {
			return err
		}
		// 窗口内沉默的应用在 relayIfProxyRequest 等首行期间发出了字节，补记成功。
		if clientSilent && t.processor.conn.GetReader().Buffered() > 0 {
			mitmBypass.noteSuccess(host, certGen)
		}
	}

	// 清除握手期的绝对截止时间。HTTP/1.1 连接现在可以跨多个请求复用，若保留一个
	// 从握手时刻计算的绝对期限，连接即使持续活跃也会在到点后失效。改由 Processor
	// 逐次续期：读侧按 ReadTimeout、写侧按 WriteTimeout，两者都是停滞期限而非总时长。
	_ = connSsl.SetDeadline(time.Time{})

	// 清空请求，避免重复处理，等待新的HTTPS请求
	t.processor.request = nil

	// 递归调用handleHttpProtocol处理后续的HTTPS请求
	return t.processor.handleHttpProtocol(server, t.processor.conn.GetReader(), t.processor.conn.GetWriter())
}
