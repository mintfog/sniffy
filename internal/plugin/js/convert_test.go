// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package js

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/mintfog/sniffy/internal/flow"
)

// convOutJSON 生成 VM 回传报文，并返回未修改的视图作为比较基准。
func convOutJSON(t *testing.T, f *flow.Flow, mutate func(*jsFlow), d jsDecision) (*jsFlow, []byte) {
	t.Helper()
	sent := requestToJS(f)
	jf := requestToJS(f)
	if mutate != nil {
		mutate(&jf)
	}
	b, err := json.Marshal(jsOut{Flow: jf, Decision: d})
	if err != nil {
		t.Fatalf("序列化 jsOut: %v", err)
	}
	return &sent, b
}

// convSameBacking 判断两个切片是否共享底层数组。
func convSameBacking(a, b []string) bool {
	return len(a) > 0 && len(b) == len(a) && &a[0] == &b[0]
}

// convRespFlow 造一条带保真字段(RawHeaders / Trailer)的已完成流。
func convRespFlow() (*flow.Flow, *flow.Response) {
	f := newReqFlow()
	resp := &flow.Response{
		Status:     200,
		StatusText: "OK",
		Header: map[string][]string{
			"Set-Cookie":   {"x=1", "y=2"},
			"Content-Type": {"application/json"},
		},
		Body:       []byte(`{"a":1}`),
		Trailer:    map[string][]string{"Grpc-Status": {"0"}},
		RawHeaders: [][2]string{{"Set-Cookie", "x=1"}, {"Set-Cookie", "y=2"}},
	}
	f.Response = resp
	return f, resp
}

// convLogSink 收集 applyHTTP 产生的插件日志。
func convLogSink() (*[]string, func(level, msg string)) {
	var got []string
	return &got, func(level, msg string) { got = append(got, level+": "+msg) }
}

// convHasLog 判断日志里是否有一条指定级别且包含 needle 的记录。
func convHasLog(entries []string, level, needle string) bool {
	for _, e := range entries {
		if strings.HasPrefix(e, level+": ") && strings.Contains(e, needle) {
			return true
		}
	}
	return false
}

// 验证各可编辑字段的回写与 Modified 标记。
func TestConvertApplyHTTPPerFieldModified(t *testing.T) {
	cases := []struct {
		name         string
		mutate       func(*jsFlow)
		wantModified bool
		check        func(*testing.T, *flow.Flow)
	}{
		{"method", func(jf *jsFlow) { jf.Method = "POST" }, true, func(t *testing.T, f *flow.Flow) {
			if f.Request.Method != "POST" {
				t.Errorf("Method = %q, 期望 POST", f.Request.Method)
			}
		}},
		{"url", func(jf *jsFlow) { jf.URL = "http://example.com/api/y" }, true, func(t *testing.T, f *flow.Flow) {
			if f.Request.URL != "http://example.com/api/y" {
				t.Errorf("URL = %q", f.Request.URL)
			}
		}},
		{"host", func(jf *jsFlow) { jf.Host = "other.test" }, true, func(t *testing.T, f *flow.Flow) {
			if f.Request.Host != "other.test" {
				t.Errorf("Host = %q", f.Request.Host)
			}
		}},
		{"path", func(jf *jsFlow) { jf.Path = "/api/y" }, true, func(t *testing.T, f *flow.Flow) {
			if f.Request.Path != "/api/y" {
				t.Errorf("Path = %q", f.Request.Path)
			}
		}},
		{"header-add", func(jf *jsFlow) { jf.Headers["X-New"] = "1" }, true, func(t *testing.T, f *flow.Flow) {
			if got := f.Request.Header["X-New"]; len(got) != 1 || got[0] != "1" {
				t.Errorf("X-New = %v", got)
			}
		}},
		{"header-first-value", func(jf *jsFlow) { jf.Headers["Cookie"] = "z=9" }, true, func(t *testing.T, f *flow.Flow) {
			if got := f.Request.Header["Cookie"]; len(got) != 1 || got[0] != "z=9" {
				t.Errorf("Cookie = %v, 期望仅剩 [z=9]", got)
			}
			if got := f.Request.Header["X-Forwarded-For"]; len(got) != 2 {
				t.Errorf("X-Forwarded-For 被塌缩: %v", got)
			}
		}},
		{"body", func(jf *jsFlow) { jf.Body = "bye" }, true, func(t *testing.T, f *flow.Flow) {
			if string(f.Request.Body) != "bye" {
				t.Errorf("Body = %q", f.Request.Body)
			}
		}},
		{"body-emptied", func(jf *jsFlow) { jf.Body = "" }, true, func(t *testing.T, f *flow.Flow) {
			if len(f.Request.Body) != 0 {
				t.Errorf("Body = %q, 期望为空", f.Request.Body)
			}
		}},
		{"untouched", nil, false, func(t *testing.T, f *flow.Flow) {
			if string(f.Request.Body) != "hello" || f.Request.Method != "GET" {
				t.Errorf("未被脚本触及的 flow 被改动: %+v", f.Request)
			}
			if got := f.Request.Header["Cookie"]; len(got) != 2 {
				t.Errorf("Cookie 被塌缩: %v", got)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newReqFlow()
			sent, out := convOutJSON(t, f, c.mutate, jsDecision{Kind: "continue"})
			d := applyHTTP(f, sent, out, flow.PhaseRequest, nil)
			if d.Kind != flow.Continue {
				t.Fatalf("处置 = %v, 期望 Continue", d.Kind)
			}
			if f.Modified != c.wantModified {
				t.Errorf("Modified = %v, 期望 %v", f.Modified, c.wantModified)
			}
			c.check(t, f)
		})
	}
}

// 验证 host/path 的空值语义。
func TestConvertBlankHostPathTreatedAsUnchanged(t *testing.T) {
	p := mustPlugin(t, Config{ID: "conv-blank", Source: "function onRequest(f){ f.host=''; delete f.path; }"})
	f := newReqFlow()
	p.OnRequest(context.Background(), f)
	if f.Request.Host != "example.com" || f.Request.Path != "/api/x" {
		t.Fatalf("空串覆盖了原值: host=%q path=%q", f.Request.Host, f.Request.Path)
	}
	if f.Modified {
		t.Fatal("置空 host/path 不得把 flow 标记为 Modified")
	}
}

// 验证 method/url 的空值语义。
func TestConvertBlankMethodURLTreatedAsUnchanged(t *testing.T) {
	for _, c := range []struct{ name, src string }{
		{"delete", "function onRequest(f){ delete f.method; delete f.url; }"},
		{"空串", "function onRequest(f){ f.method=''; f.url=''; }"},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := mustPlugin(t, Config{ID: "conv-blank-mu", Source: c.src})
			f := newReqFlow()
			p.OnRequest(context.Background(), f)
			if f.Request.Method != "GET" || f.Request.URL != "http://example.com/api/x" {
				t.Fatalf("空串覆盖了原值: method=%q url=%q", f.Request.Method, f.Request.URL)
			}
			if f.Modified {
				t.Fatal("置空 method/url 不得把 flow 标记为 Modified")
			}
		})
	}
}

