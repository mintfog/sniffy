// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package service

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

// testDefaults 与 New 里的出厂配置保持一致,供 configStore 级别的用例直接复用。
func testDefaults() AppConfig {
	return AppConfig{
		Port: 8080, EnableHTTPS: true, Recording: true, SystemProxy: true, AutoProxy: true,
		ThrottleKiBps: defaultThrottleKiBps, RunInBackground: true, DecryptScope: "all",
		LargeBodyPassthrough: true, LargeBodyKiB: defaultLargeBodyKiB, MaxFlows: 100,
	}
}

// TestDefaultConfig 出厂配置直接决定首次启动的行为,逐项守住。
func TestDefaultConfig(t *testing.T) {
	t.Parallel()
	cfg := newTestService(t).Config()

	if !cfg.EnableHTTPS || cfg.DecryptScope != "all" {
		t.Errorf("默认应全量解密 HTTPS: enableHTTPS=%v scope=%q", cfg.EnableHTTPS, cfg.DecryptScope)
	}
	if cfg.Port != 8080 || !cfg.Recording || !cfg.SystemProxy || !cfg.AutoProxy || !cfg.RunInBackground {
		t.Errorf("默认开关 = %+v", cfg)
	}
	if cfg.Throttle || cfg.ThrottleKiBps != defaultThrottleKiBps {
		t.Errorf("默认限速应关闭但速率有值: %v/%d", cfg.Throttle, cfg.ThrottleKiBps)
	}
	if !cfg.LargeBodyPassthrough || cfg.LargeBodyKiB != defaultLargeBodyKiB {
		t.Errorf("默认透传旁路 = %v/%d", cfg.LargeBodyPassthrough, cfg.LargeBodyKiB)
	}
	if cfg.Upstream || cfg.UpstreamAddr != "" {
		t.Errorf("默认不应启用上游代理: %+v", cfg)
	}
}

