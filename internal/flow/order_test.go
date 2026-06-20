// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package flow

import (
	"net/http"
	"reflect"
	"testing"
)

// applyOrdered 跑一遍 ApplyRequestToHTTP 并取出最终线缆头序列,供断言。
func applyOrdered(t *testing.T, r *Request) [][2]string {
	t.Helper()
	f := New(ProtoHTTPS)
	f.Request = r
	req, _ := http.NewRequest(r.Method, r.URL, nil)
	req = ApplyRequestToHTTP(f, req)
	ordered, ok := OrderedHeadersFrom(req.Context())
	if !ok {
		t.Fatalf("期望产出保真头序列, 实得无")
	}
	return ordered
}

// TestOrderedUnmodifiedVerbatim 未改动请求:输出与原始线上顺序/大小写逐字一致。
func TestOrderedUnmodifiedVerbatim(t *testing.T) {
	r := &Request{
		Method: http.MethodGet,
		URL:    "https://h.example/p?z=1&a=2",
		Host:   "h.example",
		Header: map[string][]string{ // 规范化视图(插件/UI 用)
			"User-Agent":      {"App/1"},
			"Accept":          {"*/*"},
			"X-Custom-Token":  {"ABC"},
			"Accept-Encoding": {"gzip, br"},
		},
		RawHeaders: [][2]string{ // 原始线上序列(顺序+大小写)
			{"Host", "h.example"},
			{"User-Agent", "App/1"},
			{"Accept", "*/*"},
			{"x-custom-token", "ABC"},
			{"accept-encoding", "gzip, br"},
		},
	}
	got := applyOrdered(t, r)
	want := [][2]string{
		{"Host", "h.example"},
		{"User-Agent", "App/1"},
		{"Accept", "*/*"},
		{"x-custom-token", "ABC"},
		{"accept-encoding", "gzip, br"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("未改动应逐字一致:\n got=%v\nwant=%v", got, want)
	}
}

// TestOrderedModifiedValueKeepsPosition 改了某头的值:位置/大小写不变,仅值更新。
func TestOrderedModifiedValueKeepsPosition(t *testing.T) {
	r := &Request{
		Method: http.MethodGet,
		URL:    "https://h/",
		Host:   "h",
		Header: map[string][]string{
			"User-Agent": {"App/1"},
			"Cookie":     {"sid=NEW"}, // 被插件改过
		},
		RawHeaders: [][2]string{
			{"Host", "h"},
			{"User-Agent", "App/1"},
			{"cookie", "sid=OLD"},
		},
	}
	got := applyOrdered(t, r)
	want := [][2]string{
		{"Host", "h"},
		{"User-Agent", "App/1"},
		{"cookie", "sid=NEW"}, // 原始大小写+原始位置,新值
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("改值应保位保大小写:\n got=%v\nwant=%v", got, want)
	}
}

// TestOrderedDeletedHeaderSkipped 删掉的头从输出消失;新增头追加在尾部。
func TestOrderedDeletedAndAdded(t *testing.T) {
	r := &Request{
		Method: http.MethodGet,
		URL:    "https://h/",
		Host:   "h",
		Header: map[string][]string{
			"User-Agent":   {"App/1"},
			"X-Plugin-Add": {"yes"}, // 新增
			// 原始里的 Accept 被删除(此 map 不含 Accept)
		},
		RawHeaders: [][2]string{
			{"Host", "h"},
			{"User-Agent", "App/1"},
			{"Accept", "*/*"},
		},
	}
	got := applyOrdered(t, r)
	want := [][2]string{
		{"Host", "h"},
		{"User-Agent", "App/1"},
		{"X-Plugin-Add", "yes"}, // 追加在尾部(规范名)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("删+增不符:\n got=%v\nwant=%v", got, want)
	}
}

// TestOrderedNoContentLengthForBodylessGET 无 body 的 GET 不应凭空带 Content-Length。
func TestOrderedNoContentLengthForBodylessGET(t *testing.T) {
	r := &Request{
		Method:     http.MethodGet,
		URL:        "https://h/",
		Host:       "h",
		Header:     map[string][]string{"User-Agent": {"x"}},
		RawHeaders: [][2]string{{"Host", "h"}, {"User-Agent", "x"}},
	}
	got := applyOrdered(t, r)
	for _, kv := range got {
		if kv[0] == "Content-Length" {
			t.Fatalf("无 body 的 GET 不应有 Content-Length, 实得 %v", got)
		}
	}
}

// TestOrderedContentLengthAtOriginalPosition POST 带 body:CL 在客户端原始位置、值被校正。
func TestOrderedContentLengthAtOriginalPosition(t *testing.T) {
	r := &Request{
		Method: http.MethodPost,
		URL:    "https://h/submit",
		Host:   "h",
		Header: map[string][]string{
			"Content-Type":   {"text/plain"},
			"Content-Length": {"3"},
		},
		Body: []byte("hello"), // 实际 5 字节,CL 应被校正为 5
		RawHeaders: [][2]string{
			{"Host", "h"},
			{"content-length", "3"},
			{"Content-Type", "text/plain"},
		},
	}
	got := applyOrdered(t, r)
	want := [][2]string{
		{"Host", "h"},
		{"content-length", "5"}, // 原位置+原大小写,值校正为 5
		{"Content-Type", "text/plain"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CL 校正/保位不符:\n got=%v\nwant=%v", got, want)
	}
}

// TestUASentinelOnFallbackPath 客户端无 UA 时,回退路径头里植入空值哨兵以抑制 net/http 注入。
func TestUASentinelOnFallbackPath(t *testing.T) {
	f := New(ProtoHTTPS)
	f.Request = &Request{
		Method:     http.MethodGet,
		URL:        "https://h/",
		Host:       "h",
		Header:     map[string][]string{"Accept": {"*/*"}},
		RawHeaders: [][2]string{{"Host", "h"}, {"Accept", "*/*"}}, // 无 UA
	}
	req, _ := http.NewRequest(http.MethodGet, "https://h/", nil)
	req = ApplyRequestToHTTP(f, req)
	v, ok := req.Header["User-Agent"]
	if !ok || len(v) != 1 || v[0] != "" {
		t.Fatalf("期望植入 UA 空值哨兵以抑制注入, 实得 %v (ok=%v)", v, ok)
	}
	// 且哨兵不得污染保真头序列。
	ordered, _ := OrderedHeadersFrom(req.Context())
	for _, kv := range ordered {
		if kv[0] == "User-Agent" {
			t.Fatalf("保真头序列不应含 UA, 实得 %v", ordered)
		}
	}
}

// TestNoRawHeadersNoOrdered 没有原始头序列(如 h2 入站)时不产出保真序列,转发器据此回退。
func TestNoRawHeadersNoOrdered(t *testing.T) {
	f := New(ProtoHTTPS)
	f.Request = &Request{
		Method: http.MethodGet,
		URL:    "https://h/",
		Host:   "h",
		Header: map[string][]string{"User-Agent": {"x"}},
	}
	req, _ := http.NewRequest(http.MethodGet, "https://h/", nil)
	req = ApplyRequestToHTTP(f, req)
	if _, ok := OrderedHeadersFrom(req.Context()); ok {
		t.Fatalf("无 RawHeaders 不应产出保真头序列")
	}
}
