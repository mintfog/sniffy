// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package outboundtls

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNormalizeHosts(t *testing.T) {
	got, err := NormalizeHosts([]string{"EXAMPLE.com.", "example.com", "localhost", "127.0.0.1", "2001:0db8:0:0::1", "2001:db8::1", "::1"})
	want := []string{"127.0.0.1", "2001:db8::1", "::1", "example.com", "localhost"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("NormalizeHosts = %v, %v; want %v", got, err, want)
	}
	for _, host := range []string{
		"", " ", " example.com", "example.com ", "example.com\n", "https://example.com", "example.com:443",
		"127.0.0.1:443", "[::1]", "[::1]:443", "fe80::1%eth0", "*.example.com", ".example.com", "example..com",
		"example.com..", "bad_name.com", "-bad.com", "bad-.com", "user@example.com", "example.com/path",
		"example.com?x", "example.com#x", "例子.com", "exam\x7fple.com", strings.Repeat("a", 64) + ".com",
		strings.Repeat("a.", 127) + "a", ".",
	} {
		t.Run(host, func(t *testing.T) {
			if _, err := NormalizeHosts([]string{host}); err == nil {
				t.Errorf("accepted invalid host %q", host)
			}
		})
	}
}

func TestPolicyExactHostAndUpdate(t *testing.T) {
	var p Policy
	if p.AllowsInsecure("example.com") || p.ConfigForHost("example.com").InsecureSkipVerify {
		t.Fatal("zero policy permits a TLS exception")
	}
	if changed, err := p.SetInsecureHosts(nil); err != nil || changed {
		t.Fatalf("initial empty update = %v, %v", changed, err)
	}
	changed, err := p.SetInsecureHosts([]string{"EXAMPLE.com.", "::1"})
	if err != nil || !changed {
		t.Fatalf("update = %v, %v", changed, err)
	}
	for _, host := range []string{"example.com", "EXAMPLE.COM", "example.com.", "::1", "0:0:0:0:0:0:0:1"} {
		if !p.AllowsInsecure(host) {
			t.Errorf("missing exception for %q", host)
		}
	}
	for _, host := range []string{"api.example.com", "notexample.com", "example.com.evil", "example.com:443", "[::1]", "example.com..", "example.com ", "EXAMPKE.COM", ""} {
		if p.AllowsInsecure(host) {
			t.Errorf("exception leaked to %q", host)
		}
	}
	if changed, err := p.SetInsecureHosts([]string{"::1", "example.com"}); changed || err != nil {
		t.Fatalf("equal update = %v, %v", changed, err)
	}
	if changed, err := p.SetInsecureHosts([]string{"*.example.com"}); changed || err == nil || !p.AllowsInsecure("example.com") {
		t.Fatalf("invalid update changed policy: %v, %v", changed, err)
	}
	if changed, err := p.SetInsecureHosts(nil); err != nil || !changed || p.AllowsInsecure("example.com") {
		t.Fatalf("clear = %v, %v", changed, err)
	}
	if (*Policy)(nil).AllowsInsecure("example.com") || (*Policy)(nil).ConfigForHost("example.com").InsecureSkipVerify {
		t.Fatal("nil policy permits a TLS exception")
	}
}

func TestPolicyConcurrentUpdates(t *testing.T) {
	var p Policy
	var wg sync.WaitGroup
	for worker := range 8 {
		wg.Go(func() {
			for range 1000 {
				if worker%2 == 0 {
					if _, err := p.SetInsecureHosts([]string{"example.com", "::1"}); err != nil {
						t.Error(err)
					}
				} else {
					if p.AllowsInsecure("not.example.com") {
						t.Error("subdomain matched concurrent snapshot")
					}
					_, _ = p.SetInsecureHosts(nil)
				}
			}
		})
	}
	wg.Wait()
}

func TestPolicyRejectsUnicodeCaseAliases(t *testing.T) {
	var p Policy
	_, _ = p.SetInsecureHosts([]string{"k.example"})
	if p.AllowsInsecure("K.EXAMPLE") || p.AllowsInsecure("K.example.") {
		t.Fatal("Unicode case alias matched an ASCII exception")
	}
}

