// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package app

import (
	"bytes"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mintfog/sniffy/internal/core"
	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/internal/pipeline"
	"github.com/mintfog/sniffy/internal/platform"
	"github.com/mintfog/sniffy/internal/service"
)

func TestBuildLifecycle(t *testing.T) {
	if !inAppTestSubprocess(t) {
		if out, err := appTestSubprocess(t); err != nil {
			t.Fatalf("启动装配子进程失败: %v\n%s", err, out)
		}
		return
	}
	isolateAppDirs(t)
	preserveAppLogging(t)
	dir, err := platform.ConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	writeAppFixture(t, filepath.Join(dir, "config.json"), `{"upstream":true,"upstreamAddr":"http://127.0.0.1:18080"}`)
	cfg := DefaultConfig()
	cfg.Address, cfg.Port = "127.0.0.1", 0
	a, err := Build(cfg, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := a.Stop(); err != nil {
			t.Errorf("停止应用: %v", err)
		}
	})
	if a.Service == nil || a.Pipeline == nil || a.Plugins == nil || a.Logger == nil {
		t.Fatal("装配缺少核心组件")
	}
	if a.ConfigDir != dir || a.CertDir != filepath.Join(dir, "certificates") {
		t.Fatalf("装配路径错误: %q, %q", a.ConfigDir, a.CertDir)
	}
	if a.Engine.Listener().IsRunning() {
		t.Fatal("Build 应等待显式 Start 后监听")
	}
	if got := a.Engine.UpstreamProxyURL(); got == nil || got.String() != "http://127.0.0.1:18080" {
		t.Fatalf("保存的上游配置未应用: %v", got)
	}
	a.Service.UpdateConfig(map[string]any{"upstreamAddr": "http://127.0.0.1:18081"})
	if got := a.Engine.UpstreamProxyURL(); got == nil || got.String() != "http://127.0.0.1:18081" {
		t.Fatalf("运行时上游配置未应用: %v", got)
	}
	a.Service.UpdateConfig(map[string]any{"upstream": false})
	if got := a.Engine.UpstreamProxyURL(); got != nil {
		t.Fatalf("关闭上游后仍有代理: %v", got)
	}

	a.Service.CreateRule(&service.InterceptRule{
		Name: "启动装配阻断规则", Enabled: true,
		Conditions: []service.InterceptCondition{{Type: "url", Operator: "contains", Value: "/blocked"}},
		Actions:    []service.InterceptAction{{Type: "block", Enabled: true}},
	})
	for _, reload := range []bool{false, true} {
		if reload {
			if err := a.Plugins.LoadAll(); err != nil {
				t.Fatal(err)
			}
		}
		f := &flow.Flow{Request: &flow.Request{URL: "http://example.test/blocked"}}
		if got := a.Pipeline.OnRequest(t.Context(), f); got.Kind != flow.Abort {
			t.Fatalf("插件重载=%v 时核心规则未生效: %+v", reload, got)
		}
	}
	if err := a.Start(); err != nil {
		t.Fatal(err)
	}
	if !a.Engine.Listener().IsRunning() {
		t.Fatal("Start 后监听器未运行")
	}
	addr := a.Engine.Listener().GetAddress()
	assertBuiltAppCapturesHTTP(t, a, addr)
	if err := a.Start(); err == nil {
		t.Fatal("重复启动应返回错误")
	}
	if err := a.Stop(); err != nil {
		t.Fatal(err)
	}
	if a.Engine.Listener().IsRunning() {
		t.Fatal("Stop 后监听器仍在运行")
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("停止后端口未释放: %v", err)
	}
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
}

