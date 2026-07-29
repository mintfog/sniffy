// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package service

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mintfog/sniffy/ca"
	"github.com/mintfog/sniffy/internal/core"
)

func TestNewUsesCertificateDir(t *testing.T) {
	rootCA, err := ca.NewInMemorySelfSignedCA()
	if err != nil {
		t.Fatalf("创建 CA 失败: %v", err)
	}
	configDir := t.TempDir()
	certDir := t.TempDir()
	svc := New(rootCA, core.NewEventBus(), configDir, certDir)
	if want := filepath.Join(certDir, serverCertFileName); svc.serverCerts.path != want {
		t.Fatalf("服务端证书路径不正确: want %q, got %q", want, svc.serverCerts.path)
	}
}

func newTestService(t *testing.T) *Service {
	t.Helper()
	c, err := ca.NewInMemorySelfSignedCA()
	if err != nil {
		t.Fatalf("创建 CA 失败: %v", err)
	}
	return New(c, core.NewEventBus(), "", "")
}

func TestUpdateConfigParsesDecryptScope(t *testing.T) {
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
	if !reflect.DeepEqual(got.allow, []string{"*.example.com", "api.test.com"}) {
		t.Errorf("allow = %v", got.allow)
	}
	if !reflect.DeepEqual(got.deny, []string{"ads.example.com"}) {
		t.Errorf("deny = %v", got.deny)
	}

	// 持久化到 AppConfig。
	cfg := svc.Config()
	if cfg.DecryptScope != "allow" || len(cfg.DecryptAllow) != 2 || len(cfg.DecryptDeny) != 1 {
		t.Errorf("配置未正确保存: %+v", cfg)
	}
}

func TestUpdateConfigClearsDecryptList(t *testing.T) {
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
	path := filepath.Join(t.TempDir(), configFileName)
	if err := os.WriteFile(path, []byte(`{"throttle":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cs := newConfigStore(path, AppConfig{ThrottleKiBps: defaultThrottleKiBps})
	if got := cs.get().ThrottleKiBps; got != defaultThrottleKiBps {
		t.Fatalf("旧配置限速默认值 = %d KiB/s,期望 %d", got, defaultThrottleKiBps)
	}
}

func TestDefaultConfigEnablesHTTPS(t *testing.T) {
	svc := newTestService(t)
	cfg := svc.Config()
	if !cfg.EnableHTTPS {
		t.Error("默认应启用 HTTPS MITM")
	}
	if cfg.DecryptScope != "all" {
		t.Errorf("默认解密范围应为 all,实际 %q", cfg.DecryptScope)
	}
	if cfg.ThrottleKiBps != defaultThrottleKiBps {
		t.Errorf("默认限速 = %d KiB/s,期望 %d", cfg.ThrottleKiBps, defaultThrottleKiBps)
	}
}