func TestEffectiveUpstream(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		cfg  AppConfig
		want string
	}{
		// 关闭开关时回空串,引擎据此判断直连。
		{"开关关闭时为空", AppConfig{Upstream: false, UpstreamAddr: "http://127.0.0.1:7890"}, ""},
		{"开启时取地址", AppConfig{Upstream: true, UpstreamAddr: "http://127.0.0.1:7890"}, "http://127.0.0.1:7890"},
		{"开启但地址为空", AppConfig{Upstream: true}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.cfg.EffectiveUpstream(); got != tt.want {
				t.Errorf("EffectiveUpstream() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestPatchInt64 前端经 JSON 传来的整数是 float64,桌面端直传 Go 值时可能是 int;带小数的值应被拒。
func TestPatchInt64(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		in     any
		want   int64
		wantOK bool
	}{
		{"int", 42, 42, true},
		{"int64", int64(42), 42, true},
		{"整数 float64", float64(42), 42, true},
		{"零值", float64(0), 0, true},
		{"负数", float64(-8), -8, true},
		{"带小数被拒", 42.5, 42, false},
		{"字符串被拒", "42", 0, false},
		{"nil 被拒", nil, 0, false},
		{"bool 被拒", true, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := patchInt64(tt.in)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && got != tt.want {
				t.Errorf("值 = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestToStringSlice(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   any
		want []string
	}{
		{"[]any 全字符串", []any{"a", "b"}, []string{"a", "b"}},
		{"[]any 混杂时跳过非字符串", []any{"a", 1, nil, "b"}, []string{"a", "b"}},
		{"空 []any 得到空切片", []any{}, []string{}},
		{"原生 []string 直通", []string{"a"}, []string{"a"}},
		{"其它类型为 nil", "a", nil},
		{"nil 为 nil", nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := toStringSlice(tt.in); !slices.Equal(got, tt.want) {
				t.Errorf("toStringSlice() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

// TestConfigStoreUpdateValidation patch 直接来自前端,非法值应被丢弃而不是写进配置并落盘。
func TestConfigStoreUpdateValidation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		patch map[string]any
		field func(AppConfig) any
		want  any
	}{
		{"端口下界", map[string]any{"port": float64(1)}, func(c AppConfig) any { return c.Port }, 1},
		{"端口上界", map[string]any{"port": float64(65535)}, func(c AppConfig) any { return c.Port }, 65535},
		{"端口 0 被拒", map[string]any{"port": float64(0)}, func(c AppConfig) any { return c.Port }, 8080},
		{"端口越界被拒", map[string]any{"port": float64(65536)}, func(c AppConfig) any { return c.Port }, 8080},
		{"端口类型不符被拒", map[string]any{"port": "8081"}, func(c AppConfig) any { return c.Port }, 8080},
		{"maxFlows 正数生效", map[string]any{"maxFlows": float64(20)}, func(c AppConfig) any { return c.MaxFlows }, 20},
		{"maxFlows 非正被拒", map[string]any{"maxFlows": float64(0)}, func(c AppConfig) any { return c.MaxFlows }, 100},
		{"限速下界", map[string]any{"throttleKiBps": float64(minThrottleKiBps)}, func(c AppConfig) any { return c.ThrottleKiBps }, minThrottleKiBps},
		{"限速上界", map[string]any{"throttleKiBps": float64(maxThrottleKiBps)}, func(c AppConfig) any { return c.ThrottleKiBps }, maxThrottleKiBps},
		{"限速 0 被拒", map[string]any{"throttleKiBps": float64(0)}, func(c AppConfig) any { return c.ThrottleKiBps }, defaultThrottleKiBps},
		{"限速超上界被拒", map[string]any{"throttleKiBps": float64(maxThrottleKiBps + 1)}, func(c AppConfig) any { return c.ThrottleKiBps }, defaultThrottleKiBps},
		{"旁路阈值正数生效", map[string]any{"largeBodyKiB": float64(512)}, func(c AppConfig) any { return c.LargeBodyKiB }, int64(512)},
		{"旁路阈值非正被拒", map[string]any{"largeBodyKiB": float64(0)}, func(c AppConfig) any { return c.LargeBodyKiB }, defaultLargeBodyKiB},
		{"布尔开关直写", map[string]any{"enableHTTPS": false}, func(c AppConfig) any { return c.EnableHTTPS }, false},
		{"字符串直写", map[string]any{"decryptScope": "deny"}, func(c AppConfig) any { return c.DecryptScope }, "deny"},
		{"上游地址直写", map[string]any{"upstreamAddr": "http://127.0.0.1:7890"}, func(c AppConfig) any { return c.UpstreamAddr }, "http://127.0.0.1:7890"},
		{"未知字段被忽略", map[string]any{"nope": float64(1)}, func(c AppConfig) any { return c.Port }, 8080},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cs := newConfigStore("", testDefaults())
			got := tt.field(cs.update(tt.patch))
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("字段 = %#v, want %#v", got, tt.want)
			}
		})
	}
}

// TestConfigStorePersistRoundTrip 配置改完立刻落盘,重建 store 后应逐字段一致。
func TestConfigStorePersistRoundTrip(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), configFileName)
	cs := newConfigStore(path, testDefaults())

	saved := cs.update(map[string]any{
		"port":                 float64(9090),
		"enableHTTPS":          false,
		"recording":            false,
		"maxFlows":             float64(1234),
		"upstream":             true,
		"upstreamAddr":         "http://127.0.0.1:7890",
		"autoSystemProxy":      false,
		"throttle":             true,
		"throttleKiBps":        float64(256),
		"largeBodyPassthrough": false,
		"largeBodyKiB":         float64(512),
		"runInBackground":      false,
		"decryptScope":         "allow",
		"decryptAllow":         []any{"*.example.com"},
		"decryptDeny":          []any{"ads.example.com"},
	})

	if got := newConfigStore(path, testDefaults()).get(); !reflect.DeepEqual(got, saved) {
		t.Fatalf("重载配置 = %+v\nwant %+v", got, saved)
	}
}

// TestConfigStoreLoadKeepsDefaultsForMissingFields 旧版本写下的 config.json 缺字段时,缺的部分保持出厂值而非清零。
func TestConfigStoreLoadKeepsDefaultsForMissingFields(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), configFileName)
	if err := os.WriteFile(path, []byte(`{"port":9090}`), 0o644); err != nil {
		t.Fatal(err)
	}

	got := newConfigStore(path, testDefaults()).get()
	if got.Port != 9090 {
		t.Errorf("文件里的字段应生效: port=%d", got.Port)
	}
	if !got.EnableHTTPS || !got.RunInBackground || got.LargeBodyKiB != defaultLargeBodyKiB {
		t.Errorf("缺失字段应保持默认: %+v", got)
	}
}

// TestConfigStoreLoadRepairsInvalidValues 手改坏的数值型配置(0 速率、非正阈值)在载入时修回默认值。
func TestConfigStoreLoadRepairsInvalidValues(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		content string
		check   func(*testing.T, AppConfig)
	}{
		{
			name:    "限速为 0",
			content: `{"throttle":true,"throttleKiBps":0}`,
			check: func(t *testing.T, c AppConfig) {
				if c.ThrottleKiBps != defaultThrottleKiBps {
					t.Errorf("限速 = %d, want %d", c.ThrottleKiBps, defaultThrottleKiBps)
				}
			},
		},
		{
			name:    "限速超上界",
			content: `{"throttleKiBps":99999999999}`,
			check: func(t *testing.T, c AppConfig) {
				if c.ThrottleKiBps != defaultThrottleKiBps {
					t.Errorf("限速 = %d, want %d", c.ThrottleKiBps, defaultThrottleKiBps)
				}
			},
		},
		{
			name:    "旁路阈值为负",
			content: `{"largeBodyKiB":-1}`,
			check: func(t *testing.T, c AppConfig) {
				if c.LargeBodyKiB != defaultLargeBodyKiB {
					t.Errorf("阈值 = %d, want %d", c.LargeBodyKiB, defaultLargeBodyKiB)
				}
			},
		},
		{
			name:    "整个文件是坏 JSON",
			content: `{ 坏掉了`,
			check: func(t *testing.T, c AppConfig) {
				if !reflect.DeepEqual(c, testDefaults()) {
					t.Errorf("坏文件应整体退回默认值: %+v", c)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), configFileName)
			if err := os.WriteFile(path, []byte(tt.content), 0o644); err != nil {
				t.Fatal(err)
			}
			tt.check(t, newConfigStore(path, testDefaults()).get())
		})
	}
}