// 验证非空 method/url 改写。
func TestConvertMethodURLRewriteStillApplies(t *testing.T) {
	p := mustPlugin(t, Config{ID: "conv-mu", Source: "function onRequest(f){ f.method='POST'; f.url='http://y/z'; }"})
	f := newReqFlow()
	p.OnRequest(context.Background(), f)
	if f.Request.Method != "POST" || f.Request.URL != "http://y/z" {
		t.Fatalf("改写未生效: method=%q url=%q", f.Request.Method, f.Request.URL)
	}
	if !f.Modified {
		t.Fatal("改写 method/url 必须置 Modified")
	}
}

// convAllBytes 生成包含 0x00-0xFF 的字节载荷。
func convAllBytes() []byte {
	b := make([]byte, 256)
	for i := range b {
		b[i] = byte(i)
	}
	return b
}

// 验证文本与 bodyB64 通道的字节保真及 Modified 判定。
func TestConvertBodyBytesSurviveRoundTrip(t *testing.T) {
	cases := []struct {
		name    string
		payload []byte
	}{
		{"utf8-含 NUL 与控制字符与多字节", []byte("h\x00\x01\x7f\"\\\né中\U0001F600")},
		{"gzip 头", []byte{0x1f, 0x8b, 0x08, 0x00}},
		{"孤立的非法字节", []byte{0xff, 0xfe, 0x80}},
		{"png 魔数", []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}},
		{"0x00-0xff 全字节", convAllBytes()},
	}
	for _, c := range cases {
		t.Run("请求体/"+c.name, func(t *testing.T) {
			p := mustPlugin(t, Config{ID: "conv-rt-req", Source: "function onRequest(f){}"})
			f := newReqFlow()
			f.Request.Body = append([]byte(nil), c.payload...)
			p.OnRequest(context.Background(), f)
			if !bytes.Equal(f.Request.Body, c.payload) {
				t.Fatalf("请求体被破坏: %x, 期望 %x", f.Request.Body, c.payload)
			}
			if f.Modified {
				t.Fatal("逐字节相同的往返不得把 flow 标记为 Modified")
			}
		})
		t.Run("响应体/"+c.name, func(t *testing.T) {
			p := mustPlugin(t, Config{ID: "conv-rt-resp", Source: "function onResponse(f){}"})
			f, resp := convRespFlow()
			resp.Body = append([]byte(nil), c.payload...)
			p.OnResponse(context.Background(), f)
			if !bytes.Equal(resp.Body, c.payload) {
				t.Fatalf("响应体被破坏: %x, 期望 %x", resp.Body, c.payload)
			}
			if f.Modified {
				t.Fatal("逐字节相同的往返不得把 flow 标记为 Modified")
			}
		})
	}
}

// 验证响应状态字段的空值语义。
func TestConvertResponseStatusGuards(t *testing.T) {
	cases := map[string]string{
		"delete-status":     "function onResponse(f){ delete f.response.status; }",
		"zero-status":       "function onResponse(f){ f.response.status = 0; }",
		"blank-status-text": "function onResponse(f){ f.response.statusText = ''; }",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			f, resp := convRespFlow()
			p := mustPlugin(t, Config{ID: "conv-st-" + name, Source: src})
			p.OnResponse(context.Background(), f)
			if resp.Status != 200 || resp.StatusText != "OK" {
				t.Fatalf("状态行被置空: %d %q", resp.Status, resp.StatusText)
			}
			if f.Modified {
				t.Fatal("置空 status/statusText 不得把 flow 标记为 Modified")
			}
		})
	}
}

