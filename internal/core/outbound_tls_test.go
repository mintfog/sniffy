// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package core

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/internal/pipeline"
)

func TestEngineOutboundHTTPSRequiresTrustedOrigin(t *testing.T) {
	for _, mode := range []string{"faithful", "fallback", "http2", "stream"} {
		t.Run(mode, func(t *testing.T) {
			if mode == "fallback" {
				t.Setenv("SNIFFY_FAITHFUL", "0")
			} else {
				t.Setenv("SNIFFY_FAITHFUL", "1")
			}
			var hits atomic.Int32
			origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				_, _ = io.WriteString(w, "origin-ok")
			}))
			t.Cleanup(origin.Close)

			engine := newProbeEngine(t)
			sink := newProbeFlowSink()
			engine.SetPipeline(pipeline.New(nil, nil))
			engine.SetFlowSink(sink)
			if engine.StreamUpstreamClient().Transport != engine.UpstreamClient().Transport {
				t.Fatal("流式与普通客户端必须共享相同 TLS 策略")
			}
			if err := engine.SetDecryptScope(true, "all", nil, nil); err != nil {
				t.Fatal(err)
			}
			if err := engine.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = engine.Stop() })

			// 客户端只信任 Sniffy CA,源站测试证书不在任何一侧的信任库中。
			roots := x509.NewCertPool()
			roots.AddCert(engine.CA().GetCA())
			proxyURL, err := url.Parse("http://" + engine.Listener().GetAddress())
			if err != nil {
				t.Fatal(err)
			}
			client := &http.Client{
				Transport: &http.Transport{
					Proxy:             http.ProxyURL(proxyURL),
					TLSClientConfig:   &tls.Config{RootCAs: roots},
					ForceAttemptHTTP2: mode == "http2",
				},
				Timeout: 5 * time.Second,
			}
			t.Cleanup(client.CloseIdleConnections)

			check := func(stage string, wantStatus int, wantHits int32) {
				t.Helper()
				req, err := http.NewRequest(http.MethodPost, origin.URL, strings.NewReader("private-payload"))
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set("Authorization", "Bearer private-token")
				req.Header.Set("Cookie", "session=private-cookie")
				if mode == "stream" {
					req.Header.Set("Accept", "text/event-stream")
				}
				resp, err := client.Do(req)
				if err != nil {
					t.Fatalf("%s: 客户端应成功校验 Sniffy 证书并收到代理响应: %v", stage, err)
				}
				body, readErr := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				if readErr != nil {
					t.Fatal(readErr)
				}
				if resp.StatusCode != wantStatus {
					t.Fatalf("%s: 状态码 = %d, 期望 %d, body=%q", stage, resp.StatusCode, wantStatus, body)
				}
				if resp.TLS == nil || len(resp.TLS.VerifiedChains) == 0 {
					t.Fatalf("%s: 客户端没有正常验证 Sniffy 证书", stage)
				}
				if mode == "http2" && resp.ProtoMajor != 2 {
					t.Fatalf("%s: 未覆盖 HTTP/2 入口: %s", stage, resp.Proto)
				}
				if got := hits.Load(); got != wantHits {
					t.Fatalf("%s: 源站收到 %d 次含敏感数据的 HTTP 请求, 期望 %d", stage, got, wantHits)
				}
				f := sink.take(t, stage)
				if wantStatus == http.StatusBadGateway {
					if f.State != flow.StateErrored || !strings.Contains(f.Error, "x509:") {
						t.Fatalf("%s: 应记录源站证书验证失败, state=%s error=%q", stage, f.State, f.Error)
					}
				} else if string(body) != "origin-ok" {
					t.Fatalf("%s: 响应体 = %q", stage, body)
				}
			}

			check("默认严格验证", http.StatusBadGateway, 0)
			if err := engine.SetTLSInsecureHosts([]string{"other.test"}); err != nil {
				t.Fatal(err)
			}
			check("其他主机例外不外溢", http.StatusBadGateway, 0)
			if err := engine.SetTLSInsecureHosts([]string{"127.0.0.1"}); err != nil {
				t.Fatal(err)
			}
			check("显式精确主机例外", http.StatusOK, 1)
			if err := engine.SetTLSInsecureHosts(nil); err != nil {
				t.Fatal(err)
			}
			// 复用同一个已验证的客户端隧道,仍不能复用旧的未验证源站连接。
			check("撤销例外", http.StatusBadGateway, 1)
		})
	}
}

func TestEngineUpstreamClientRejectsUntrustedHTTPS(t *testing.T) {
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("证书验证失败时不应向源站发送 HTTP 请求")
	}))
	t.Cleanup(origin.Close)
	engine := &Engine{}
	client := engine.buildUpstreamClient()
	t.Cleanup(client.CloseIdleConnections)
	resp, err := client.Get(origin.URL)
	if resp != nil {
		_ = resp.Body.Close()
	}
	var unknownAuthority x509.UnknownAuthorityError
	if !errors.As(err, &unknownAuthority) {
		t.Fatalf("默认出站应拒绝未知 CA, 实际错误: %v", err)
	}
	var nilEngine *Engine
	if nilEngine.OutboundTLSConfig("example.com").InsecureSkipVerify {
		t.Fatal("缺少引擎时仍必须默认验证证书")
	}
}

func TestEngineTLSInsecureHostsValidatesBeforeApplying(t *testing.T) {
	t.Cleanup(restoreRuntimeDefaults)
	transport := &closeTrackingTransport{}
	engine := &Engine{upstream: &http.Client{Transport: transport}}
	if err := engine.SetTLSInsecureHosts([]string{"EXAMPLE.COM."}); err != nil {
		t.Fatal(err)
	}
	if transport.closes != 1 || !engine.OutboundTLSConfig("example.com").InsecureSkipVerify {
		t.Fatal("合法例外必须生效并清理旧连接池")
	}
	if err := engine.SetTLSInsecureHosts([]string{"example.com"}); err != nil {
		t.Fatal(err)
	}
	if transport.closes != 1 {
		t.Fatal("等价名单不应反复清理连接池")
	}
	if err := engine.SetTLSInsecureHosts([]string{"*"}); err == nil {
		t.Fatal("通配符不能作为证书例外")
	}
	if transport.closes != 1 || !engine.OutboundTLSConfig("example.com").InsecureSkipVerify {
		t.Fatal("非法配置不能污染已生效策略")
	}
	if engine.OutboundTLSConfig("other.test").InsecureSkipVerify {
		t.Fatal("证书例外不能扩展到其他主机")
	}
	if err := engine.SetTLSInsecureHosts(nil); err != nil {
		t.Fatal(err)
	}
	if transport.closes != 2 || engine.OutboundTLSConfig("example.com").InsecureSkipVerify {
		t.Fatal("清空名单必须恢复严格验证并清理连接池")
	}
}
