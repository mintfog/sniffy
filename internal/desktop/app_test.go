// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build desktop

package desktop

import (
	"errors"
	"testing"

	appcore "github.com/mintfog/sniffy/internal/app"
	"github.com/mintfog/sniffy/internal/core"
	"github.com/mintfog/sniffy/internal/pipeline"
	"github.com/mintfog/sniffy/internal/service"
)

func newTestBridge() *Bridge {
	a := &appcore.App{
		Service:  service.New(nil, nil, "", ""),
		Pipeline: pipeline.New(nil, nil),
	}
	return New(a)
}

func TestBridgeEmptySessionViews(t *testing.T) {
	b := newTestBridge()
	if b.ServiceName() != "sniffy.Bridge" {
		t.Fatalf("ServiceName() = %q", b.ServiceName())
	}

	if got := b.GetSessions(1, 20); got.Total != 0 || len(got.Data) != 0 {
		t.Fatalf("GetSessions() = %+v", got)
	}
	if b.GetSession("missing") != nil || b.GetSessionBody("missing", "request") != nil {
		t.Fatal("不存在的 HTTP 会话应返回 nil")
	}
	b.DeleteSession("missing")
	b.ClearSessions()

	if got := b.GetWSSessions(1, 20); got.Total != 0 || len(got.Data) != 0 {
		t.Fatalf("GetWSSessions() = %+v", got)
	}
	if b.GetWSSession("missing") != nil {
		t.Fatal("不存在的 WebSocket 会话应返回 nil")
	}

	if got := b.GetStreamSessions(1, 20); got.Total != 0 || len(got.Data) != 0 {
		t.Fatalf("GetStreamSessions() = %+v", got)
	}
	if b.GetStreamSession("missing") != nil {
		t.Fatal("不存在的流式会话应返回 nil")
	}

	_ = b.GetStatistics()
	ch := make(chan core.Event)
	close(ch)
	b.forwardEvents(ch)
}

func TestBridgeConfigRecordingAndRules(t *testing.T) {
	b := newTestBridge()
	if got := b.GetConfig(); got.Port != 8080 || !got.Recording {
		t.Fatalf("默认配置异常：%+v", got)
	}
	if got := b.UpdateConfig(map[string]any{"recording": false}); got.Recording {
		t.Fatalf("UpdateConfig() 未关闭录制：%+v", got)
	}
	if b.IsRecording() {
		t.Fatal("录制状态应随配置关闭")
	}
	b.StartRecording()
	if !b.IsRecording() {
		t.Fatal("StartRecording() 后应处于录制状态")
	}
	b.StopRecording()
	if b.IsRecording() {
		t.Fatal("StopRecording() 后应停止录制")
	}

	if len(b.GetRules()) != 0 {
		t.Fatal("新服务不应含拦截规则")
	}
	created := b.CreateRule(&service.InterceptRule{Name: "测试规则", Enabled: true})
	if created == nil || created.ID == "" {
		t.Fatalf("CreateRule() = %+v", created)
	}
	updated := b.UpdateRule(created.ID, &service.InterceptRule{Name: "新名称", Enabled: true})
	if updated == nil || updated.Name != "新名称" {
		t.Fatalf("UpdateRule() = %+v", updated)
	}
	if b.UpdateRule("missing", &service.InterceptRule{}) != nil {
		t.Fatal("更新不存在的规则应返回 nil")
	}
	if !b.ToggleRule(created.ID, false) || b.ToggleRule("missing", true) {
		t.Fatal("ToggleRule() 返回值异常")
	}
	b.DeleteRule(created.ID)
	if len(b.GetRules()) != 0 {
		t.Fatal("DeleteRule() 后规则应为空")
	}
}

func TestBridgeUnavailablePlugins(t *testing.T) {
	b := newTestBridge()
	if b.GetPlugins() != nil || b.GetPluginSource("missing") != "" {
		t.Fatal("未装配插件管理器时查询结果应为空")
	}

	tests := []struct {
		name string
		call func() error
	}{
		{"EnablePlugin", func() error { return b.EnablePlugin("missing", true) }},
		{"SavePluginSource", func() error { return b.SavePluginSource("missing", "") }},
		{"DeletePlugin", func() error { return b.DeletePlugin("missing") }},
		{"UpdatePluginManifest", func() error { return b.UpdatePluginManifest("missing", nil) }},
		{"ClearPluginLogs", func() error { return b.ClearPluginLogs("missing") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(); !errors.Is(err, errPluginsUnavailable) {
				t.Fatalf("错误 = %v，期望 %v", err, errPluginsUnavailable)
			}
		})
	}
	if got, err := b.CreatePlugin(nil, ""); got != nil || !errors.Is(err, errPluginsUnavailable) {
		t.Fatalf("CreatePlugin() = (%v, %v)", got, err)
	}
}

func TestBridgeBreakpointRules(t *testing.T) {
	b := newTestBridge()
	if len(b.GetBreakpoints()) != 0 || b.ResumeBreakpoint("missing", nil) || b.AbortBreakpoint("missing") {
		t.Fatal("初始断点状态异常")
	}
	if got := b.GetGlobalBreak(); got.OnRequest || got.OnResponse {
		t.Fatalf("初始全局断点 = %+v", got)
	}
	b.SetGlobalBreak(true, false)
	if got := b.GetGlobalBreak(); !got.OnRequest || got.OnResponse {
		t.Fatalf("设置后的全局断点 = %+v", got)
	}

	rule := b.AddBreakRule("example.com", true, false)
	if rule == nil || rule.ID == "" || len(b.GetBreakRules()) != 1 {
		t.Fatalf("AddBreakRule() = %+v", rule)
	}
	if b.UpdateBreakRule("missing", "", false, false, false) {
		t.Fatal("更新不存在的断点规则应失败")
	}
	if !b.UpdateBreakRule(rule.ID, "*.example.com", false, true, true) {
		t.Fatal("更新断点规则应成功")
	}
	if !b.ToggleBreakRule(rule.ID, false) || b.ToggleBreakRule("missing", false) {
		t.Fatal("ToggleBreakRule() 返回值异常")
	}
	b.DeleteBreakRule(rule.ID)
	if len(b.GetBreakRules()) != 0 {
		t.Fatal("DeleteBreakRule() 后规则应为空")
	}
}

func TestBridgeServerCertificateValidation(t *testing.T) {
	b := newTestBridge()
	if len(b.GetServerCerts()) != 0 {
		t.Fatal("新服务不应含导入的服务端证书")
	}
	if got, err := b.ImportServerCert("invalid", "invalid"); got != nil || err == nil {
		t.Fatalf("ImportServerCert() = (%v, %v)，期望校验失败", got, err)
	}
	b.DeleteServerCert("missing")
}
