// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package service

import (
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// testDefaults 复用出厂配置,只把会话容量调小以便断言;供 configStore 级别的用例直接使用。
func testDefaults() AppConfig {
	c := defaultAppConfig()
	c.MaxFlows = 100
	return c
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
		{
			"认证关闭时不添加凭据",
			AppConfig{Upstream: true, UpstreamAddr: "proxy.example:8080", UpstreamUsername: "user", UpstreamPassword: "pass"},
			"proxy.example:8080",
		},
		{
			"关闭认证时移除地址内凭据",
			AppConfig{Upstream: true, UpstreamAddr: "http://old:secret@proxy.example:8080"},
			"http://proxy.example:8080",
		},
		{
			"认证开启时编码凭据",
			AppConfig{Upstream: true, UpstreamAddr: "proxy.example:8080", UpstreamAuth: true, UpstreamUsername: "user@example.com", UpstreamPassword: "p:a/ss"},
			"http://user%40example.com:p%3Aa%2Fss@proxy.example:8080",
		},
		{
			"独立凭据覆盖地址内旧凭据",
			AppConfig{Upstream: true, UpstreamAddr: "http://old:secret@proxy.example:8080", UpstreamAuth: true, UpstreamUsername: "new", UpstreamPassword: "password"},
			"http://new:password@proxy.example:8080",
		},
		// url.UserPassword("", "") 编码出的 "http://:@host" 会让链路发出 Basic Og==,
		// 足以让原本免认证的代理开始回 407。
		{
			"认证开启但凭据全空时不编码",
			AppConfig{Upstream: true, UpstreamAddr: "http://proxy.example:8080", UpstreamAuth: true},
			"http://proxy.example:8080",
		},
		{
			"认证开启但凭据全空时也清掉地址内旧凭据",
			AppConfig{Upstream: true, UpstreamAddr: "http://old:secret@proxy.example:8080", UpstreamAuth: true},
			"http://proxy.example:8080",
		},
		{
			"仅有账号时照常编码",
			AppConfig{Upstream: true, UpstreamAddr: "proxy.example:8080", UpstreamAuth: true, UpstreamUsername: "user"},
			"http://user:@proxy.example:8080",
		},
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
		{"上游认证开关直写", map[string]any{"upstreamAuth": true}, func(c AppConfig) any { return c.UpstreamAuth }, true},
		{"上游账号直写", map[string]any{"upstreamUsername": "proxy-user"}, func(c AppConfig) any { return c.UpstreamUsername }, "proxy-user"},
		{"关闭认证时密码被清除", map[string]any{"upstreamPassword": "proxy-pass"}, func(c AppConfig) any { return c.UpstreamPassword }, ""},
		{"本地代理认证开关直写", map[string]any{"proxyAuth": true}, func(c AppConfig) any { return c.ProxyAuth }, true},
		{"本地代理账号直写", map[string]any{"proxyUsername": "sniffy-user"}, func(c AppConfig) any { return c.ProxyUsername }, "sniffy-user"},
		{"本地代理密码直写", map[string]any{"proxyAuth": true, "proxyPassword": "proxy-pass"}, func(c AppConfig) any { return c.ProxyPassword }, "proxy-pass"},
		{"本地认证关闭时密码被清除", map[string]any{"proxyPassword": "proxy-pass"}, func(c AppConfig) any { return c.ProxyPassword }, ""},
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
		"upstreamAuth":         true,
		"upstreamUsername":     "proxy-user",
		"upstreamPassword":     "proxy-pass",
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
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("配置文件权限 = %o, want 600", got)
		}
	}
}

func TestConfigStoreNormalizesLegacyUpstreamCredentials(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), configFileName)
	legacy := `{"upstream":true,"upstreamAddr":"http://old-user:old-password@proxy.example:8080","upstreamPassword":"stored-password"}`
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}

	got := newConfigStore(path, testDefaults()).get()
	if got.UpstreamAddr != "http://proxy.example:8080" {
		t.Fatalf("遗留地址 = %q, want 无 userinfo 的地址", got.UpstreamAddr)
	}
	if got.UpstreamPassword != "" {
		t.Fatalf("关闭认证后遗留密码仍在内存中: %q", got.UpstreamPassword)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(saved), "old-password") || strings.Contains(string(saved), "stored-password") {
		t.Fatalf("规范化后的配置仍含旧密码: %s", saved)
	}
}

