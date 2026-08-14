// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package service

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"strings"
	"testing"

	"github.com/mintfog/sniffy/ca"
	"github.com/mintfog/sniffy/internal/core"
)

// brokenCA 模拟"CA 对象在、根证书却没生成出来"的中间态:c != nil 的判断挡不住,
// 需各导出函数自己兜底。
type brokenCA struct{}

func (brokenCA) GetCA() *x509.Certificate                   { return nil }
func (brokenCA) GetCAKey() any                              { return nil }
func (brokenCA) IssueCert(string) (*tls.Certificate, error) { return nil, errors.New("CA 未就绪") }

func newTestCA(t *testing.T) ca.CA {
	t.Helper()
	c, err := ca.NewInMemorySelfSignedCA()
	if err != nil {
		t.Fatalf("创建 CA 失败: %v", err)
	}
	return c
}

// TestCertExportFormats 逐项覆盖"下载证书"下拉框支持的格式,含 crt/cer、pfx、pem-bundle 等别名。
func TestCertExportFormats(t *testing.T) {
	t.Parallel()
	rootCA := newTestCA(t)
	cs := newCertStore(rootCA)

	tests := []struct {
		name     string
		format   string
		wantMime string
	}{
		{"缺省按 PEM", "", "application/x-pem-file"},
		{"pem", ca.FormatPEM, "application/x-pem-file"},
		{"crt", ca.FormatCRT, "application/x-pem-file"},
		{"cer 别名", "cer", "application/x-pem-file"},
		{"大小写与空格不敏感", "  PEM ", "application/x-pem-file"},
		{"der", ca.FormatDER, "application/x-x509-ca-cert"},
		{"p12", ca.FormatP12, "application/x-pkcs12"},
		{"pfx 别名", "pfx", "application/x-pkcs12"},
		{"bundle", ca.FormatBundle, "application/x-pem-file"},
		{"pem-bundle 别名", "pem-bundle", "application/x-pem-file"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			data, mime, err := cs.ExportAs(tt.format, "pass")
			if err != nil {
				t.Fatalf("导出失败: %v", err)
			}
			if len(data) == 0 {
				t.Fatal("导出内容不应为空")
			}
			if mime != tt.wantMime {
				t.Errorf("MIME = %q, want %q", mime, tt.wantMime)
			}
		})
	}

	if _, _, err := cs.ExportAs("docx", ""); err == nil {
		t.Error("不支持的格式应返回错误")
	}
}

// TestCertExportContents 各格式导出的应是同一张根证书。
func TestCertExportContents(t *testing.T) {
	t.Parallel()
	rootCA := newTestCA(t)
	cs := newCertStore(rootCA)

	pemData, _, err := cs.ExportAs(ca.FormatPEM, "")
	if err != nil {
		t.Fatalf("PEM 导出失败: %v", err)
	}
	block, _ := pem.Decode(pemData)
	if block == nil || block.Type != "CERTIFICATE" {
		t.Fatalf("PEM 应是单个 CERTIFICATE 块: %+v", block)
	}
	if !bytes.Equal(block.Bytes, rootCA.GetCA().Raw) {
		t.Error("PEM 里的证书应是根 CA 本身")
	}

	derData, _, err := cs.ExportAs(ca.FormatDER, "")
	if err != nil {
		t.Fatalf("DER 导出失败: %v", err)
	}
	if !bytes.Equal(derData, rootCA.GetCA().Raw) {
		t.Error("DER 应是根 CA 的裸字节")
	}

	// bundle 供 curl/nginx 一次性载入,必须同时含证书与私钥。
	bundle, _, err := cs.ExportAs(ca.FormatBundle, "")
	if err != nil {
		t.Fatalf("bundle 导出失败: %v", err)
	}
	certBlock, rest := pem.Decode(bundle)
	keyBlock, _ := pem.Decode(rest)
	if certBlock == nil || certBlock.Type != "CERTIFICATE" {
		t.Fatalf("bundle 首块应为证书: %+v", certBlock)
	}
	if keyBlock == nil || !strings.Contains(keyBlock.Type, "PRIVATE KEY") {
		t.Fatalf("bundle 次块应为私钥: %+v", keyBlock)
	}

	// p12 应能用同一口令导回。
	p12, _, err := cs.ExportAs(ca.FormatP12, "s3cret")
	if err != nil {
		t.Fatalf("p12 导出失败: %v", err)
	}
	gotCert, gotKey, err := ca.ImportFromPKCS12(p12, "s3cret")
	if err != nil {
		t.Fatalf("p12 应能用同一口令导回: %v", err)
	}
	if !bytes.Equal(gotCert.Raw, rootCA.GetCA().Raw) || gotKey == nil {
		t.Error("p12 导回的应是同一张根证书与配套私钥")
	}
	if _, _, err := ca.ImportFromPKCS12(p12, "wrong"); err == nil {
		t.Error("口令错误时不应能导回")
	}
}