// TestLoadSaved 装配层在引擎创建前读回上次的设置;读不到时回 false,由调用方走默认值。
func TestLoadSaved(t *testing.T) {
	t.Parallel()

	t.Run("目录为空", func(t *testing.T) {
		t.Parallel()
		if _, ok := LoadSaved(""); ok {
			t.Error("空目录应返回 false")
		}
	})
	t.Run("文件不存在", func(t *testing.T) {
		t.Parallel()
		if _, ok := LoadSaved(t.TempDir()); ok {
			t.Error("无配置文件应返回 false")
		}
	})
	t.Run("坏 JSON", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, configFileName), []byte("{坏"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, ok := LoadSaved(dir); ok {
			t.Error("坏 JSON 应返回 false")
		}
	})
	t.Run("正常读取", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		newConfigStore(filepath.Join(dir, configFileName), testDefaults()).update(map[string]any{"port": float64(9091)})
		got, ok := LoadSaved(dir)
		if !ok || got.Port != 9091 {
			t.Fatalf("读取结果 = %+v ok=%v", got, ok)
		}
	})
}

// TestSetSystemProxyStatePersists 桌面端启动时自动开启系统代理,该状态要写回并落盘配置。
func TestSetSystemProxyStatePersists(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	svc := New(nil, nil, dir, "")

	svc.SetSystemProxyState(false)
	if svc.Config().SystemProxy {
		t.Error("状态未写入内存配置")
	}
	if got, ok := LoadSaved(dir); !ok || got.SystemProxy {
		t.Errorf("状态未落盘: %+v ok=%v", got, ok)
	}
}