func assertBuiltAppCapturesHTTP(t *testing.T, a *App, addr string) {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "装配链路响应")
	}))
	t.Cleanup(upstream.Close)
	proxyURL, err := url.Parse("http://" + addr)
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL), DisableKeepAlives: true}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	events, unsubscribe := a.Engine.Bus().Subscribe()
	t.Cleanup(unsubscribe)
	resp, err := client.Get(upstream.URL + "/capture")
	if err != nil {
		t.Fatalf("经装配后的代理请求失败: %v", err)
	}
	body, readErr := io.ReadAll(resp.Body)
	closeErr := resp.Body.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("读取响应失败: %v, %v", readErr, closeErr)
	}
	if resp.StatusCode != http.StatusOK || string(body) != "装配链路响应" {
		t.Fatalf("代理响应 = %d %q", resp.StatusCode, body)
	}
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	started := false
	for {
		select {
		case event := <-events:
			if event.Type == core.EventFlowStarted {
				started = true
			}
			if event.Type != core.EventFlowCompleted {
				continue
			}
			if !started {
				t.Fatal("完成事件之前未收到开始事件")
			}
			sessions, total := a.Service.Sessions(1, 10)
			if total != 1 || len(sessions) != 1 {
				t.Fatalf("抓包会话数量 = %d/%d，期望 1", total, len(sessions))
			}
			session := sessions[0]
			if session.Request.URL != upstream.URL+"/capture" || session.Response == nil || session.Response.Status != http.StatusOK || session.Response.Body != string(body) {
				t.Fatalf("服务层会话与实际响应不一致: %+v", session)
			}
			return
		case <-deadline.C:
			t.Fatal("等待抓包完成事件超时")
		}
	}
}

