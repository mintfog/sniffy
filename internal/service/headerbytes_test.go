// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package service

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/mintfog/sniffy/internal/flow"
)

// latin1Disposition 模拟含 Latin-1 文件名的 Content-Disposition 值。
const latin1Disposition = "attachment; filename=\"caf\xe9.pdf\""

// jsonKeys 返回 DTO JSON 对象中的顶层键。
func jsonKeys(t *testing.T, v any) map[string]json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("DTO 必须能序列化: %v", err)
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("DTO 应是 JSON 对象: %v", err)
	}
	return out
}

// decodeB64 解码标准 base64 旁路项。
func decodeB64(t *testing.T, enc string) string {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		t.Fatalf("旁路项应是标准 base64: %v", err)
	}
	return string(raw)
}

// roundTripSession 返回会话 DTO 经过 JSON 边界后的形态。
func roundTripSession(t *testing.T, dto HTTPSessionDTO) HTTPSessionDTO {
	t.Helper()
	raw, err := json.Marshal(dto)
	if err != nil {
		t.Fatalf("会话 DTO 必须能序列化: %v", err)
	}
	var out HTTPSessionDTO
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("会话 DTO 解码失败: %v", err)
	}
	return out
}

// 全部合法头值的请求与响应 DTO 省略 headersB64 字段。
func TestSessionDTOOmitsHeadersB64WhenAllValid(t *testing.T) {
	t.Parallel()
	f := newFlow("clean-1",
		withRequest(http.MethodGet, "https://api.example.com/v1/ping"),
		withRequestHeader("Accept", "application/json"),
		withResponse(http.StatusOK, "application/json", []byte(`{"ok":true}`)),
	)
	dto := SessionDTO(f)
	if _, found := jsonKeys(t, dto.Request)["headersB64"]; found {
		t.Error("全合法请求头不应出现 headersB64 键")
	}
	if _, found := jsonKeys(t, dto.Response)["headersB64"]; found {
		t.Error("全合法响应头不应出现 headersB64 键")
	}
}

// 详情 DTO 为含非法 UTF-8 字节的头值提供同名 headersB64 旁路。
func TestSessionDTOCarriesHeaderBytesSidecar(t *testing.T) {
	t.Parallel()
	f := newFlow("dirty-1",
		withRequest(http.MethodGet, "https://api.example.com/v1/ping"),
		withRequestHeader("X-Note", latin1Disposition),
		withRequestHeader("Accept", "application/json"),
		withResponse(http.StatusOK, "application/octet-stream", []byte("x")),
		withResponseHeader("Content-Disposition", latin1Disposition),
	)
	// 模拟详情页读取 DTO 的 JSON 形态。
	dto := roundTripSession(t, SessionDTO(f))

	if dto.Request.Headers["X-Note"] == latin1Disposition {
		t.Fatal("明文槽经 JSON 出境后仍是原始字节,本用例已失去区分力")
	}
	if got := decodeB64(t, dto.Request.HeadersB64["X-Note"]); got != latin1Disposition {
		t.Errorf("请求头旁路 = %q, want %q", got, latin1Disposition)
	}
	if _, found := dto.Request.HeadersB64["Accept"]; found {
		t.Error("旁路是稀疏的:合法头不该进去")
	}
	if got := decodeB64(t, dto.Response.HeadersB64["Content-Disposition"]); got != latin1Disposition {
		t.Errorf("响应头旁路 = %q, want %q", got, latin1Disposition)
	}
}

// 旁路键与 headers 中的头名保持一致。
func TestSessionDTOHeadersB64KeysMatchHeaders(t *testing.T) {
	t.Parallel()
	f := newFlow("dirty-2",
		withRequest(http.MethodGet, "https://api.example.com/v1/ping"),
		withRequestHeader("x-lower-case", "a\x80b"),
	)
	dto := SessionDTO(f)
	for k := range dto.Request.HeadersB64 {
		if _, found := dto.Request.Headers[k]; !found {
			t.Errorf("旁路键 %q 在 headers 里不存在", k)
		}
	}
	if len(dto.Request.HeadersB64) != 1 {
		t.Fatalf("旁路条目数 = %d, want 1", len(dto.Request.HeadersB64))
	}
}

// 旁路与 headers 使用相同的首值折叠粒度。
func TestFlattenHeadersB64TakesFirstValueOnly(t *testing.T) {
	t.Parallel()
	got, b64 := flattenHeaders(map[string][]string{"X-Tag": {"first-\xe9", "second-\xe9"}})
	if got["X-Tag"] != "first-\xe9" {
		t.Fatalf("明文槽 = %q, want 首值", got["X-Tag"])
	}
	if decodeB64(t, b64["X-Tag"]) != "first-\xe9" {
		t.Fatalf("旁路 = %q, want 首值的字节", b64["X-Tag"])
	}
}

// 明文槽保留原始字符串，由 JSON 边界完成字符串净化。
func TestFlattenHeadersPlainSlotKeepsRawString(t *testing.T) {
	t.Parallel()
	if flow.SanitizeJSONString(latin1Disposition) == latin1Disposition {
		t.Fatal("这条用例要求样本的 sanitize 形态与原串不同,否则断言不成立")
	}
	got, _ := flattenHeaders(map[string][]string{"X-Tag": {latin1Disposition}})
	if got["X-Tag"] != latin1Disposition {
		t.Fatalf("明文槽 = %q, want 原始字符串", got["X-Tag"])
	}
}