func TestUpdateConfigParsesDecryptScope(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)

	var got struct {
		enabled bool
		mode    string
		allow   []string
		deny    []string
		calls   int
	}
	svc.SetDecryptScopeApplier(func(enabled bool, mode string, allow, deny []string) error {
		got.enabled, got.mode, got.allow, got.deny = enabled, mode, allow, deny
		got.calls++
		return nil
	})

	// 模拟 JSON 解码后的 patch:数组元素为 []any。
	svc.UpdateConfig(map[string]any{
		"enableHTTPS":  true,
		"decryptScope": "allow",
		"decryptAllow": []any{"*.example.com", "api.test.com"},
		"decryptDeny":  []any{"ads.example.com"},
	})

	if got.calls != 1 {
		t.Fatalf("applier 应被调用 1 次,实际 %d", got.calls)
	}
	if !got.enabled || got.mode != "allow" {
		t.Errorf("enabled/mode = %v/%q, want true/allow", got.enabled, got.mode)
	}
	if !slices.Equal(got.allow, []string{"*.example.com", "api.test.com"}) {
		t.Errorf("allow = %v", got.allow)
	}
	if !slices.Equal(got.deny, []string{"ads.example.com"}) {
		t.Errorf("deny = %v", got.deny)
	}

	cfg := svc.Config()
	if cfg.DecryptScope != "allow" || len(cfg.DecryptAllow) != 2 || len(cfg.DecryptDeny) != 1 {
		t.Errorf("配置未正确保存: %+v", cfg)
	}
}

func TestUpdateConfigClearsDecryptList(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	svc.UpdateConfig(map[string]any{"decryptAllow": []any{"a.com"}})
	if len(svc.Config().DecryptAllow) != 1 {
		t.Fatalf("初始白名单未写入")
	}
	// 传空数组应清空列表。
	svc.UpdateConfig(map[string]any{"decryptAllow": []any{}})
	if len(svc.Config().DecryptAllow) != 0 {
		t.Errorf("空数组应清空白名单,实际 %v", svc.Config().DecryptAllow)
	}
}

func TestUpdateConfigAppliesThrottle(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)

	type throttleCall struct {
		enabled bool
		rate    int64
	}
	var got []throttleCall
	svc.SetThrottleApplier(func(enabled bool, rate int64) error {
		got = append(got, throttleCall{enabled: enabled, rate: rate})
		return nil
	})

	cfg := svc.UpdateConfig(map[string]any{"throttle": true, "throttleKiBps": float64(256)})
	if !cfg.Throttle || cfg.ThrottleKiBps != 256 {
		t.Fatalf("限速配置未写入: %+v", cfg)
	}
	cfg = svc.UpdateConfig(map[string]any{"throttleKiBps": float64(64)})
	if !cfg.Throttle || cfg.ThrottleKiBps != 64 {
		t.Fatalf("单独修改速率未生效: %+v", cfg)
	}
	cfg = svc.UpdateConfig(map[string]any{"throttleKiBps": float64(0)})
	if cfg.ThrottleKiBps != 64 {
		t.Fatalf("非法速率覆盖了有效配置: %+v", cfg)
	}
	// 与限速无关的字段变更不该重建限速器。
	svc.UpdateConfig(map[string]any{"port": float64(8081)})
	cfg = svc.UpdateConfig(map[string]any{"throttle": false})
	if cfg.Throttle {
		t.Fatal("throttle 应可关闭")
	}
	want := []throttleCall{
		{enabled: true, rate: 256},
		{enabled: true, rate: 64},
		{enabled: false, rate: 64},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("throttle applier = %v, want %v", got, want)
	}
}

