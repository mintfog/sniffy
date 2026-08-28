// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package flow

import (
	"encoding/base64"
	"strings"
	"testing"
)

// latin1Disposition 模拟含 Latin-1 文件名的 Content-Disposition 值。
const latin1Disposition = "attachment; filename=\"caf\xe9.pdf\""

// b64 使用线上契约的标准 base64 编码。
func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// 全部合法的头值返回 nil 旁路。
func TestHeaderPairValuesB64NilWhenAllValid(t *testing.T) {
	t.Parallel()
	pairs := [][2]string{{"Host", "x.com"}, {"Accept", "*/*"}, {"X-Cn", "中文也是合法 UTF-8"}}
	if got := HeaderPairValuesB64(pairs); got != nil {
		t.Fatalf("全合法头的旁路 = %q, want nil", got)
	}
}

// 旁路按 pairs 下标对齐，非法值按原始字节编码。
func TestHeaderPairValuesB64AlignsWithPairs(t *testing.T) {
	t.Parallel()
	pairs := [][2]string{
		{"Host", "x.com"},
		{"Content-Disposition", latin1Disposition},
		{"Accept", "*/*"},
	}
	got := HeaderPairValuesB64(pairs)
	if len(got) != len(pairs) {
		t.Fatalf("旁路长度 = %d, want %d", len(got), len(pairs))
	}
	if got[0] != "" || got[2] != "" {
		t.Errorf("合法行不应有旁路项: %q", got)
	}
	raw, err := base64.StdEncoding.DecodeString(got[1])
	if err != nil {
		t.Fatalf("旁路项应是标准 base64: %v", err)
	}
	if string(raw) != latin1Disposition {
		t.Errorf("解出的字节 = %q, want %q", raw, latin1Disposition)
	}
}

// 旁路只编码头值，头名保持文本 token。
func TestHeaderPairValuesB64EncodesValueOnly(t *testing.T) {
	t.Parallel()
	got := HeaderPairValuesB64([][2]string{{"X-Tag", "a\x80b"}})
	raw, err := base64.StdEncoding.DecodeString(got[0])
	if err != nil {
		t.Fatalf("旁路项应是标准 base64: %v", err)
	}
	if string(raw) != "a\x80b" {
		t.Fatalf("旁路项 = %q, want 仅值的字节", raw)
	}
}

// 全合法值的旁路生成保持零分配。
func TestHeaderPairValuesB64ZeroAllocOnValid(t *testing.T) {
	pairs := [][2]string{
		{"Host", "api.example.com"},
		{"User-Agent", "sniffy-test/1.0"},
		{"Accept", "application/json"},
		{"Content-Type", "application/json; charset=utf-8"},
	}
	var sink []string
	if n := testing.AllocsPerRun(200, func() { sink = HeaderPairValuesB64(pairs) }); n != 0 {
		t.Fatalf("全合法头的旁路分配 = %v allocs/op, want 0", n)
	}
	if sink != nil {
		t.Fatal("全合法头不应产出旁路")
	}
}

// 旁路存在时按旁路字节写线，明文值仅用于展示。
func TestResolveEditedHeadersSidecarTakesFullAuthority(t *testing.T) {
	t.Parallel()
	basis := [][2]string{{"X-Tag", "raw-\xe9"}}
	edited := [][2]string{{"X-Tag", SanitizeJSONString("raw-\xe9")}}
	got, err := ResolveEditedHeaders(edited, []string{b64("用户手打的�")}, basis)
	if err != nil {
		t.Fatalf("解析应成功: %v", err)
	}
	if got[0][1] != "用户手打的�" {
		t.Fatalf("值 = %q, want 旁路里的字节", got[0][1])
	}
}

// 空旁路项使用明文值。
func TestResolveEditedHeadersEmptySlotTakesPlainValue(t *testing.T) {
	t.Parallel()
	edited := [][2]string{{"Accept", "*/*"}, {"X-Tag", "unused"}}
	got, err := ResolveEditedHeaders(edited, []string{"", b64("raw-\xe9")}, [][2]string{{"Accept", "old"}})
	if err != nil {
		t.Fatalf("解析应成功: %v", err)
	}
	if got[0][1] != "*/*" {
		t.Errorf("空旁路项应取明文值, got %q", got[0][1])
	}
	if got[1][1] != "raw-\xe9" {
		t.Errorf("非空旁路项应取字节, got %q", got[1][1])
	}
}

