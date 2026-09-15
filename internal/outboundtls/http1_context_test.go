// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package outboundtls

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

type requestRecorder struct {
	request *http.Request
}

func (r *requestRecorder) RoundTrip(req *http.Request) (*http.Response, error) {
	r.request = req
	return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Request: req}, nil
}

func TestHTTP1ContextMatchesStandardWebSocketPredicate(t *testing.T) {
	for _, tc := range []struct {
		name       string
		connection []string
		upgrade    []string
		onlyH1     bool
	}{
		{"ordinary", nil, nil, false},
		{"websocket without connection", nil, []string{"websocket"}, false},
		{"connection without websocket", []string{"Upgrade"}, nil, false},
		{"simple", []string{"Upgrade"}, []string{"websocket"}, true},
		{"mixed case", []string{"uPgRaDe"}, []string{"WebSocket"}, true},
		{"comma tokens", []string{"keep-alive, Upgrade"}, []string{"websocket"}, true},
		{"comma whitespace", []string{"keep-alive,\t Upgrade ,"}, []string{"websocket"}, true},
		{"space boundary", []string{"keep-alive Upgrade"}, []string{"websocket"}, true},
		{"tab boundary", []string{"keep-alive\tUpgrade"}, []string{"websocket"}, true},
		{"embedded token", []string{"not-upgrade"}, []string{"websocket"}, false},
		{"semicolon boundary", []string{"keep-alive;Upgrade"}, []string{"websocket"}, false},
		{"non ASCII boundary", []string{"keep-alive\u00a0Upgrade"}, []string{"websocket"}, false},
		{"non ASCII websocket", []string{"Upgrade"}, []string{"web\u017focket"}, false},
		{"other protocol", []string{"Upgrade"}, []string{"h2c"}, false},
		{"padded websocket", []string{"Upgrade"}, []string{" websocket "}, false},
		{"secondary connection", []string{"keep-alive", "Upgrade"}, []string{"websocket"}, false},
		{"secondary upgrade", []string{"Upgrade"}, []string{"h2c", "websocket"}, false},
		{"first connection", []string{"Upgrade", "keep-alive"}, []string{"websocket"}, true},
		{"first upgrade", []string{"Upgrade"}, []string{"websocket", "h2c"}, true},
	} {
		for _, allow := range []bool{false, true} {
			name := tc.name + "/strict"
			if allow {
				name = tc.name + "/exception"
			}
			t.Run(name, func(t *testing.T) {
				var p Policy
				if allow {
					if _, err := p.SetInsecureHosts([]string{"example.com"}); err != nil {
						t.Fatal(err)
					}
				}
				type retainedContextKey struct{}
				ctx := context.WithValue(context.Background(), retainedContextKey{}, "retained")
				req := httptest.NewRequest(http.MethodGet, "https://example.com/", nil).WithContext(ctx)
				req.Header["Connection"] = tc.connection
				req.Header["Upgrade"] = tc.upgrade
				recorder := &requestRecorder{}
				tr := NewTransport(&p, recorder, recorder)
				if _, err := tr.RoundTrip(req); err != nil {
					t.Fatal(err)
				}
				marked, _ := recorder.request.Context().Value(http1OnlyContextKey{}).(bool)
				if marked != (allow && tc.onlyH1) {
					t.Fatalf("HTTP/1 marker = %v, want %v", marked, allow && tc.onlyH1)
				}
				if !marked && recorder.request != req {
					t.Fatal("unmarked request was unnecessarily copied")
				}
				if recorder.request.Context().Value(retainedContextKey{}) != "retained" {
					t.Fatal("request context values were lost")
				}
				if req.Context() != ctx || req.Context().Value(http1OnlyContextKey{}) != nil {
					t.Fatal("original request context was mutated")
				}
			})
		}
	}
}

func BenchmarkTransportDispatchUpgrade(b *testing.B) {
	for _, allow := range []bool{false, true} {
		name := "strict"
		if allow {
			name = "exception"
		}
		b.Run(name, func(b *testing.B) {
			var p Policy
			if allow {
				_, _ = p.SetInsecureHosts([]string{"example.com"})
			}
			tr := NewTransport(&p, &trackingTransport{}, &trackingTransport{})
			req := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
			req.Header.Set("Connection", "keep-alive, Upgrade")
			req.Header.Set("Upgrade", "websocket")
			b.ReportAllocs()
			for b.Loop() {
				_, _ = tr.RoundTrip(req)
			}
		})
	}
}
