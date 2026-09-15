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
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestWebSocketUpgradeDirectPreservesProtocol(t *testing.T) {
	for _, mode := range []string{"native", "exception"} {
		for _, warm := range []bool{false, true} {
			name := mode + "/cold"
			if warm {
				name = mode + "/warm"
			}
			t.Run(name, func(t *testing.T) {
				origin := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != "/upgrade" {
						_, _ = io.WriteString(w, r.Proto)
						return
					}
					if r.ProtoMajor != 1 || r.TLS.NegotiatedProtocol == "h2" {
						t.Errorf("Upgrade reached origin over %s with ALPN %q", r.Proto, r.TLS.NegotiatedProtocol)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					conn, rw, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					defer conn.Close()
					_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
					if _, err := rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n"); err != nil {
						return
					}
					if err := rw.Flush(); err != nil {
						return
					}
					payload := make([]byte, len("upgrade-echo"))
					if _, err := io.ReadFull(rw, payload); err != nil {
						return
					}
					_, _ = conn.Write(payload)
				}))
				origin.EnableHTTP2 = true
				origin.StartTLS()
				t.Cleanup(origin.Close)
				roots := x509.NewCertPool()
				roots.AddCert(origin.Certificate())
				var p Policy
				strictConfig := &tls.Config{}
				if mode == "native" {
					strictConfig.RootCAs = roots
				}
				strict := &http.Transport{TLSClientConfig: strictConfig, ForceAttemptHTTP2: true}
				insecure := &http.Transport{TLSClientConfig: p.InsecureTLSConfig(), ForceAttemptHTTP2: true}
				p.ConfigureInsecureHTTPTransport(insecure)
				tr := NewTransport(&p, strict, insecure)
				t.Cleanup(tr.CloseIdleConnections)
				client := &http.Client{Transport: tr}
				if mode == "exception" {
					if _, err := p.SetInsecureHosts([]string{"127.0.0.1"}); err != nil {
						t.Fatal(err)
					}
				}
				assertHTTP2 := func() {
					t.Helper()
					ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
					defer cancel()
					req, err := http.NewRequestWithContext(ctx, http.MethodGet, origin.URL, nil)
					if err != nil {
						t.Fatal(err)
					}
					resp, err := client.Do(req)
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
				if warm {
					assertHTTP2()
				}
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, origin.URL+"/upgrade", nil)
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set("Connection", "keep-alive, Upgrade")
				req.Header.Set("Upgrade", "websocket")
				resp, err := client.Do(req)
				if err != nil {
					t.Fatalf("Upgrade request = %v", err)
				}
				defer resp.Body.Close()
				if resp.StatusCode != http.StatusSwitchingProtocols || resp.ProtoMajor != 1 {
					t.Fatalf("Upgrade response = %s %s", resp.Proto, resp.Status)
				}
				duplex, ok := resp.Body.(io.ReadWriteCloser)
				if !ok {
					t.Fatal("Upgrade response body is not bidirectional")
				}
				if _, err := io.WriteString(duplex, "upgrade-echo"); err != nil {
					t.Fatal(err)
				}
				payload := make([]byte, len("upgrade-echo"))
				if _, err := io.ReadFull(duplex, payload); err != nil || string(payload) != "upgrade-echo" {
					t.Fatalf("Upgrade echo = %q, %v", payload, err)
				}
				_ = resp.Body.Close()
				assertHTTP2()
				if mode == "exception" {
					if _, err := p.SetInsecureHosts(nil); err != nil {
						t.Fatal(err)
					}
					resp, err := client.Do(req)
					if resp != nil {
						_ = resp.Body.Close()
					}
					var unknown x509.UnknownAuthorityError
					if !errors.As(err, &unknown) {
						t.Fatalf("revoked Upgrade must reject unknown CA: %v", err)
					}
				}
			})
		}
	}
}

func TestWebSocketUpgradeDirectVerifiesCertificate(t *testing.T) {
	origin := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("Upgrade must not reach an untrusted origin")
	}))
	origin.EnableHTTP2 = true
	origin.StartTLS()
	t.Cleanup(origin.Close)
	var p Policy
	insecure := &http.Transport{TLSClientConfig: p.InsecureTLSConfig(), ForceAttemptHTTP2: true}
	p.ConfigureInsecureHTTPTransport(insecure)
	tr := NewTransport(&p, &http.Transport{ForceAttemptHTTP2: true}, insecure)
	t.Cleanup(tr.CloseIdleConnections)
	client := &http.Client{Transport: tr, Timeout: 3 * time.Second}
	for _, hosts := range [][]string{nil, {"other.example"}} {
		if _, err := p.SetInsecureHosts(hosts); err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequest(http.MethodGet, origin.URL+"/upgrade", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Connection", "keep-alive, Upgrade")
		req.Header.Set("Upgrade", "websocket")
		resp, err := client.Do(req)
		if resp != nil {
			_ = resp.Body.Close()
		}
		var unknown x509.UnknownAuthorityError
		if !errors.As(err, &unknown) {
			t.Fatalf("Upgrade must reject unknown CA: %v", err)
		}
	}
}