// 验证响应字段就地应用并保留其他字段。
func TestConvertResponseFieldsAppliedInPlace(t *testing.T) {
	cases := []struct {
		name  string
		src   string
		check func(*testing.T, *flow.Response)
	}{
		{"status", "function onResponse(f){ f.response.status = 503; }", func(t *testing.T, r *flow.Response) {
			if r.Status != 503 || r.StatusText != "OK" {
				t.Errorf("状态 = %d %q, 期望 503 且状态文案保持原值", r.Status, r.StatusText)
			}
		}},
		{"statusText", "function onResponse(f){ f.response.statusText = 'Teapot'; }", func(t *testing.T, r *flow.Response) {
			if r.StatusText != "Teapot" || r.Status != 200 {
				t.Errorf("status = %d %q", r.Status, r.StatusText)
			}
		}},
		{"body", "function onResponse(f){ f.response.body = 'replaced'; }", func(t *testing.T, r *flow.Response) {
			if string(r.Body) != "replaced" {
				t.Errorf("Body = %q", r.Body)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, resp := convRespFlow()
			p := mustPlugin(t, Config{ID: "conv-rf-" + c.name, Source: c.src})
			p.OnResponse(context.Background(), f)
			if f.Response != resp {
				t.Fatal("Response 结构体被整体替换,而非就地改写")
			}
			if !f.Modified {
				t.Error("改动过的响应必须把 flow 标记为 Modified")
			}
			if len(resp.RawHeaders) != 2 || len(resp.Header["Set-Cookie"]) != 2 {
				t.Errorf("保真字段被破坏: raw=%v setCookie=%v", resp.RawHeaders, resp.Header["Set-Cookie"])
			}
			c.check(t, resp)
		})
	}
}

// 验证响应头改写保留响应对象、原始头、Trailer 与未改多值头。
func TestConvertResponseHeaderEditKeepsFidelity(t *testing.T) {
	f, resp := convRespFlow()
	origSetCookie := resp.Header["Set-Cookie"]
	p := mustPlugin(t, Config{ID: "conv-rh", Source: "function onResponse(f){ f.response.headers['X-Add']='1'; }"})
	p.OnResponse(context.Background(), f)

	if f.Response != resp {
		t.Fatal("Response 结构体被整体替换,而非就地改写")
	}
	if !f.Modified {
		t.Fatal("改头必须把 flow 标记为 Modified")
	}
	if got := f.Response.Header["X-Add"]; len(got) != 1 || got[0] != "1" {
		t.Fatalf("X-Add = %v", got)
	}
	if got := f.Response.Header["Set-Cookie"]; !convSameBacking(origSetCookie, got) {
		t.Fatalf("Set-Cookie 被重建而非原样保留: %v", got)
	}
	if len(f.Response.RawHeaders) != 2 || len(f.Response.Trailer["Grpc-Status"]) != 1 {
		t.Fatalf("保真字段丢失: raw=%v trailer=%v", f.Response.RawHeaders, f.Response.Trailer)
	}
	if string(f.Response.Body) != `{"a":1}` {
		t.Fatalf("Body = %q", f.Response.Body)
	}
}

// 验证请求阶段 mock 根据脚本响应视图创建 Response。
func TestConvertMockBuildsResponseFromScratch(t *testing.T) {
	f := newReqFlow()
	sent, out := convOutJSON(t, f, func(jf *jsFlow) {
		jf.Response = &jsResponse{
			Status:     201,
			StatusText: "Created",
			Headers:    map[string]string{"Content-Type": "text/plain", "X-Mock": "1"},
			Body:       "mocked",
		}
	}, jsDecision{Kind: "mock", Reason: "by-test"})

	d := applyHTTP(f, sent, out, flow.PhaseRequest, nil)
	if d.Kind != flow.Mock || d.Reason != "by-test" {
		t.Fatalf("处置 = %+v, 期望 Mock/by-test", d)
	}
	if f.Response == nil {
		t.Fatal("mock 响应未被创建")
	}
	if f.Response.Status != 201 || f.Response.StatusText != "Created" || string(f.Response.Body) != "mocked" {
		t.Fatalf("mock 响应 = %+v", f.Response)
	}
	for k, v := range f.Response.Header {
		if len(v) != 1 {
			t.Fatalf("头 %q 不是单值: %v", k, v)
		}
	}
	if got := f.Response.Header["X-Mock"]; len(got) != 1 || got[0] != "1" {
		t.Fatalf("X-Mock = %v", got)
	}
	if !f.Modified {
		t.Fatal("创建 mock 响应必须把 flow 标记为 Modified")
	}
}

// mergeHeaders 保留未改多值头，应用修改、新增与删除。
func TestConvertMergeHeaders(t *testing.T) {
	newOrig := func() map[string][]string {
		return map[string][]string{
			"Cookie":          {"a=1", "b=2"},
			"X-Forwarded-For": {"1.1.1.1", "2.2.2.2"},
			"X-Single":        {"v"},
		}
	}
	cases := []struct {
		name        string
		edit        func(map[string]string)
		wantChanged bool
		check       func(*testing.T, map[string][]string, map[string][]string)
	}{
		{"untouched", nil, false, func(t *testing.T, orig, got map[string][]string) {
			if !convSameBacking(orig["Cookie"], got["Cookie"]) {
				t.Errorf("Cookie 被重建: %v", got["Cookie"])
			}
		}},
		{"add", func(e map[string]string) { e["X-New"] = "1" }, true, func(t *testing.T, orig, got map[string][]string) {
			if v := got["X-New"]; len(v) != 1 || v[0] != "1" {
				t.Errorf("X-New = %v", v)
			}
			if !convSameBacking(orig["Cookie"], got["Cookie"]) {
				t.Errorf("新增其它头时 Cookie 被重建: %v", got["Cookie"])
			}
		}},
		{"change-first-value", func(e map[string]string) { e["Cookie"] = "z=9" }, true, func(t *testing.T, orig, got map[string][]string) {
			if v := got["Cookie"]; len(v) != 1 || v[0] != "z=9" {
				t.Errorf("Cookie = %v, 期望仅剩 [z=9]", v)
			}
			if !convSameBacking(orig["X-Forwarded-For"], got["X-Forwarded-For"]) {
				t.Errorf("X-Forwarded-For 被重建: %v", got["X-Forwarded-For"])
			}
		}},
		{"delete", func(e map[string]string) { delete(e, "Cookie") }, true, func(t *testing.T, orig, got map[string][]string) {
			if _, ok := got["Cookie"]; ok {
				t.Errorf("被删除的头仍然存在: %v", got["Cookie"])
			}
			if len(got) != 2 {
				t.Errorf("合并后的头集合不符: %v", got)
			}
		}},
		{"rewrite-first-value-identically", func(e map[string]string) {
			e["Cookie"] = "a=1"
			e["X-New"] = "1"
		}, true, func(t *testing.T, orig, got map[string][]string) {
			if !convSameBacking(orig["Cookie"], got["Cookie"]) {
				t.Errorf("首值被写回原值后 Cookie 仍被塌缩: %v", got["Cookie"])
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			orig := newOrig()
			sent := flatten(orig)
			edited := flatten(orig)
			if c.edit != nil {
				c.edit(edited)
			}
			got, changed := mergeHeaders(orig, sent, edited)
			if changed != c.wantChanged {
				t.Fatalf("changed = %v, 期望 %v", changed, c.wantChanged)
			}
			c.check(t, orig, got)
		})
	}
}

// 验证 sameStringMap 对脚本头视图的完整相等判断。
func TestConvertSameStringMap(t *testing.T) {
	base := map[string]string{"A": "1", "B": "2"}
	cases := []struct {
		name string
		b    map[string]string
		want bool
	}{
		{"equal", map[string]string{"A": "1", "B": "2"}, true},
		{"shorter", map[string]string{"A": "1"}, false},
		{"longer", map[string]string{"A": "1", "B": "2", "C": "3"}, false},
		{"key-missing", map[string]string{"A": "1", "C": "2"}, false},
		{"value-differs", map[string]string{"A": "1", "B": "9"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sameStringMap(base, c.b); got != c.want {
				t.Fatalf("sameStringMap(%v, %v) = %v, 期望 %v", base, c.b, got, c.want)
			}
		})
	}
}

// 验证 flatten 的首值视图及 mergeHeaders 对空值头的保留。
func TestConvertFlattenDropsValuelessHeader(t *testing.T) {
	orig := map[string][]string{"X-Empty": {}, "X-Keep": {"1", "2"}}
	view := flatten(orig)
	if _, ok := view["X-Empty"]; ok {
		t.Fatalf("空值头漏进了扁平视图: %v", view)
	}
	if view["X-Keep"] != "1" {
		t.Fatalf("扁平视图应只取首值, 实际 %q", view["X-Keep"])
	}

	kept, changed := mergeHeaders(orig, flatten(orig), flatten(orig))
	if changed {
		t.Fatalf("未被改动的头视图不得报告改动: %v", kept)
	}
	if _, ok := kept["X-Empty"]; !ok {
		t.Fatalf("未发生任何编辑时空值头被丢弃: %v", kept)
	}
}

// 验证 unflatten 返回可写的非 nil 单值头 map。
func TestConvertUnflattenReturnsWritableMap(t *testing.T) {
	for _, in := range []map[string]string{nil, {}} {
		got := unflatten(in)
		if got == nil {
			t.Fatalf("unflatten(%v) = nil, 期望空 map", in)
		}
	}
	got := unflatten(map[string]string{"A": "1", "B": "2"})
	for k, v := range got {
		if len(v) != 1 {
			t.Fatalf("键 %q = %v, 期望恰好一个值", k, v)
		}
	}
}

// 验证 deepCopyJSON 返回可写 map，并隔离嵌套结构。
func TestConvertDeepCopyJSON(t *testing.T) {
	t.Run("nil-and-empty", func(t *testing.T) {
		for _, in := range []map[string]any{nil, {}} {
			got, _ := deepCopyJSON(in)
			if got == nil {
				t.Fatalf("deepCopyJSON(%v) = nil", in)
			}
		}
	})
	t.Run("nested-isolated", func(t *testing.T) {
		src := map[string]any{"n": map[string]any{"list": []any{1.0, 2.0}}}
		cp, _ := deepCopyJSON(src)
		cp["n"].(map[string]any)["list"].([]any)[0] = 99.0
		cp["n"].(map[string]any)["added"] = true
		if got := src["n"].(map[string]any)["list"].([]any)[0]; got != 1.0 {
			t.Fatalf("源 map 被副本的改动带走: %v", got)
		}
		if _, ok := src["n"].(map[string]any)["added"]; ok {
			t.Fatal("副本上新增的嵌套键漏回了源 map")
		}
		src["n"].(map[string]any)["list"].([]any)[1] = 42.0
		if got := cp["n"].(map[string]any)["list"].([]any)[1]; got != 2.0 {
			t.Fatalf("副本被源 map 的改动带走: %v", got)
		}
	})
	// 验证不可序列化值按键递归跳过，并记录被跳过的路径。
	t.Run("逐键降级", func(t *testing.T) {
		cases := []struct {
			name        string
			in          map[string]any
			wantDropped []string
			check       func(*testing.T, map[string]any)
		}{
			{"顶层坏叶子", map[string]any{"keep": "v", "bad": math.NaN()}, []string{"bad"},
				func(t *testing.T, got map[string]any) {
					if got["keep"] != "v" {
						t.Errorf("正常键被连坐丢弃: %v", got)
					}
					if _, ok := got["bad"]; ok {
						t.Errorf("不可序列化的键仍在副本里: %v", got)
					}
				}},
			{"不可序列化的类型", map[string]any{"keep": 1.0, "c": make(chan int)}, []string{"c"},
				func(t *testing.T, got map[string]any) {
					if got["keep"] != 1.0 {
						t.Errorf("正常键被连坐丢弃: %v", got)
					}
				}},
			{"嵌套坏叶子", map[string]any{"stats": map[string]any{"n": 2.0, "avg": math.Inf(1)}}, []string{"stats.avg"},
				func(t *testing.T, got map[string]any) {
					sub, ok := got["stats"].(map[string]any)
					if !ok || sub["n"] != 2.0 {
						t.Errorf("同一子 map 里的正常键被连坐丢弃: %v", got)
					}
					if _, ok := sub["avg"]; ok {
						t.Errorf("不可序列化的嵌套键仍在副本里: %v", sub)
					}
				}},
			{"数组里的坏元素", map[string]any{"list": []any{1.0, math.NaN(), 3.0}}, []string{"list[1]"},
				func(t *testing.T, got map[string]any) {
					if l, ok := got["list"].([]any); !ok || len(l) != 2 || l[0] != 1.0 || l[1] != 3.0 {
						t.Errorf("数组里的正常元素被连坐丢弃: %v", got["list"])
					}
				}},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				got, dropped := deepCopyJSON(c.in)
				if got == nil {
					t.Fatal("deepCopyJSON = nil")
				}
				if !reflect.DeepEqual(dropped, c.wantDropped) {
					t.Errorf("dropped = %v, 期望 %v", dropped, c.wantDropped)
				}
				c.check(t, got)
			})
		}
	})
}

