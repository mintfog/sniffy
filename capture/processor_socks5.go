// Copyright 2025 The f-dong Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package capture

import "fmt"

// ProxySocks5 SOCKS5协议处理器
type ProxySocks5 struct {
	ConnPeer
}

func (p *ProxySocks5) Process() error {
	// TODO: 实现SOCKS5协议处理逻辑
	p.server.logInfo("Processing SOCKS5 connection from %s", p.conn.RemoteAddr().String())
	return p.handleSocks5Protocol()
}

func (p *ProxySocks5) GetProtocolName() string {
	return "SOCKS5"
}

func (p *ProxySocks5) handleSocks5Protocol() error {
	// 读取版本和认证方法数量
	version, err := p.reader.ReadByte()
	if err != nil {
		return err
	}

	if version != 0x05 {
		return fmt.Errorf("unsupported SOCKS version: %d", version)
	}

	p.server.logInfo("SOCKS5 version: %d", version)

	// 简单回复，拒绝连接
	_, err = p.writer.Write([]byte{0x05, 0xFF}) // 无可接受的方法
	if err != nil {
		return err
	}
	return p.writer.Flush()
}
