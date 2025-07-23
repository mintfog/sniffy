// Copyright 2025 The f-dong Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package capture

// ProxyHttp HTTP协议处理器
type ProxyHttp struct {
	ConnPeer
}

func (p *ProxyHttp) Process() error {
	// TODO: 实现HTTP协议处理逻辑
	p.server.logInfo("Processing HTTP connection from %s", p.conn.RemoteAddr().String())
	return p.handleHttpProtocol()
}

func (p *ProxyHttp) GetProtocolName() string {
	return "HTTP"
}

func (p *ProxyHttp) handleHttpProtocol() error {
	for {
		line, err := p.reader.ReadString('\n')
		if err != nil {
			return err
		}

		if p.server.config.IsLoggingEnabled() {
			p.server.logInfo("HTTP line: %q", line)
		}

		// 简单回复
		response := "HTTP/1.1 200 OK\r\nContent-Length: 13\r\n\r\nHello, World!"
		_, err = p.writer.WriteString(response)
		if err != nil {
			return err
		}
		return p.writer.Flush()
	}
}
