// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package ca

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/stretchr/testify/require"
	pkcs12 "software.sslmate.com/src/go-pkcs12"
)

var errNoRandom = errors.New("random source unavailable")

// serialReadSize 是 crypto/rand.Int 求 128 位序列号时一次性读取的字节数,
// ECDSA/RSA 密钥生成的读取长度都与之不同(P256 一次读 40 字节,RSA 按素数长度读)。
const serialReadSize = 16

type failingRandReader struct{}

func (failingRandReader) Read([]byte) (int, error) { return 0, errNoRandom }

// sizedFailRandReader 只让指定长度的读取失败,其余读取转发给真实随机源,
// 以此把失败点精确地钉在序列号生成而不是前面的密钥生成上。
type sizedFailRandReader struct {
	src  io.Reader
	size int
}

func (r sizedFailRandReader) Read(b []byte) (int, error) {
	if len(b) == r.size {
		return 0, errNoRandom
	}
	return r.src.Read(b)
}

// armedFailRandReader 如实转发随机字节,直到出现一次序列号长度的读取(即 crypto/rand.Int
// 取序列号)后开始一律失败,以此把失败点钉在紧随其后的证书签名步骤上。
type armedFailRandReader struct {
	src   io.Reader
	armed bool
}

func (r *armedFailRandReader) Read(b []byte) (int, error) {
	if r.armed {
		return 0, errNoRandom
	}
	if len(b) == serialReadSize {
		r.armed = true
	}
	return r.src.Read(b)
}

// swapRandReader 临时替换 crypto/rand.Reader,用例结束后还原。密钥生成与证书签名默认改用
// 系统随机源,只有打开 cryptocustomrand 才会读这里换进去的 reader(crypto/rand.Int 不受此开关影响)。
func swapRandReader(t *testing.T, r io.Reader) {
	t.Helper()
	t.Setenv("GODEBUG", "cryptocustomrand=1")
	orig := rand.Reader
	rand.Reader = r
	t.Cleanup(func() { rand.Reader = orig })
}

// TestNewCA_RandomSourceFailure 覆盖 newCA 中依赖随机源的三条错误分支:
// 生成 P256 私钥、生成证书序列号、自签名。三者失败时都必须原样返回错误而不是半成品 CA。
func TestNewCA_RandomSourceFailure(t *testing.T) {
	t.Run("serial number", func(t *testing.T) {
		swapRandReader(t, sizedFailRandReader{src: rand.Reader, size: serialReadSize})
		c, err := newCA()
		require.Nil(t, c)
		require.ErrorIs(t, err, errNoRandom)
	})

	t.Run("key generation", func(t *testing.T) {
		swapRandReader(t, failingRandReader{})
		c, err := newCA()
		require.Nil(t, c)
		require.ErrorIs(t, err, errNoRandom)
	})

	t.Run("self signing", func(t *testing.T) {
		swapRandReader(t, &armedFailRandReader{src: rand.Reader})
		c, err := newCA()
		require.Nil(t, c)
		require.ErrorIs(t, err, errNoRandom)
	})
}

// TestNewAndSaveCA_GenerateError 覆盖 newAndSaveCA 中 newCA 失败的分支,
// 并确认生成阶段失败时不会在目录里留下半截的 cert/key。
func TestNewAndSaveCA_GenerateError(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "sniffy-ca.crt")
	keyPath := filepath.Join(dir, "sniffy-ca.key")

	swapRandReader(t, failingRandReader{})
	c, err := newAndSaveCA(certPath, keyPath)
	require.Nil(t, c)
	require.ErrorIs(t, err, errNoRandom)

	entries, readErr := os.ReadDir(dir)
	require.NoError(t, readErr)
	require.Empty(t, entries, "生成失败时不应写出任何文件")
}