func TestConfigStoreMigratesLegacyUpstreamCredentials(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), configFileName)
	legacy := `{"upstream":true,"upstreamAddr":"http://old-user:old-password@proxy.example:8080"}`
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}

	got := newConfigStore(path, testDefaults()).get()
	if !got.UpstreamAuth || got.UpstreamUsername != "old-user" || got.UpstreamPassword != "old-password" {
		t.Fatalf("旧凭据未迁移: %+v", got)
	}
	// 迁移后仍要能拼回可用的上游地址,否则升级即掉认证。
	if want := "http://old-user:old-password@proxy.example:8080"; got.EffectiveUpstream() != want {
		t.Fatalf("EffectiveUpstream() = %q, want %q", got.EffectiveUpstream(), want)
	}
	if view := PublicConfig(got); view.UpstreamAddr != "http://proxy.example:8080" || !view.UpstreamPasswordSet {
		t.Fatalf("对外视图 = %+v", view)
	}
}

func TestConfigStoreClearsPasswordWhenAuthDisabled(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), configFileName)
	cs := newConfigStore(path, testDefaults())
	cs.update(map[string]any{
		"upstream":         true,
		"upstreamAddr":     "http://old-user:old-password@proxy.example:8080",
		"upstreamAuth":     true,
		"upstreamPassword": "secret",
	})

	got := cs.update(map[string]any{"upstreamAuth": false})
	if got.UpstreamAddr != "http://proxy.example:8080" {
		t.Fatalf("关闭认证后地址 = %q, want 无 userinfo 的地址", got.UpstreamAddr)
	}
	if got.UpstreamPassword != "" {
		t.Fatalf("关闭认证后密码 = %q, want 空", got.UpstreamPassword)
	}
	reloaded := newConfigStore(path, testDefaults()).get()
	if reloaded.UpstreamPassword != "" {
		t.Fatalf("重载后密码 = %q, want 空", reloaded.UpstreamPassword)
	}
}

