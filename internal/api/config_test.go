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

// 本文件对应 runtime.go 的四组端点(status / statistics / config / recording)。
// 它们的行覆盖率全部来自方法矩阵,没有一条断言看过响应体或副作用。

// TestConfigNeverExposesPasswords 明文上游代理密码与本地代理密码只存在 config.json 里,
// 对外一律走 service.PublicConfig。这条回归等于把用户的上游凭据通过管理 API 发出去:
// 任何能读到管理端口响应的调用方(浏览器扩展、日志抓取、代理链路)直接拿到它。
func TestConfigNeverExposesPasswords(t *testing.T) {
	t.Parallel()
	const (
		upstreamSecret = "s3cr3t"
		proxySecret    = "p@ss"
	)
	s, mux := newTestServer(t)
	patch := `{"upstream":true,"upstreamAddr":"http://u:` + upstreamSecret + `@gw:3128","upstreamAuth":true,` +
		`"proxyAuth":true,"proxyUsername":"local","proxyPassword":"` + proxySecret + `"}`

	// 写入与回读走的是同一个 PublicConfig,两条都要核对:只测其中一条,另一条回归时无人发现。
	for _, c := range []struct{ name, method, body string }{
		{"PUT 的回执", http.MethodPut, patch},
		{"随后的 GET", http.MethodGet, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			rec := do(t, mux, c.method, "/api/config", c.body)
			if rec.Code != http.StatusOK {
				t.Fatalf("状态码 = %d,响应 %s", rec.Code, rec.Body.String())
			}
			// 对整段响应文本断言,而不是只看解出的结构体:密码若从别的字段(如 Extra 回存)
			// 漏出来,按字段断言完全看不见。
			raw := rec.Body.String()
			// 密码字段按 JSON 键的完整形式比对:直接找 "upstreamPassword" 子串会被
			// 合法的 "upstreamPasswordSet" 命中,那反而把这条断言变成永远失败的噪声。
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

	// 密码确实被保存了 —— 否则上面的「没泄漏」可能只是因为它压根没存进来。
	if got := s.svc.Config().UpstreamPassword; got != upstreamSecret {
		t.Errorf("service 内部应保留明文上游密码,got %q", got)
	}
}

// TestConfigPutAndPostAreEquivalent 两个方法共用一条分支而前端只用其中一个。哪天有人把 POST
// 拆成「创建」语义或只留 PUT,另一半调用方拿到 405、配置面板整页保存失败。
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

		// 回传的必须是更新后的视图:回归成回传旧值时,用户点保存后界面回滚,会以为没保存而反复重试。
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

// TestConfigInvalidBodyLeavesConfigUntouched /api/config 是唯一会落盘 0600 config.json 的写入口。
// 解码失败若变成「部分应用」或触发一次 save,用户的上游代理配置会被一次畸形请求改坏,代理立刻开始 407。
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

// TestRecordingSwitchRoundTrip 三个 handler 的行覆盖率全部来自方法矩阵,没有一条断言状态真的联动。
// start/stop 接错 service 方法或响应常量写反时,用户点「停止录制」拿到 200 和 recording:false,
// 抓包却仍在持续写库 —— 隐私敏感场景下这是最不该静默失败的一个开关。
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

	// 配置面板里的录制开关走的是另一条路径,两条必须落到同一份状态。
	if rec := do(t, mux, http.MethodPut, "/api/config", `{"recording":false}`); rec.Code != http.StatusOK {
		t.Fatalf("PUT /api/config = %d", rec.Code)
	}
	assertRecording("经 /api/config 关闭录制", false)
}

// TestStatusAndStatisticsEnvelope 这两条是探活脚本与仪表盘的直接数据源:status 键改名会让
// 探活脚本一直判定服务未就绪,分布字段变成 null 会让图表组件在空数据时崩。
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
		// 用 RawMessage 断言:解进 map 会把 null 与 {} 抹平成同一个 nil。
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
