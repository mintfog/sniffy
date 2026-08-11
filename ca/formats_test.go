// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package ca

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	pkcs12 "software.sslmate.com/src/go-pkcs12"
)

type exportTestCA struct {
	cert *x509.Certificate
	key  any
}

// wrappedCurve delegates all curve operations but is deliberately not one of
// the named curve values recognized by crypto/x509.
type wrappedCurve struct {
	elliptic.Curve
}

func (c *exportTestCA) GetCA() *x509.Certificate { return c.cert }
func (c *exportTestCA) GetCAKey() any            { return c.key }
func (c *exportTestCA) IssueCert(string) (*tls.Certificate, error) {
	return nil, nil
}

func newRSATestCA(t *testing.T) (*x509.Certificate, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "RSA Test Root"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return cert, key
}

func pemBlock(typ string, der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der})
}

func TestExportRootCertificateAndKey(t *testing.T) {
	c, err := NewInMemorySelfSignedCA()
	require.NoError(t, err)

	certPEM := ExportRootCertPEM(c)
	block, rest := pem.Decode(certPEM)
	require.NotNil(t, block)
	require.Empty(t, rest)
	require.Equal(t, "CERTIFICATE", block.Type)
	require.Equal(t, c.GetCA().Raw, block.Bytes)
	require.Equal(t, c.GetCA().Raw, ExportRootCertDER(c))

	keyPEM, err := ExportRootKeyPEM(c)
	require.NoError(t, err)
	keyBlock, rest := pem.Decode(keyPEM)
	require.NotNil(t, keyBlock)
	require.Empty(t, rest)
	require.Equal(t, "EC PRIVATE KEY", keyBlock.Type)
	key, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	require.NoError(t, err)
	require.True(t, key.PublicKey.Equal(c.GetCA().PublicKey))

	bundle, err := ExportRootBundlePEM(c)
	require.NoError(t, err)
	require.Equal(t, append(certPEM, keyPEM...), bundle)
	cert, importedKey, err := ImportFromPEMBundle(bundle)
	require.NoError(t, err)
	require.Equal(t, c.GetCA().Raw, cert.Raw)
	require.NoError(t, ensureSignerMatchesCert(importedKey, cert))
}

func TestExportRootNilValues(t *testing.T) {
	require.Nil(t, ExportRootCertPEM(nil))
	require.Nil(t, ExportRootCertPEM(&exportTestCA{}))
	require.Nil(t, ExportRootCertDER(nil))
	require.Nil(t, ExportRootCertDER(&exportTestCA{}))

	_, err := ExportRootKeyPEM(nil)
	require.ErrorContains(t, err, "private key unavailable")
	_, err = ExportRootKeyPEM(&exportTestCA{})
	require.ErrorContains(t, err, "private key unavailable")

	_, err = ExportRootBundlePEM(nil)
	require.ErrorContains(t, err, "certificate unavailable")
	_, err = ExportRootBundlePEM(&exportTestCA{cert: &x509.Certificate{}})
	require.ErrorContains(t, err, "private key unavailable")

	_, err = ExportRootPKCS12(nil, "secret")
	require.ErrorContains(t, err, "certificate unavailable")
	_, err = ExportRootPKCS12(&exportTestCA{cert: &x509.Certificate{}}, "secret")
	require.ErrorContains(t, err, "private key unavailable")
}

func TestPKCS12RoundTrip(t *testing.T) {
	c, err := NewInMemorySelfSignedCA()
	require.NoError(t, err)

	data, err := ExportRootPKCS12(c, "correct horse battery staple")
	require.NoError(t, err)
	require.NotEmpty(t, data)

	cert, key, err := ImportFromPKCS12(data, "correct horse battery staple")
	require.NoError(t, err)
	require.Equal(t, c.GetCA().Raw, cert.Raw)
	require.NoError(t, ensureSignerMatchesCert(key, cert))

	_, _, err = ImportFromPKCS12(data, "wrong password")
	require.ErrorContains(t, err, "解析 PKCS12 失败")
}