func TestLoadSavedMigratesUpstreamCredentials(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, configFileName)
	if err := os.WriteFile(path, []byte(`{"upstreamAddr":"http://old-user:old-password@proxy.example:8080"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got, ok := LoadSaved(dir)
	if !ok {
		t.Fatal("应读取旧配置")
	}
	if got.UpstreamAddr != "http://proxy.example:8080" {
		t.Fatalf("LoadSaved 地址 = %q, want 无 userinfo 的地址", got.UpstreamAddr)
	}
	// 旧版本只能靠地址内嵌凭据认证,丢弃而不迁移会让升级后的上游认证静默失效。
	if !got.UpstreamAuth || got.UpstreamUsername != "old-user" || got.UpstreamPassword != "old-password" {
		t.Fatalf("旧凭据未迁移到独立字段: auth=%v user=%q passSet=%v", got.UpstreamAuth, got.UpstreamUsername, got.UpstreamPassword != "")
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(saved), "old-user:old-password@") {
		t.Fatalf("落盘配置仍在地址里内嵌凭据: %s", saved)
	}
}

// TestLoadSavedKeepsDefaultsForMissingFields LoadSaved 触发迁移时会整体回写 config.json,
// 以零值为底解码就会把文件里缺失的字段固化成 false。
func TestLoadSavedKeepsDefaultsForMissingFields(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, configFileName)
	partial := `{"port":9090,"upstream":true,"upstreamAddr":"http://u:p@proxy.example:8080"}`
	if err := os.WriteFile(path, []byte(partial), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := LoadSaved(dir); !ok {
		t.Fatal("应读取旧配置")
	}

	got := newConfigStore(path, testDefaults()).get()
	if !got.EnableHTTPS || !got.Recording || !got.SystemProxy || !got.AutoProxy || !got.RunInBackground || !got.LargeBodyPassthrough {
		t.Fatalf("缺失字段被回写成零值: %+v", got)
	}
	if got.Port != 9090 || !got.Upstream {
		t.Fatalf("文件中已有的字段被覆盖: %+v", got)
	}
}

func TestPublicConfigDoesNotExposePassword(t *testing.T) {
	view := PublicConfig(AppConfig{
		UpstreamAddr:     "http://user:secret@proxy.example:8080",
		UpstreamPassword: "secret",
		ProxyAuth:        true,
		ProxyUsername:    "sniffy-user",
		ProxyPassword:    "local-secret",
	})
	if !view.UpstreamPasswordSet {
		t.Fatal("已设置密码时 UpstreamPasswordSet 应为 true")
	}
	if !view.ProxyPasswordSet || view.ProxyUsername != "sniffy-user" || !view.ProxyAuth {
		t.Fatalf("本地代理认证视图不正确: %+v", view)
	}
	if view.UpstreamAddr != "http://proxy.example:8080" {
		t.Fatalf("公开配置地址仍含 userinfo: %q", view.UpstreamAddr)
	}
	data, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "secret") || strings.Contains(string(data), "local-secret") ||
		strings.Contains(string(data), `"upstreamPassword":`) || strings.Contains(string(data), `"proxyPassword":`) {
		t.Fatalf("公开配置不应包含密码字段: %s", data)
	}
}

// TestPublicConfigKeepsUnparsableAddrWithoutCredentials 地址解析不了时不能整串丢弃:
// IPv6 zone、未转义的 % 这类不含凭据的地址也解析不了,回空串会让前端以为地址被清空。
func TestPublicConfigKeepsUnparsableAddrWithoutCredentials(t *testing.T) {
	t.Parallel()
	for _, addr := range []string{"http://[fe80::1%eth0]:8080", "http://gw%.example:3128", "proxy%"} {
		if got := PublicConfig(AppConfig{UpstreamAddr: addr}).UpstreamAddr; got != addr {
			t.Errorf("PublicConfig(%q).UpstreamAddr = %q, want 原样保留", addr, got)
		}
	}
	// 不可解析但带凭据时仍不能泄露。
	got := PublicConfig(AppConfig{UpstreamAddr: "http://user:pa/ss@gw%.example:3128"}).UpstreamAddr
	if strings.Contains(got, "pa/ss") || strings.Contains(got, "user") {
		t.Fatalf("对外视图泄露了凭据: %q", got)
	}
	if got == "" {
		t.Fatal("对外视图不应整串丢弃地址")
	}
}

// TestPublicConfigDoesNotLeakSentinelAccount 对外视图会被前端回灌再下发,其中留下
// "xxxxx@" 哨兵就会被当成合法 userinfo 迁成一个不存在的账号,并把 upstreamAuth 置真。
func TestPublicConfigDoesNotLeakSentinelAccount(t *testing.T) {
	t.Parallel()
	// 未转义的 % 让 url.Parse 失败,凭据不会被规范化剥离。
	const raw = "http://user:pa%ss@proxy.example:8080"
	view := PublicConfig(AppConfig{UpstreamAddr: raw})
	if strings.Contains(view.UpstreamAddr, "@") {
		t.Fatalf("对外视图不应留下 userinfo 残留: %q", view.UpstreamAddr)
	}
	if !strings.Contains(view.UpstreamAddr, "proxy.example:8080") {
		t.Fatalf("对外视图丢失了地址主体: %q", view.UpstreamAddr)
	}

	cs := newConfigStore(filepath.Join(t.TempDir(), configFileName), defaultAppConfig())
	cs.cfg.UpstreamAddr = raw
	got := cs.update(map[string]any{"upstreamAddr": view.UpstreamAddr})
	if got.UpstreamAuth || got.UpstreamUsername != "" {
		t.Fatalf("回灌对外视图不应凭空造出账号: auth=%v user=%q", got.UpstreamAuth, got.UpstreamUsername)
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
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("载入旧配置后权限 = %o, want 600", perm)
		}
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

	svc.UpdateConfig(map[string]any{
		"upstream": true, "upstreamAddr": "http://127.0.0.1:7890",
		"upstreamAuth": true, "upstreamUsername": "user", "upstreamPassword": "p:a/ss",
	})
	svc.UpdateConfig(map[string]any{"upstream": false})

	want := []string{"http://user:p%3Aa%2Fss@127.0.0.1:7890", ""}
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

// TestRedactUpstreamError url.Parse 的错误串原样回显输入地址,而不可解析的地址不会
// 被规范化剥离凭据,只能在写进日志前抹掉。
func TestRedactUpstreamError(t *testing.T) {
	t.Parallel()
	_, err := url.Parse("http://user:sup3rsecret@proxy.example:8o8o/x")
	if err == nil {
		t.Fatal("用例地址应解析失败")
	}
	got := RedactUpstreamError(err).Error()
	if strings.Contains(got, "sup3rsecret") || strings.Contains(got, "user") {
		t.Fatalf("错误串仍含凭据: %s", got)
	}
	if !strings.Contains(got, "proxy.example") {
		t.Fatalf("错误串丢失了定位信息: %s", got)
	}
	plain := errors.New("普通错误")
	if RedactUpstreamError(plain) != plain {
		t.Error("非 url.Error 应原样返回")
	}
}

func TestRedactUserinfo(t *testing.T) {
	t.Parallel()
	tests := []struct{ in, want string }{
		{"http://user:pass@proxy.example:8080", "http://xxxxx@proxy.example:8080"},
		{"user:pass@proxy.example:8080", "xxxxx@proxy.example:8080"},
		{"http://proxy.example:8080", "http://proxy.example:8080"},
		{"http://user:pa@ss@proxy.example/path", "http://xxxxx@proxy.example/path"},
		// 密码里未转义的 /?# 会把 authority 提前截断,凭据落在后面那段,只按 authority 找会漏掉。
		{"http://user:pa/ss@proxy.example:3128", "http://xxxxx@proxy.example:3128"},
		{"http://user:pa?ss@proxy.example:3128", "http://xxxxx@proxy.example:3128"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := redactUserinfo(tt.in); got != tt.want {
			t.Errorf("redactUserinfo(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestUpdateConfigAppliesInWriteOrder 锁定「写入顺序 = 下发顺序」:第一个更新在 applier
// 内部停住时第二个更新到来,若写入与下发不是一个整体,运行时会停在陈旧值上,与持久化
// 配置相矛盾 —— 配置显示认证已关闭,监听端却仍开着,或者反过来。
func TestUpdateConfigAppliesInWriteOrder(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)

	var mu sync.Mutex
	var applied []bool
	entered := make(chan struct{})
	release := make(chan struct{})
	svc.SetProxyAuthApplier(func(enabled bool, _, _ string) error {
		if enabled { // 只有「开启」这一次停住，注入时的初始下发不受影响
			close(entered)
			<-release
		}
		// 记在等待之后:applied 的顺序即实际生效顺序。
		mu.Lock()
		applied = append(applied, enabled)
		mu.Unlock()
		return nil
	})

	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		svc.UpdateConfig(map[string]any{"proxyAuth": true, "proxyUsername": "u", "proxyPassword": "p"})
	}()
	<-entered

	secondDone := make(chan struct{})
	go func() {
		defer close(secondDone)
		svc.UpdateConfig(map[string]any{"proxyAuth": false})
	}()
	// 留出抢跑窗口:串行化后第二个更新必然还堵着,未串行化则早已跑完。
	select {
	case <-secondDone:
		t.Error("第二个更新在第一个仍持有下发过程时就完成了")
	case <-time.After(100 * time.Millisecond):
	}

	close(release)
	<-firstDone
	<-secondDone

	mu.Lock()
	last := applied[len(applied)-1]
	mu.Unlock()
	if got := svc.Config().ProxyAuth; last != got {
		t.Fatalf("运行时认证状态与持久化配置相反: 最后下发 %v, 配置 %v (下发序列 %v)", last, got, applied)
	}
	if last {
		t.Fatalf("最终应停在关闭状态, 下发序列 %v", applied)
	}
}