// 验证 requestToJS 对缺失请求、响应及进程信息的字段表示。
func TestConvertRequestToJSBranches(t *testing.T) {
	t.Run("nil-request", func(t *testing.T) {
		got := requestToJS(&flow.Flow{ID: "x"})
		if got.Headers != nil || got.Method != "" || got.Body != "" {
			t.Fatalf("Request 为 nil 时产出了 %+v", got)
		}
	})
	t.Run("nil-response", func(t *testing.T) {
		if got := requestToJS(newReqFlow()); got.Response != nil {
			t.Fatalf("凭空造出了 response: %+v", got.Response)
		}
	})
	t.Run("with-response-and-process", func(t *testing.T) {
		f, _ := convRespFlow()
		f.SetProcess(&flow.ProcessInfo{Name: "curl", PID: 4242, Path: "/usr/bin/curl"})
		got := requestToJS(f)
		if got.Response == nil || got.Response.Status != 200 || got.Response.Headers["Set-Cookie"] != "x=1" {
			t.Fatalf("response 视图 = %+v", got.Response)
		}
		if got.Process == nil || got.Process.Name != "curl" || got.Process.PID != 4242 || got.Process.Path != "/usr/bin/curl" {
			t.Fatalf("process 视图 = %+v", got.Process)
		}
	})
}

// 验证进程信息在脚本视图中的可见性。
func TestConvertProcessVisibleToScript(t *testing.T) {
	src := `function onRequest(f){
	  f.headers['X-P'] = f.process ? (f.process.name + '/' + f.process.pid + '/' + f.process.path) : 'none';
	}`
	f := newReqFlow()
	f.SetProcess(&flow.ProcessInfo{Name: "curl", PID: 4242, Path: "/usr/bin/curl"})
	p := mustPlugin(t, Config{ID: "conv-proc", Source: src})
	p.OnRequest(context.Background(), f)
	if got := f.Request.Header["X-P"]; len(got) != 1 || got[0] != "curl/4242//usr/bin/curl" {
		t.Fatalf("X-P = %v", got)
	}

	f2 := newReqFlow()
	p2 := mustPlugin(t, Config{ID: "conv-proc2", Source: src})
	p2.OnRequest(context.Background(), f2)
	if got := f2.Request.Header["X-P"]; len(got) != 1 || got[0] != "none" {
		t.Fatalf("未解析到进程时 VM 里应为假值, 实际 %v", got)
	}
}

// 验证 VM 处置字符串到引擎 Decision 的映射。
func TestConvertDecisionFromJS(t *testing.T) {
	cases := []struct {
		name  string
		in    jsDecision
		phase flow.Phase
		want  flow.Decision
	}{
		{"mock", jsDecision{Kind: "mock", Reason: "m"}, flow.PhaseRequest,
			flow.Decision{Kind: flow.Mock, Reason: "m"}},
		{"abort", jsDecision{Kind: "abort", Status: 403, Reason: "denied"}, flow.PhaseRequest,
			flow.Decision{Kind: flow.Abort, StatusOnAbort: 403, Reason: "denied"}},
		{"abort-close", jsDecision{Kind: "abort"}, flow.PhaseRequest,
			flow.Decision{Kind: flow.Abort}},
		{"breakpoint-request", jsDecision{Kind: "breakpoint", Reason: "b"}, flow.PhaseRequest,
			flow.Decision{Kind: flow.Breakpoint, Reason: "b", BreakpointOn: flow.PhaseRequest}},
		{"breakpoint-response", jsDecision{Kind: "breakpoint"}, flow.PhaseResponse,
			flow.Decision{Kind: flow.Breakpoint, BreakpointOn: flow.PhaseResponse}},
		{"unknown", jsDecision{Kind: "explode", Status: 500, Reason: "r"}, flow.PhaseRequest,
			flow.Decision{Kind: flow.Continue}},
		{"empty", jsDecision{}, flow.PhaseResponse,
			flow.Decision{Kind: flow.Continue}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := decisionFromJS(c.in, c.phase); got != c.want {
				t.Fatalf("decisionFromJS(%+v, %q) = %+v, 期望 %+v", c.in, c.phase, got, c.want)
			}
		})
	}
}

