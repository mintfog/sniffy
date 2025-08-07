// Copyright 2025 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package ca

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"
	"golang.org/x/net/idna"
	"golang.org/x/sync/singleflight"
)

const defaultCacheSize = 2048

// SelfSignedCA implements the CA interface with a self-signed root certificate.
type SelfSignedCA struct {
	caCert *x509.Certificate
	caKey  any

	certCache  *lru.Cache[string, *tls.Certificate]
	issueGroup singleflight.Group
}

// NewSelfSignedCA creates a new self-signed CA.
// It will try to load the CA certificate and key from the given path.
// If the files do not exist, it will generate a new CA and save it to the path.
// If no path is provided, it will use ~/.sniffy as the default path.
func NewSelfSignedCA(storePath ...string) (CA, error) {
	var p string
	if len(storePath) > 0 {
		p = storePath[0]
	}

	path, err := getStorePath(p)
	if err != nil {
		return nil, err
	}

	certPath := filepath.Join(path, "sniffy-ca.crt")
	keyPath := filepath.Join(path, "sniffy-ca.key")

	if _, err := os.Stat(certPath); err == nil {
		if _, err := os.Stat(keyPath); err == nil {
			return loadCA(certPath, keyPath)
		}
	}

	return newAndSaveCA(certPath, keyPath)
}

// NewInMemorySelfSignedCA creates a new self-signed CA in memory.
func NewInMemorySelfSignedCA() (CA, error) {
	return newCA()
}

func loadCA(certPath, keyPath string) (CA, error) {
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, err
	}

	certDER, _ := pem.Decode(certPEM)
	if certDER == nil {
		return nil, errors.New("failed to decode certificate PEM")
	}

	caCert, err := x509.ParseCertificate(certDER.Bytes)
	if err != nil {
		return nil, err
	}

	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, err
	}

	keyDER, _ := pem.Decode(keyPEM)
	if keyDER == nil {
		return nil, errors.New("failed to decode private key PEM")
	}

	caKey, err := x509.ParseECPrivateKey(keyDER.Bytes)
	if err != nil {
		return nil, err
	}

	cache, err := lru.New[string, *tls.Certificate](defaultCacheSize)
	if err != nil {
		return nil, err
	}

	return &SelfSignedCA{
		caCert:    caCert,
		caKey:     caKey,
		certCache: cache,
	}, nil
}

func newAndSaveCA(certPath, keyPath string) (CA, error) {
	ca, err := newCA()
	if err != nil {
		return nil, err
	}

	s := ca.(*SelfSignedCA)

	// save cert
	certPEM := &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: s.caCert.Raw,
	}
	certOut, err := os.OpenFile(certPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return nil, err
	}
	defer func(certOut *os.File) {
		_ = certOut.Close()
	}(certOut)
	if err := pem.Encode(certOut, certPEM); err != nil {
		return nil, err
	}

	// save key
	keyBytes, err := x509.MarshalECPrivateKey(s.caKey.(*ecdsa.PrivateKey))
	if err != nil {
		return nil, err
	}
	keyPEM := &pem.Block{
		Type:  "EC PRIVATE KEY",
		Bytes: keyBytes,
	}
	keyOut, err := os.OpenFile(keyPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return nil, err
	}
	defer func(keyOut *os.File) {
		_ = keyOut.Close()
	}(keyOut)
	if err := pem.Encode(keyOut, keyPEM); err != nil {
		return nil, err
	}

	return ca, nil
}

func newCA() (CA, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization:  []string{"Sniffy Self-Signed CA"},
			Country:       []string{"CN"},
			Province:      []string{"Henan"},
			Locality:      []string{"zhengzhou"},
			StreetAddress: []string{"zhengzhou"},
			PostalCode:    []string{"450000"},
			CommonName:    "Sniffy Self-Signed CA",
		},
		NotBefore:             time.Now().Add(-time.Hour * 24),
		NotAfter:              time.Now().AddDate(99, 0, 0), // Valid for 99 years
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            1, // 允许一级子CA
		MaxPathLenZero:        false,
		SignatureAlgorithm:    x509.ECDSAWithSHA256,
		// 添加CA证书的扩展用法，确保兼容性
		ExtKeyUsage: []x509.ExtKeyUsage{
			x509.ExtKeyUsageServerAuth,
			x509.ExtKeyUsageClientAuth,
		},
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return nil, err
	}

	caCert, err := x509.ParseCertificate(derBytes)
	if err != nil {
		return nil, err
	}

	cache, err := lru.New[string, *tls.Certificate](defaultCacheSize)
	if err != nil {
		return nil, err
	}

	return &SelfSignedCA{
		caCert:    caCert,
		caKey:     priv,
		certCache: cache,
	}, nil
}

