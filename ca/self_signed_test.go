// Copyright 2025 The f-dong Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package ca

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"
	"testing"

	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/stretchr/testify/require"
)

// --- 辅助函数 ---
func createTempDir(t *testing.T, prefix string) string {
	dir, err := os.MkdirTemp("", prefix)
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func createTempFile(t *testing.T, prefix string) string {
	f, err := os.CreateTemp("", prefix)
	require.NoError(t, err)
	name := f.Name()
	require.NoError(t, f.Close())
	t.Cleanup(func() { _ = os.Remove(name) })
	return name
}

func parseLeafCert(t *testing.T, cert *tls.Certificate) *x509.Certificate {
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	require.NoError(t, err)
	return leaf
}

// --- 测试代码 ---
func Test_getStorePath(t *testing.T) {
	t.Run("default path", func(t *testing.T) {
		tmpHome := createTempDir(t, "fake-home")
		t.Setenv("HOME", tmpHome)
		t.Setenv("USERPROFILE", tmpHome)
		path, err := getStorePath("")
		require.NoError(t, err)
		require.Equal(t, filepath.Join(tmpHome, ".sniffy"), path)
		_, err = os.Stat(path)
		require.NoError(t, err)
	})

	t.Run("relative path", func(t *testing.T) {
		wd, err := os.Getwd()
		require.NoError(t, err)
		relativePath := "test-dir"
		path, err := getStorePath(relativePath)
		require.NoError(t, err)
		require.Equal(t, filepath.Join(wd, relativePath), path)
		_, err = os.Stat(path)
		require.NoError(t, err)
		_ = os.RemoveAll(filepath.Join(wd, relativePath))
	})

	t.Run("absolute path", func(t *testing.T) {
		tmpDir := createTempDir(t, "abs-path-test")
		path, err := getStorePath(tmpDir)
		require.NoError(t, err)
		require.Equal(t, tmpDir, path)
	})

	t.Run("path is file", func(t *testing.T) {
		tmpFile := createTempFile(t, "ca-path-is-file")
		_, err := getStorePath(tmpFile)
		require.Error(t, err)
	})
}

func TestNewSelfSignedCA_Persistence(t *testing.T) {
	dir := createTempDir(t, "test-ca")
	ca, err := NewSelfSignedCA(dir)
	require.NoError(t, err)
	require.NotNil(t, ca)
	certPath := filepath.Join(dir, "sniffy-ca.crt")
	keyPath := filepath.Join(dir, "sniffy-ca.key")
	_, err = os.Stat(certPath)
	require.NoError(t, err)
	_, err = os.Stat(keyPath)
	require.NoError(t, err)
	loadedCA, err := NewSelfSignedCA(dir)
	require.NoError(t, err)
	require.NotNil(t, loadedCA)
	require.True(t, reflect.DeepEqual(ca.GetCA().Raw, loadedCA.GetCA().Raw))
}

func TestNewInMemorySelfSignedCA(t *testing.T) {
	ca, err := NewInMemorySelfSignedCA()
	require.NoError(t, err)
	require.NotNil(t, ca)
	rootCert := ca.GetCA()
	require.NotNil(t, rootCert)
	require.True(t, rootCert.IsCA)
	require.Equal(t, []string{"Sniffy Self-Signed CA"}, rootCert.Subject.Organization)
}

func TestSelfSignedCA_IssueCert(t *testing.T) {
	ca, err := NewInMemorySelfSignedCA()
	require.NoError(t, err)
	testCases := []struct {
		name       string
		domain     string
		expectIsIP bool
	}{
		{"domain", "example.com", false},
		{"ip address", "127.0.0.1", true},
		{"localhost", "localhost", false},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cert, err := ca.IssueCert(tc.domain)
			require.NoError(t, err)
			require.NotNil(t, cert.PrivateKey)
			require.GreaterOrEqual(t, len(cert.Certificate), 2)
			leafCert := parseLeafCert(t, cert)
			if tc.expectIsIP {
				ip := net.ParseIP(tc.domain)
				require.Len(t, leafCert.IPAddresses, 1)
				require.True(t, leafCert.IPAddresses[0].Equal(ip))
			} else {
				require.Len(t, leafCert.DNSNames, 1)
				require.Equal(t, tc.domain, leafCert.DNSNames[0])
			}
			rootPool := x509.NewCertPool()
			rootPool.AddCert(ca.GetCA())
			opts := x509.VerifyOptions{Roots: rootPool, DNSName: tc.domain}
			_, err = leafCert.Verify(opts)
			require.NoError(t, err)
		})
	}
	t.Run("issue cached cert", func(t *testing.T) {
		domain := "cached.example.com"
		cert1, err := ca.IssueCert(domain)
		require.NoError(t, err)
		cert2, err := ca.IssueCert(domain)
		require.NoError(t, err)
		require.Equal(t, cert1, cert2)
	})
}

