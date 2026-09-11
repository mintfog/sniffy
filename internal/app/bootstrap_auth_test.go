// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package app

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mintfog/sniffy/internal/platform"
)

func TestBuildRestoresProxyAuth(t *testing.T) {
	for _, tt := range []struct{ name, username, password string }{
		{"完整凭据", "alice", "secret"},
		{"缺少账号", "", "secret"},
		{"缺少密码", "alice", ""},
		{"空凭据", "", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if !inAppTestSubprocess(t) {
				if out, err := appTestSubprocess(t); err != nil {
					t.Fatalf("认证启动子进程失败: %v\n%s", err, out)
				}
				return
			}
			isolateAppDirs(t)
			preserveAppLogging(t)
			dir, err := platform.ConfigDir()
			if err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(map[string]any{
				"proxyAuth":     true,
				"proxyUsername": tt.username,
				"proxyPassword": tt.password,
			})
			if err != nil {
				t.Fatal(err)
			}
			writeAppFixture(t, filepath.Join(dir, "config.json"), string(data))
			cfg := DefaultConfig()
			cfg.Address, cfg.Port = "127.0.0.1", 0
			a, err := Build(cfg, false)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := a.Stop(); err != nil {
					t.Error(err)
				}
			})
			if err := a.Start(); err != nil {
				t.Fatal(err)
			}

			var hits atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				if r.Header.Get("Proxy-Authorization") != "" {
					t.Error("代理凭据被转发到上游")
				}
				_, _ = io.WriteString(w, "authenticated")
			}))
			t.Cleanup(upstream.Close)
			proxyURL, err := url.Parse("http://" + a.Engine.Listener().GetAddress())
			if err != nil {
				t.Fatal(err)
			}
			transport := &http.Transport{Proxy: http.ProxyURL(proxyURL), DisableKeepAlives: true}
			t.Cleanup(transport.CloseIdleConnections)
			client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
			basic := func(value string) string { return "Basic " + base64.StdEncoding.EncodeToString([]byte(value)) }
			savedCredentials := basic(tt.username + ":" + tt.password)
			for _, request := range []struct {
				name, credentials string
				allowed           bool
			}{
				{"无凭据", "", false},
				{"错误凭据", basic("wrong:wrong"), false},
				{"保存的凭据", savedCredentials, tt.username != "" && tt.password != ""},
			} {
				t.Run(request.name, func(t *testing.T) {
					req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, upstream.URL, nil)
					if err != nil {
						t.Fatal(err)
					}
					req.Header.Set("Proxy-Authorization", request.credentials)
					before := hits.Load()
					resp, err := client.Do(req)
					if err != nil {
						t.Fatal(err)
					}
					body, readErr := io.ReadAll(resp.Body)
					_ = resp.Body.Close()
					if readErr != nil {
						t.Fatal(readErr)
					}
					wantStatus, wantHits := http.StatusProxyAuthRequired, int32(0)
					if request.allowed {
						wantStatus, wantHits = http.StatusOK, 1
					}
					if resp.StatusCode != wantStatus {
						t.Fatalf("响应状态 = %d，期望 %d", resp.StatusCode, wantStatus)
					}
					if got := hits.Load() - before; got != wantHits {
						t.Fatalf("上游请求数 = %d，期望 %d", got, wantHits)
					}
					if request.allowed && string(body) != "authenticated" {
						t.Fatalf("上游响应 = %q，期望 authenticated", body)
					}
					if !request.allowed && resp.Header.Get("Proxy-Authenticate") == "" {
						t.Fatal("407 响应缺少 Proxy-Authenticate 头")
					}
				})
			}
		})
	}
}
