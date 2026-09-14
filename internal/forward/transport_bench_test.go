// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package forward

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mintfog/sniffy/internal/flow"
)

func BenchmarkTransportRoundTrip(b *testing.B) {
	b.Run("KeepAlive", func(b *testing.B) {
		benchmarkTransportRoundTrip(b, true)
	})
	b.Run("NewConnection", func(b *testing.B) {
		benchmarkTransportRoundTrip(b, false)
	})
}

func benchmarkTransportRoundTrip(b *testing.B, keepAlive bool) {
	b.Helper()
	payload := []byte(strings.Repeat("x", 1024))
	var connections atomic.Uint64
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(payload)
	}))
	server.Config.SetKeepAlivesEnabled(keepAlive)
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	server.Start()
	b.Cleanup(server.Close)

	tr := New(Config{Fallback: &errRT{}})
	b.Cleanup(tr.CloseIdleConnections)
	req, err := http.NewRequest(http.MethodGet, server.URL+"/capture?z=1&a=2", nil)
	if err != nil {
		b.Fatal(err)
	}
	ordered := [][2]string{
		{"Host", req.URL.Host},
		{"User-Agent", "sniffy-benchmark/1.0"},
		{"Accept", "*/*"},
		{"x-custom-token", "ABC"},
		{"X-Request-ID", "42"},
		{"accept-encoding", "identity"},
		{"Cookie", "sid=xyz"},
	}
	req = req.WithContext(flow.WithOrderedHeaders(req.Context(), ordered))
	request := func() {
		resp, err := tr.RoundTrip(req)
		if err != nil {
			b.Fatalf("RoundTrip: %v", err)
		}
		n, readErr := io.Copy(io.Discard, resp.Body)
		closeErr := resp.Body.Close()
		if readErr != nil || closeErr != nil {
			b.Fatalf("响应体读取失败: read=%v, close=%v", readErr, closeErr)
		}
		if resp.StatusCode != http.StatusOK || n != int64(len(payload)) {
			b.Fatalf("响应不完整: status=%d, bytes=%d", resp.StatusCode, n)
		}
	}

	// 首次请求验证保真路径,并将 keep-alive 建连开销移出计时。
	request()
	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	for b.Loop() {
		request()
	}
	wantConnections := uint64(b.N + 1)
	if keepAlive {
		wantConnections = 1
	}
	if got := connections.Load(); got != wantConnections {
		b.Fatalf("连接数量不符: got=%d, want=%d", got, wantConnections)
	}
}

var transportBenchmarkSink *Transport

func BenchmarkTransportNew(b *testing.B) {
	cfg := Config{Fallback: &errRT{}}
	b.ReportAllocs()
	for b.Loop() {
		transportBenchmarkSink = New(cfg)
	}
}