// 无旁路客户端使用基准头部恢复原始值。
func TestResolveEditedHeadersFallsBackToBasis(t *testing.T) {
	t.Parallel()
	basis := [][2]string{{"Content-Disposition", latin1Disposition}}
	edited := [][2]string{{"Content-Disposition", SanitizeJSONString(latin1Disposition)}}
	got, err := ResolveEditedHeaders(edited, nil, basis)
	if err != nil {
		t.Fatalf("解析应成功: %v", err)
	}
	if got[0][1] != latin1Disposition {
		t.Fatalf("值 = %q, want 还原成原始字节", got[0][1])
	}
}

// 空头列表与空旁路列表表示清空全部头部。
func TestResolveEditedHeadersEmptyListsClearHeaders(t *testing.T) {
	t.Parallel()
	got, err := ResolveEditedHeaders([][2]string{}, []string{}, [][2]string{{"X-Tag", "raw-\xe9"}})
	if err != nil {
		t.Fatalf("解析应成功: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("清空全部头应得空列表, got %q", got)
	}
}

// 旁路长度必须与头部列表一致。
func TestResolveEditedHeadersLengthMismatchErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		edited [][2]string
		b64s   []string
	}{
		{"旁路多一项", [][2]string{{"A", "1"}}, []string{b64("x"), b64("y")}},
		{"旁路少一项", [][2]string{{"A", "1"}, {"B", "2"}}, []string{b64("x")}},
		{"只发旁路不发明文", nil, []string{b64("raw-\xe9")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ResolveEditedHeaders(tt.edited, tt.b64s, nil)
			if err == nil {
				t.Fatalf("应报错拒绝, got %q", got)
			}
			if got != nil {
				t.Errorf("报错时不应回半成品头列表: %q", got)
			}
		})
	}
}

// 解码错误包含对应头名，便于定位协议字段。
func TestResolveEditedHeadersBadBase64Errors(t *testing.T) {
	t.Parallel()
	edited := [][2]string{{"Accept", "*/*"}, {"Content-Disposition", "inline"}}
	_, err := ResolveEditedHeaders(edited, []string{"", "not base64!!"}, nil)
	if err == nil {
		t.Fatal("非法 base64 应报错")
	}
	if !strings.Contains(err.Error(), "Content-Disposition") {
		t.Errorf("错误信息应指明是哪个头: %v", err)
	}
}

// 旁路使用带填充的标准 base64 编码。
func TestResolveEditedHeadersRejectsNonStdEncodings(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		enc  string
	}{
		{"去填充的 RawStd", base64.RawStdEncoding.EncodeToString([]byte("\xe9"))},
		{"URL 变体", base64.URLEncoding.EncodeToString([]byte{0xfb, 0xef, 0xbe})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := ResolveEditedHeaders([][2]string{{"X-Tag", "x"}}, []string{tt.enc}, nil); err == nil {
				t.Fatalf("%s 应被拒绝", tt.name)
			}
		})
	}
}

// 解析返回独立结果，不修改输入头部。
func TestResolveEditedHeadersDoesNotMutateInput(t *testing.T) {
	t.Parallel()
	edited := [][2]string{{"X-Tag", "shown"}}
	if _, err := ResolveEditedHeaders(edited, []string{b64("raw-\xe9")}, nil); err != nil {
		t.Fatalf("解析应成功: %v", err)
	}
	if edited[0][1] != "shown" {
		t.Fatalf("入参被改写成 %q", edited[0][1])
	}
}

// 旁路字节解析到 pairs 后由 ValidateHeaderPairs 校验报文字符。
func TestResolveEditedHeadersSurfacesInjectedCRLF(t *testing.T) {
	t.Parallel()
	edited := [][2]string{{"X-Tag", "clean"}}
	if err := ValidateHeaderPairs(edited); err != nil {
		t.Fatalf("解码前的 pairs 本就是干净的,用例前提不成立: %v", err)
	}
	got, err := ResolveEditedHeaders(edited, []string{b64("a\r\nX-Injected: 1")}, nil)
	if err != nil {
		t.Fatalf("解析应成功(拦截由校验负责): %v", err)
	}
	if err := ValidateHeaderPairs(got); err == nil {
		t.Fatal("解析后的 pairs 必须能被 ValidateHeaderPairs 拦下")
	}
}
