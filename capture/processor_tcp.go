// Copyright 2025 The f-dong Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package capture

import "fmt"

// ProxyTcp 默认TCP协议处理器
type ProxyTcp struct {
	ConnPeer
}

func (p *ProxyTcp) Process() error {
	// TODO: 实现通用TCP协议处理逻辑
	p.server.logInfo("Processing TCP connection from %s", p.conn.RemoteAddr().String())
	return p.handleTcpProtocol()
}

func (p *ProxyTcp) GetProtocolName() string {
	return "TCP"
}

func (p *ProxyTcp) handleTcpProtocol() error {
	// 处理原始TCP数据
	buffer := make([]byte, p.server.config.GetBufferSize())

	for {
		n, err := p.reader.Read(buffer)
		if err != nil {
			return err
		}

		if n > 0 {
			if p.server.config.IsLoggingEnabled() {
				p.server.logInfo("TCP data: %d bytes - %s", n, p.server.formatDataPreview(buffer[:n]))
			}

			// Echo back the data
			response := fmt.Sprintf("TCP Echo: %d bytes received\n", n)
			_, err = p.writer.WriteString(response)
			if err != nil {
				return err
			}
			err = p.writer.Flush()
			if err != nil {
				return err
			}
		}
	}
}