// TestSelfSignedCA_Issue_RandomSourceFailure 覆盖 issue 中随机源不可用的两条错误分支
// (叶子 RSA 私钥与序列号),并确认签发失败的域名不会被写进证书缓存。
func TestSelfSignedCA_Issue_RandomSourceFailure(t *testing.T) {
	// 根 CA 必须在破坏随机源之前建好,否则失败点会落在根的生成而非叶子签发。
	newIssuer := func(t *testing.T) *SelfSignedCA {
		t.Helper()
		c, err := NewInMemorySelfSignedCA()
		require.NoError(t, err)
		return c.(*SelfSignedCA)
	}
	assertIssueFails := func(t *testing.T, s *SelfSignedCA, domain string) {
		t.Helper()
		cert, err := s.IssueCert(domain)
		require.Nil(t, cert)
		require.ErrorIs(t, err, errNoRandom)
		_, cached := s.certCache.Get(domain)
		require.False(t, cached, "签发失败的域名不应进入缓存")
	}

	t.Run("serial number", func(t *testing.T) {
		s := newIssuer(t)
		swapRandReader(t, sizedFailRandReader{src: rand.Reader, size: serialReadSize})
		assertIssueFails(t, s, "serial.example.com")
	})

	t.Run("leaf key generation", func(t *testing.T) {
		s := newIssuer(t)
		t.Setenv("GODEBUG", "cryptocustomrand=1")
		swapRandReader(t, failingRandReader{})
		assertIssueFails(t, s, "leafkey.example.com")
	})
}

// TestSelfSignedCA_Issue_ParentKeyMismatch 覆盖 issue 中 x509.CreateCertificate 失败的分支:
// 根证书与根私钥来自两个不同的 CA,签名时 x509 会拒绝。
func TestSelfSignedCA_Issue_ParentKeyMismatch(t *testing.T) {
	first, err := NewInMemorySelfSignedCA()
	require.NoError(t, err)
	second, err := NewInMemorySelfSignedCA()
	require.NoError(t, err)
	cache, err := lru.New[string, *tls.Certificate](8)
	require.NoError(t, err)

	broken := &SelfSignedCA{caCert: first.GetCA(), caKey: second.GetCAKey(), certCache: cache}
	cert, err := broken.issue("mismatch.example.com")
	require.Nil(t, cert)
	require.ErrorContains(t, err, "doesn't match parent's PublicKey")
}

// TestImportCA_KeyEncodingFailure 覆盖 ImportCA 中 encodePrivateKeyPEM 失败的分支:
// 私钥与证书公钥同源(能过配对校验),但曲线不是标准命名曲线,无法编码成 PEM。
func TestImportCA_KeyEncodingFailure(t *testing.T) {
	base, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	key := *base
	key.Curve = wrappedCurve{Curve: base.Curve}
	cert := &x509.Certificate{IsCA: true, PublicKey: &key.PublicKey}

	dir := t.TempDir()
	c, err := ImportCA(cert, &key, dir)
	require.Nil(t, c)
	require.ErrorContains(t, err, "unknown elliptic curve")

	entries, readErr := os.ReadDir(dir)
	require.NoError(t, readErr)
	require.Empty(t, entries, "私钥编码失败应发生在落盘之前")
}

// TestGetStorePath_GetwdError 覆盖 getStorePath 中 os.Getwd 失败的分支:
// 进程的工作目录被删除后,解析相对路径所需的 Getwd 会失败。
func TestGetStorePath_GetwdError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 不允许删除进程当前工作目录")
	}

	gone := filepath.Join(t.TempDir(), "gone")
	require.NoError(t, os.Mkdir(gone, 0o700))
	t.Chdir(gone)
	require.NoError(t, os.Remove(gone))

	path, err := getStorePath("relative-store")
	require.Empty(t, path)
	require.Error(t, err)
	require.True(t, os.IsNotExist(err), "工作目录已消失,Getwd 应报路径不存在")
}

// TestImportFromPKCS12_KeyCertMismatch 覆盖 ImportFromPKCS12 中配对校验失败的分支。
// go-pkcs12 的 Encode 不校验私钥与证书是否配对,因此可以造出这种文件。
func TestImportFromPKCS12_KeyCertMismatch(t *testing.T) {
	certOwner, err := NewInMemorySelfSignedCA()
	require.NoError(t, err)
	keyOwner, err := NewInMemorySelfSignedCA()
	require.NoError(t, err)

	data, err := pkcs12.Modern2023.Encode(keyOwner.GetCAKey(), certOwner.GetCA(), nil, "secret")
	require.NoError(t, err)

	cert, key, err := ImportFromPKCS12(data, "secret")
	require.Nil(t, cert)
	require.Nil(t, key)
	require.ErrorContains(t, err, "证书公钥与私钥不匹配")
}
