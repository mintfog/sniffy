// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package service

import (
	"reflect"
	"testing"

	"github.com/mintfog/sniffy/ca"
	"github.com/mintfog/sniffy/internal/core"
)

func newTestService(t *testing.T) *Service {
	t.Helper()
	c, err := ca.NewSelfSignedCA()
	if err != nil {
		t.Fatalf("创建 CA 失败: %v", err)
	}
	return New(c, core.NewEventBus(), "")
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

func TestDefaultConfigEnablesHTTPS(t *testing.T) {
	svc := newTestService(t)
	cfg := svc.Config()
	if !cfg.EnableHTTPS {
		t.Error("默认应启用 HTTPS MITM")
	}
	if cfg.DecryptScope != "all" {
		t.Errorf("默认解密范围应为 all,实际 %q", cfg.DecryptScope)
	}
}