func TestImportFromPKCS12Errors(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		_, _, err := ImportFromPKCS12(nil, "")
		require.ErrorContains(t, err, "数据为空")
	})

	t.Run("invalid data", func(t *testing.T) {
		_, _, err := ImportFromPKCS12([]byte("not a p12"), "")
		require.ErrorContains(t, err, "解析 PKCS12 失败")
	})

	t.Run("leaf certificate is rejected", func(t *testing.T) {
		root, err := NewInMemorySelfSignedCA()
		require.NoError(t, err)
		issued, err := root.IssueCert("leaf.example")
		require.NoError(t, err)
		leaf, err := x509.ParseCertificate(issued.Certificate[0])
		require.NoError(t, err)
		data, err := pkcs12.Modern2023.Encode(issued.PrivateKey, leaf, nil, "secret")
		require.NoError(t, err)

		_, _, err = ImportFromPKCS12(data, "secret")
		require.ErrorContains(t, err, "不是 CA")
	})
}

func TestImportFromPEMBundle(t *testing.T) {
	rsaCert, rsaKey := newRSATestCA(t)
	rsaDER := x509.MarshalPKCS1PrivateKey(rsaKey)
	unknown := pemBlock("COMMENT", []byte("ignored"))
	bundle := append(unknown, pemBlock("RSA PRIVATE KEY", rsaDER)...)
	bundle = append(bundle, pemBlock("CERTIFICATE", rsaCert.Raw)...)
	// A second key must be ignored after the first usable key.
	bundle = append(bundle, pemBlock("PRIVATE KEY", []byte("invalid but ignored"))...)

	cert, key, err := ImportFromPEMBundle(bundle)
	require.NoError(t, err)
	require.Equal(t, rsaCert.Raw, cert.Raw)
	require.IsType(t, &rsa.PrivateKey{}, key)
	require.NoError(t, ensureSignerMatchesCert(key, cert))
}

func TestImportFromPEMBundleErrors(t *testing.T) {
	ecCA, err := NewInMemorySelfSignedCA()
	require.NoError(t, err)
	ecCert := ecCA.GetCA()
	ecKey := ecCA.GetCAKey().(*ecdsa.PrivateKey)
	ecDER, err := x509.MarshalECPrivateKey(ecKey)
	require.NoError(t, err)

	root, err := NewInMemorySelfSignedCA()
	require.NoError(t, err)
	issued, err := root.IssueCert("not-a-ca.example")
	require.NoError(t, err)
	leaf, err := x509.ParseCertificate(issued.Certificate[0])
	require.NoError(t, err)

	tests := []struct {
		name    string
		data    []byte
		wantErr string
	}{
		{"empty", nil, "PEM 数据为空"},
		{"not PEM", []byte("plain text"), "未识别到任何 PEM 块"},
		{"invalid certificate", pemBlock("CERTIFICATE", []byte("bad cert")), "解析证书 PEM 失败"},
		{"encrypted type", pemBlock("ENCRYPTED PRIVATE KEY", ecDER), "私钥被口令加密"},
		{"legacy encrypted header", pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Headers: map[string]string{"DEK-Info": "AES-256-CBC,00"}, Bytes: ecDER}), "私钥被口令加密"},
		{"invalid private key", pemBlock("PRIVATE KEY", []byte("bad key")), "解析私钥 PEM 失败"},
		{"no CA certificate", append(pemBlock("CERTIFICATE", leaf.Raw), pemBlock("EC PRIVATE KEY", ecDER)...), "未找到 CA 证书"},
		{"no private key", pemBlock("CERTIFICATE", ecCert.Raw), "未找到匹配的私钥"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := ImportFromPEMBundle(tt.data)
			require.ErrorContains(t, err, tt.wantErr)
		})
	}

	t.Run("mismatched private key", func(t *testing.T) {
		other, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		require.NoError(t, err)
		otherDER, err := x509.MarshalECPrivateKey(other)
		require.NoError(t, err)
		bundle := append(pemBlock("CERTIFICATE", ecCert.Raw), pemBlock("EC PRIVATE KEY", otherDER)...)
		_, _, err = ImportFromPEMBundle(bundle)
		require.ErrorContains(t, err, "不匹配")
	})
}

