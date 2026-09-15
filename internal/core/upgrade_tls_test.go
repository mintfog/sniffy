// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package core

import (
	"crypto/x509"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/mintfog/sniffy/internal/flow"
)

func TestEngineWebSocketUpgradeFallbackPreservesProtocol(t *testing.T) {
	for _, mode := range []string{"faithful", "fallback"} {
		t.Run(mode, func(t *testing.T) {
			t.Cleanup(restoreRuntimeDefaults)
			if mode == "fallback" {
				t.Setenv("SNIFFY_FAITHFUL", "0")
			} else {
				t.Setenv("SNIFFY_FAITHFUL", "1")
			}
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
				conn, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.Close()
				_, _ = io.WriteString(conn, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
			}))
			origin.EnableHTTP2 = true
			origin.StartTLS()
			t.Cleanup(origin.Close)
			engine := &Engine{}
			engine.upstream = engine.buildUpstreamClient()
			client := engine.UpstreamClient()
			t.Cleanup(client.CloseIdleConnections)
			if err := engine.SetTLSInsecureHosts([]string{"127.0.0.1"}); err != nil {
				t.Fatal(err)
			}
			assertHTTP2 := func() {
				t.Helper()
				resp, err := client.Get(origin.URL)
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				body, err := io.ReadAll(resp.Body)
				if err != nil {
					t.Fatal(err)
				}
				if resp.ProtoMajor != 2 || string(body) != "HTTP/2.0" {
					t.Fatalf("ordinary fallback lost HTTP/2: response=%s origin=%q", resp.Proto, body)
				}
			}
			assertHTTP2()
			req, err := http.NewRequest(http.MethodGet, origin.URL+"/upgrade", nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Connection", "keep-alive, Upgrade")
			req.Header.Set("Upgrade", "websocket")
			target, _ := url.Parse(origin.URL)
			req = req.WithContext(flow.WithOrderedHeaders(req.Context(), [][2]string{
				{"Host", target.Host}, {"Connection", "keep-alive, Upgrade"}, {"Upgrade", "websocket"},
			}))
			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("Upgrade fallback = %v", err)
			}
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusSwitchingProtocols || resp.ProtoMajor != 1 {
				t.Fatalf("Upgrade response = %s %s", resp.Proto, resp.Status)
			}
			assertHTTP2()
			if err := engine.SetTLSInsecureHosts(nil); err != nil {
				t.Fatal(err)
			}
			resp, err = client.Do(req)
			if resp != nil {
				_ = resp.Body.Close()
			}
			var unknown x509.UnknownAuthorityError
			if !errors.As(err, &unknown) {
				t.Fatalf("revoked Upgrade exception must fail certificate validation: %v", err)
			}
		})
	}
}
