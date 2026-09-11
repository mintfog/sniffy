// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package app

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mintfog/sniffy/ca"
	"github.com/mintfog/sniffy/internal/core"
	"github.com/mintfog/sniffy/internal/service"
)

func assertAppCA(t *testing.T, a *App, want *x509.Certificate, returnedPEM string) {
	t.Helper()
	if !bytes.Equal(a.Engine.CA().GetCA().Raw, want.Raw) {
		t.Fatal("引擎根证书与预期不一致")
	}
	for name, data := range map[string][]byte{"返回值": []byte(returnedPEM), "服务层": a.Service.CertificatePEM()} {
		block, _ := pem.Decode(data)
		if block == nil || !bytes.Equal(block.Bytes, want.Raw) {
			t.Fatalf("%s 根证书与预期不一致", name)
		}
	}
	reloaded, err := ca.NewSelfSignedCA(a.CertDir)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(reloaded.GetCA().Raw, want.Raw) {
		t.Fatal("重启后根证书与预期不一致")
	}
	leaf, err := a.Engine.CA().IssueCert("example.test")
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(leaf.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := cert.CheckSignatureFrom(want); err != nil {
		t.Fatalf("引擎未使用当前根证书签发: %v", err)
	}
}

func TestRegenerateCAUpdatesAllConsumers(t *testing.T) {
	a := newComposeApp(t)
	original := a.Engine.CA().GetCA()
	got, err := a.RegenerateCA()
	if err != nil {
		t.Fatal(err)
	}
	replacement := a.Engine.CA().GetCA()
	if bytes.Equal(original.Raw, replacement.Raw) {
		t.Fatal("重新生成后根证书未更新")
	}
	assertAppCA(t, a, replacement, got)
}

func TestExportCAFormats(t *testing.T) {
	a := newComposeApp(t)
	for _, tt := range []struct{ format, mime string }{
		{"pem", "application/x-pem-file"},
		{"der", "application/x-x509-ca-cert"},
		{"bundle", "application/x-pem-file"},
		{"p12", "application/x-pkcs12"},
	} {
		t.Run(tt.format, func(t *testing.T) {
			data, mime, err := a.ExportCAAs(tt.format, "test-password")
			if err != nil {
				t.Fatal(err)
			}
			if mime != tt.mime {
				t.Fatalf("导出媒体类型 = %q，期望 %q", mime, tt.mime)
			}
			var cert *x509.Certificate
			switch tt.format {
			case "pem":
				block, rest := pem.Decode(data)
				if block == nil || len(rest) != 0 {
					t.Fatal("证书导出应只包含一个 PEM 块")
				}
				cert, err = x509.ParseCertificate(block.Bytes)
			case "der":
				cert, err = x509.ParseCertificate(data)
			case "bundle":
				cert, _, err = ca.ImportFromPEMBundle(data)
			case "p12":
				cert, _, err = ca.ImportFromPKCS12(data, "test-password")
			}
			if err != nil {
				t.Fatalf("解析导出证书: %v", err)
			}
			if cert == nil || !bytes.Equal(cert.Raw, a.Engine.CA().GetCA().Raw) {
				t.Fatal("导出内容与当前根证书不一致")
			}
		})
	}
	if data, mime, err := a.ExportCAAs("unknown", ""); err == nil || len(data) != 0 || mime != "" {
		t.Fatalf("未知格式未返回失败: mime=%q, err=%v", mime, err)
	}
}

func TestImportCAFromFileFormats(t *testing.T) {
	source, err := ca.NewInMemorySelfSignedCA()
	if err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"bundle", "p12"} {
		t.Run(format, func(t *testing.T) {
			var data []byte
			var err error
			switch format {
			case "bundle":
				data, err = ca.ExportRootBundlePEM(source)
			case "p12":
				data, err = ca.ExportRootPKCS12(source, "test-password")
			}
			if err != nil {
				t.Fatal(err)
			}
			a := newComposeApp(t)
			path := filepath.Join(t.TempDir(), "导入证书."+format)
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := a.ImportCAFromFile(path, "test-password")
			if err != nil {
				t.Fatal(err)
			}
			assertAppCA(t, a, source.GetCA(), got)
		})
	}
}

func TestCAFailuresPreserveActiveCertificate(t *testing.T) {
	replacement, err := ca.NewInMemorySelfSignedCA()
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := ca.ExportRootBundlePEM(replacement)
	if err != nil {
		t.Fatal(err)
	}
	pkcs12, err := ca.ExportRootPKCS12(replacement, "correct-password")
	if err != nil {
		t.Fatal(err)
	}
	blockedDir := filepath.Join(t.TempDir(), "占用路径")
	writeAppFixture(t, blockedDir, "占用证书目录")

	for _, tt := range []struct {
		name, path, password, certDir string
		data                          []byte
		regenerate, invalidInput      bool
		wantErr                       error
	}{
		{name: "未选择文件"},
		{name: "文件缺失", path: filepath.Join(t.TempDir(), "缺失.p12"), wantErr: os.ErrNotExist},
		{name: "PKCS12密码错误", data: pkcs12, password: "wrong-password", invalidInput: true},
		{name: "导入目录不可用", data: bundle, certDir: blockedDir},
		{name: "重建目录不可用", regenerate: true, certDir: blockedDir},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a := newComposeApp(t)
			original := a.Engine.CA().GetCA()
			originalPEM := a.Service.CertificatePEM()
			if tt.certDir != "" {
				a.CertDir = tt.certDir
			}

			var result string
			var err error
			switch {
			case tt.regenerate:
				result, err = a.RegenerateCA()
			case tt.data != nil:
				result, err = a.ImportCA(tt.data, tt.password)
			default:
				result, err = a.ImportCAFromFile(tt.path, tt.password)
			}
			if err == nil || result != "" {
				t.Fatalf("失败操作应返回空证书和错误，得到 %q, %v", result, err)
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Fatalf("文件错误未保留: %v", err)
			}
			if tt.invalidInput {
				var invalid interface{ InvalidInput() bool }
				if !errors.As(err, &invalid) || !invalid.InvalidInput() || errors.Unwrap(err) == nil {
					t.Fatalf("密码错误未分类为输入错误: %v", err)
				}
			}
			if tt.certDir != "" && !strings.Contains(err.Error(), tt.certDir) {
				t.Fatalf("错误未指出不可用的证书路径: %v", err)
			}
			if !bytes.Equal(a.Engine.CA().GetCA().Raw, original.Raw) {
				t.Fatal("失败后引擎根证书发生变化")
			}
			if !bytes.Equal(a.Service.CertificatePEM(), originalPEM) {
				t.Fatal("失败后服务层根证书发生变化")
			}
		})
	}
}

func TestCertificateOperationsRequireCA(t *testing.T) {
	a := &App{Service: service.New(nil, core.NewEventBus(), "", "")}
	if err := a.InstallCAToSystem(); err == nil {
		t.Fatal("根证书未就绪时应拒绝安装")
	}
	if data, _, err := a.ExportCAAs("pem", ""); err == nil || len(data) != 0 {
		t.Fatal("根证书未就绪时应拒绝导出")
	}
}
