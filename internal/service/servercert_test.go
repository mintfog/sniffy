// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package service

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// buildCert 生成一对自签名证书 + EC 私钥的 PEM,SAN 由 dnsNames/ips 指定(可空以测试 CN 回退)。
func buildCert(t *testing.T, cn string, dnsNames []string, ips []net.IP) (certPEM, keyPEM string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("生成私钥失败: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     dnsNames,
		IPAddresses:  ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("签发证书失败: %v", err)
	}
	certPEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("编码私钥失败: %v", err)
	}
	keyPEM = string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
	return certPEM, keyPEM
}

// genCert 是 buildCert 的便捷包装:只带 DNS SAN(可空)。
func genCert(t *testing.T, cn string, dnsNames ...string) (certPEM, keyPEM string) {
	return buildCert(t, cn, dnsNames, nil)
}

func TestServerCertStoreImportExtractsHosts(t *testing.T) {
	path := filepath.Join(t.TempDir(), serverCertFileName)
	cs := newServerCertStore(path)

	// SAN 有多个域名(含通配符):导入后应全部成为匹配域名,无需手动指定。
	certPEM, keyPEM := genCert(t, "api.example.com", "api.example.com", "*.example.com")
	dto, err := cs.importCert(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("导入有效证书应成功: %v", err)
	}
	if dto.ID == "" {
		t.Fatal("摘要应带证书指纹作为 ID")
	}
	if got := dto.Hosts; len(got) != 2 || got[0] != "api.example.com" || got[1] != "*.example.com" {
		t.Fatalf("Hosts 应从 SAN 提取,得到 %v", got)
	}
	if dto.Subject != "api.example.com" {
		t.Fatalf("摘要 Subject 应为 CN,得到 %q", dto.Subject)
	}

	list := cs.certList()
	if len(list) != 1 {
		t.Fatalf("certList 应含 1 张证书,得到 %d", len(list))
	}
	if list[0].Leaf == nil || list[0].Leaf.VerifyHostname("api.example.com") != nil {
		t.Fatal("下发证书的 Leaf 应已解析且按 SAN 覆盖 api.example.com")
	}

	// 落盘文件权限应收紧到 0600(含私钥)。
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("落盘文件应存在: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("证书文件权限应为 0600,得到 %o", perm)
	}
}

func TestServerCertStoreFallsBackToCN(t *testing.T) {
	cs := newServerCertStore(filepath.Join(t.TempDir(), serverCertFileName))
	// 无 SAN 时回退 CN。
	certPEM, keyPEM := genCert(t, "legacy.example.com")
	dto, err := cs.importCert(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("无 SAN 证书应回退 CN 导入成功: %v", err)
	}
	if len(dto.Hosts) != 1 || dto.Hosts[0] != "legacy.example.com" {
		t.Fatalf("无 SAN 时应回退 CN,得到 %v", dto.Hosts)
	}
}

func TestServerCertStoreRejectsInvalid(t *testing.T) {
	cs := newServerCertStore(filepath.Join(t.TempDir(), serverCertFileName))

	certPEM, _ := genCert(t, "a.com", "a.com")
	_, otherKey := genCert(t, "b.com", "b.com") // 另一把不匹配的私钥
	if _, err := cs.importCert(certPEM, otherKey); err == nil {
		t.Fatal("证书与私钥不匹配时应返回错误")
	}

	// 既无 SAN 又无 CN:提取不到任何可匹配域名,应拒绝。
	noHostCert, noHostKey := genCert(t, "")
	if _, err := cs.importCert(noHostCert, noHostKey); err == nil {
		t.Fatal("证书不含可用域名时应返回错误")
	}
}

func TestServerCertStoreUpsertDeletePersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), serverCertFileName)
	cs := newServerCertStore(path)

	c1, k1 := genCert(t, "h1.com", "h1.com")
	c2, k2 := genCert(t, "h1.com", "h1.com") // 同一组域名再次导入 = 替换(续期语义)
	c3, k3 := genCert(t, "h2.com", "h2.com")
	_, _ = cs.importCert(c1, k1)
	_, _ = cs.importCert(c2, k2)
	dto3, _ := cs.importCert(c3, k3)

	if got := len(cs.dtos()); got != 2 {
		t.Fatalf("同域名集合 upsert 后应有 2 条,得到 %d", got)
	}

	cs.delete(dto3.ID)
	dtos := cs.dtos()
	if len(dtos) != 1 || dtos[0].Hosts[0] != "h1.com" {
		t.Fatalf("按指纹删除后应剩 h1.com,得到 %+v", dtos)
	}

	// 从同一路径重建应加载持久化内容。
	reloaded := newServerCertStore(path)
	rd := reloaded.dtos()
	if len(rd) != 1 || rd[0].Hosts[0] != "h1.com" {
		t.Fatalf("持久化重载应剩 h1.com,得到 %+v", rd)
	}
}

func TestServerCertStoreImportsIPSAN(t *testing.T) {
	cs := newServerCertStore(filepath.Join(t.TempDir(), serverCertFileName))
	// 仅含 IP SAN、无 DNS、空 CN 的证书应能导入,并把 IP 作为覆盖主机。
	certPEM, keyPEM := buildCert(t, "", nil, []net.IP{net.ParseIP("10.0.0.5")})
	dto, err := cs.importCert(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("IP-SAN 证书应可导入: %v", err)
	}
	if len(dto.Hosts) != 1 || dto.Hosts[0] != "10.0.0.5" {
		t.Fatalf("Hosts 应含 IP SAN,得到 %v", dto.Hosts)
	}
	if list := cs.certList(); len(list) != 1 || list[0].Leaf.VerifyHostname("10.0.0.5") != nil {
		t.Fatalf("下发证书应覆盖 10.0.0.5,得到 %+v", list)
	}
}

// TestServerCertStoreTightensExistingPerms:即使目标文件已存在且权限宽松,导入(原子 rename)也应
// 把最终文件权限固定为 0600——os.WriteFile 不会 chmod 既有文件,这里靠 temp+rename 覆盖来保证。
func TestServerCertStoreTightensExistingPerms(t *testing.T) {
	path := filepath.Join(t.TempDir(), serverCertFileName)
	if err := os.WriteFile(path, []byte("[]"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	cs := newServerCertStore(path)
	certPEM, keyPEM := genCert(t, "api.example.com", "api.example.com")
	if _, err := cs.importCert(certPEM, keyPEM); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("导入后应把文件权限收紧到 0600,得到 %o", perm)
	}
}

// TestServerCertStoreImportSaveFailureRollsBack:落盘失败时导入应报错并回滚内存,不留下"当次生效、
// 重启即失"的假成功状态。
func TestServerCertStoreImportSaveFailureRollsBack(t *testing.T) {
	badPath := filepath.Join(t.TempDir(), "no-such-dir", serverCertFileName)
	cs := newServerCertStore(badPath)
	certPEM, keyPEM := genCert(t, "api.example.com", "api.example.com")
	if _, err := cs.importCert(certPEM, keyPEM); err == nil {
		t.Fatal("落盘失败时导入应返回错误")
	}
	if got := len(cs.dtos()); got != 0 {
		t.Fatalf("落盘失败应回滚内存,dtos 应为空,得到 %d", got)
	}
}

// TestServiceServerCertApplyChain 守住 ImportServerCert→applyServerCerts→引擎 的下发链:
// 若装配层漏接或 apply 被摘掉,导入的证书永不到达握手,此测试应失败。
func TestServiceServerCertApplyChain(t *testing.T) {
	svc := newTestService(t)
	var got []*tls.Certificate
	svc.SetServerCertsApplier(func(list []*tls.Certificate) error {
		got = list
		return nil
	})
	if len(got) != 0 {
		t.Fatalf("注入即应用一次,初始应为空,得到 %d", len(got))
	}

	certPEM, keyPEM := genCert(t, "api.example.com", "api.example.com")
	dto, err := svc.ImportServerCert(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("导入失败: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("导入后应下发 1 张证书,得到 %d", len(got))
	}
	if got[0].Leaf == nil || got[0].Leaf.VerifyHostname("api.example.com") != nil {
		t.Fatal("下发的证书应覆盖 api.example.com")
	}

	svc.DeleteServerCert(dto.ID)
	if len(got) != 0 {
		t.Fatalf("删除后应再次下发空列表,得到 %d", len(got))
	}
}
