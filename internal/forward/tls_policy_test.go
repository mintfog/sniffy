// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package forward

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/mintfog/sniffy/internal/flow"
)

func TestTLSConfigForHostUsesActualTarget(t *testing.T) {
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(origin.Close)
	for _, trusted := range []bool{false, true} {
		t.Run(map[bool]string{false: "拒绝未知CA", true: "显式信任CA"}[trusted], func(t *testing.T) {
			roots := x509.NewCertPool()
			if trusted {
				roots.AddCert(origin.Certificate())
			}
			var target string
			transport := New(Config{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
				TLSConfigForHost: func(host string) *tls.Config {
					target = host
					return &tls.Config{RootCAs: roots}
				},
				TLSTimeout: time.Second,
			})
			raw, err := net.Dial("tcp", origin.Listener.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			conn, _, err := transport.tlsHandshake(context.Background(), raw, "127.0.0.1")
			if conn != nil {
				_ = conn.Close()
			}
			if target != "127.0.0.1" {
				t.Fatalf("TLS 策略收到目标 %q, 期望实际拨号主机", target)
			}
			if trusted && err != nil {
				t.Fatalf("已信任 CA 应成功握手: %v", err)
			}
			var unknownAuthority x509.UnknownAuthorityError
			if !trusted && !errors.As(err, &unknownAuthority) {
				t.Fatalf("逐主机策略必须覆盖共享的不安全配置, 实际错误: %v", err)
			}
		})
	}
}

func TestNonHTTPProxyFallsBackBeforeSendingCredentials(t *testing.T) {
	for _, scheme := range []string{"https", "socks5", "socks5h"} {
		t.Run(scheme, func(t *testing.T) {
			proxyURL, err := url.Parse(scheme + "://private:secret@127.0.0.1:1")
			if err != nil {
				t.Fatal(err)
			}
			fallback := &recordRT{}
			transport := New(Config{
				Fallback: fallback,
				Proxy:    func(*http.Request) (*url.URL, error) { return proxyURL, nil },
			})
			req := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
			req = req.WithContext(flow.WithOrderedHeaders(req.Context(), [][2]string{{"Host", "example.com"}}))
			resp, err := transport.RoundTrip(req)
			if err != nil {
				t.Fatal(err)
			}
			if resp != nil {
				_ = resp.Body.Close()
			}
			if fallback.called != 1 {
				t.Fatalf("不支持的保真代理协议必须直接走标准转发器, 回退次数: %d", fallback.called)
			}
		})
	}
}
