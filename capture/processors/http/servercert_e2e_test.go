// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package http

import (
	"crypto/tls"
	"testing"
)

func restoreImportedServerCerts(t *testing.T) {
	prev := importedServerCertsPtr.Load()
	t.Cleanup(func() { importedServerCertsPtr.Store(prev) })
}

// TestImportedServerCertServedInMITM:为主机导入真实服务端证书后,MITM 握手应把这张导入证书
// 呈给客户端(Issuer=导入证书 CN),而非 Sniffy CA 现签的伪造证书,也非源站证书——这正是固定
// 证书客户端得以校验通过的关键。复用 scope_e2e_test.go 的真 socket 代理/源站/握手辅助。
func TestImportedServerCertServedInMITM(t *testing.T) {
	restoreScopeGlobals(t)
	restoreImportedServerCerts(t)
	origin := startOrigin(t)
	defer origin.Close()
	proxy := startProxy(t)
	defer proxy.Close()

	const importedCN = "sniffy-e2e-imported"
	imported := selfSignedCert(t, importedCN) // selfSignedCert 带 127.0.0.1 的 IP SAN,匹配源站

	SetDecryptScope(true, "all", nil, nil)                      // 命中 MITM 路径
	SetImportedServerCerts([]*tls.Certificate{&imported})       // 按证书自身 SAN(127.0.0.1)匹配

	caCN := currentCA().GetCA().Subject.CommonName
	issuer, _ := connectAndHandshake(t, proxy.Addr().String(), origin.Addr().String(), false)

	if issuer == originCN {
		t.Fatal("命中 MITM 时客户端不应看到源站证书")
	}
	if issuer == caCN {
		t.Fatalf("导入证书存在时不应回退到 Sniffy CA 现签证书(Issuer=%q)", caCN)
	}
	if issuer != importedCN {
		t.Fatalf("MITM 应呈给客户端导入证书(Issuer=%q),实际 %q", importedCN, issuer)
	}
}

// TestImportedServerCertMissFallsBackToCA:未为主机导入证书时,MITM 仍回退到 Sniffy CA 现签证书。
func TestImportedServerCertMissFallsBackToCA(t *testing.T) {
	restoreScopeGlobals(t)
	restoreImportedServerCerts(t)
	origin := startOrigin(t)
	defer origin.Close()
	proxy := startProxy(t)
	defer proxy.Close()

	SetDecryptScope(true, "all", nil, nil)
	// 导入证书的 SAN 只覆盖 other.example.com、不含源站 127.0.0.1,应回退 CA。
	other := makeCert(t, "", []string{"other.example.com"}, nil)
	SetImportedServerCerts([]*tls.Certificate{other})

	caCN := currentCA().GetCA().Subject.CommonName
	issuer, _ := connectAndHandshake(t, proxy.Addr().String(), origin.Addr().String(), false)

	if issuer != caCN {
		t.Fatalf("未命中导入证书时应回退 Sniffy CA(Issuer=%q),实际 %q", caCN, issuer)
	}
}
