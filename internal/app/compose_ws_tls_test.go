// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package app

import (
	"crypto/x509"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	gws "github.com/gorilla/websocket"

	"github.com/mintfog/sniffy/internal/flow"
)

func newUntrustedWSServer(t *testing.T) (string, *atomic.Int64, <-chan http.Header) {
	t.Helper()
	requests := &atomic.Int64{}
	headers := make(chan http.Header, 8)
	upgrader := gws.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		headers <- r.Header.Clone()
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	return "wss" + strings.TrimPrefix(srv.URL, "https"), requests, headers
}

func TestOpenWebSocketRejectsUntrustedTLS(t *testing.T) {
	for _, tc := range []struct {
		name       string
		withEngine bool
	}{
		{name: "引擎默认验证", withEngine: true},
		{name: "无引擎也默认验证"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := &App{}
			if tc.withEngine {
				app = newComposeApp(t)
			}
			url, requests, _ := newUntrustedWSServer(t)
			id, err := app.OpenWebSocket(flow.RequestSpec{
				URL: url,
				Headers: [][2]string{
					{"Authorization", "Bearer secret"},
					{"Cookie", "session=secret"},
				},
			})
			var unknownAuthority x509.UnknownAuthorityError
			if id != "" || !errors.As(err, &unknownAuthority) {
				if id != "" {
					_ = app.CloseWebSocket(id)
				}
				t.Fatalf("未受信任 wss 应被拒绝: id=%q err=%v", id, err)
			}
			if requests.Load() != 0 {
				t.Fatal("证书验证失败时不得向上游发送握手或敏感请求头")
			}
			if len(app.outWS.dials)+len(app.outWS.conns) != 0 {
				t.Fatal("TLS 失败不应保留出站连接名额")
			}
			if app.Service != nil {
				if _, total := app.Service.WSSessions(1, 100); total != 0 {
					t.Fatal("TLS 失败不应导入 WebSocket 会话")
				}
			}
		})
	}
}

func TestOpenWebSocketTLSHostPolicy(t *testing.T) {
	app := newComposeApp(t)
	t.Cleanup(func() { _ = app.Engine.SetTLSInsecureHosts(nil) })
	url, requests, headers := newUntrustedWSServer(t)
	t.Cleanup(app.CloseAllWebSockets)
	for _, tc := range []struct {
		name    string
		hosts   []string
		host    string
		allowed bool
	}{
		{name: "例外不外溢到 URL 主机", hosts: []string{"localhost"}},
		{name: "精确 URL 主机例外", hosts: []string{"127.0.0.1"}, allowed: true},
		{name: "撤销后下一次连接恢复验证"},
		{name: "Host 头不能授予 URL 主机例外", hosts: []string{"example.test"}, host: "example.test"},
		{name: "拨号主机例外不取 Host 头", hosts: []string{"127.0.0.1"}, host: "example.test", allowed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := app.Engine.SetTLSInsecureHosts(tc.hosts); err != nil {
				t.Fatal(err)
			}
			before := requests.Load()
			spec := flow.RequestSpec{
				URL: url,
				Headers: [][2]string{
					{"Authorization", "Bearer secret"},
					{"Cookie", "session=secret"},
				},
			}
			if tc.host != "" {
				spec.Headers = append(spec.Headers, [2]string{"Host", tc.host})
			}
			id, err := app.OpenWebSocket(spec)
			if id != "" {
				defer app.CloseWebSocket(id)
			}
			if tc.allowed {
				if err != nil || id == "" {
					t.Fatalf("显式 URL 主机例外应建立连接: id=%q err=%v", id, err)
				}
				h := <-headers
				if requests.Load() != before+1 || h.Get("Authorization") != "Bearer secret" || h.Get("Cookie") != "session=secret" {
					t.Fatalf("显式例外握手未正常发送: requests=%d headers=%v", requests.Load(), h)
				}
			} else {
				var unknownAuthority x509.UnknownAuthorityError
				if id != "" || !errors.As(err, &unknownAuthority) {
					t.Fatalf("非例外 URL 主机应被拒绝: id=%q err=%v", id, err)
				}
				if requests.Load() != before {
					t.Fatal("非例外主机不应收到敏感握手头")
				}
			}
		})
	}
}
