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
)

func Test_getStorePath(t *testing.T) {
	t.Run("default path", func(t *testing.T) {
		// Setup a temporary home directory to avoid cluttering the real one.
		tmpHome, err := os.MkdirTemp("", "fake-home")
		if err != nil {
			t.Fatalf("Failed to create temp home dir: %v", err)
		}
		defer os.RemoveAll(tmpHome)

		// Temporarily set the user's home directory to our temp directory.
		t.Setenv("HOME", tmpHome)
		t.Setenv("USERPROFILE", tmpHome) // for Windows

		path, err := getStorePath("")
		if err != nil {
			t.Fatalf("getStorePath() with default path error = %v", err)
		}

		expectedPath := filepath.Join(tmpHome, ".sniffy")
		if path != expectedPath {
			t.Errorf("getStorePath() returned %q, want %q", path, expectedPath)
		}

		// Check that the directory was created
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("getStorePath() did not create directory at %s", path)
		}
	})

	t.Run("relative path", func(t *testing.T) {
		wd, err := os.Getwd()
		if err != nil {
			t.Fatalf("os.Getwd() failed: %v", err)
		}

		relativePath := "test-dir"
		defer os.RemoveAll(filepath.Join(wd, relativePath))

		path, err := getStorePath(relativePath)
		if err != nil {
			t.Fatalf("getStorePath() with relative path error = %v", err)
		}

		expectedPath := filepath.Join(wd, relativePath)
		if path != expectedPath {
			t.Errorf("getStorePath() returned %q, want %q", path, expectedPath)
		}

		// Check that the directory was created
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("getStorePath() did not create directory at %s", path)
		}
	})

	t.Run("absolute path", func(t *testing.T) {
		tmpDir, err := os.MkdirTemp("", "abs-path-test")
		if err != nil {
			t.Fatalf("Failed to create temp dir: %v", err)
		}
		defer os.RemoveAll(tmpDir)

		path, err := getStorePath(tmpDir)
		if err != nil {
			t.Fatalf("getStorePath() with absolute path error = %v", err)
		}

		if path != tmpDir {
			t.Errorf("getStorePath() returned %q, want %q", path, tmpDir)
		}
	})

	t.Run("path is file", func(t *testing.T) {
		// Create a temporary file to act as an invalid path.
		tmpFile, err := os.CreateTemp("", "ca-path-is-file")
		if err != nil {
			t.Fatalf("Failed to create temp file: %v", err)
		}
		defer os.Remove(tmpFile.Name())
		tmpFile.Close()

		// Attempt to create a CA where the path is a file, which should fail.
		_, err = getStorePath(tmpFile.Name())
		if err == nil {
			t.Fatalf("Expected an error when path is a file, but got nil")
		}
	})
}

func TestNewSelfSignedCA_Persistence(t *testing.T) {
	// Test persisted CA with a specific path
	dir, err := os.MkdirTemp("", "test-ca")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	// Create a new CA, which should be saved to the directory.
	ca, err := NewSelfSignedCA(dir)
	if err != nil {
		t.Fatalf("NewSelfSignedCA(%q) error = %v", dir, err)
	}
	if ca == nil {
		t.Fatal("NewSelfSignedCA() ca is nil")
	}

	// Check if the files were created.
	certPath := filepath.Join(dir, "sniffy-ca.crt")
	keyPath := filepath.Join(dir, "sniffy-ca.key")
	if _, err := os.Stat(certPath); os.IsNotExist(err) {
		t.Errorf("CA certificate file was not created at %s", certPath)
	}
	if _, err := os.Stat(keyPath); os.IsNotExist(err) {
		t.Errorf("CA key file was not created at %s", keyPath)
	}

	// Now, load the CA from the same directory.
	loadedCA, err := NewSelfSignedCA(dir)
	if err != nil {
		t.Fatalf("NewSelfSignedCA(%q) on existing ca error = %v", dir, err)
	}
	if loadedCA == nil {
		t.Fatal("loaded CA is nil")
	}

	if !reflect.DeepEqual(ca.GetCA().Raw, loadedCA.GetCA().Raw) {
		t.Error("loaded CA certificate does not match saved CA certificate")
	}
}

func TestNewInMemorySelfSignedCA(t *testing.T) {
	// Test in-memory CA
	ca, err := NewInMemorySelfSignedCA()
	if err != nil {
		t.Fatalf("NewInMemorySelfSignedCA() error = %v, wantErr nil", err)
	}
	if ca == nil {
		t.Fatal("NewInMemorySelfSignedCA() ca is nil")
	}

	rootCert := ca.GetCA()
	if rootCert == nil {
		t.Fatal("ca.GetCA() is nil")
	}

	if !rootCert.IsCA {
		t.Error("root certificate is not a CA")
	}

	expectedOrg := []string{"Sniffy Self-Signed CA"}
	if !reflect.DeepEqual(rootCert.Subject.Organization, expectedOrg) {
		t.Errorf("root certificate organization is %v, want %v", rootCert.Subject.Organization, expectedOrg)
	}
}