func TestThrottleRateDefaultsForLegacyConfig(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), configFileName)
	if err := os.WriteFile(path, []byte(`{"throttle":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cs := newConfigStore(path, AppConfig{ThrottleKiBps: defaultThrottleKiBps})
	if got := cs.get().ThrottleKiBps; got != defaultThrottleKiBps {
		t.Fatalf("旧配置限速默认值 = %d KiB/s,期望 %d", got, defaultThrottleKiBps)
	}
}

// TestUpdateConfigAppliesUpstream 上游地址以合并后的最终配置下发(引擎侧幂等),关掉开关时下发空串。
func TestUpdateConfigAppliesUpstream(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	var got []string
	svc.SetUpstreamApplier(func(addr string) error {
		got = append(got, addr)
		return nil
	})

	svc.UpdateConfig(map[string]any{"upstream": true, "upstreamAddr": "http://127.0.0.1:7890"})
	svc.UpdateConfig(map[string]any{"upstream": false})

	want := []string{"http://127.0.0.1:7890", ""}
	if !slices.Equal(got, want) {
		t.Fatalf("upstream applier = %v, want %v", got, want)
	}
}

// TestUpdateConfigSystemProxyOnlyOnChange 前端每次推送都带 systemProxy,
// 仅在值变化时才执行外部命令。
func TestUpdateConfigSystemProxyOnlyOnChange(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	var got []bool
	svc.SetSystemProxyApplier(func(enabled bool) error {
		got = append(got, enabled)
		return nil
	})

	svc.UpdateConfig(map[string]any{"systemProxy": true}) // 与当前值相同:不动
	svc.UpdateConfig(map[string]any{"port": float64(8081)})
	svc.UpdateConfig(map[string]any{"systemProxy": false})
	svc.UpdateConfig(map[string]any{"systemProxy": false}) // 重复推送:不动
	svc.UpdateConfig(map[string]any{"systemProxy": true})

	if want := []bool{false, true}; !slices.Equal(got, want) {
		t.Fatalf("系统代理 applier = %v, want %v", got, want)
	}
}

// TestPassthroughApplier 旁路开关与阈值要在注入时立刻按持久化配置生效一次,
// 之后仅在这两个字段变化时下发。
func TestPassthroughApplier(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)

	type call struct {
		enabled   bool
		threshold int64
	}
	var got []call
	svc.SetPassthroughApplier(func(enabled bool, threshold int64) error {
		got = append(got, call{enabled, threshold})
		return nil
	})

	svc.UpdateConfig(map[string]any{"largeBodyKiB": float64(512)})
	svc.UpdateConfig(map[string]any{"largeBodyPassthrough": false})
	svc.UpdateConfig(map[string]any{"largeBodyKiB": float64(0)}) // 非法值:既不写入也不下发
	svc.UpdateConfig(map[string]any{"port": float64(8081)})      // 无关字段:不下发

	want := []call{
		{true, defaultLargeBodyKiB * 1024},
		{true, 512 * 1024},
		{false, 512 * 1024},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("旁路 applier = %v, want %v", got, want)
	}
}

func TestUpdateConfigTogglesRecording(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	if !svc.IsRecording() {
		t.Fatal("默认应处于录制态")
	}
	svc.UpdateConfig(map[string]any{"recording": false})
	if svc.IsRecording() {
		t.Error("配置关闭录制后应停止录制")
	}
	svc.UpdateConfig(map[string]any{"port": float64(8081)})
	if svc.IsRecording() {
		t.Error("无关配置变更不应重新打开录制")
	}
	svc.UpdateConfig(map[string]any{"recording": true})
	if !svc.IsRecording() {
		t.Error("配置开启录制后应恢复录制")
	}
}

// TestUpdateConfigResizesSessionStore 调小会话上限应立刻生效并淘汰超出的旧记录。
func TestUpdateConfigResizesSessionStore(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	for _, id := range []string{"a", "b", "c", "d"} {
		svc.RecordFlowStarted(newFlow(id))
	}

	svc.UpdateConfig(map[string]any{"maxFlows": float64(2)})

	list, total := svc.Sessions(1, 10)
	if total != 2 {
		t.Fatalf("缩容后总数 = %d, want 2", total)
	}
	if got := sessionIDs(list); !slices.Equal(got, []string{"d", "c"}) {
		t.Fatalf("缩容应淘汰最旧的,剩下 %v", got)
	}
	// 非法值不该动存储。
	svc.UpdateConfig(map[string]any{"maxFlows": float64(0)})
	if _, total := svc.Sessions(1, 10); total != 2 {
		t.Fatalf("非法上限不应改动存储,总数 = %d", total)
	}
}

// TestUpdateConfigWithoutAppliers headless 与浏览器预览下多数 applier 未注入,配置更新应静默跳过。
func TestUpdateConfigWithoutAppliers(t *testing.T) {
	t.Parallel()
	svc := New(nil, nil, "", "")
	cfg := svc.UpdateConfig(map[string]any{
		"upstream": true, "upstreamAddr": "http://127.0.0.1:7890",
		"systemProxy": false, "throttle": true, "throttleKiBps": float64(64),
		"largeBodyPassthrough": false, "largeBodyKiB": float64(128),
		"enableHTTPS": false, "decryptScope": "deny", "decryptDeny": []any{"a.com"},
	})
	if cfg.UpstreamAddr != "http://127.0.0.1:7890" || cfg.ThrottleKiBps != 64 || cfg.LargeBodyKiB != 128 {
		t.Fatalf("未注入 applier 时配置本身仍应写入: %+v", cfg)
	}
}
