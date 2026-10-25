// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package truststore

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
	"testing"
	"time"
)

func TestVerifyTrustMatchesCurrentCertificate(t *testing.T) {
	t.Parallel()
	current := parseCertPEM(t, testCertPEM(t))
	previous := parseCertPEM(t, testCertPEM(t))
	for _, tc := range []struct {
		name    string
		root    *x509.Certificate
		trusted bool
	}{
		{name: "未安装"},
		{name: "已安装当前证书", root: current, trusted: true},
		{name: "仅安装同名旧证书", root: previous},
	} {
		t.Run(tc.name, func(t *testing.T) {
			roots := x509.NewCertPool()
			if tc.root != nil {
				roots.AddCert(tc.root)
			}
			got, err := verifyTrust(current, x509.VerifyOptions{Roots: roots})
			if err != nil || got != tc.trusted {
				t.Fatalf("信任状态 = %v, %v，期望 %v", got, err, tc.trusted)
			}
		})
	}
}

func TestVerifyTrustPreservesValidationError(t *testing.T) {
	t.Parallel()
	cert := parseCertPEM(t, testCertPEM(t))
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	trusted, err := verifyTrust(cert, x509.VerifyOptions{
		Roots:       roots,
		CurrentTime: cert.NotAfter.Add(time.Hour),
	})
	var invalid x509.CertificateInvalidError
	if trusted || !errors.As(err, &invalid) || invalid.Reason != x509.Expired {
		t.Fatalf("过期证书检测 = %v, %v，期望保留过期错误", trusted, err)
	}
}

func TestIsTrustedRejectsInvalidCertificate(t *testing.T) {
	t.Parallel()
	for _, certPEM := range [][]byte{
		nil,
		[]byte("invalid"),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("invalid")}),
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("invalid")}),
	} {
		if trusted, err := IsTrusted(certPEM); trusted || err == nil {
			t.Fatalf("无效证书检测 = %v, %v，期望错误", trusted, err)
		}
	}
}
