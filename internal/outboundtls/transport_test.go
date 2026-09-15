// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package outboundtls

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type trackingTransport struct {
	requests int
	closed   int
	proxy    *url.URL
	response http.Response
}

func (t *trackingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	t.requests++
	return &t.response, nil
}

func (t *trackingTransport) CloseIdleConnections() { t.closed++ }

func (t *trackingTransport) ResolveProxy(*http.Request) (*url.URL, error) { return t.proxy, nil }

func TestConfigureInsecureHTTPTransportTLSConfig(t *testing.T) {
	roots := x509.NewCertPool()
	roots.AddCert(newTestCA(t).cert)
	p := NewWithRootCAs(roots)
	t.Run("policy defaults", func(t *testing.T) {
		tr := &http.Transport{}
		p.ConfigureInsecureHTTPTransport(tr)
		cfg := tr.TLSClientConfig
		if cfg.RootCAs == nil || !cfg.RootCAs.Equal(roots) {
			t.Fatal("default TLS config must use policy roots")
		}
		if cfg.ServerName != "" || !cfg.InsecureSkipVerify || cfg.VerifyConnection == nil || tr.DialTLSContext == nil {
			t.Fatalf("exception TLS configuration is incomplete: %+v", cfg)
		}
	})
	t.Run("caller config", func(t *testing.T) {
		original := &tls.Config{
			ServerName: "caller.example",
			RootCAs:    roots,
			MinVersion: tls.VersionTLS13,
			NextProtos: []string{"h2", "http/1.1"},
		}
		tr := &http.Transport{TLSClientConfig: original}
		p.ConfigureInsecureHTTPTransport(tr)
		cfg := tr.TLSClientConfig
		if cfg == original {
			t.Fatal("TLS config must be cloned before configuring verification")
		}
		if original.ServerName != "caller.example" || original.InsecureSkipVerify || original.VerifyConnection != nil {
			t.Fatalf("caller TLS config was mutated: %+v", original)
		}
		if cfg.RootCAs != roots || cfg.MinVersion != tls.VersionTLS13 || !slices.Equal(cfg.NextProtos, []string{"h2", "http/1.1"}) {
			t.Fatalf("caller TLS settings were lost: %+v", cfg)
		}
	})
}

func TestTransportDispatchAndRevocation(t *testing.T) {
	var p Policy
	secure := &trackingTransport{proxy: &url.URL{Host: "strict-proxy"}}
	insecure := &trackingTransport{proxy: &url.URL{Host: "debug-proxy"}}
	tr := NewTransport(&p, secure, insecure)
	_, _ = p.SetInsecureHosts([]string{"debug.example"})
	for _, target := range []string{"https://debug.example/", "https://other.example/", "https://api.debug.example/"} {
		if _, err := tr.RoundTrip(httptest.NewRequest(http.MethodGet, target, nil)); err != nil {
			t.Fatal(err)
		}
	}
	if secure.requests != 2 || insecure.requests != 1 {
		t.Fatalf("request routing: secure=%d insecure=%d", secure.requests, insecure.requests)
	}
	debugReq := httptest.NewRequest(http.MethodGet, "https://debug.example/", nil)
	if proxy, err := tr.ResolveProxy(debugReq); err != nil || proxy.Host != "debug-proxy" {
		t.Fatalf("debug proxy = %v, %v", proxy, err)
	}
	_, _ = p.SetInsecureHosts(nil)
	_, _ = tr.RoundTrip(debugReq)
	if secure.requests != 3 || insecure.requests != 1 {
		t.Fatal("revoked exception reused insecure transport")
	}
	if proxy, err := tr.ResolveProxy(debugReq); err != nil || proxy.Host != "strict-proxy" {
		t.Fatalf("strict proxy = %v, %v", proxy, err)
	}
	tr.CloseIdleConnections()
	if secure.closed != 1 || insecure.closed != 1 {
		t.Fatalf("close forwarding: secure=%d insecure=%d", secure.closed, insecure.closed)
	}
}

func TestTransportRevocationRejectsPooledUntrustedOrigin(t *testing.T) {
	ca := newTestCA(t)
	origin := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	origin.TLS = &tls.Config{Certificates: []tls.Certificate{ca.leaf(t, "127.0.0.1", false)}}
	origin.StartTLS()
	defer origin.Close()
	var p Policy
	strict := &http.Transport{}
	insecure := &http.Transport{TLSClientConfig: p.InsecureTLSConfig()}
	p.ConfigureInsecureHTTPTransport(insecure)
	tr := NewTransport(&p, strict, insecure)
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr}
	_, _ = p.SetInsecureHosts([]string{"127.0.0.1"})
	resp, err := client.Get(origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	_, _ = p.SetInsecureHosts(nil)
	if resp, err := client.Get(origin.URL); err == nil {
		_ = resp.Body.Close()
		t.Fatal("revocation reused an unverified keep-alive connection")
	}
}

