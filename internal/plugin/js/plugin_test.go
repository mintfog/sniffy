// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package js

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mintfog/sniffy/internal/flow"
)

func newReqFlow() *flow.Flow {
	return &flow.Flow{
		ID: "f1",
		Request: &flow.Request{
			Method: "GET",
			URL:    "http://example.com/api/x",
			Host:   "example.com",
			Path:   "/api/x",
			Header: map[string][]string{
				"Cookie":          {"a=1", "b=2"},
				"X-Forwarded-For": {"1.1.1.1", "2.2.2.2"},
				"X-Single":        {"v"},
			},
			Body: []byte("hello"),
		},
	}
}

func mustPlugin(t *testing.T, cfg Config) *Plugin {
	t.Helper()
	cfg.Enabled = true
	p, err := NewPlugin(cfg, nil)
	if err != nil {
		t.Fatalf("NewPlugin: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}

// 核心回归:无操作的 onRequest 绝不能塌缩多值头(无侵入转发保证)。
func TestNoopPreservesMultiValueHeaders(t *testing.T) {
	p := mustPlugin(t, Config{ID: "noop", Source: "function onRequest(f){}"})
	f := newReqFlow()
	d := p.OnRequest(context.Background(), f)
	if d.Kind != flow.Continue {
		t.Fatalf("decision = %v, want Continue", d.Kind)
	}
	if f.Modified {
		t.Fatal("no-op plugin must not mark flow Modified")
	}
	if got := f.Request.Header["Cookie"]; len(got) != 2 || got[0] != "a=1" || got[1] != "b=2" {
		t.Fatalf("Cookie collapsed: %v", got)
	}
	if got := f.Request.Header["X-Forwarded-For"]; len(got) != 2 {
		t.Fatalf("X-Forwarded-For collapsed: %v", got)
	}
}

// 改一个头时:被改的头单值写入,其它多值头保持原样。
func TestSetHeaderPreservesOthers(t *testing.T) {
	p := mustPlugin(t, Config{ID: "seth", Source: "function onRequest(f){ header.set(f.headers,'X-New','1'); }"})
	f := newReqFlow()
	d := p.OnRequest(context.Background(), f)
	if d.Kind != flow.Continue || !f.Modified {
		t.Fatalf("want Continue+Modified, got %v modified=%v", d.Kind, f.Modified)
	}
	if got := f.Request.Header["X-New"]; len(got) != 1 || got[0] != "1" {
		t.Fatalf("X-New = %v", got)
	}
	if got := f.Request.Header["Cookie"]; len(got) != 2 {
		t.Fatalf("Cookie should remain multi-value: %v", got)
	}
}

// settings 必须是 VM 独占的深拷贝:脚本写入不得污染传入的 manifest map。
func TestSettingsIsolatedFromManifest(t *testing.T) {
	orig := map[string]any{"k": "v"}
	p := mustPlugin(t, Config{ID: "set", Settings: orig, Source: "function onRequest(f){ settings.k='changed'; settings.n=9; }"})
	p.OnRequest(context.Background(), newReqFlow())
	if orig["k"] != "v" {
		t.Fatalf("manifest settings mutated: %v", orig)
	}
	if _, ok := orig["n"]; ok {
		t.Fatal("script leaked new key into manifest settings")
	}
}

// store 须跨实例(热重载/重启)存活:落盘后新实例可读回。
func TestStorePersistsAcrossInstances(t *testing.T) {
	sp := filepath.Join(t.TempDir(), "state.json")
	p1 := mustPlugin(t, Config{ID: "st", StatePath: sp,
		Source: "function onRequest(f){ store.set('n', (store.get('n')||0)+1); }"})
	p1.OnRequest(context.Background(), newReqFlow())
	p1.Close() // 同步落盘

	p2 := mustPlugin(t, Config{ID: "st", StatePath: sp, Source: "function onResponse(f){}"})
	if got := fmt.Sprint(p2.Snapshot()["n"]); got != "1" {
		t.Fatalf("store not persisted: n=%q", got)
	}
}

// InitialStore(热重载迁移)优先于磁盘。
func TestInitialStoreMigration(t *testing.T) {
	p := mustPlugin(t, Config{ID: "mig", InitialStore: map[string]any{"carried": "yes"}, Source: "function onRequest(f){}"})
	if got := fmt.Sprint(p.Snapshot()["carried"]); got != "yes" {
		t.Fatalf("InitialStore not applied: %q", got)
	}
}

func TestHelpers(t *testing.T) {
	src := `function onRequest(f){
	  header.set(f.headers,'X-B64', base64.encode('hi'));
	  header.set(f.headers,'X-Hex', hex.encode('hi'));
	  header.set(f.headers,'X-Dec', base64.decode('aGk='));
	  header.set(f.headers,'X-Uuid', uuid());
	  var u = url.parse(f.url);
	  header.set(f.headers,'X-Path', u.path);
	}`
	p := mustPlugin(t, Config{ID: "help", Source: src})
	f := newReqFlow()
	p.OnRequest(context.Background(), f)
	check := func(k, want string) {
		if got := f.Request.Header[k]; len(got) != 1 || got[0] != want {
			t.Fatalf("%s = %v, want %q", k, got, want)
		}
	}
	check("X-B64", "aGk=")
	check("X-Hex", "6869")
	check("X-Dec", "hi")
	check("X-Path", "/api/x")
	if got := f.Request.Header["X-Uuid"]; len(got) != 1 || len(got[0]) != 36 {
		t.Fatalf("uuid malformed: %v", got)
	}
}

// console.log 对对象应输出 JSON,而非 [object Object]。
func TestConsoleLogFormatsObjects(t *testing.T) {
	p := mustPlugin(t, Config{ID: "log", Source: "function onRequest(f){ console.log('o', {a:1}); }"})
	p.OnRequest(context.Background(), newReqFlow())
	var found bool
	for _, e := range p.Logs() {
		if strings.Contains(e.Msg, `{"a":1}`) {
			found = true
		}
	}
	if !found {
		t.Fatalf("object not JSON-formatted in logs: %+v", p.Logs())
	}
}

// 在不支持的阶段调用 mock() 应给出告警而非静默失效。
func TestMockGuardedToRequestPhase(t *testing.T) {
	f := newReqFlow()
	f.Response = &flow.Response{Status: 200, Header: map[string][]string{}}
	p := mustPlugin(t, Config{ID: "mk", Source: "function onResponse(f){ mock({status:201}); }"})
	d := p.OnResponse(context.Background(), f)
	if d.Kind == flow.Mock {
		t.Fatal("response-phase mock() must not produce a Mock decision")
	}
	var warned bool
	for _, e := range p.Logs() {
		if e.Level == "warn" && strings.Contains(e.Msg, "mock()") {
			warned = true
		}
	}
	if !warned {
		t.Fatalf("expected warn log for misused mock(); logs=%+v", p.Logs())
	}
}

// 请求阶段 mock() 应短路并落地 response。
func TestMockInRequestPhase(t *testing.T) {
	p := mustPlugin(t, Config{ID: "mk2", Source: "function onRequest(f){ mock({status:418, body:'teapot'}); }"})
	f := newReqFlow()
	d := p.OnRequest(context.Background(), f)
	if d.Kind != flow.Mock {
		t.Fatalf("decision = %v, want Mock", d.Kind)
	}
	if f.Response == nil || f.Response.Status != 418 || string(f.Response.Body) != "teapot" {
		t.Fatalf("mock response not applied: %+v", f.Response)
	}
}

// 响应阶段无操作不得整体替换 Response(保住 RawHeaders/Trailer 等保真信息)。
func TestNoopResponsePreservesStruct(t *testing.T) {
	f := newReqFlow()
	resp := &flow.Response{
		Status:     200,
		Header:     map[string][]string{"Set-Cookie": {"x=1", "y=2"}},
		Body:       []byte("body"),
		RawHeaders: [][2]string{{"Set-Cookie", "x=1"}, {"Set-Cookie", "y=2"}},
	}
	f.Response = resp
	p := mustPlugin(t, Config{ID: "rno", Source: "function onResponse(f){}"})
	p.OnResponse(context.Background(), f)
	if f.Response != resp {
		t.Fatal("no-op response plugin replaced the Response struct")
	}
	if len(f.Response.RawHeaders) != 2 {
		t.Fatalf("RawHeaders lost: %v", f.Response.RawHeaders)
	}
	if got := f.Response.Header["Set-Cookie"]; len(got) != 2 {
		t.Fatalf("Set-Cookie collapsed: %v", got)
	}
}