func TestSelfSignedCA_IssueCert_WithPort(t *testing.T) {
	ca, err := NewInMemorySelfSignedCA()
	require.NoError(t, err)

	testCases := []struct {
		name         string
		input        string
		expectedHost string
		expectIsIP   bool
	}{
		{"domain with standard HTTPS port", "www.baidu.com:443", "www.baidu.com", false},
		{"domain with custom port", "example.com:8080", "example.com", false},
		{"IP with port", "192.168.1.1:443", "192.168.1.1", true},
		{"IPv6 with port", "[::1]:8080", "::1", true},
		{"localhost with port", "localhost:3000", "localhost", false},
		{"domain without port", "www.google.com", "www.google.com", false},
		{"IP without port", "127.0.0.1", "127.0.0.1", true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cert, err := ca.IssueCert(tc.input)
			require.NoError(t, err)
			require.NotNil(t, cert.PrivateKey)
			require.GreaterOrEqual(t, len(cert.Certificate), 2)

			leafCert := parseLeafCert(t, cert)

			// 验证证书中使用的是预期的主机名（不包含端口）
			if tc.expectIsIP {
				ip := net.ParseIP(tc.expectedHost)
				require.Len(t, leafCert.IPAddresses, 1, "应该有一个IP地址")
				require.True(t, leafCert.IPAddresses[0].Equal(ip), "IP地址应该匹配")
			} else {
				require.Len(t, leafCert.DNSNames, 1, "应该有一个DNS名称")
				require.Equal(t, tc.expectedHost, leafCert.DNSNames[0], "DNS名称应该匹配")
			}

			// 验证CommonName也是正确的主机名（不包含端口）
			require.Equal(t, tc.expectedHost, leafCert.Subject.CommonName, "CommonName应该是主机名，不包含端口")
		})
	}

	// 测试缓存行为：同一主机名的不同端口应该返回相同的证书
	t.Run("cache behavior with different ports", func(t *testing.T) {
		cert1, err := ca.IssueCert("example.org:443")
		require.NoError(t, err)

		cert2, err := ca.IssueCert("example.org:8080")
		require.NoError(t, err)

		cert3, err := ca.IssueCert("example.org")
		require.NoError(t, err)

		// 所有证书应该是相同的，因为它们的主机名相同
		require.Equal(t, cert1, cert2, "不同端口的相同主机名应该返回相同的证书")
		require.Equal(t, cert1, cert3, "有端口和无端口的相同主机名应该返回相同的证书")
	})
}

func TestParseHostname(t *testing.T) {
	testCases := []struct {
		name        string
		input       string
		expected    string
		expectError bool
	}{
		{"domain with port", "www.baidu.com:443", "www.baidu.com", false},
		{"domain without port", "www.google.com", "www.google.com", false},
		{"IP with port", "192.168.1.1:8080", "192.168.1.1", false},
		{"IP without port", "127.0.0.1", "127.0.0.1", false},
		{"IPv6 with port", "[::1]:8080", "::1", false},
		{"IPv6 without port", "::1", "::1", false},
		{"localhost with port", "localhost:3000", "localhost", false},
		{"localhost without port", "localhost", "localhost", false},
		{"empty string", "", "", false},
		{"port only", ":8080", "", true},
		{"invalid format", "invalid:]8080", "", true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := parseHostname(tc.input)
			if tc.expectError {
				require.Error(t, err, "应该返回错误")
			} else {
				require.NoError(t, err, "不应该返回错误")
				require.Equal(t, tc.expected, result, "返回的主机名应该匹配")
			}
		})
	}
}