func TestResolveStandardTransportProxy(t *testing.T) {
	want := &url.URL{Scheme: "https", Host: "proxy.example:443"}
	secure := &http.Transport{Proxy: http.ProxyURL(want)}
	tr := NewTransport(nil, secure, nil)
	got, err := tr.ResolveProxy(httptest.NewRequest(http.MethodGet, "https://example.com/", nil))
	if err != nil || got != want {
		t.Fatalf("ResolveProxy = %v, %v", got, err)
	}
}

func TestHTTPSProxyDoesNotInheritOriginException(t *testing.T) {
	for _, tc := range []struct {
		name       string
		originHost string
		proxyHost  string
		trusted    bool
	}{
		{"untrusted IP proxy", "origin.example", "127.0.0.1", false},
		{"trusted IP proxy", "origin.example", "127.0.0.1", true},
		{"untrusted DNS proxy IP origin", "127.0.0.1", "proxy.example", false},
		{"trusted DNS proxy IP origin", "127.0.0.1", "proxy.example", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			originCA, proxyCA := newTestCA(t), newTestCA(t)
			var originRequests, connectRequests atomic.Int32
			origin := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				originRequests.Add(1)
				_, _ = io.WriteString(w, "ok")
			}))
			origin.TLS = &tls.Config{Certificates: []tls.Certificate{originCA.leaf(t, tc.originHost, true)}}
			origin.StartTLS()
			defer origin.Close()
			originURL, _ := url.Parse(origin.URL)
			originAddr := originURL.Host
			originURL.Host = net.JoinHostPort(tc.originHost, originURL.Port())
			proxy := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodConnect {
					t.Errorf("unexpected proxy method %s", r.Method)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				connectRequests.Add(1)
				upstream, err := net.Dial("tcp", originAddr)
				if err != nil {
					w.WriteHeader(http.StatusBadGateway)
					return
				}
				defer upstream.Close()
				client, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				defer client.Close()
				if _, err := io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
					return
				}
				go func() {
					_, _ = io.Copy(upstream, client)
					_ = upstream.Close()
				}()
				_, _ = io.Copy(client, upstream)
			}))
			proxy.TLS = &tls.Config{Certificates: []tls.Certificate{proxyCA.leaf(t, tc.proxyHost, false)}}
			proxy.StartTLS()
			defer proxy.Close()
			proxyURL, _ := url.Parse(proxy.URL)
			proxyAddr := proxyURL.Host
			proxyURL.Host = net.JoinHostPort(tc.proxyHost, proxyURL.Port())
			roots := x509.NewCertPool()
			if tc.trusted {
				roots.AddCert(proxyCA.cert)
			}
			p := NewWithRootCAs(roots)
			_, _ = p.SetInsecureHosts([]string{tc.originHost})
			insecure := &http.Transport{
				Proxy: http.ProxyURL(proxyURL), TLSClientConfig: p.InsecureTLSConfig(),
				TLSHandshakeTimeout: time.Second,
				DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
					if strings.HasPrefix(addr, tc.proxyHost+":") {
						addr = proxyAddr
					}
					return (&net.Dialer{}).DialContext(ctx, network, addr)
				},
			}
			p.ConfigureInsecureHTTPTransport(insecure)
			tr := NewTransport(p, &http.Transport{}, insecure)
			defer tr.CloseIdleConnections()
			client := &http.Client{Transport: tr, Timeout: 3 * time.Second}
			resp, err := client.Get(originURL.String())
			if tc.trusted {
				if err != nil {
					t.Fatalf("trusted proxy with allowed origin = %v", err)
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
				if originRequests.Load() != 1 || connectRequests.Load() != 1 {
					t.Fatal("request did not traverse trusted proxy")
				}
			} else {
				if err == nil {
					_ = resp.Body.Close()
					t.Fatal("HTTPS proxy inherited an origin certificate exception")
				}
				if originRequests.Load() != 0 || connectRequests.Load() != 0 {
					t.Fatal("sent proxy CONNECT before verifying its certificate")
				}
			}
		})
	}
}

func BenchmarkTransportDispatch(b *testing.B) {
	b.Run("default", func(b *testing.B) {
		var p Policy
		tr := NewTransport(&p, &trackingTransport{}, &trackingTransport{})
		req := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
		b.ReportAllocs()
		for b.Loop() {
			_, _ = tr.RoundTrip(req)
		}
	})
	for _, host := range []string{"example.com", "other.example"} {
		b.Run(host, func(b *testing.B) {
			var p Policy
			_, _ = p.SetInsecureHosts([]string{"example.com"})
			tr := NewTransport(&p, &trackingTransport{}, &trackingTransport{})
			req := httptest.NewRequest(http.MethodGet, "https://"+host+"/", nil)
			b.ReportAllocs()
			for b.Loop() {
				_, _ = tr.RoundTrip(req)
			}
		})
	}
}

func BenchmarkTransportBaseline(b *testing.B) {
	tr := &trackingTransport{}
	req := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
	b.ReportAllocs()
	for b.Loop() {
		_, _ = tr.RoundTrip(req)
	}
}
