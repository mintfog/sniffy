// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/mintfog/sniffy/internal/core"
	"github.com/mintfog/sniffy/internal/service"
)

// 本文件覆盖 runtime.go 的 status、statistics、config 和 recording 端点及其状态联动；方法白名单见 method_test.go。

// TestConfigNeverExposesPasswords PublicConfig 对外隐藏上游与本地代理密码，保留用户名、凭据存在标志和脱敏地址。
func TestConfigNeverExposesPasswords(t *testing.T) {
	t.Parallel()
	const (
		upstreamSecret = "s3cr3t"
		proxySecret    = "p@ss"
	)
	s, mux := newTestServer(t)
	patch := `{"upstream":true,"upstreamAddr":"http://u:` + upstreamSecret + `@gw:3128","upstreamAuth":true,` +
		`"proxyAuth":true,"proxyUsername":"local","proxyPassword":"` + proxySecret + `"}`

	// PUT 回执与后续 GET 均按 PublicConfig 返回，写入和读取共享同一脱敏视图。
	for _, c := range []struct{ name, method, body string }{
		{"PUT 的回执", http.MethodPut, patch},
		{"随后的 GET", http.MethodGet, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			rec := do(t, mux, c.method, "/api/config", c.body)
			if rec.Code != http.StatusOK {
				t.Fatalf("状态码 = %d,响应 %s", rec.Code, rec.Body.String())
			}
			// 检查完整响应文本，覆盖 DTO 之外可能出现的敏感字段。
			raw := rec.Body.String()
			// 按完整 JSON 键匹配，区分 upstreamPassword 与 upstreamPasswordSet。
			for _, secret := range []string{upstreamSecret, proxySecret, `"upstreamPassword":`, `"proxyPassword":`} {
				if strings.Contains(raw, secret) {
					t.Errorf("响应体出现了 %s: %s", secret, raw)
				}
			}
			var view service.ConfigView
			decodeEnvelope(t, rec).into(t, &view)
			if view.UpstreamAddr != "http://gw:3128" {
				t.Errorf("上游地址应剥掉内嵌 userinfo,got %q", view.UpstreamAddr)
			}
			if view.UpstreamUsername != "u" || !view.UpstreamPasswordSet {
				t.Errorf("上游凭据视图 = %q/%v,期望 \"u\"/true", view.UpstreamUsername, view.UpstreamPasswordSet)
			}
			if view.ProxyUsername != "local" || !view.ProxyPasswordSet {
				t.Errorf("本地代理凭据视图 = %q/%v,期望 \"local\"/true", view.ProxyUsername, view.ProxyPasswordSet)
			}
		})
	}

	// service 内部仍保存上游密码，脱敏只发生在 PublicConfig 边界。
	if got := s.svc.Config().UpstreamPassword; got != upstreamSecret {
		t.Errorf("service 内部应保留明文上游密码,got %q", got)
	}
}

// TestConfigPutAndPostAreEquivalent PUT 与 POST 更新同一份配置并返回相同视图，兼容配置面板的两种提交方式。
func TestConfigPutAndPostAreEquivalent(t *testing.T) {
	t.Parallel()
	const patch = `{"port":9091,"recording":false,"throttle":true,"throttleKiBps":256}`

	bodies := make(map[string]string, 2)
	for _, method := range []string{http.MethodPut, http.MethodPost} {
		s, mux := newTestServer(t)
		rec := do(t, mux, method, "/api/config", patch)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s 状态码 = %d,响应 %s", method, rec.Code, rec.Body.String())
		}
		bodies[method] = string(decodeEnvelope(t, rec).Data)

		// 回传更新后的视图，供配置面板立即刷新。
		var view service.ConfigView
		decodeEnvelope(t, rec).into(t, &view)
		if view.Port != 9091 || view.Recording || !view.Throttle || view.ThrottleKiBps != 256 {
			t.Errorf("%s 回传的视图 = %+v", method, view)
		}
		if cfg := s.svc.Config(); cfg.Port != 9091 || s.svc.IsRecording() {
			t.Errorf("%s 未落到 service: port=%d recording=%v", method, cfg.Port, s.svc.IsRecording())
		}
	}
	if bodies[http.MethodPut] != bodies[http.MethodPost] {
		t.Errorf("PUT 与 POST 的回执应逐字节相等:\nPUT  %s\nPOST %s", bodies[http.MethodPut], bodies[http.MethodPost])
	}
}

// TestConfigInvalidBodyLeavesConfigUntouched /api/config 的畸形 JSON 返回 400，内存配置和 0600 config.json 均保持原值。
func TestConfigInvalidBodyLeavesConfigUntouched(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	svc := service.New(nil, core.NewEventBus(), configDir, t.TempDir())
	_, mux := newTestServer(t, withService(svc))

	if rec := do(t, mux, http.MethodPut, "/api/config", `{"port":9090}`); rec.Code != http.StatusOK {
		t.Fatalf("预置配置失败: %d %s", rec.Code, rec.Body.String())
	}
	before := svc.Config()
	beforeFile, err := os.ReadFile(filepath.Join(configDir, "config.json"))
	if err != nil {
		t.Fatalf("读取 config.json: %v", err)
	}

	for _, c := range []struct{ name, body string }{
		{"截断的 JSON", `{"port":`},
		{"空体", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			rec := do(t, mux, http.MethodPut, "/api/config", c.body)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("状态码 = %d,期望 400", rec.Code)
			}
			if e := decodeEnvelope(t, rec); e.Success || e.Message != "invalid json" {
				t.Errorf("响应 = success:%v message:%q", e.Success, e.Message)
			}
		})
	}

	if got := svc.Config(); !reflect.DeepEqual(got, before) {
		t.Errorf("畸形请求改动了内存配置:\n前 %+v\n后 %+v", before, got)
	}
	afterFile, err := os.ReadFile(filepath.Join(configDir, "config.json"))
	if err != nil {
		t.Fatalf("读取 config.json: %v", err)
	}
	if string(afterFile) != string(beforeFile) {
		t.Errorf("畸形请求触发了一次落盘:\n前 %s\n后 %s", beforeFile, afterFile)
	}
}