// 验证脚本处置从钩子到引擎 Decision 的端到端映射。
func TestConvertDecisionEndToEnd(t *testing.T) {
	t.Run("abort", func(t *testing.T) {
		p := mustPlugin(t, Config{ID: "conv-abort", Source: "function onRequest(f){ abort({status:403, reason:'x'}); }"})
		d := p.OnRequest(context.Background(), newReqFlow())
		if d.Kind != flow.Abort || d.StatusOnAbort != 403 || d.Reason != "x" {
			t.Fatalf("decision = %+v", d)
		}
	})
	t.Run("breakpoint-response-phase", func(t *testing.T) {
		f, _ := convRespFlow()
		p := mustPlugin(t, Config{ID: "conv-bp", Source: "function onResponse(f){ setBreakpoint(); }"})
		d := p.OnResponse(context.Background(), f)
		if d.Kind != flow.Breakpoint || d.BreakpointOn != flow.PhaseResponse {
			t.Fatalf("处置 = %+v, 期望响应阶段的 Breakpoint", d)
		}
	})
}

// 验证 VM 回传报文解析失败时保持 Continue 与原始 flow。
func TestConvertApplyHTTPFailsOpenOnMalformedJSON(t *testing.T) {
	f := newReqFlow()
	sent := requestToJS(f)
	sink, logf := convLogSink()
	d := applyHTTP(f, &sent, []byte(`{"flow":{"method":`), flow.PhaseRequest, logf)
	if d.Kind != flow.Continue {
		t.Fatalf("处置 = %v, 期望 Continue", d.Kind)
	}
	if f.Modified {
		t.Error("半截报文不得把 flow 标记为 Modified")
	}
	if f.Request.Method != "GET" || f.Request.URL != "http://example.com/api/x" || string(f.Request.Body) != "hello" {
		t.Errorf("请求被改动: %+v", f.Request)
	}
	if got := f.Request.Header["Cookie"]; len(got) != 2 {
		t.Errorf("Cookie 被塌缩: %v", got)
	}
	if f.Response != nil {
		t.Errorf("凭空造出了 response: %+v", f.Response)
	}
	if !convHasLog(*sink, "error", "插件返回的 flow 无法解析") {
		t.Errorf("无声吞掉了整条结果: %v", *sink)
	}
}

// 验证 applyHTTP 在缺失 Request 时仍可应用响应字段。
func TestConvertApplyHTTPTolerantToNilRequest(t *testing.T) {
	f := &flow.Flow{ID: "no-req"}
	sent := requestToJS(f)
	out, err := json.Marshal(jsOut{
		Flow: jsFlow{
			ID:      "no-req",
			Method:  "POST",
			Headers: map[string]string{"A": "b"},
			Body:    "x",
			Response: &jsResponse{
				Status: 200,
				Body:   "ok",
			},
		},
		Decision: jsDecision{Kind: "continue"},
	})
	if err != nil {
		t.Fatalf("序列化: %v", err)
	}
	if d := applyHTTP(f, &sent, out, flow.PhaseRequest, nil); d.Kind != flow.Continue {
		t.Fatalf("处置 = %v, 期望 Continue", d.Kind)
	}
	if f.Request != nil {
		t.Fatalf("凭空造出了 Request: %+v", f.Request)
	}
	if f.Response == nil || string(f.Response.Body) != "ok" {
		t.Fatalf("response = %+v", f.Response)
	}
}

// 验证通过 b64 通道改写请求体和响应体。
func TestConvertBinaryPayloadRewrittenViaB64(t *testing.T) {
	t.Run("请求体", func(t *testing.T) {
		src := `function onRequest(f){
		  var b = base64.decodeBytes(f.bodyB64);
		  b[0] = 1; b.push(254);
		  f.bodyB64 = base64.encodeBytes(b);
		}`
		p := mustPlugin(t, Config{ID: "conv-b64-req", Source: src})
		f := newReqFlow()
		f.Request.Body = []byte{0x00, 0xff, 0xfe, 0x41, 0x80}
		p.OnRequest(context.Background(), f)
		want := []byte{0x01, 0xff, 0xfe, 0x41, 0x80, 0xfe}
		if !bytes.Equal(f.Request.Body, want) {
			t.Fatalf("请求体 = %x, 期望 %x", f.Request.Body, want)
		}
		if !f.Modified {
			t.Fatal("经 b64 通道改写载荷必须把 flow 标记为 Modified")
		}
	})
	t.Run("响应体", func(t *testing.T) {
		src := `function onResponse(f){
		  var b = base64.decodeBytes(f.response.bodyB64);
		  b[1] = 7;
		  f.response.bodyB64 = base64.encodeBytes(b);
		}`
		p := mustPlugin(t, Config{ID: "conv-b64-resp", Source: src})
		f, resp := convRespFlow()
		resp.Body = []byte{0x1f, 0x8b, 0x08, 0x00}
		p.OnResponse(context.Background(), f)
		want := []byte{0x1f, 0x07, 0x08, 0x00}
		if !bytes.Equal(resp.Body, want) {
			t.Fatalf("响应体 = %x, 期望 %x", resp.Body, want)
		}
		if f.Response != resp {
			t.Fatal("Response 结构体被整体替换,而非就地改写")
		}
		if !f.Modified {
			t.Fatal("经 b64 通道改写载荷必须把 flow 标记为 Modified")
		}
	})
}

// 验证文本字段优先于 b64，并支持通过删除 b64 清空载荷。
func TestConvertTextWriteOverridesB64Channel(t *testing.T) {
	t.Run("写文本", func(t *testing.T) {
		p := mustPlugin(t, Config{ID: "conv-text-wins", Source: "function onRequest(f){ f.body='hello'; }"})
		f := newReqFlow()
		f.Request.Body = []byte{0xff, 0xfe, 0x80}
		p.OnRequest(context.Background(), f)
		if string(f.Request.Body) != "hello" {
			t.Fatalf("请求体 = %x, 期望 hello 的 5 个字节", f.Request.Body)
		}
		if !f.Modified {
			t.Fatal("改写载荷必须把 flow 标记为 Modified")
		}
	})
	t.Run("delete-b64-即清空", func(t *testing.T) {
		p := mustPlugin(t, Config{ID: "conv-del-b64", Source: "function onRequest(f){ delete f.bodyB64; }"})
		f := newReqFlow()
		f.Request.Body = []byte{0xff, 0xfe, 0x80}
		p.OnRequest(context.Background(), f)
		if len(f.Request.Body) != 0 {
			t.Fatalf("请求体 = %x, 期望为空", f.Request.Body)
		}
		if !f.Modified {
			t.Fatal("清空载荷必须把 flow 标记为 Modified")
		}
	})
}