// TestCertExportWithoutCA CA 尚未就绪(启动早期 / 生成失败)时,导出应返回错误而非空内容。
func TestCertExportWithoutCA(t *testing.T) {
	t.Parallel()
	cs := newCertStore(nil)

	for _, format := range []string{"", ca.FormatPEM, ca.FormatDER, ca.FormatP12, ca.FormatBundle} {
		if _, _, err := cs.ExportAs(format, "pw"); err == nil {
			t.Errorf("格式 %q 在无 CA 时应返回错误", format)
		}
	}
	if got := cs.ExportPEM(); got != nil {
		t.Errorf("无 CA 时 PEM 应为 nil,实际 %d 字节", len(got))
	}
	if got := cs.ExportMobileconfig(); got != nil {
		t.Errorf("无 CA 时描述文件应为 nil,实际 %d 字节", len(got))
	}
}

// TestCertExportWithBrokenCA 根证书缺失时每种格式都应报错,不交出空内容。
func TestCertExportWithBrokenCA(t *testing.T) {
	t.Parallel()
	cs := newCertStore(brokenCA{})

	for _, format := range []string{ca.FormatPEM, ca.FormatDER, ca.FormatP12, ca.FormatBundle} {
		if data, _, err := cs.ExportAs(format, "pw"); err == nil {
			t.Errorf("格式 %q 在根证书缺失时应返回错误,却给出 %d 字节", format, len(data))
		}
	}
	if got := cs.ExportMobileconfig(); got != nil {
		t.Errorf("根证书缺失时描述文件应为 nil,实际 %d 字节", len(got))
	}
}

// TestCertStoreSetCASwapsExports 重新生成根 CA 后,导出应立刻换成新证书。
func TestCertStoreSetCASwapsExports(t *testing.T) {
	t.Parallel()
	first, second := newTestCA(t), newTestCA(t)
	cs := newCertStore(first)

	before := cs.ExportPEM()
	cs.setCA(second)
	after := cs.ExportPEM()

	if bytes.Equal(before, after) {
		t.Fatal("换 CA 后导出内容应随之改变")
	}
	block, _ := pem.Decode(after)
	if block == nil || !bytes.Equal(block.Bytes, second.GetCA().Raw) {
		t.Error("导出的应是新 CA 的证书")
	}
}

// TestMobileconfigEmbedsRootCert iOS 经描述文件装证书,证书字节应嵌在 plist 中。
func TestMobileconfigEmbedsRootCert(t *testing.T) {
	t.Parallel()
	rootCA := newTestCA(t)
	data := newCertStore(rootCA).ExportMobileconfig()

	text := string(data)
	if !strings.Contains(text, "<key>PayloadType</key>") || !strings.Contains(text, "com.apple.security.root") {
		t.Fatal("描述文件应是安装根证书的 plist")
	}
	// plist 里的 base64 按 64 字符分行并带缩进,先还原成连续串再比对。
	compact := strings.NewReplacer("\n", "", "\t", "", " ", "").Replace(text)
	if !strings.Contains(compact, mobileconfigCertB64(t, rootCA)) {
		t.Error("描述文件里嵌的应是当前根证书")
	}
}

func mobileconfigCertB64(t *testing.T, c ca.CA) string {
	t.Helper()
	block := &pem.Block{Type: "CERTIFICATE", Bytes: c.GetCA().Raw}
	body := strings.NewReplacer("\n", "").Replace(string(pem.EncodeToMemory(block)))
	body = strings.TrimPrefix(body, "-----BEGIN CERTIFICATE-----")
	return strings.TrimSuffix(body, "-----END CERTIFICATE-----")
}

// TestServiceCertificateDelegates 守住 Service 到 certStore 的方法转发。
func TestServiceCertificateDelegates(t *testing.T) {
	t.Parallel()
	rootCA := newTestCA(t)
	svc := New(rootCA, core.NewEventBus(), "", "")

	if block, _ := pem.Decode(svc.CertificatePEM()); block == nil || !bytes.Equal(block.Bytes, rootCA.GetCA().Raw) {
		t.Error("CertificatePEM 应导出根证书")
	}
	if len(svc.IOSMobileconfig()) == 0 {
		t.Error("IOSMobileconfig 不应为空")
	}
	if _, mime, err := svc.CertificateExportAs(ca.FormatDER, ""); err != nil || mime != "application/x-x509-ca-cert" {
		t.Errorf("CertificateExportAs = %q, %v", mime, err)
	}

	replacement := newTestCA(t)
	svc.SetCA(replacement)
	if block, _ := pem.Decode(svc.CertificatePEM()); block == nil || !bytes.Equal(block.Bytes, replacement.GetCA().Raw) {
		t.Error("SetCA 后导出应换成新证书")
	}
}