// GetCA returns the root CA certificate.
func (s *SelfSignedCA) GetCA() *x509.Certificate {
	return s.caCert
}

// IssueCert issues a certificate for the given domain.
// If the domain contains a port (e.g., "www.baidu.com:443"), the port will be stripped
// and only the hostname part will be used for certificate generation.
func (s *SelfSignedCA) IssueCert(domain string) (*tls.Certificate, error) {
	// Parse the domain to extract hostname without port
	hostname, err := parseHostname(domain)
	if err != nil {
		return nil, fmt.Errorf("invalid domain format: %w", err)
	}

	if cert, ok := s.certCache.Get(hostname); ok {
		return cert, nil
	}

	cert, err, _ := s.issueGroup.Do(hostname, func() (any, error) {
		newCert, err := s.issue(hostname)
		if err != nil {
			return nil, err
		}
		s.certCache.Add(hostname, newCert)
		return newCert, nil
	})
	if err != nil {
		return nil, err
	}

	return cert.(*tls.Certificate), nil
}

func (s *SelfSignedCA) issue(domain string) (*tls.Certificate, error) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}

	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName: domain,
		},
		NotBefore:             time.Now().Add(-time.Hour * 24),
		NotAfter:              time.Now().AddDate(10, 0, 0), // Valid for 10 years
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  false, // 明确标记这不是CA证书
	}

	// 确保设置Subject Alternative Name (SAN)扩展，这对macOS信任验证很重要
	if ip := net.ParseIP(domain); ip != nil {
		template.IPAddresses = append(template.IPAddresses, ip)
		// 对于IP地址，也添加到SAN中
		if ip.To4() != nil {
			// IPv4地址，同时添加localhost别名以提高兼容性
			if domain == "127.0.0.1" {
				template.DNSNames = append(template.DNSNames, "localhost")
			}
		}
	} else {
		punycode, err := idna.ToASCII(domain)
		if err != nil {
			return nil, err
		}
		template.DNSNames = append(template.DNSNames, punycode)

		// 如果原始域名不是punycode，也将其添加到DNSNames中
		if punycode != domain {
			template.DNSNames = append(template.DNSNames, domain)
		}

		// 为localhost添加额外的SAN条目以提高兼容性
		if domain == "localhost" {
			template.IPAddresses = append(template.IPAddresses, net.IPv4(127, 0, 0, 1))
			template.IPAddresses = append(template.IPAddresses, net.IPv6loopback)
		}
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, template, s.caCert, &priv.PublicKey, s.caKey)
	if err != nil {
		return nil, err
	}

	return &tls.Certificate{
		Certificate: [][]byte{derBytes, s.caCert.Raw},
		PrivateKey:  priv,
	}, nil
}

func getStorePath(path string) (string, error) {
	if path == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(homeDir, ".sniffy")
	}

	if !filepath.IsAbs(path) {
		dir, err := os.Getwd()
		if err != nil {
			return "", err
		}
		path = filepath.Join(dir, path)
	}

	stat, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			err = os.MkdirAll(path, os.ModePerm)
			if err != nil {
				return "", err
			}
		} else {
			return "", err
		}
	} else {
		if !stat.Mode().IsDir() {
			return "", fmt.Errorf("路径 %v 存在但不是目录（类型：%v），请将其删除后重试", path, stat.Mode().Type())
		}
	}

	return path, nil
}

// parseHostname extracts the hostname from a domain string, removing the port if present.
// It handles various input formats:
//   - "example.com" -> "example.com"
//   - "example.com:443" -> "example.com"
//   - "192.168.1.1:8080" -> "192.168.1.1"
//   - "[::1]:8080" -> "::1"
//   - "::1" -> "::1" (IPv6 without brackets)
func parseHostname(domain string) (string, error) {
	// Allow empty domain to maintain backward compatibility
	if domain == "" {
		return "", nil
	}

	// Special case: check for port-only format like ":8080"
	// But exclude IPv6 addresses like "::1" or "::ffff:192.0.2.1"
	if strings.HasPrefix(domain, ":") && !strings.HasPrefix(domain, "::") {
		return "", errors.New("invalid format: port without host")
	}

	// Try to parse as host:port first
	host, _, err := net.SplitHostPort(domain)
	if err != nil {
		// If SplitHostPort fails, it might be because there's no port
		// Check if it's an invalid format or just a plain hostname/IP
		if strings.Contains(err.Error(), "missing port") ||
			strings.Contains(err.Error(), "too many colons") {
			// It's a plain hostname or IP without port (including bare IPv6), which is valid
			return domain, nil
		}
		// Other parsing errors indicate invalid format
		return "", err
	}

	// Successfully split host:port, return the host part
	return host, nil
}
