// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package app

import (
	"bytes"
	"encoding/pem"
	"errors"
	"strings"
	"testing"

	"github.com/mintfog/sniffy/ca"
	"github.com/mintfog/sniffy/internal/core"
	"github.com/mintfog/sniffy/internal/service"
)

func TestImportCAFromBytesHotSwapsEngineAndService(t *testing.T) {
	original, err := ca.NewInMemorySelfSignedCA()
	if err != nil {
		t.Fatal(err)
	}
	engine, err := core.NewEngine(DefaultConfig(), core.WithCA(original))
	if err != nil {
		t.Fatal(err)
	}
	svc := service.New(original, engine.Bus(), "", "")
	application := &App{Engine: engine, Service: svc, CertDir: t.TempDir()}

	replacement, err := ca.NewInMemorySelfSignedCA()
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := ca.ExportRootBundlePEM(replacement)
	if err != nil {
		t.Fatal(err)
	}
	gotPEM, err := application.ImportCA(bundle, "")
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(engine.CA().GetCA().Raw, replacement.GetCA().Raw) {
		t.Fatal("engine 未热切换为导入的 CA")
	}
	block, _ := pem.Decode([]byte(gotPEM))
	if block == nil || !bytes.Equal(block.Bytes, replacement.GetCA().Raw) {
		t.Fatal("返回的 PEM 不是导入的 CA")
	}
	serviceBlock, _ := pem.Decode(svc.CertificatePEM())
	if serviceBlock == nil || !bytes.Equal(serviceBlock.Bytes, replacement.GetCA().Raw) {
		t.Fatal("service 未切换为导入的 CA")
	}

	reloaded, err := ca.NewSelfSignedCA(application.CertDir)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(reloaded.GetCA().Raw, replacement.GetCA().Raw) {
		t.Fatal("导入的 CA 未正确持久化")
	}
}

func TestImportCAAcceptsPEMWithBOMAndLeadingWhitespace(t *testing.T) {
	original, err := ca.NewInMemorySelfSignedCA()
	if err != nil {
		t.Fatal(err)
	}
	engine, err := core.NewEngine(DefaultConfig(), core.WithCA(original))
	if err != nil {
		t.Fatal(err)
	}
	svc := service.New(original, engine.Bus(), "", "")
	application := &App{Engine: engine, Service: svc, CertDir: t.TempDir()}

	replacement, err := ca.NewInMemorySelfSignedCA()
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := ca.ExportRootBundlePEM(replacement)
	if err != nil {
		t.Fatal(err)
	}
	prefixed := append([]byte{0xEF, 0xBB, 0xBF}, []byte(" \r\n\t")...)
	prefixed = append(prefixed, bundle...)

	if _, err := application.ImportCA(prefixed, ""); err != nil {
		t.Fatalf("带 BOM/前导空白的 PEM 导入失败: %v", err)
	}
	if !bytes.Equal(engine.CA().GetCA().Raw, replacement.GetCA().Raw) {
		t.Fatal("engine 未热切换为带前缀 PEM 中的 CA")
	}
}

func TestImportCAAcceptsOpenSSLPEMBundlePreamble(t *testing.T) {
	original, err := ca.NewInMemorySelfSignedCA()
	if err != nil {
		t.Fatal(err)
	}
	engine, err := core.NewEngine(DefaultConfig(), core.WithCA(original))
	if err != nil {
		t.Fatal(err)
	}
	svc := service.New(original, engine.Bus(), "", "")
	application := &App{Engine: engine, Service: svc, CertDir: t.TempDir()}

	replacement, err := ca.NewInMemorySelfSignedCA()
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := ca.ExportRootBundlePEM(replacement)
	if err != nil {
		t.Fatal(err)
	}
	withPreamble := append([]byte("Bag Attributes\n    friendlyName: sniffy-ca\n"), bundle...)

	if _, err := application.ImportCA(withPreamble, ""); err != nil {
		t.Fatalf("带 OpenSSL 前言的 PEM bundle 导入失败: %v", err)
	}
	if !bytes.Equal(engine.CA().GetCA().Raw, replacement.GetCA().Raw) {
		t.Fatal("engine 未热切换为带 OpenSSL 前言 PEM 中的 CA")
	}
}

func TestImportCAReportsPEMErrorInsteadOfFallingBackToPKCS12(t *testing.T) {
	original, err := ca.NewInMemorySelfSignedCA()
	if err != nil {
		t.Fatal(err)
	}
	engine, err := core.NewEngine(DefaultConfig(), core.WithCA(original))
	if err != nil {
		t.Fatal(err)
	}
	svc := service.New(original, engine.Bus(), "", "")
	application := &App{Engine: engine, Service: svc, CertDir: t.TempDir()}

	certificateOnly := ca.ExportRootCertPEM(original)
	if _, err := application.ImportCA(certificateOnly, ""); err == nil {
		t.Fatal("仅含证书的 PEM 应导入失败")
	} else if !strings.Contains(err.Error(), "未找到匹配的私钥") {
		t.Fatalf("应保留 PEM 缺少私钥错误,实际: %v", err)
	}
}

func TestImportCAClassifiesInvalidInput(t *testing.T) {
	application := &App{}
	_, err := application.ImportCA(nil, "")
	if err == nil {
		t.Fatal("空导入数据应返回错误")
	}
	var invalid interface {
		error
		InvalidInput() bool
	}
	if !errors.As(err, &invalid) || !invalid.InvalidInput() {
		t.Fatalf("空导入数据错误未标记为输入无效: %v", err)
	}
}