func TestBuildDirectoryFailures(t *testing.T) {
	for _, tt := range []struct{ name, blocked, wantError string }{
		{"配置目录故障", ".", "创建配置目录失败"},
		{"证书目录故障", "certificates", "创建证书目录失败"},
		{"日志目录故障时启动", "logs", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if !inAppTestSubprocess(t) {
				if out, err := appTestSubprocess(t); err != nil {
					t.Fatalf("目录故障子进程失败: %v\n%s", err, out)
				}
				return
			}
			isolateAppDirs(t)
			preserveAppLogging(t)
			dir, err := platform.ConfigDir()
			if err != nil {
				t.Fatal(err)
			}
			if tt.blocked == "." {
				if err := os.Remove(dir); err != nil {
					t.Fatal(err)
				}
			}
			writeAppFixture(t, filepath.Join(dir, tt.blocked), "占用目录路径")
			a, err := Build(DefaultConfig(), false)
			if tt.wantError != "" {
				if a != nil {
					_ = a.Stop()
					t.Fatal("启动失败仍返回应用实例")
				}
				var pathErr *os.PathError
				if err == nil || !strings.Contains(err.Error(), tt.wantError) || !errors.As(err, &pathErr) {
					t.Fatalf("期望保留文件错误及 %q 上下文，得到 %v", tt.wantError, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("日志目录故障应允许启动: %v", err)
			}
			if err := a.Stop(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRestoreBreakRulesPersistence(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	newService := func() *service.Service { return service.New(nil, core.NewEventBus(), dir, "") }
	svc := newService()
	want := []service.BreakRuleSpec{
		{ID: "request", URL: "*/request", OnRequest: true, Enabled: true},
		{ID: "response", URL: "*/response", OnResponse: true, Enabled: true},
		{ID: "disabled", URL: "*", OnRequest: true, OnResponse: true},
	}
	if err := svc.SaveBreakRules(want); err != nil {
		t.Fatal(err)
	}
	pipe := pipeline.New(nil, nil)
	restoreBreakRules(pipe, newService(), NewLogger(false))
	bp := pipe.Breakpoints()
	assertRules := func(want []service.BreakRuleSpec) {
		t.Helper()
		got := make([]service.BreakRuleSpec, 0)
		for _, r := range bp.ListRules() {
			got = append(got, service.BreakRuleSpec{ID: r.ID, URL: r.URL, OnRequest: r.OnRequest, OnResponse: r.OnResponse, Enabled: r.Enabled})
		}
		if !slices.Equal(got, want) {
			t.Fatalf("管道断点规则 = %+v，期望 %+v", got, want)
		}
		disk := newService().BreakRules()
		if !slices.Equal(disk, want) {
			t.Fatalf("重启后断点规则 = %+v，期望 %+v", disk, want)
		}
	}
	assertRules(want)
	for _, tt := range []struct {
		url   string
		phase flow.Phase
		want  bool
	}{
		{"http://example.test/request", flow.PhaseRequest, true},
		{"http://example.test/request", flow.PhaseResponse, false},
		{"http://example.test/response", flow.PhaseResponse, true},
		{"http://example.test/other", flow.PhaseRequest, false},
	} {
		if got := bp.ShouldBreakFor(tt.url, tt.phase); got != tt.want {
			t.Errorf("ShouldBreakFor(%q, %v) = %v，期望 %v", tt.url, tt.phase, got, tt.want)
		}
	}
	added := bp.AddRule("*/added", true, true)
	want = append(want, service.BreakRuleSpec{ID: added.ID, URL: added.URL, OnRequest: true, OnResponse: true, Enabled: true})
	assertRules(want)
	if !bp.UpdateRule(added.ID, "*/updated", false, true, true) {
		t.Fatal("更新断点失败")
	}
	want[3].URL, want[3].OnRequest = "*/updated", false
	assertRules(want)
	if _, ok := bp.ToggleRule(added.ID, false); !ok {
		t.Fatal("禁用断点失败")
	}
	want[3].Enabled = false
	assertRules(want)
	bp.SetGlobalBreak(true, true)
	restarted := pipeline.New(nil, nil)
	restoreBreakRules(restarted, newService(), NewLogger(false))
	if req, resp := restarted.Breakpoints().GlobalBreak(); req || resp {
		t.Fatal("重启应将全局暂停开关复位")
	}
	for _, r := range want {
		if !bp.DeleteRule(r.ID) {
			t.Fatalf("删除断点 %q 失败", r.ID)
		}
	}
	assertRules([]service.BreakRuleSpec{})
}

func TestRestoreBreakRulesRetriesPersistence(t *testing.T) {
	preserveAppLogging(t)
	var logs bytes.Buffer
	log.SetOutput(&logs)
	dir := t.TempDir()
	svc := service.New(nil, core.NewEventBus(), dir, "")
	pipe := pipeline.New(nil, nil)
	restoreBreakRules(pipe, svc, NewLogger(false))
	blocked := filepath.Join(dir, "breakpoints.json.tmp")
	if err := os.Mkdir(blocked, 0o700); err != nil {
		t.Fatal(err)
	}
	rule := pipe.Breakpoints().AddRule("*/retry", true, false)
	if !strings.Contains(logs.String(), "保存断点规则失败") {
		t.Fatalf("持久化失败未记录诊断信息: %q", logs.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "breakpoints.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("持久化失败仍发布了文件: %v", err)
	}
	if !pipe.Breakpoints().ShouldBreakFor("http://example.test/retry", flow.PhaseRequest) {
		t.Fatal("磁盘故障后内存规则应继续生效")
	}
	if err := os.Remove(blocked); err != nil {
		t.Fatal(err)
	}
	if !pipe.Breakpoints().UpdateRule(rule.ID, "*/recovered", false, true, true) {
		t.Fatal("更新恢复后的规则失败")
	}
	reloaded := service.New(nil, core.NewEventBus(), dir, "").BreakRules()
	want := []service.BreakRuleSpec{{ID: rule.ID, URL: "*/recovered", OnResponse: true, Enabled: true}}
	if !slices.Equal(reloaded, want) {
		t.Fatalf("故障恢复后规则未完整落盘: %+v", reloaded)
	}
}

func TestBuildRejectsCorruptCA(t *testing.T) {
	if !inAppTestSubprocess(t) {
		if out, err := appTestSubprocess(t); err != nil {
			t.Fatalf("证书启动校验子进程失败: %v\n%s", err, out)
		}
		return
	}
	isolateAppDirs(t)
	preserveAppLogging(t)
	dir, err := platform.CertificatesDir()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"sniffy-ca.crt", "sniffy-ca.key"} {
		writeAppFixture(t, filepath.Join(dir, name), "损坏的证书材料")
	}
	a, err := Build(DefaultConfig(), false)
	if a != nil {
		_ = a.Stop()
		t.Fatal("证书加载失败仍创建了应用")
	}
	if err == nil || !strings.Contains(err.Error(), "加载根 CA 失败") {
		t.Fatalf("证书启动失败缺少上下文: %v", err)
	}
	for _, name := range []string{"sniffy-ca.crt", "sniffy-ca.key"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || string(data) != "损坏的证书材料" {
			t.Fatalf("启动失败后证书文件被改写: %q, %v", data, err)
		}
	}
}