func TestPrivateKeyEncodingAndParsing(t *testing.T) {
	rsaCert, rsaKey := newRSATestCA(t)
	rsaPEM, err := encodePrivateKeyPEM(rsaKey)
	require.NoError(t, err)
	block, rest := pem.Decode(rsaPEM)
	require.NotNil(t, block)
	require.Empty(t, rest)
	require.Equal(t, "PRIVATE KEY", block.Type)
	parsed, err := parsePrivateKeyDER(block.Type, block.Bytes)
	require.NoError(t, err)
	require.NoError(t, ensureSignerMatchesCert(parsed, rsaCert))

	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	ecDER, err := x509.MarshalECPrivateKey(ecKey)
	require.NoError(t, err)
	parsed, err = parsePrivateKeyDER("PRIVATE KEY", ecDER)
	require.NoError(t, err)
	require.IsType(t, &ecdsa.PrivateKey{}, parsed)

	rsaDER := x509.MarshalPKCS1PrivateKey(rsaKey)
	parsed, err = parsePrivateKeyDER("PRIVATE KEY", rsaDER)
	require.NoError(t, err)
	require.IsType(t, &rsa.PrivateKey{}, parsed)

	_, err = parsePrivateKeyDER("PRIVATE KEY", []byte("invalid"))
	require.Error(t, err)
	_, err = encodePrivateKeyPEM(struct{}{})
	require.ErrorContains(t, err, "不支持的私钥类型")

	invalidEC := *ecKey
	invalidEC.Curve = wrappedCurve{Curve: ecKey.Curve}
	_, err = encodePrivateKeyPEM(&invalidEC)
	require.ErrorContains(t, err, "unknown elliptic curve")
}

func TestEnsureSignerMatchesCert(t *testing.T) {
	ecCA, err := NewInMemorySelfSignedCA()
	require.NoError(t, err)
	ecCert := ecCA.GetCA()
	ecKey := ecCA.GetCAKey()
	require.NoError(t, ensureSignerMatchesCert(ecKey, ecCert))

	rsaCert, rsaKey := newRSATestCA(t)
	require.NoError(t, ensureSignerMatchesCert(rsaKey, rsaCert))

	otherEC, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	otherRSA, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	require.ErrorContains(t, ensureSignerMatchesCert(rsaKey, ecCert), "私钥类型不匹配")
	require.ErrorContains(t, ensureSignerMatchesCert(otherEC, ecCert), "公钥与私钥不匹配")
	require.ErrorContains(t, ensureSignerMatchesCert(ecKey, rsaCert), "私钥类型不匹配")
	require.ErrorContains(t, ensureSignerMatchesCert(otherRSA, rsaCert), "公钥与私钥不匹配")

	public, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	require.ErrorContains(t, ensureSignerMatchesCert(ecKey, &x509.Certificate{PublicKey: public}), "暂不支持")
}

func TestImportCA(t *testing.T) {
	t.Run("ECDSA round trip", func(t *testing.T) {
		source, err := NewInMemorySelfSignedCA()
		require.NoError(t, err)
		dir := t.TempDir()

		imported, err := ImportCA(source.GetCA(), source.GetCAKey(), dir)
		require.NoError(t, err)
		require.Equal(t, source.GetCA().Raw, imported.GetCA().Raw)
		require.Equal(t, source.GetCAKey(), imported.GetCAKey())

		loaded, err := NewSelfSignedCA(dir)
		require.NoError(t, err)
		require.Equal(t, source.GetCA().Raw, loaded.GetCA().Raw)
		require.NoError(t, ensureSignerMatchesCert(loaded.GetCAKey(), loaded.GetCA()))
	})

	t.Run("RSA overwrites existing CA", func(t *testing.T) {
		dir := t.TempDir()
		old, err := NewSelfSignedCA(dir)
		require.NoError(t, err)
		rsaCert, rsaKey := newRSATestCA(t)

		imported, err := ImportCA(rsaCert, rsaKey, dir)
		require.NoError(t, err)
		require.NotEqual(t, old.GetCA().Raw, imported.GetCA().Raw)
		loaded, err := NewSelfSignedCA(dir)
		require.NoError(t, err)
		require.Equal(t, rsaCert.Raw, loaded.GetCA().Raw)
		require.IsType(t, &rsa.PrivateKey{}, loaded.GetCAKey())
	})
}

