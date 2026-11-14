// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package forward

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/mintfog/sniffy/capture/types"
)

// TestIsolatedTransportDialsProxyItself：透明连接的请求经上游代理发出时，即使请求的 authority
// 与代理地址相同，连的也是代理而不是应用的原始目标。
func TestIsolatedTransportDialsProxyItself(t *testing.T) {
	var originHits atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		originHits.Add(1)
	}))
	defer origin.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "proxy saw "+r.URL.String())
	}))
	defer proxy.Close()
	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}

	base := &http.Transport{
		Proxy: http.ProxyURL(proxyURL),
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, types.PinnedDialAddr(ctx, address))
		},
	}
	defer base.CloseIdleConnections()
	transport := IsolateByOriginalDst(base)

	ctx := types.WithOriginalDst(context.Background(), proxyURL.Host, origin.Listener.Addr().String())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, proxy.URL+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if want := "proxy saw " + proxy.URL + "/"; string(body) != want {
		t.Fatalf("响应体 = %q，期望 %q", body, want)
	}
	if n := originHits.Load(); n != 0 {
		t.Fatalf("原始目标收到 %d 个请求，期望 0", n)
	}
}