// 验证合法 UTF-8 载荷继续使用文本字段，脚本保持既有文本接口。
func TestConvertUTF8PayloadStaysOnTextChannel(t *testing.T) {
	src := `function onRequest(f){
	  f.headers['X-Probe'] = typeof f.bodyB64;
	  f.body = f.body.replace('世界', 'world');
	}
	function onResponse(f){
	  f.response.headers['X-Probe'] = typeof f.response.bodyB64;
	  f.response.body = f.response.body.replace('旧', '新');
	}`
	p := mustPlugin(t, Config{ID: "conv-utf8-text", Source: src})

	f := newReqFlow()
	f.Request.Body = []byte("你好, 世界 🌏")
	p.OnRequest(context.Background(), f)
	if got := f.Request.Header["X-Probe"]; len(got) != 1 || got[0] != "undefined" {
		t.Fatalf("文本载荷下 typeof f.bodyB64 = %v, 期望 undefined", got)
	}
	if got := string(f.Request.Body); got != "你好, world 🌏" {
		t.Fatalf("请求体 = %q", got)
	}

	f2, resp := convRespFlow()
	resp.Body = []byte("答案:旧 ✅")
	p.OnResponse(context.Background(), f2)
	if got := resp.Header["X-Probe"]; len(got) != 1 || got[0] != "undefined" {
		t.Fatalf("文本载荷下 typeof f.response.bodyB64 = %v, 期望 undefined", got)
	}
	if got := string(resp.Body); got != "答案:新 ✅" {
		t.Fatalf("响应体 = %q", got)
	}
}

// 验证 mock 响应支持 bodyB64，并记录非法编码。
func TestConvertMockBinaryResponseBody(t *testing.T) {
	t.Run("合法 b64", func(t *testing.T) {
		src := `function onRequest(f){
		  mock({status:200, headers:{'Content-Type':'image/png'},
		        bodyB64: base64.encodeBytes([137,80,78,71,13,10,26,10,0,255])});
		}`
		p := mustPlugin(t, Config{ID: "conv-mock-b64", Source: src})
		f := newReqFlow()
		d := p.OnRequest(context.Background(), f)
		if d.Kind != flow.Mock {
			t.Fatalf("处置 = %v, 期望 Mock", d.Kind)
		}
		want := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0xff}
		if f.Response == nil || !bytes.Equal(f.Response.Body, want) {
			t.Fatalf("mock 响应体 = %x, 期望 %x", f.Response.Body, want)
		}
		if got := f.Response.Header["Content-Type"]; len(got) != 1 || got[0] != "image/png" {
			t.Fatalf("Content-Type = %v", got)
		}
	})
	t.Run("非法 b64", func(t *testing.T) {
		p := mustPlugin(t, Config{ID: "conv-mock-bad-b64",
			Source: "function onRequest(f){ mock({status:200, bodyB64:'!!!'}); }"})
		f := newReqFlow()
		if d := p.OnRequest(context.Background(), f); d.Kind != flow.Mock {
			t.Fatalf("处置 = %v, 期望 Mock", d.Kind)
		}
		if f.Response == nil || len(f.Response.Body) != 0 {
			t.Fatalf("mock 响应体 = %x, 期望为空", f.Response.Body)
		}
		if !hookHasLog(p, "error", "mock 的 bodyB64") {
			t.Fatalf("解码失败未留痕: %+v", p.Logs())
		}
	})
}

// 验证 b64 解码失败时保留原载荷并记录 error 日志。
func TestConvertUndecodableB64KeepsPayload(t *testing.T) {
	orig := []byte{0x00, 0xff, 0xfe, 0x41, 0x80}
	t.Run("请求体", func(t *testing.T) {
		p := mustPlugin(t, Config{ID: "conv-bad-b64-req", Source: "function onRequest(f){ f.bodyB64='!!!'; }"})
		f := newReqFlow()
		f.Request.Body = append([]byte(nil), orig...)
		p.OnRequest(context.Background(), f)
		if !bytes.Equal(f.Request.Body, orig) {
			t.Fatalf("请求体 = %x, 期望保持 %x", f.Request.Body, orig)
		}
		if f.Modified {
			t.Fatal("解码失败的改动不得把 flow 标记为 Modified")
		}
		if !hookHasLog(p, "error", "flow.bodyB64") {
			t.Fatalf("解码失败未留痕: %+v", p.Logs())
		}
	})
	t.Run("响应体", func(t *testing.T) {
		p := mustPlugin(t, Config{ID: "conv-bad-b64-resp",
			Source: "function onResponse(f){ f.response.bodyB64='!!!'; }"})
		f, resp := convRespFlow()
		resp.Body = append([]byte(nil), orig...)
		p.OnResponse(context.Background(), f)
		if !bytes.Equal(resp.Body, orig) {
			t.Fatalf("响应体 = %x, 期望保持 %x", resp.Body, orig)
		}
		if f.Modified {
			t.Fatal("解码失败的改动不得把 flow 标记为 Modified")
		}
		if !hookHasLog(p, "error", "flow.response.bodyB64") {
			t.Fatalf("解码失败未留痕: %+v", p.Logs())
		}
	})
}

// 验证非字符串 body 字段的处理及同次调用中其他字段的回写。
func TestConvertNonStringBodyKeepsOtherEdits(t *testing.T) {
	cases := []struct {
		name     string
		assign   string
		wantBody string
		wantLog  bool
	}{
		{"数字", "f.body = 123;", "hello", true},
		{"对象", "f.body = {a:1};", "hello", true},
		{"null 即清空", "f.body = null;", "", false},
		{"delete 即清空", "delete f.body;", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := "function onRequest(f){ f.headers['X-Tag']='1'; " + c.assign + " }"
			p := mustPlugin(t, Config{ID: "conv-badbody", Source: src})
			f := newReqFlow()
			d := p.OnRequest(context.Background(), f)
			if d.Kind != flow.Continue {
				t.Fatalf("处置 = %v, 期望 Continue", d.Kind)
			}
			if string(f.Request.Body) != c.wantBody {
				t.Fatalf("请求体 = %q, 期望 %q", f.Request.Body, c.wantBody)
			}
			if got := f.Request.Header["X-Tag"]; len(got) != 1 || got[0] != "1" {
				t.Fatalf("同一次调用里对头的改动被吞掉: X-Tag = %v", got)
			}
			if got := hookHasLog(p, "error", "flow.body"); got != c.wantLog {
				t.Fatalf("错误日志 = %v, 期望 %v: %+v", got, c.wantLog, p.Logs())
			}
		})
	}
}

