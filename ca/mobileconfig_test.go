// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package ca

import (
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"io"
	"strings"
	"testing"
)

func TestMobileconfigNil(t *testing.T) {
	if got := Mobileconfig(nil); got != nil {
		t.Errorf("Mobileconfig(nil) = %v, want nil", got)
	}
}

func TestMobileconfig(t *testing.T) {
	c, err := NewInMemorySelfSignedCA()
	if err != nil {
		t.Fatalf("创建 CA 失败: %v", err)
	}
	cert := c.GetCA()
	profile := Mobileconfig(cert)

	// 良构性:整份 plist 能被 XML 解析器走完全部 token 而不报错
	// (可捕获未转义字符、标签不闭合等结构错误)。
	dec := xml.NewDecoder(bytes.NewReader(profile))
	for {
		if _, err := dec.Token(); err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("mobileconfig 不是良构 XML: %v", err)
		}
	}

	s := string(profile)
	for _, want := range []string{
		"<key>PayloadType</key>",
		"com.apple.security.root", // 根证书 payload 类型
		"Sniffy Root CA",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("mobileconfig 缺少 %q", want)
		}
	}

	// 内嵌 <data> 去除空白后 base64 解码,应精确还原输入 DER 字节
	// (证明证书在描述文件中无损嵌入)。
	start := strings.Index(s, "<data>")
	end := strings.Index(s, "</data>")
	if start < 0 || end < 0 || end < start {
		t.Fatal("mobileconfig 未找到 <data> 块")
	}
	b64 := strings.Join(strings.Fields(s[start+len("<data>"):end]), "")
	got, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("内嵌证书 base64 解码失败: %v", err)
	}
	if !bytes.Equal(got, cert.Raw) {
		t.Errorf("内嵌证书与输入 DER 不一致: got %d 字节, want %d 字节", len(got), len(cert.Raw))
	}
}
