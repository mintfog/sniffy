// Copyright 2025 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package http

import "time"

const (
	// 协议检测相关常量
	TLSHandshakeRecordType = 0x16 // TLS握手记录类型
	HTTPGetByte            = 0x47 // 'G' (GET)
	HTTPPostByte           = 0x50 // 'P' (POST)

	// 连接池配置常量
	MaxIdleConns          = 1000             // 最大空闲连接数
	MaxIdleConnsPerHost   = 100              // 每个主机的最大空闲连接数
	MaxConnsPerHost       = 500              // 每个主机的最大连接数
	IdleConnTimeout       = 90 * time.Second // 空闲连接超时时间
	ResponseHeaderTimeout = 30 * time.Second // 响应头超时
	ExpectContinueTimeout = 1 * time.Second  // 100-continue超时
	ClientTimeout         = 10 * time.Minute // 客户端总超时时间

	// TLS相关常量
	TLSHandshakeTimeout  = 30 * time.Second // TLS握手超时
	TLSConnectionTimeout = 5 * time.Minute  // TLS连接超时

	// HTTP响应模板
	ConnectEstablishedResponse = "HTTP/1.1 200 Connection Established\r\n\r\n"
	BadGatewayResponse         = "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 15\r\n\r\n502 Bad Gateway"
)
