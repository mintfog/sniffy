// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package service

import (
	"strings"
	"testing"

	"github.com/mintfog/sniffy/internal/flow"
)

func bizFlow(host, body string) *flow.Flow {
	return &flow.Flow{
		ID:       "f1",
		Request:  &flow.Request{Host: host, URL: "https://" + host + "/"},
		Response: &flow.Response{Status: 200, Body: []byte(body)},
	}
}

func TestBizExtract(t *testing.T) {
	m := newBizMatcher(normalizeBizStatusRules([]BizStatusRule{
		{Host: "*.example.com", Fields: []string{"code", "errcode"}, Success: []string{"0", "200"}, MessageField: "msg"},
		{Host: "10.0.3.21:8080", Fields: []string{"status"}, Success: []string{"OK"}},
		{Host: "show.only", Fields: []string{"ok"}},
	}))
	cases := []struct {
		name, host, body string
		want             *flow.BizStatus
	}{
		{"成功值", "api.example.com", `{"code":0,"msg":"ok","data":{}}`, &flow.BizStatus{Code: "0", Message: "ok"}},
		{"裸域与端口", "EXAMPLE.com:443", `{"code":200}`, &flow.BizStatus{Code: "200"}},
		{"失败值与消息", "api.example.com", ` {"data":{"code":0,"a":[1,"}"]},"msg":"余额不足","code":40013}`, &flow.BizStatus{Code: "40013", Message: "余额不足", Failed: true}},
		{"候选字段回退", "api.example.com", `{"errcode":"E1"}`, &flow.BizStatus{Code: "E1", Failed: true}},
		{"首选字段优先于出现顺序", "api.example.com", `{"errcode":7,"code":0}`, &flow.BizStatus{Code: "0"}},
		{"非标量视为缺失", "api.example.com", `{"code":{"v":1},"errcode":5}`, &flow.BizStatus{Code: "5", Failed: true}},
		{"大整数保留字面量", "api.example.com", `{"code":12345678901234567890}`, &flow.BizStatus{Code: "12345678901234567890", Failed: true}},
		{"转义键与 BOM", "api.example.com", "\xef\xbb\xbf" + `{"c\u006fde":"0"}`, &flow.BizStatus{Code: "0"}},
		{"带端口规则命中", "10.0.3.21:8080", `{"status":"FAIL"}`, &flow.BizStatus{Code: "FAIL", Failed: true}},
		{"带端口规则不匹配其他端口", "10.0.3.21:9090", `{"status":"FAIL"}`, nil},
		{"无成功值只展示", "show.only", `{"ok":false}`, &flow.BizStatus{Code: "false"}},
		{"未命中主机", "other.com", `{"code":1}`, nil},
		{"字段不存在", "api.example.com", `{"data":1}`, nil},
		{"顶级是数组", "api.example.com", `[{"code":1}]`, nil},
		{"非 JSON", "api.example.com", `<html>code</html>`, nil},
		{"残缺 JSON 仍取已读到的字段", "api.example.com", `{"code":500,"data":[1,2`, &flow.BizStatus{Code: "500", Failed: true}},
		{"空体", "api.example.com", ``, nil},
	}
	for _, c := range cases {
		got := m.extract(bizFlow(c.host, c.body))
		if (got == nil) != (c.want == nil) || (got != nil && *got != *c.want) {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestBizExtractWithoutRules(t *testing.T) {
	var m *bizMatcher
	if got := m.extract(bizFlow("a.com", `{"code":1}`)); got != nil {
		t.Fatalf("got %+v", got)
	}
	if newBizMatcher(nil) != nil {
		t.Fatal("空规则应得到 nil matcher")
	}
}

func TestBizRuleWithDefaultPort(t *testing.T) {
	m := newBizMatcher([]BizStatusRule{{Host: "api.example.com:443", Fields: []string{"code"}}})
	f := bizFlow("api.example.com", `{"code":0}`)
	if got := m.extract(f); got == nil || got.Code != "0" {
		t.Fatalf("Host 头省略默认端口时应命中 :443 规则, got %+v", got)
	}
	f.Request.URL = "http://api.example.com/"
	if got := m.extract(f); got != nil {
		t.Fatalf("http 请求不应命中 :443 规则, got %+v", got)
	}
}

func TestBizTruncatesLongValues(t *testing.T) {
	m := newBizMatcher([]BizStatusRule{{Host: "a.com", Fields: []string{"code"}}})
	got := m.extract(bizFlow("a.com", `{"code":"`+strings.Repeat("码", 100)+`"}`))
	if want := strings.Repeat("码", maxBizCodeRunes) + "…"; got == nil || got.Code != want {
		t.Fatalf("got %+v", got)
	}
}

func TestNormalizeBizStatusRules(t *testing.T) {
	got := normalizeBizStatusRules([]BizStatusRule{
		{Host: " API.Example.com ", Fields: []string{" code ", "", "code"}, Success: []string{" 0 "}, MessageField: " msg "},
		{Host: "", Fields: []string{"code"}},
		{Host: "b.com", Fields: []string{" "}},
		{Host: "https://", Fields: []string{"code"}},
	})
	if len(got) != 1 {
		t.Fatalf("got %+v", got)
	}
	r := got[0]
	if r.Host != "api.example.com" || len(r.Fields) != 1 || r.Fields[0] != "code" || r.Success[0] != "0" || r.MessageField != "msg" {
		t.Fatalf("got %+v", r)
	}
}

func TestBizStatusRulesConfigRoundTrip(t *testing.T) {
	svc := New(nil, nil, t.TempDir(), "")
	f := bizFlow("api.example.com", `{"code":1}`)
	svc.RecordFlowCompleted(f)
	if f.Biz != nil {
		t.Fatalf("未配置规则时不应提取: %+v", f.Biz)
	}

	c := svc.UpdateConfig(map[string]any{"bizStatusRules": []any{
		map[string]any{"host": "*.example.com", "fields": []any{"code"}, "success": []any{"0"}},
	}})
	if len(c.BizStatusRules) != 1 || len(PublicConfig(c).BizStatusRules) != 1 {
		t.Fatalf("got %+v", c.BizStatusRules)
	}
	svc.RecordFlowCompleted(f)
	if f.Biz == nil || f.Biz.Code != "1" || !f.Biz.Failed {
		t.Fatalf("got %+v", f.Biz)
	}
	if dto, ok := svc.Session(f.ID); !ok || dto.Biz == nil || dto.Biz.Code != "1" {
		t.Fatalf("会话 DTO 缺少业务状态: %+v", dto.Biz)
	}

	// 无关补丁不清空规则;空列表才清空。
	if c = svc.UpdateConfig(map[string]any{"recording": true}); len(c.BizStatusRules) != 1 {
		t.Fatalf("got %+v", c.BizStatusRules)
	}
	if c = svc.UpdateConfig(map[string]any{"bizStatusRules": []any{}}); len(c.BizStatusRules) != 0 {
		t.Fatalf("got %+v", c.BizStatusRules)
	}
	svc.RecordFlowCompleted(f)
	if f.Biz != nil {
		t.Fatalf("清空规则后不应提取: %+v", f.Biz)
	}
}

func BenchmarkBizExtract(b *testing.B) {
	rules := []BizStatusRule{
		{Host: "*.internal.test", Fields: []string{"ret"}},
		{Host: "10.0.*", Fields: []string{"status"}},
		{Host: "*.example.com", Fields: []string{"code", "errcode"}, Success: []string{"0"}, MessageField: "msg"},
	}
	small := `{"code":0,"msg":"ok","data":{"id":1,"name":"sniffy","tags":["a","b"]}}`
	// 业务码排在大 data 之后是最坏情况:整个响应体都要扫一遍。
	tail := `{"data":[` + strings.TrimSuffix(strings.Repeat(`{"id":123456,"name":"some item","price":"12.50"},`, 2000), ",") + `],"msg":"ok","code":0}`
	cases := []struct {
		name string
		m    *bizMatcher
		f    *flow.Flow
	}{
		{"未配置规则", nil, bizFlow("api.example.com", small)},
		{"未命中主机", newBizMatcher(rules), bizFlow("cdn.other.com", small)},
		{"命中_小响应", newBizMatcher(rules), bizFlow("api.example.com", small)},
		{"命中_100KB_字段在末尾", newBizMatcher(rules), bizFlow("api.example.com", tail)},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			b.SetBytes(int64(len(c.f.Response.Body)))
			b.ReportAllocs()
			for b.Loop() {
				c.m.extract(c.f)
			}
		})
	}
}

func TestBizRuleHostFromURL(t *testing.T) {
	m := newBizMatcher(normalizeBizStatusRules([]BizStatusRule{
		{Host: "HTTPS://Api.Example.com/v1/users?id=1", Fields: []string{"code"}},
		{Host: "http://10.0.3.21:8080/", Fields: []string{"ret"}},
	}))
	if got := m.extract(bizFlow("api.example.com", `{"code":0}`)); got == nil || got.Code != "0" {
		t.Fatalf("带协议与路径的主机应按域名命中, got %+v", got)
	}
	f := bizFlow("10.0.3.21:8080", `{"ret":7}`)
	if got := m.extract(f); got == nil || got.Code != "7" {
		t.Fatalf("带协议的 host:port 应保留端口匹配, got %+v", got)
	}
}
