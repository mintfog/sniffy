// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package http

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"testing"
	"time"
)

// makeCertValid 生成一张带指定 SAN 与有效期的自签名证书(Leaf 留空以便走 SetImportedServerCerts
// 的解析路径);匹配只看证书内容,不需要私钥。
func makeCertValid(t *testing.T, cn string, dnsNames []string, ips []net.IP, notBefore, notAfter time.Time) *tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("生成私钥失败: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		DNSNames:     dnsNames,
		IPAddresses:  ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("签发证书失败: %v", err)
	}
	return &tls.Certificate{Certificate: [][]byte{der}}
}

// makeCert 生成一张当前有效(前后各留 1 小时)的证书。
func makeCert(t *testing.T, cn string, dnsNames []string, ips []net.IP) *tls.Certificate {
	now := time.Now()
	return makeCertValid(t, cn, dnsNames, ips, now.Add(-time.Hour), now.Add(time.Hour))
}

// TestImportedServerCertRFCMatching 校验匹配遵循 x509(RFC 6125)语义,而非解密范围的宽松 glob:
// 通配只匹配单层子域(不匹配裸域/多层)、IP SAN 生效、CN-only 可命中、精确优先于通配。
func TestImportedServerCertRFCMatching(t *testing.T) {
	t.Cleanup(func() { SetImportedServerCerts(nil) })

	wild := makeCert(t, "", []string{"*.example.com"}, nil) // 仅通配,不含裸域
	exact := makeCert(t, "", []string{"api.example.com"}, nil)
	ipc := makeCert(t, "", nil, []net.IP{net.ParseIP("10.0.0.5")})
	cnOnly := makeCert(t, "legacy.example.com", nil, nil)

	SetImportedServerCerts([]*tls.Certificate{wild, exact, ipc, cnOnly})

	cases := []struct {
		name string
		host string
		want *tls.Certificate
	}{
		{"通配命中单层子域", "a.example.com:443", wild},
		{"通配不匹配裸域", "example.com", nil},
		{"通配不匹配多层子域", "a.b.example.com", nil},
		{"精确优先于通配", "api.example.com:443", exact},
		{"大小写不敏感", "API.Example.com", exact},
		{"IP SAN 命中", "10.0.0.5:8443", ipc},
		{"CN-only 命中(VerifyHostname 会拒,靠 exact 兜)", "legacy.example.com", cnOnly},
		{"未导入回退 nil", "other.org", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := importedServerCertFor(c.host); got != c.want {
				t.Fatalf("importedServerCertFor(%q) = %p, want %p", c.host, got, c.want)
			}
		})
	}
}

// TestImportedServerCertSkipsInvalid 校验过期/未生效证书不命中(应回退 CA 现签)。
func TestImportedServerCertSkipsInvalid(t *testing.T) {
	t.Cleanup(func() { SetImportedServerCerts(nil) })
	now := time.Now()
	expired := makeCertValid(t, "", []string{"past.example.com"}, nil, now.Add(-2*time.Hour), now.Add(-time.Hour))
	future := makeCertValid(t, "", []string{"future.example.com"}, nil, now.Add(time.Hour), now.Add(2*time.Hour))
	SetImportedServerCerts([]*tls.Certificate{expired, future})

	if got := importedServerCertFor("past.example.com"); got != nil {
		t.Fatalf("过期证书不应命中(应回退 CA),得到 %p", got)
	}
	if got := importedServerCertFor("future.example.com"); got != nil {
		t.Fatalf("未生效证书不应命中,得到 %p", got)
	}
}

// TestImportedServerCertExpiredExactFallsThroughToWildcard 校验:一张过期的精确证书应被跳过,
// 让位给仍有效的通配证书(而非直接回退 CA),即失效证书不阻断后续候选。
func TestImportedServerCertExpiredExactFallsThroughToWildcard(t *testing.T) {
	t.Cleanup(func() { SetImportedServerCerts(nil) })
	now := time.Now()
	expiredExact := makeCertValid(t, "", []string{"api.example.com"}, nil, now.Add(-2*time.Hour), now.Add(-time.Hour))
	validWild := makeCert(t, "", []string{"*.example.com"}, nil)
	SetImportedServerCerts([]*tls.Certificate{expiredExact, validWild})

	if got := importedServerCertFor("api.example.com"); got != validWild {
		t.Fatalf("过期的精确证书应被跳过,命中有效通配证书 %p,得到 %p", validWild, got)
	}
}

func TestImportedServerCertEmptyAndNil(t *testing.T) {
	t.Cleanup(func() { SetImportedServerCerts(nil) })

	SetImportedServerCerts(nil)
	if got := importedServerCertFor("any.host"); got != nil {
		t.Fatalf("空导入应返回 nil,得到 %p", got)
	}

	// nil 指针与无证书字节的条目应被跳过,不 panic。
	SetImportedServerCerts([]*tls.Certificate{nil, {}})
	if got := importedServerCertFor("skip.com"); got != nil {
		t.Fatalf("无效条目应被跳过,得到 %p", got)
	}
}