func TestSelfSignedCA_IssueCert(t *testing.T) {
	ca, err := NewInMemorySelfSignedCA()
	if err != nil {
		t.Fatalf("NewInMemorySelfSignedCA() error = %v", err)
	}

	testCases := []struct {
		name       string
		domain     string
		expectIsIP bool
	}{
		{
			name:       "domain",
			domain:     "example.com",
			expectIsIP: false,
		},
		{
			name:       "ip address",
			domain:     "127.0.0.1",
			expectIsIP: true,
		},
		{
			name:       "localhost",
			domain:     "localhost",
			expectIsIP: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cert, err := ca.IssueCert(tc.domain)
			if err != nil {
				t.Fatalf("IssueCert(%q) returned error: %v", tc.domain, err)
			}
			if cert.PrivateKey == nil {
				t.Fatal("IssueCert() returned certificate with nil PrivateKey")
			}
			if len(cert.Certificate) < 2 {
				t.Fatalf("IssueCert() returned cert chain with %d certs, want at least 2", len(cert.Certificate))
			}

			leafCert, err := x509.ParseCertificate(cert.Certificate[0])
			if err != nil {
				t.Fatalf("failed to parse leaf certificate: %v", err)
			}

			if tc.expectIsIP {
				ip := net.ParseIP(tc.domain)
				if len(leafCert.IPAddresses) != 1 || !leafCert.IPAddresses[0].Equal(ip) {
					t.Errorf("expected IP address %q, got %v", tc.domain, leafCert.IPAddresses)
				}
			} else {
				if len(leafCert.DNSNames) != 1 || leafCert.DNSNames[0] != tc.domain {
					t.Errorf("expected DNS name %q, got %v", tc.domain, leafCert.DNSNames)
				}
			}

			rootPool := x509.NewCertPool()
			rootPool.AddCert(ca.GetCA())

			opts := x509.VerifyOptions{
				Roots:   rootPool,
				DNSName: tc.domain,
			}

			if _, err := leafCert.Verify(opts); err != nil {
				t.Errorf("failed to verify certificate for %q: %v", tc.domain, err)
			}
		})
	}

	t.Run("issue cached cert", func(t *testing.T) {
		domain := "cached.example.com"

		// Issue the certificate for the first time.
		cert1, err := ca.IssueCert(domain)
		if err != nil {
			t.Fatalf("IssueCert(%q) returned error on first call: %v", domain, err)
		}

		// Issue the same certificate again.
		cert2, err := ca.IssueCert(domain)
		if err != nil {
			t.Fatalf("IssueCert(%q) returned error on second call: %v", domain, err)
		}

		// The two certificates should be the same object from the cache.
		if cert1 != cert2 {
			t.Error("expected the same certificate object from cache, but got different objects")
		}
	})
}

func TestSelfSignedCA_IssueCert_Concurrency(t *testing.T) {
	ca, err := NewInMemorySelfSignedCA()
	if err != nil {
		t.Fatalf("NewInMemorySelfSignedCA() error = %v", err)
	}

	var wg sync.WaitGroup
	numGoroutines := 50

	// Test issuing the same certificate concurrently
	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				_, err := ca.IssueCert("concurrent.example.com")
				if err != nil {
					// Use t.Errorf for concurrent tests to avoid halting execution
					t.Errorf("IssueCert failed for concurrent.example.com: %v", err)
				}
			}
		}()
	}

	// Test issuing different certificates concurrently
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
		dir, err := os.MkdirTemp("", "test-ca-corrupt-cert")
		if err != nil {
			t.Fatalf("failed to create temp dir: %v", err)
		}
		defer os.RemoveAll(dir)

		certPath := filepath.Join(dir, "sniffy-ca.crt")

		// Create a valid CA to get a key file
		_, err = NewSelfSignedCA(dir)
		if err != nil {
			t.Fatalf("failed to create initial CA: %v", err)
		}

		// Write a garbage certificate file
		if err := os.WriteFile(certPath, []byte("this is not a valid cert"), 0644); err != nil {
			t.Fatalf("failed to write corrupted cert file: %v", err)
		}

		_, err = NewSelfSignedCA(dir)
		if err == nil {
			t.Error("expected an error for corrupted certificate file, but got nil")
		}
	})

	t.Run("corrupted key file", func(t *testing.T) {
		dir, err := os.MkdirTemp("", "test-ca-corrupt-key")
		if err != nil {
			t.Fatalf("failed to create temp dir: %v", err)
		}
		defer os.RemoveAll(dir)

		keyPath := filepath.Join(dir, "sniffy-ca.key")

		// Create a valid CA to get a cert file
		_, err = NewSelfSignedCA(dir)
		if err != nil {
			t.Fatalf("failed to create initial CA: %v", err)
		}

		// Write a garbage key file
		if err := os.WriteFile(keyPath, []byte("this is not a valid key"), 0600); err != nil {
			t.Fatalf("failed to write corrupted key file: %v", err)
		}

		_, err = NewSelfSignedCA(dir)
		if err == nil {
			t.Error("expected an error for corrupted key file, but got nil")
		}
	})

	t.Run("unreadable cert file", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("skipping file permission test on windows")
		}
		dir, err := os.MkdirTemp("", "test-ca-unreadable-cert")
		if err != nil {
			t.Fatalf("failed to create temp dir: %v", err)
		}
		defer os.RemoveAll(dir)

		_, err = NewSelfSignedCA(dir)
		if err != nil {
			t.Fatalf("failed to create initial CA: %v", err)
		}

		certPath := filepath.Join(dir, "sniffy-ca.crt")
		if err := os.Chmod(certPath, 0000); err != nil {
			t.Fatalf("failed to change cert file permissions: %v", err)
		}
		defer func() { _ = os.Chmod(certPath, 0644) }()

		_, err = NewSelfSignedCA(dir)
		if !os.IsPermission(err) {
			t.Errorf("expected permission error, got: %v", err)
		}
	})

	t.Run("cannot create directory", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("skipping file permission test on windows")
		}

		readOnlyDir, err := os.MkdirTemp("", "readonly")
		if err != nil {
			t.Fatalf("failed to create temp dir: %v", err)
		}
		defer os.RemoveAll(readOnlyDir)

		if err := os.Chmod(readOnlyDir, 0555); err != nil {
			t.Fatalf("failed to chmod: %v", err)
		}

		storePath := filepath.Join(readOnlyDir, "test-ca")
		_, err = NewSelfSignedCA(storePath)
		if !os.IsPermission(err) {
			t.Errorf("expected permission error for creating dir, got: %v", err)
		}
	})
}