// 验证 nil 与空切片载荷保持各自语义。
func TestConvertEmptyBodyNotReshaped(t *testing.T) {
	t.Run("nil-请求体", func(t *testing.T) {
		p := mustPlugin(t, Config{ID: "conv-nil-body", Source: "function onRequest(f){}"})
		f := newReqFlow()
		f.Request.Body = nil
		p.OnRequest(context.Background(), f)
		if f.Request.Body != nil {
			t.Fatalf("nil 请求体被换成了 %#v", f.Request.Body)
		}
		if f.Modified {
			t.Fatal("空载荷的往返不得把 flow 标记为 Modified")
		}
	})
	t.Run("空切片-请求体", func(t *testing.T) {
		p := mustPlugin(t, Config{ID: "conv-empty-body", Source: "function onRequest(f){}"})
		f := newReqFlow()
		f.Request.Body = []byte{}
		p.OnRequest(context.Background(), f)
		if f.Request.Body == nil || len(f.Request.Body) != 0 {
			t.Fatalf("空切片请求体被换成了 %#v", f.Request.Body)
		}
		if f.Modified {
			t.Fatal("空载荷的往返不得把 flow 标记为 Modified")
		}
	})
	t.Run("nil-响应体", func(t *testing.T) {
		p := mustPlugin(t, Config{ID: "conv-nil-resp-body", Source: "function onResponse(f){}"})
		f, resp := convRespFlow()
		resp.Body = nil
		p.OnResponse(context.Background(), f)
		if resp.Body != nil {
			t.Fatalf("nil 响应体被换成了 %#v", resp.Body)
		}
		if f.Modified {
			t.Fatal("空载荷的往返不得把 flow 标记为 Modified")
		}
	})
}

// 验证脚本将 flow 替换为非法类型时保留原始请求。
func TestConvertBrokenFlowKeepsRequestIntact(t *testing.T) {
	for _, assign := range []string{"flow = 'oops';", "flow = 42;", "flow = [1,2];"} {
		t.Run(assign, func(t *testing.T) {
			p := mustPlugin(t, Config{ID: "conv-broken-flow", Source: "function onRequest(f){ " + assign + " }"})
			f := newReqFlow()
			d := p.OnRequest(context.Background(), f)
			if d.Kind != flow.Continue {
				t.Fatalf("处置 = %v, 期望 Continue", d.Kind)
			}
			if f.Request.Method != "GET" || f.Request.URL != "http://example.com/api/x" {
				t.Fatalf("请求行被清空: method=%q url=%q", f.Request.Method, f.Request.URL)
			}
			if string(f.Request.Body) != "hello" {
				t.Fatalf("请求体 = %q, 期望保持 hello", f.Request.Body)
			}
			if f.Modified {
				t.Fatal("拿不到任何字段的一次调用不得把 flow 标记为 Modified")
			}
			if !hookHasLog(p, "error", "插件把 flow 写成了非法类型") {
				t.Fatalf("未留痕: %+v", p.Logs())
			}
		})
	}
}

// 验证降级解析在缺少 flow 且 decision 类型错误时保留请求。
func TestConvertMissingFlowInDegradedOutput(t *testing.T) {
	f := newReqFlow()
	sent := requestToJS(f)
	got, logf := convLogSink()
	d := applyHTTP(f, &sent, []byte(`{"decision":{"kind":"continue","status":"x"}}`), flow.PhaseRequest, logf)
	if d.Kind != flow.Continue {
		t.Fatalf("处置 = %v, 期望 Continue", d.Kind)
	}
	if f.Request.Method != "GET" || f.Request.URL != "http://example.com/api/x" || string(f.Request.Body) != "hello" {
		t.Fatalf("请求被清空: method=%q url=%q body=%q", f.Request.Method, f.Request.URL, f.Request.Body)
	}
	if f.Modified {
		t.Fatal("没有 flow 的报文不得把 flow 标记为 Modified")
	}
	if !convHasLog(*got, "error", "decision.status") {
		t.Fatalf("类型错误未留痕: %v", *got)
	}
}

// 验证非字符串头值按字段跳过，并保留其余头部改动。
func TestConvertNonStringHeaderKeepsOriginal(t *testing.T) {
	for _, assign := range []string{"f.headers['X-Single'] = {bad:1};", "f.headers['X-Single'] = 7;"} {
		t.Run(assign, func(t *testing.T) {
			p := mustPlugin(t, Config{ID: "conv-bad-header", Source: "function onRequest(f){ " + assign + " }"})
			f := newReqFlow()
			p.OnRequest(context.Background(), f)
			if got := f.Request.Header["X-Single"]; len(got) != 1 || got[0] != "v" {
				t.Fatalf("X-Single = %v, 期望保持原值 [v]", got)
			}
			if got := f.Request.Header["Cookie"]; len(got) != 2 {
				t.Fatalf("同一次调用里其余头被连坐改写: Cookie = %v", got)
			}
			if f.Modified {
				t.Fatal("被忽略的头改动不得把 flow 标记为 Modified")
			}
			if !hookHasLog(p, "error", "本次头改动已忽略") {
				t.Fatalf("未留痕: %+v", p.Logs())
			}
		})
	}
}

// 验证文本载荷上的 b64 写入记录冲突，并支持先清空文本再写入 b64。
func TestConvertBinaryWriteOnTextPayload(t *testing.T) {
	t.Run("只写-b64-无效并留痕", func(t *testing.T) {
		p := mustPlugin(t, Config{ID: "conv-b64-on-text",
			Source: "function onRequest(f){ f.bodyB64 = base64.encodeBytes([0,255,254]); }"})
		f := newReqFlow()
		p.OnRequest(context.Background(), f)
		if string(f.Request.Body) != "hello" {
			t.Fatalf("请求体 = %x, 期望保持 hello", f.Request.Body)
		}
		if f.Modified {
			t.Fatal("未生效的改动不得把 flow 标记为 Modified")
		}
		if !hookHasLog(p, "error", "同时有值") {
			t.Fatalf("未留痕: %+v", p.Logs())
		}
	})
	t.Run("先清文本再写-b64", func(t *testing.T) {
		p := mustPlugin(t, Config{ID: "conv-b64-after-clear",
			Source: "function onRequest(f){ f.body = ''; f.bodyB64 = base64.encodeBytes([0,255,254]); }"})
		f := newReqFlow()
		p.OnRequest(context.Background(), f)
		want := []byte{0x00, 0xff, 0xfe}
		if !bytes.Equal(f.Request.Body, want) {
			t.Fatalf("请求体 = %x, 期望 %x", f.Request.Body, want)
		}
		if !f.Modified {
			t.Fatal("改写载荷必须把 flow 标记为 Modified")
		}
		if hookHasLog(p, "error", "同时有值") {
			t.Fatalf("规范写法不该留下告警: %+v", p.Logs())
		}
	})
}

// convBadDisposition 表示 RFC 6266 允许的 ISO-8859-1 文件名。
const convBadDisposition = "attachment; filename=\"caf\xe9.pdf\""

// convBadHeaderValues 覆盖常见的非法 UTF-8 字节形态。
var convBadHeaderValues = []struct{ name, value string }{
	{"latin1-文件名", convBadDisposition},
	{"孤立-continuation-字节", "a\x80b"},
	{"连续非法字节", "\xff\xfe"},
	{"截断的多字节序列", "x\xe4\xb8"},
}