// 测量全合法头值路径的旁路检查开销，并以同输入的首值折叠作为基准。
func TestFlattenHeadersSidecarCostsNoAllocationWhenValid(t *testing.T) {
	h := benchHeaders(false)
	var plain, got map[string]string
	var sidecar map[string]string

	baseline := testing.AllocsPerRun(200, func() {
		out := make(map[string]string, len(h))
		for k, v := range h {
			if len(v) == 0 {
				continue
			}
			out[k] = v[0]
		}
		plain = out
	})
	actual := testing.AllocsPerRun(200, func() { got, sidecar = flattenHeaders(h) })

	if actual > baseline {
		t.Fatalf("全合法头的折叠分配 = %v allocs/op, 裸折叠基准 = %v allocs/op", actual, baseline)
	}
	if sidecar != nil {
		t.Fatalf("全合法头的旁路 = %v, want nil", sidecar)
	}
	if len(got) != len(plain) {
		t.Fatalf("折叠结果条目数 = %d, want %d", len(got), len(plain))
	}
}

// 构造器蓝本的旁路与 headers 按下标对齐。
func TestComposeSeedHeadersB64AlignsWithHeaders(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	f := newFlow("seed-b64",
		withRequest(http.MethodGet, "https://api.example.com/v1/ping"),
		withRequestHeader("Content-Disposition", latin1Disposition),
		withRequestHeader("Accept", "*/*"),
		withRawHeaders(
			[2]string{"Host", "api.example.com"},
			[2]string{"Content-Disposition", latin1Disposition},
			[2]string{"Accept", "*/*"},
		),
	)
	svc.ImportFlowStarted(f)

	seed, ok := svc.ComposeSeed("seed-b64")
	if !ok {
		t.Fatal("ComposeSeed 未找到会话")
	}
	if len(seed.HeadersB64) != len(seed.Headers) {
		t.Fatalf("旁路长度 = %d, want %d", len(seed.HeadersB64), len(seed.Headers))
	}
	if seed.HeadersB64[0] != "" || seed.HeadersB64[2] != "" {
		t.Errorf("合法行不该有旁路项: %q", seed.HeadersB64)
	}
	if got := decodeB64(t, seed.HeadersB64[1]); got != latin1Disposition {
		t.Errorf("旁路 = %q, want %q", got, latin1Disposition)
	}
}

// 全部合法的构造器蓝本省略 headersB64 字段。
func TestComposeSeedOmitsHeadersB64WhenAllValid(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	f := newFlow("seed-clean",
		withRequest(http.MethodGet, "https://api.example.com/v1/ping"),
		withRequestHeader("Accept", "*/*"),
		withRawHeaders([2]string{"Host", "api.example.com"}, [2]string{"Accept", "*/*"}),
	)
	svc.ImportFlowStarted(f)

	seed, ok := svc.ComposeSeed("seed-clean")
	if !ok {
		t.Fatal("ComposeSeed 未找到会话")
	}
	if _, found := jsonKeys(t, seed)["headersB64"]; found {
		t.Fatalf("全合法蓝本不应出现 headersB64 键: %q", seed.HeadersB64)
	}
}

// benchHeaders 提供典型请求头样本。
func benchHeaders(dirty bool) map[string][]string {
	h := map[string][]string{
		"Host":                      {"api.example.com"},
		"User-Agent":                {"Mozilla/5.0 (X11; Linux x86_64) sniffy/1.0"},
		"Accept":                    {"application/json, text/plain, */*"},
		"Accept-Language":           {"zh-CN,zh;q=0.9,en;q=0.8"},
		"Accept-Encoding":           {"gzip, deflate, br"},
		"Content-Type":              {"application/json; charset=utf-8"},
		"Content-Length":            {"1024"},
		"Authorization":             {"Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.payload.signature"},
		"Cookie":                    {"sid=abcdef0123456789; theme=dark; lang=zh-CN"},
		"Origin":                    {"https://app.example.com"},
		"Referer":                   {"https://app.example.com/dashboard"},
		"Sec-Fetch-Mode":            {"cors"},
		"Sec-Fetch-Site":            {"same-site"},
		"X-Requested-With":          {"XMLHttpRequest"},
		"X-Request-Id":              {"7f3a2b1c-8d4e-4f6a-9b0c-1d2e3f4a5b6c"},
		"Upgrade-Insecure-Requests": {"1"},
	}
	if dirty {
		h["Content-Disposition"] = []string{latin1Disposition}
	}
	return h
}

var benchFlatten map[string]string

// BenchmarkFlattenHeaders 测量全合法头值路径。
func BenchmarkFlattenHeaders(b *testing.B) {
	h := benchHeaders(false)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchFlatten, _ = flattenHeaders(h)
	}
}

// BenchmarkFlattenHeadersDirty 测量含 Latin-1 头值时的路径。
func BenchmarkFlattenHeadersDirty(b *testing.B) {
	h := benchHeaders(true)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchFlatten, _ = flattenHeaders(h)
	}
}

// BenchmarkSessionDTOMarshal 测量完整会话 DTO 的 JSON 编码成本。
func BenchmarkSessionDTOMarshal(b *testing.B) {
	f := newFlow("bench-1", withRequest(http.MethodGet, "https://api.example.com/v1/ping"))
	f.Request.Header = benchHeaders(false)
	dto := SessionDTO(f)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := json.Marshal(dto.Request); err != nil {
			b.Fatal(err)
		}
	}
}
