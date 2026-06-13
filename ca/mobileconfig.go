// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package ca

import (
	"bytes"
	"crypto/x509"
	"encoding/base64"
)

// Mobileconfig 生成 Apple 配置描述文件(未签名),内嵌 DER 编码的根证书,
// 供 iOS Safari 下载安装。返回的字节即 .mobileconfig 文件内容;cert 为 nil 时返回 nil。
//
// 注意:描述文件只能将证书加入设备,「完全信任」仍需用户在
// 「设置 → 通用 → 关于本机 → 证书信任设置」手动开启(Apple 自 iOS 10.3 起的限制)。
func Mobileconfig(cert *x509.Certificate) []byte {
	if cert == nil {
		return nil
	}
	// plist <data> 惯例:base64 按 64 字符换行并缩进。
	raw := base64.StdEncoding.EncodeToString(cert.Raw)
	var b64 bytes.Buffer
	for i := 0; i < len(raw); i += 64 {
		end := min(i+64, len(raw))
		b64.WriteString("\t\t\t\t")
		b64.WriteString(raw[i:end])
		b64.WriteByte('\n')
	}
	return []byte(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>PayloadContent</key>
	<array>
		<dict>
			<key>PayloadCertificateFileName</key>
			<string>sniffy-ca.crt</string>
			<key>PayloadContent</key>
			<data>
` + b64.String() + `			</data>
			<key>PayloadDescription</key>
			<string>Sniffy Root CA</string>
			<key>PayloadDisplayName</key>
			<string>Sniffy Root CA</string>
			<key>PayloadIdentifier</key>
			<string>com.mintfog.sniffy.ca</string>
			<key>PayloadType</key>
			<string>com.apple.security.root</string>
			<key>PayloadUUID</key>
			<string>1A2B3C4D-5E6F-7890-ABCD-EF1234567890</string>
			<key>PayloadVersion</key>
			<integer>1</integer>
		</dict>
	</array>
	<key>PayloadDescription</key>
	<string>安装 Sniffy 根证书以解密 HTTPS 流量</string>
	<key>PayloadDisplayName</key>
	<string>Sniffy Root CA</string>
	<key>PayloadIdentifier</key>
	<string>com.mintfog.sniffy</string>
	<key>PayloadRemovalDisallowed</key>
	<false/>
	<key>PayloadType</key>
	<string>Configuration</string>
	<key>PayloadUUID</key>
	<string>A1B2C3D4-E5F6-7890-ABCD-123456789012</string>
	<key>PayloadVersion</key>
	<integer>1</integer>
</dict>
</plist>`)
}
