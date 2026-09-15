// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package outboundtls

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

type upgradeProxyFixture struct {
	policy          *Policy
	client          *http.Client
	originURL       *url.URL
	originRequests  atomic.Int32
	connectRequests atomic.Int32
	proxyHTTP2      atomic.Bool
}

type upgradeProxyConfig struct {
	tlsProxy   bool
	h2Proxy    bool
	trustProxy bool
}

func newUpgradeProxyFixture(t *testing.T, config upgradeProxyConfig) *upgradeProxyFixture {
	t.Helper()
	f := &upgradeProxyFixture{}
	originCA, proxyCA := newTestCA(t), newTestCA(t)
	origin := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.originRequests.Add(1)
		if r.URL.Path != "/upgrade" {
			_, _ = io.WriteString(w, r.Proto)
			return
		}
		if r.ProtoMajor != 1 || r.TLS.NegotiatedProtocol == "h2" {
			t.Errorf("Upgrade reached origin over %s with ALPN %q", r.Proto, r.TLS.NegotiatedProtocol)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		_, _ = io.WriteString(conn, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
	}))
	origin.EnableHTTP2 = true
	origin.TLS = &tls.Config{Certificates: []tls.Certificate{originCA.leaf(t, "origin.example", false)}}
	origin.StartTLS()
	t.Cleanup(origin.Close)
	f.originURL, _ = url.Parse(origin.URL)
	originAddr := f.originURL.Host
	f.originURL.Host = net.JoinHostPort("origin.example", f.originURL.Port())

	proxy := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || r.Host != f.originURL.Host {
			t.Errorf("unexpected proxy request: %s %s", r.Method, r.Host)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		f.connectRequests.Add(1)
		if r.ProtoMajor != 1 || r.TLS != nil && r.TLS.NegotiatedProtocol == "h2" {
			t.Errorf("CONNECT reached proxy over %s", r.Proto)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		upstream, err := net.DialTimeout("tcp", originAddr, time.Second)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		defer upstream.Close()
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		if _, err := io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			return
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			_, _ = io.Copy(upstream, conn)
			_ = upstream.Close()
		}()
		_, _ = io.Copy(conn, upstream)
		_ = conn.Close()
		<-done
	}))
	if config.tlsProxy {
		proxy.EnableHTTP2 = config.h2Proxy
		proxyTLS := &tls.Config{Certificates: []tls.Certificate{proxyCA.leaf(t, "proxy.example", false)}}
		if config.h2Proxy {
			proxyTLS.NextProtos = []string{"h2", "http/1.1"}
			proxyTLS.GetConfigForClient = func(*tls.ClientHelloInfo) (*tls.Config, error) {
				cfg := proxyTLS.Clone()
				cfg.GetConfigForClient = nil
				if !f.proxyHTTP2.Load() {
					cfg.NextProtos = []string{"http/1.1"}
				}
				return cfg, nil
			}
		}
		proxy.TLS = proxyTLS
		proxy.StartTLS()
	} else {
		proxy.Start()
	}
	t.Cleanup(proxy.Close)
	proxyURL, _ := url.Parse(proxy.URL)
	proxyAddr := proxyURL.Host
	proxyURL.Host = net.JoinHostPort("proxy.example", proxyURL.Port())
	roots := x509.NewCertPool()
	if config.trustProxy {
		roots.AddCert(proxyCA.cert)
	}
	f.policy = NewWithRootCAs(roots)
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		if addr == proxyURL.Host {
			addr = proxyAddr
		}
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}
	strict := &http.Transport{
		Proxy: http.ProxyURL(proxyURL), DialContext: dial, ForceAttemptHTTP2: true,
		TLSClientConfig: &tls.Config{RootCAs: roots}, TLSHandshakeTimeout: time.Second,
	}
	insecure := &http.Transport{
		Proxy: http.ProxyURL(proxyURL), DialContext: dial, ForceAttemptHTTP2: true,
		TLSClientConfig: f.policy.InsecureTLSConfig(), TLSHandshakeTimeout: time.Second,
	}
	f.policy.ConfigureInsecureHTTPTransport(insecure)
	transport := NewTransport(f.policy, strict, insecure)
	t.Cleanup(transport.CloseIdleConnections)
	f.client = &http.Client{Transport: transport, Timeout: 3 * time.Second}
	return f
}