func TestAllowsInsecureCanonicalizationDoesNotAllocate(t *testing.T) {
	var p Policy
	_, _ = p.SetInsecureHosts([]string{"example.com", "127.0.0.1", "2001:db8::1"})
	for _, host := range []string{"example.com", "other.example", "EXAMPLE.COM.", "127.0.0.1", "2001:db8::1", "2001:0db8:0:0::1"} {
		if got := testing.AllocsPerRun(100, func() { p.AllowsInsecure(host) }); got != 0 {
			t.Errorf("AllowsInsecure(%q) allocated %v times", host, got)
		}
	}
}

type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

func newTestCA(t testing.TB) testCA {
	t.Helper()
	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "outboundtls test CA"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &private.PublicKey, private)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return testCA{cert: cert, key: private}
}

func (ca testCA) leaf(t testing.TB, host string, expired bool) tls.Certificate {
	t.Helper()
	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(2), NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if expired {
		template.NotBefore = now.Add(-2 * time.Hour)
		template.NotAfter = now.Add(-time.Hour)
	}
	if ip := net.ParseIP(host); ip != nil {
		template.IPAddresses = []net.IP{ip}
	} else {
		template.DNSNames = []string{host}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca.cert, &private.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der, ca.cert.Raw}, PrivateKey: private, Leaf: leaf}
}

func TestInsecureTLSConfigVerifiesNonExceptions(t *testing.T) {
	trustedCA, unknownCA := newTestCA(t), newTestCA(t)
	pool := x509.NewCertPool()
	pool.AddCert(trustedCA.cert)
	p := NewWithRootCAs(pool)
	if _, err := p.SetInsecureHosts([]string{"debug.example"}); err != nil {
		t.Fatal(err)
	}
	cfg := p.InsecureTLSConfig()
	if !cfg.InsecureSkipVerify || cfg.VerifyConnection == nil {
		t.Fatal("exception config is missing guarded verification")
	}
	for _, tc := range []struct {
		name    string
		host    string
		cert    tls.Certificate
		wantErr bool
	}{
		{"trusted", "proxy.example", trustedCA.leaf(t, "proxy.example", false), false},
		{"unknown root", "proxy.example", unknownCA.leaf(t, "proxy.example", false), true},
		{"expired", "proxy.example", trustedCA.leaf(t, "proxy.example", true), true},
		{"hostname mismatch", "proxy.example", trustedCA.leaf(t, "other.example", false), true},
		{"allowed unknown root", "debug.example", unknownCA.leaf(t, "debug.example", false), false},
		{"allowed expired mismatch", "debug.example", unknownCA.leaf(t, "other.example", true), false},
		{"sibling unknown root", "api.debug.example", unknownCA.leaf(t, "api.debug.example", false), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := tls.ConnectionState{ServerName: tc.host, PeerCertificates: []*x509.Certificate{tc.cert.Leaf}}
			err := cfg.VerifyConnection(state)
			if (err != nil) != tc.wantErr {
				t.Errorf("verification = %v; want error=%v", err, tc.wantErr)
			}
		})
	}
	if err := cfg.VerifyConnection(tls.ConnectionState{}); err == nil {
		t.Fatal("accepted missing TLS identity")
	}
	_, _ = p.SetInsecureHosts(nil)
	debug := unknownCA.leaf(t, "debug.example", false)
	if err := cfg.VerifyConnection(tls.ConnectionState{ServerName: "debug.example", PeerCertificates: []*x509.Certificate{debug.Leaf}}); err == nil {
		t.Fatal("old TLS config still accepts revoked exception")
	}
}

func TestSystemRootsRejectUnknownCertificate(t *testing.T) {
	ca := newTestCA(t)
	cert := ca.leaf(t, "proxy.example", false)
	var p Policy
	cfg := p.InsecureTLSConfig()
	if cfg.RootCAs != nil {
		t.Fatal("default config overrides system roots")
	}
	err := cfg.VerifyConnection(tls.ConnectionState{ServerName: "proxy.example", PeerCertificates: []*x509.Certificate{cert.Leaf, ca.cert}})
	var unknown x509.UnknownAuthorityError
	if !errors.As(err, &unknown) {
		t.Fatalf("untrusted certificate verification = %v", err)
	}
}

