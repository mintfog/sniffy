// Copyright 2025 The f-dong Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package capture

// HTTP Method constants
const (
	MethodGet     = 'G' // GET
	MethodPost    = 'P' // POST, PUT, PATCH
	MethodDelete  = 'D' // DELETE
	MethodOptions = 'O' // OPTIONS
	MethodHead    = 'H' // HEAD
	MethodConnect = 'C' // CONNECT
)

// Protocol constants
const (
	SocksFive = 0x05 // SOCKS5协议标识

	// TLS/SSL
	TLSHandshake = 0x16 // TLS握手
	TLSAlert     = 0x15 // TLS警告
	TLSAppData   = 0x17 // TLS应用数据

	// SSH
	SSHVersion = 'S' // SSH-2.0 开头

	// FTP
	FTPResponse = '2' // FTP响应通常以数字开头 (220, 230等)

	// Other protocols
	DNSQuery    = 0x00 // DNS查询标识
	MQTTConnect = 0x10 // MQTT连接
	RDPRequest  = 0x03 // RDP连接请求
)