func (f *upgradeProxyFixture) requestUpgrade(t *testing.T) (*http.Response, error) {
	t.Helper()
	target := *f.originURL
	target.Path = "/upgrade"
	req, err := http.NewRequest(http.MethodGet, target.String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Connection", "keep-alive, Upgrade")
	req.Header.Set("Upgrade", "websocket")
	return f.client.Do(req)
}

func (f *upgradeProxyFixture) assertHTTP2(t *testing.T) {
	t.Helper()
	resp, err := f.client.Get(f.originURL.String())
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.ProtoMajor != 2 || string(body) != "HTTP/2.0" {
		t.Fatalf("ordinary request lost HTTP/2: response=%s origin=%q", resp.Proto, body)
	}
}

func TestWebSocketUpgradeViaCONNECTPreservesProtocol(t *testing.T) {
	for _, tc := range []struct {
		name     string
		tlsProxy bool
		h2Proxy  bool
	}{
		{"HTTP proxy", false, false},
		{"HTTPS proxy", true, false},
		{"HTTP2 capable HTTPS proxy", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newUpgradeProxyFixture(t, upgradeProxyConfig{
				tlsProxy:   tc.tlsProxy,
				h2Proxy:    tc.h2Proxy,
				trustProxy: tc.tlsProxy,
			})
			if _, err := f.policy.SetInsecureHosts([]string{"origin.example"}); err != nil {
				t.Fatal(err)
			}
			f.assertHTTP2(t)
			// 普通 CONNECT 的 h2 首跳是标准库既有限制；只在 Upgrade 新连接时提供 h2，检验首跳的 HTTP/1.1 约束。
			f.proxyHTTP2.Store(tc.h2Proxy)
			resp, err := f.requestUpgrade(t)
			if err != nil {
				t.Fatalf("Upgrade through proxy = %v", err)
			}
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusSwitchingProtocols || resp.ProtoMajor != 1 {
				t.Fatalf("Upgrade response = %s %s", resp.Proto, resp.Status)
			}
			f.proxyHTTP2.Store(false)
			f.assertHTTP2(t)
			if f.originRequests.Load() != 3 || f.connectRequests.Load() != 2 {
				t.Fatalf("request/pool isolation: origin=%d CONNECT=%d", f.originRequests.Load(), f.connectRequests.Load())
			}
			if _, err := f.policy.SetInsecureHosts(nil); err != nil {
				t.Fatal(err)
			}
			resp, err = f.requestUpgrade(t)
			if err == nil {
				_ = resp.Body.Close()
				t.Fatal("Upgrade reused a revoked certificate exception")
			}
			var unknown x509.UnknownAuthorityError
			if !errors.As(err, &unknown) {
				t.Fatalf("revoked Upgrade did not verify origin certificate: %v", err)
			}
			if f.originRequests.Load() != 3 {
				t.Fatal("revoked Upgrade reached untrusted origin")
			}
		})
	}
}

func TestWebSocketUpgradeViaCONNECTVerifiesCertificates(t *testing.T) {
	for _, tc := range []struct {
		name        string
		tlsProxy    bool
		trustProxy  bool
		allowOrigin bool
		wantConnect int32
	}{
		{"HTTP proxy untrusted origin", false, false, false, 1},
		{"HTTPS proxy untrusted origin", true, true, false, 1},
		{"origin exception does not trust HTTPS proxy", true, false, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newUpgradeProxyFixture(t, upgradeProxyConfig{
				tlsProxy:   tc.tlsProxy,
				h2Proxy:    tc.tlsProxy,
				trustProxy: tc.trustProxy,
			})
			if tc.allowOrigin {
				if _, err := f.policy.SetInsecureHosts([]string{"origin.example"}); err != nil {
					t.Fatal(err)
				}
			}
			f.proxyHTTP2.Store(true)
			resp, err := f.requestUpgrade(t)
			if err == nil {
				_ = resp.Body.Close()
				t.Fatal("Upgrade accepted an untrusted TLS peer")
			}
			var unknown x509.UnknownAuthorityError
			if !errors.As(err, &unknown) {
				t.Fatalf("expected certificate verification failure, got %v", err)
			}
			if f.originRequests.Load() != 0 || f.connectRequests.Load() != tc.wantConnect {
				t.Fatalf("sent traffic before verification: origin=%d CONNECT=%d", f.originRequests.Load(), f.connectRequests.Load())
			}
		})
	}
}