func TestConfigForHostHandshake(t *testing.T) {
	ca := newTestCA(t)
	unknownCA := newTestCA(t)
	roots := x509.NewCertPool()
	roots.AddCert(ca.cert)
	p := NewWithRootCAs(roots)
	// 新根不得穿透构造时的快照。
	roots.AddCert(unknownCA.cert)
	for _, tc := range []struct {
		name    string
		host    string
		cert    tls.Certificate
		allowed bool
		wantErr bool
	}{
		{"trusted DNS", "trusted.example", ca.leaf(t, "trusted.example", false), false, false},
		{"trusted IP", "127.0.0.1", ca.leaf(t, "127.0.0.1", false), false, false},
		{"unknown CA", "untrusted.example", unknownCA.leaf(t, "untrusted.example", false), false, true},
		{"expired", "trusted.example", ca.leaf(t, "trusted.example", true), false, true},
		{"wrong hostname", "trusted.example", ca.leaf(t, "wrong.example", false), false, true},
		{"allowed DNS", "debug.example", unknownCA.leaf(t, "debug.example", true), true, false},
		{"allowed IP", "127.0.0.1", unknownCA.leaf(t, "127.0.0.1", true), true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var hosts []string
			if tc.allowed {
				hosts = []string{tc.host}
			}
			_, _ = p.SetInsecureHosts(hosts)
			cfg := p.ConfigForHost(tc.host)
			if cfg.ServerName != tc.host || cfg.InsecureSkipVerify != tc.allowed {
				t.Fatalf("unexpected TLS config: %+v", cfg)
			}
			clientSide, serverSide := net.Pipe()
			deadline := time.Now().Add(3 * time.Second)
			_ = clientSide.SetDeadline(deadline)
			_ = serverSide.SetDeadline(deadline)
			client := tls.Client(clientSide, cfg)
			server := tls.Server(serverSide, &tls.Config{Certificates: []tls.Certificate{tc.cert}})
			done := make(chan struct{})
			go func() {
				defer close(done)
				_ = server.Handshake()
				_ = serverSide.Close()
			}()
			err := client.Handshake()
			_ = clientSide.Close()
			<-done
			if (err != nil) != tc.wantErr {
				t.Fatalf("handshake = %v; want error=%v", err, tc.wantErr)
			}
		})
	}
}

func BenchmarkPolicyAllowsInsecure(b *testing.B) {
	for _, host := range []string{"example.com", "api.example.com", "127.0.0.1", "2001:db8::1", "2001:0db8:0:0::1", "EXAMPLE.com."} {
		b.Run(host, func(b *testing.B) {
			var p Policy
			_, _ = p.SetInsecureHosts([]string{"example.com", "127.0.0.1", "2001:db8::1"})
			b.ReportAllocs()
			for b.Loop() {
				p.AllowsInsecure(host)
			}
		})
	}
}

func BenchmarkTLSHandshake(b *testing.B) {
	ca := newTestCA(b)
	cert := ca.leaf(b, "origin.example", false)
	roots := x509.NewCertPool()
	roots.AddCert(ca.cert)
	serverConfig := &tls.Config{Certificates: []tls.Certificate{cert}}
	for _, allowed := range []bool{false, true} {
		name := "verified"
		if allowed {
			name = "exception"
		}
		b.Run(name, func(b *testing.B) {
			p := NewWithRootCAs(roots)
			if allowed {
				_, _ = p.SetInsecureHosts([]string{"origin.example"})
			}
			clientConfig := p.ConfigForHost("origin.example")
			b.ReportAllocs()
			for b.Loop() {
				clientSide, serverSide := net.Pipe()
				client := tls.Client(clientSide, clientConfig)
				server := tls.Server(serverSide, serverConfig)
				done := make(chan error, 1)
				go func() {
					done <- server.Handshake()
					_ = serverSide.Close()
				}()
				err := client.Handshake()
				_ = clientSide.Close()
				serverErr := <-done
				if err != nil || serverErr != nil {
					b.Fatalf("client handshake %v, server handshake %v", err, serverErr)
				}
			}
		})
	}
}