func TestSelfSignedCA_IssueCert_Concurrency(t *testing.T) {
	ca, err := NewInMemorySelfSignedCA()
	require.NoError(t, err)
	var wg sync.WaitGroup
	numGoroutines := 50
	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				_, err := ca.IssueCert("concurrent.example.com")
				if err != nil {
					t.Errorf("IssueCert failed for concurrent.example.com: %v", err)
				}
			}
		}()
	}
	numDifferentDomains := 50
	wg.Add(numDifferentDomains)
	for i := 0; i < numDifferentDomains; i++ {
		go func(i int) {
			defer wg.Done()
			domain := fmt.Sprintf("test-%d.example.com", i)
			_, err := ca.IssueCert(domain)
			if err != nil {
				t.Errorf("IssueCert failed for %s: %v", domain, err)
			}
		}(i)
	}
	wg.Wait()
}

func TestNewSelfSignedCA_ErrorPaths(t *testing.T) {
	t.Run("corrupted cert file", func(t *testing.T) {
		dir := createTempDir(t, "test-ca-corrupt-cert")
		certPath := filepath.Join(dir, "sniffy-ca.crt")
		_, err := NewSelfSignedCA(dir)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(certPath, []byte("this is not a valid cert"), 0644))
		_, err = NewSelfSignedCA(dir)
		require.Error(t, err)
	})
	t.Run("corrupted key file", func(t *testing.T) {
		dir := createTempDir(t, "test-ca-corrupt-key")
		keyPath := filepath.Join(dir, "sniffy-ca.key")
		_, err := NewSelfSignedCA(dir)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(keyPath, []byte("this is not a valid key"), 0600))
		_, err = NewSelfSignedCA(dir)
		require.Error(t, err)
	})
	t.Run("unreadable cert file", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("skipping file permission test on windows")
		}
		dir := createTempDir(t, "test-ca-unreadable-cert")
		_, err := NewSelfSignedCA(dir)
		require.NoError(t, err)
		certPath := filepath.Join(dir, "sniffy-ca.crt")
		require.NoError(t, os.Chmod(certPath, 0000))
		t.Cleanup(func() { _ = os.Chmod(certPath, 0644) })
		_, err = NewSelfSignedCA(dir)
		require.Error(t, err)
		require.True(t, os.IsPermission(err))
	})
	t.Run("cannot create directory", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("skipping file permission test on windows")
		}
		readOnlyDir := createTempDir(t, "readonly")
		require.NoError(t, os.Chmod(readOnlyDir, 0555))
		storePath := filepath.Join(readOnlyDir, "test-ca")
		_, err := NewSelfSignedCA(storePath)
		require.Error(t, err)
		require.True(t, os.IsPermission(err))
	})
}

func TestSelfSignedCA_BoundaryValues(t *testing.T) {
	ca, err := NewInMemorySelfSignedCA()
	require.NoError(t, err)
	testCases := []struct {
		name    string
		domain  string
		wantDNS string
	}{
		{"empty domain", "", ""},
		{"non-ascii domain", "蔡徐坤.com", "xn--tfsz3qky6a.com"},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cert, err := ca.IssueCert(tc.domain)
			require.NoError(t, err)
			leafCert := parseLeafCert(t, cert)
			found := false
			for _, dnsName := range leafCert.DNSNames {
				if dnsName == tc.wantDNS || dnsName == tc.domain {
					found = true
					break
				}
			}
			if !found && leafCert.Subject.CommonName == tc.domain {
				found = true
			}
			require.True(t, found, "expected domain in DNSNames or CommonName")
		})
	}
}

func TestSelfSignedCA_CacheEviction(t *testing.T) {
	caInterface, err := NewInMemorySelfSignedCA()
	require.NoError(t, err)
	ca := caInterface.(*SelfSignedCA)
	cache, err := lru.New[string, *tls.Certificate](2)
	require.NoError(t, err)
	ca.certCache = cache
	domain1 := "a.example.com"
	cert1, err := ca.IssueCert(domain1)
	require.NoError(t, err)
	domain2 := "b.example.com"
	_, err = ca.IssueCert(domain2)
	require.NoError(t, err)
	domain3 := "c.example.com"
	_, err = ca.IssueCert(domain3)
	require.NoError(t, err)
	_, ok := ca.certCache.Get(domain1)
	require.False(t, ok)
	_, ok = ca.certCache.Get(domain2)
	require.True(t, ok)
	_, ok = ca.certCache.Get(domain3)
	require.True(t, ok)
	newCert1, err := ca.IssueCert(domain1)
	require.NoError(t, err)
	require.NotEqual(t, cert1, newCert1)
}