// 验证无操作插件保留非法 UTF-8 头值的原始字节及其他多值头。
func TestConvertNoOpPreservesInvalidUTF8HeaderBytes(t *testing.T) {
	for _, c := range convBadHeaderValues {
		t.Run("请求头/"+c.name, func(t *testing.T) {
			p := mustPlugin(t, Config{ID: "conv-bad-noop-req", Source: "function onRequest(f){}"})
			f := newReqFlow()
			f.Request.Header["Content-Disposition"] = []string{c.value}
			origCookie := f.Request.Header["Cookie"]
			p.OnRequest(context.Background(), f)

			if got := f.Request.Header["Content-Disposition"]; len(got) != 1 || got[0] != c.value {
				t.Fatalf("头值 = %q, 期望原始字节 %q", got, c.value)
			}
			if f.Modified {
				t.Fatal("no-op 插件不得把 flow 标记为 Modified")
			}
			if got := f.Request.Header["Cookie"]; !convSameBacking(origCookie, got) {
				t.Fatalf("正常多值头被连坐重建: %v", got)
			}
		})
		t.Run("响应头/"+c.name, func(t *testing.T) {
			p := mustPlugin(t, Config{ID: "conv-bad-noop-resp", Source: "function onResponse(f){}"})
			f, resp := convRespFlow()
			resp.Header["Content-Disposition"] = []string{c.value}
			origSetCookie := resp.Header["Set-Cookie"]
			p.OnResponse(context.Background(), f)

			if got := resp.Header["Content-Disposition"]; len(got) != 1 || got[0] != c.value {
				t.Fatalf("头值 = %q, 期望原始字节 %q", got, c.value)
			}
			if f.Modified {
				t.Fatal("no-op 插件不得把 flow 标记为 Modified")
			}
			if got := resp.Header["Set-Cookie"]; !convSameBacking(origSetCookie, got) {
				t.Fatalf("正常多值头被连坐重建: %v", got)
			}
		})
	}
}

// 验证编辑其他头时保留未触碰头值的原始字节。
func TestConvertUntouchedInvalidHeaderSurvivesOtherEdit(t *testing.T) {
	p := mustPlugin(t, Config{ID: "conv-bad-other", Source: "function onRequest(f){ header.set(f.headers,'X-New','1'); }"})
	f := newReqFlow()
	f.Request.Header["Content-Disposition"] = []string{convBadDisposition}
	origCookie := f.Request.Header["Cookie"]
	p.OnRequest(context.Background(), f)

	if got := f.Request.Header["Content-Disposition"]; len(got) != 1 || got[0] != convBadDisposition {
		t.Fatalf("未被触碰的非法头值 = %q, 期望原始字节 %q", got, convBadDisposition)
	}
	if got := f.Request.Header["X-New"]; len(got) != 1 || got[0] != "1" {
		t.Fatalf("X-New = %v", got)
	}
	if !f.Modified {
		t.Fatal("真实的头改动必须置 Modified")
	}
	if got := f.Request.Header["Cookie"]; !convSameBacking(origCookie, got) {
		t.Fatalf("Cookie 被重建: %v", got)
	}
}

// 验证非法头值的显式改写与 Modified 标记。
func TestConvertInvalidHeaderRewriteApplies(t *testing.T) {
	p := mustPlugin(t, Config{ID: "conv-bad-set",
		Source: `function onRequest(f){ header.set(f.headers,'Content-Disposition','attachment; filename="ok.pdf"'); }`})
	f := newReqFlow()
	f.Request.Header["Content-Disposition"] = []string{convBadDisposition}
	origCookie := f.Request.Header["Cookie"]
	p.OnRequest(context.Background(), f)

	want := `attachment; filename="ok.pdf"`
	if got := f.Request.Header["Content-Disposition"]; len(got) != 1 || got[0] != want {
		t.Fatalf("头值 = %q, 期望 %q", got, want)
	}
	if !f.Modified {
		t.Fatal("改写头值必须置 Modified")
	}
	if got := f.Request.Header["Cookie"]; !convSameBacking(origCookie, got) {
		t.Fatalf("其余键受连累: Cookie = %v", got)
	}
}

// 验证非法头值的显式删除。
func TestConvertInvalidHeaderDeleteApplies(t *testing.T) {
	p := mustPlugin(t, Config{ID: "conv-bad-del", Source: "function onRequest(f){ header.del(f.headers,'Content-Disposition'); }"})
	f := newReqFlow()
	f.Request.Header["Content-Disposition"] = []string{convBadDisposition}
	p.OnRequest(context.Background(), f)

	if got, ok := f.Request.Header["Content-Disposition"]; ok {
		t.Fatalf("被删除的头仍然存在: %v", got)
	}
	if !f.Modified {
		t.Fatal("删头必须置 Modified")
	}
	if got := f.Request.Header["Cookie"]; len(got) != 2 {
		t.Fatalf("其余键受连累: Cookie = %v", got)
	}
}

// 验证脚本回写 sanitize 后的可见形态时恢复基准原始字节。
func TestConvertHeaderRewrittenToSanitizedFormCountsAsUnchanged(t *testing.T) {
	p := mustPlugin(t, Config{ID: "conv-bad-echo",
		Source: `function onRequest(f){ f.headers['Content-Disposition'] = 'attachment; filename="caf\ufffd.pdf"'; }`})
	f := newReqFlow()
	f.Request.Header["Content-Disposition"] = []string{convBadDisposition}
	p.OnRequest(context.Background(), f)

	if got := f.Request.Header["Content-Disposition"]; len(got) != 1 || got[0] != convBadDisposition {
		t.Fatalf("头值 = %q, 期望保留原始字节 %q", got, convBadDisposition)
	}
	if f.Modified {
		t.Fatal("与发出去的视图逐字节相同的回程值不得置 Modified")
	}
}

// 验证 headers 的 nil 与空 map 分别表示保持原值和清空全部头部。
func TestConvertHeadersNilVersusEmpty(t *testing.T) {
	t.Run("delete-视作没碰", func(t *testing.T) {
		p := mustPlugin(t, Config{ID: "conv-h-del", Source: "function onRequest(f){ delete f.headers; }"})
		f := newReqFlow()
		origCookie := f.Request.Header["Cookie"]
		p.OnRequest(context.Background(), f)
		if got := f.Request.Header["Cookie"]; !convSameBacking(origCookie, got) {
			t.Fatalf("Cookie = %v, 期望原样保留", got)
		}
		if len(f.Request.Header) != 3 || f.Modified {
			t.Fatalf("头集合 = %v, Modified = %v", f.Request.Header, f.Modified)
		}
	})
	t.Run("null-视作没碰", func(t *testing.T) {
		p := mustPlugin(t, Config{ID: "conv-h-null", Source: "function onRequest(f){ f.headers = null; }"})
		f := newReqFlow()
		p.OnRequest(context.Background(), f)
		if len(f.Request.Header) != 3 || f.Modified {
			t.Fatalf("头集合 = %v, Modified = %v", f.Request.Header, f.Modified)
		}
	})
	t.Run("空对象-删光", func(t *testing.T) {
		p := mustPlugin(t, Config{ID: "conv-h-empty", Source: "function onRequest(f){ f.headers = {}; }"})
		f := newReqFlow()
		p.OnRequest(context.Background(), f)
		if len(f.Request.Header) != 0 {
			t.Fatalf("头集合 = %v, 期望删光", f.Request.Header)
		}
		if !f.Modified {
			t.Fatal("删光所有头必须置 Modified")
		}
	})
}