func TestImportCAValidation(t *testing.T) {
	c, err := NewInMemorySelfSignedCA()
	require.NoError(t, err)

	_, err = ImportCA(nil, c.GetCAKey(), t.TempDir())
	require.ErrorContains(t, err, "证书为空")
	_, err = ImportCA(&x509.Certificate{}, c.GetCAKey(), t.TempDir())
	require.ErrorContains(t, err, "不是 CA 证书")
	_, err = ImportCA(c.GetCA(), nil, t.TempDir())
	require.ErrorContains(t, err, "私钥为空")
	_, err = ImportCA(c.GetCA(), c.GetCAKey(), filepath.Join(t.TempDir(), "missing", "nested"))
	require.NoError(t, err)

	pathFile := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(pathFile, []byte("x"), 0o600))
	_, err = ImportCA(c.GetCA(), c.GetCAKey(), pathFile)
	require.Error(t, err)

	_, err = ImportCA(c.GetCA(), struct{}{}, t.TempDir())
	require.ErrorContains(t, err, "私钥类型不匹配")

	t.Run("mismatched key is rejected before writing", func(t *testing.T) {
		dir := t.TempDir()
		other, err := NewInMemorySelfSignedCA()
		require.NoError(t, err)

		_, err = ImportCA(c.GetCA(), other.GetCAKey(), dir)
		require.ErrorContains(t, err, "证书公钥与私钥不匹配")
		_, certErr := os.Stat(filepath.Join(dir, "sniffy-ca.crt"))
		require.True(t, os.IsNotExist(certErr))
		_, keyErr := os.Stat(filepath.Join(dir, "sniffy-ca.key"))
		require.True(t, os.IsNotExist(keyErr))
	})
}

func TestImportCAWriteFailures(t *testing.T) {
	c, err := NewInMemorySelfSignedCA()
	require.NoError(t, err)

	t.Run("key write failure", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.Mkdir(filepath.Join(dir, "sniffy-ca.key.tmp"), 0o700))
		_, err := ImportCA(c.GetCA(), c.GetCAKey(), dir)
		require.Error(t, err)
	})

	t.Run("cert write failure removes new key", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.Mkdir(filepath.Join(dir, "sniffy-ca.crt.tmp"), 0o700))
		_, err := ImportCA(c.GetCA(), c.GetCAKey(), dir)
		require.Error(t, err)
		_, statErr := os.Stat(filepath.Join(dir, "sniffy-ca.key"))
		require.True(t, os.IsNotExist(statErr))
	})

	t.Run("cert write failure restores previous key", func(t *testing.T) {
		dir := t.TempDir()
		keyPath := filepath.Join(dir, "sniffy-ca.key")
		oldKey := []byte("previous key contents")
		require.NoError(t, os.WriteFile(keyPath, oldKey, 0o600))
		require.NoError(t, os.Mkdir(filepath.Join(dir, "sniffy-ca.crt.tmp"), 0o700))

		_, err := ImportCA(c.GetCA(), c.GetCAKey(), dir)
		require.Error(t, err)
		got, readErr := os.ReadFile(keyPath)
		require.NoError(t, readErr)
		require.True(t, bytes.Equal(oldKey, got))
	})
}

func TestReadFileIfExists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "value")
	data, ok := readFileIfExists(path)
	require.False(t, ok)
	require.Nil(t, data)

	require.NoError(t, os.WriteFile(path, []byte("stored"), 0o600))
	data, ok = readFileIfExists(path)
	require.True(t, ok)
	require.Equal(t, []byte("stored"), data)
}
