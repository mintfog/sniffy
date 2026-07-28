// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build desktop

package desktop

import "testing"

func TestBodyFilename(t *testing.T) {
	cases := []struct {
		name   string
		rawURL string
		mime   string
		want   string
	}{
		{"URL 自带扩展名", "https://a.com/img/logo.png", "image/png", "logo.png"},
		{"无扩展名按 MIME 补全", "https://a.com/avatar?size=64", "image/jpeg", "avatar.jpg"},
		{"根路径回退默认名", "https://a.com/", "image/png", "body.png"},
		{"空 URL", "", "image/webp", "body.webp"},
		{"清洗非法字符", "https://a.com/fi%3Ale%2A.png", "image/png", "fi_le_.png"},
		{"svg+xml 映射", "https://a.com/icon", "image/svg+xml", "icon.svg"},
		{"未知 MIME 兜底 bin", "https://a.com/blob", "application/x-unknown-foo", "blob.bin"},
		{"百分号编码斜杠只取末段", "https://a.com/a%2Fb.png", "image/png", "b.png"},
		{"路径穿越末段回退默认名", "https://a.com/%2e%2e", "image/png", "body.png"},
		{"点点末段回退默认名", "https://a.com/foo/..", "image/jpeg", "body.jpg"},
		{"非图片 MIME 固定映射", "https://a.com/stream", "video/mp4", "stream.mp4"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := bodyFilename(c.rawURL, c.mime); got != c.want {
				t.Errorf("bodyFilename(%q, %q) = %q, want %q", c.rawURL, c.mime, got, c.want)
			}
		})
	}
}

func TestCAExportDialogHints(t *testing.T) {
	tests := []struct {
		format    string
		wantName  string
		wantLabel string
		wantExt   string
	}{
		{"der", "sniffy-ca.der", "DER 证书", ".der"},
		{"p12", "sniffy-ca.p12", "PKCS#12 (.p12)", ".p12"},
		{"PFX", "sniffy-ca.p12", "PKCS#12 (.p12)", ".p12"},
		{"bundle", "sniffy-ca-bundle.pem", "PEM Bundle", ".pem"},
		{"pem", "sniffy-ca.pem", "PEM 证书", ".pem"},
		{"crt", "sniffy-ca.crt", "CRT 证书", ".crt"},
		{"unknown", "sniffy-ca.crt", "CRT 证书", ".crt"},
	}

	for _, tt := range tests {
		t.Run(tt.format, func(t *testing.T) {
			name, label, ext := caExportDialogHints(tt.format)
			if name != tt.wantName || label != tt.wantLabel || ext != tt.wantExt {
				t.Fatalf("caExportDialogHints(%q) = (%q, %q, %q)", tt.format, name, label, ext)
			}
		})
	}
}

func TestWindowActionsWithoutWailsApplication(t *testing.T) {
	b := newTestBridge()

	b.OpenWindow("unknown", "")
	b.OpenWindow("settings", "tool=base64")
	if b.SaveTextFile("test.txt", "content") {
		t.Fatal("Wails 应用未启动时不应保存文件")
	}
	if saved, err := b.SaveSessionBody("missing", "response"); saved || err != nil {
		t.Fatalf("SaveSessionBody() = (%v, %v)，期望 (false, nil)", saved, err)
	}
	b.FocusMain()
	if saved, err := b.ExportCACertAs("pem", ""); saved || err != nil {
		t.Fatalf("ExportCACertAs() = (%v, %v)，期望 (false, nil)", saved, err)
	}
	if got := b.PickImportCAFile(); got != "" {
		t.Fatalf("PickImportCAFile() = %q，期望空串", got)
	}
}