// TestRecordingSwitchRoundTrip start/stop 的回执、状态端点和 service 状态保持一致，确保录制开关立即生效。
func TestRecordingSwitchRoundTrip(t *testing.T) {
	t.Parallel()
	s, mux := newTestServer(t)

	assertRecording := func(step string, want bool) {
		t.Helper()
		if got := s.svc.IsRecording(); got != want {
			t.Errorf("%s 后 service 侧录制状态 = %v,期望 %v", step, got, want)
		}
		rec := do(t, mux, http.MethodGet, "/api/recording/status", "")
		var body struct {
			Recording bool `json:"recording"`
		}
		decodeEnvelope(t, rec).into(t, &body)
		if body.Recording != want {
			t.Errorf("%s 后 status 端点回 %v,期望 %v", step, body.Recording, want)
		}
	}

	for _, c := range []struct {
		step string
		path string
		want bool
	}{
		{"停止录制", "/api/recording/stop", false},
		{"开始录制", "/api/recording/start", true},
	} {
		rec := do(t, mux, http.MethodPost, c.path, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("POST %s = %d", c.path, rec.Code)
		}
		var body struct {
			Recording bool `json:"recording"`
		}
		decodeEnvelope(t, rec).into(t, &body)
		if body.Recording != c.want {
			t.Errorf("%s 的回执 recording = %v,期望 %v", c.step, body.Recording, c.want)
		}
		assertRecording(c.step, c.want)
	}

	// 配置面板通过 /api/config 更新录制开关，仍与 recording 端点共享状态。
	if rec := do(t, mux, http.MethodPut, "/api/config", `{"recording":false}`); rec.Code != http.StatusOK {
		t.Fatalf("PUT /api/config = %d", rec.Code)
	}
	assertRecording("经 /api/config 关闭录制", false)
}

// TestStatusAndStatisticsEnvelope status 和 statistics 的信封字段供探活脚本与仪表盘直接消费。
func TestStatusAndStatisticsEnvelope(t *testing.T) {
	t.Parallel()
	_, mux := newTestServer(t)

	t.Run("status", func(t *testing.T) {
		t.Parallel()
		rec := do(t, mux, http.MethodGet, "/api/status", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("状态码 = %d", rec.Code)
		}
		e := decodeEnvelope(t, rec)
		if !e.Success || e.Timestamp == "" {
			t.Errorf("信封 = success:%v timestamp:%q", e.Success, e.Timestamp)
		}
		var data struct {
			Status  string `json:"status"`
			Version string `json:"version"`
			Uptime  int64  `json:"uptime"`
		}
		e.into(t, &data)
		if data.Status != "running" {
			t.Errorf("status = %q,期望 \"running\"", data.Status)
		}
		if data.Version == "" || data.Uptime < 0 {
			t.Errorf("version/uptime = %q/%d", data.Version, data.Uptime)
		}
		var keys map[string]json.RawMessage
		if err := json.Unmarshal(e.Data, &keys); err != nil {
			t.Fatalf("data 不是对象: %v", err)
		}
		got := make([]string, 0, len(keys))
		for k := range keys {
			got = append(got, k)
		}
		slices.Sort(got)
		if !slices.Equal(got, []string{"status", "uptime", "version"}) {
			t.Errorf("data 键 = %v,期望恰为 {status,uptime,version}", got)
		}
	})

	t.Run("statistics 的空分布不是 null", func(t *testing.T) {
		t.Parallel()
		rec := do(t, mux, http.MethodGet, "/api/statistics", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("状态码 = %d", rec.Code)
		}
		// 用 RawMessage 区分 null 与 {}，保留线上字段形状。
		var data struct {
			TotalRequests          json.RawMessage `json:"totalRequests"`
			StatusCodeDistribution json.RawMessage `json:"statusCodeDistribution"`
			MethodDistribution     json.RawMessage `json:"methodDistribution"`
			TopHosts               json.RawMessage `json:"topHosts"`
		}
		decodeEnvelope(t, rec).into(t, &data)
		if len(data.TotalRequests) == 0 {
			t.Error("缺少 totalRequests 字段")
		}
		for name, raw := range map[string]json.RawMessage{
			"statusCodeDistribution": data.StatusCodeDistribution,
			"methodDistribution":     data.MethodDistribution,
		} {
			if string(raw) != "{}" {
				t.Errorf("零请求时 %s = %s,期望 {}", name, raw)
			}
		}
		if string(data.TopHosts) != "[]" {
			t.Errorf("零请求时 topHosts = %s,期望 []", data.TopHosts)
		}
	})
}