func TestSelfSignedCA_BoundaryValues(t *testing.T) {
	ca, err := NewInMemorySelfSignedCA()
	if err != nil {
		t.Fatalf("NewInMemorySelfSignedCA() error = %v", err)
	}

	testCases := []struct {
		name        string
		domain      string
		expectError bool
	}{
		{
			name:   "empty domain",
			domain: "",
		},
		{
			name:   "non-ascii domain",
			domain: "你好世界.com",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cert, err := ca.IssueCert(tc.domain)
			if err != nil {
				t.Fatalf("IssueCert(%q) returned error: %v", tc.domain, err)
			}

			leafCert, err := x509.ParseCertificate(cert.Certificate[0])
			if err != nil {
				t.Fatalf("failed to parse leaf certificate: %v", err)
			}

			var found bool
			if len(leafCert.DNSNames) > 0 {
				for _, dnsName := range leafCert.DNSNames {
					if dnsName == tc.domain {
						found = true
						break
					}
				}
			} else if leafCert.Subject.CommonName == tc.domain {
				found = true
			}

			if !found {
				t.Errorf("expected domain %q in DNSNames or CommonName, got DNSNames: %v, CommonName: %q", tc.domain, leafCert.DNSNames, leafCert.Subject.CommonName)
			}
		})
	}
}

func TestSelfSignedCA_CacheEviction(t *testing.T) {
	caInterface, err := NewInMemorySelfSignedCA()
	if err != nil {
		t.Fatalf("newCA() failed: %v", err)
	}
	ca := caInterface.(*SelfSignedCA)

	cache, err := lru.New[string, *tls.Certificate](2)
	if err != nil {
		t.Fatalf("lru.New() failed: %v", err)
	}
	ca.certCache = cache

	domain1 := "a.example.com"
	cert1, err := ca.IssueCert(domain1)
	if err != nil {
		t.Fatalf("IssueCert(%q) failed: %v", domain1, err)
	}

	domain2 := "b.example.com"
	if _, err := ca.IssueCert(domain2); err != nil {
		t.Fatalf("IssueCert(%q) failed: %v", domain2, err)
	}

	domain3 := "c.example.com"
	if _, err := ca.IssueCert(domain3); err != nil {
		t.Fatalf("IssueCert(%q) failed: %v", domain3, err)
	}

	if _, ok := ca.certCache.Get(domain1); ok {
		t.Errorf("cert for %q was found in cache, but should have been evicted", domain1)
	}
	if _, ok := ca.certCache.Get(domain2); !ok {
		t.Errorf("cert for %q was not in cache", domain2)
	}
	if _, ok := ca.certCache.Get(domain3); !ok {
		t.Errorf("cert for %q was not in cache", domain3)
	}

	newCert1, err := ca.IssueCert(domain1)
	if err != nil {
		t.Fatalf("re-issuing cert for %q failed: %v", domain1, err)
	}
	if newCert1 == cert1 {
		t.Errorf("re-issued cert is the same object as the original, cache eviction failed")
	}
}
