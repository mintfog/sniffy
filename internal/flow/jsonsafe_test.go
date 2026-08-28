// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package flow

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

// jsonRoundTrip 返回字符串经过 encoding/json 边界后的形态。
func jsonRoundTrip(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("json.Marshal(%q): %v", s, err)
	}
	var out string
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("json.Unmarshal(%s): %v", b, err)
	}
	return out
}

// SanitizeJSONString 与 encoding/json 对非法 UTF-8 的处理保持逐字节一致。
func TestSanitizeJSONStringMatchesEncodingJSON(t *testing.T) {
	cases := []struct{ name, in string }{
		{"空串", ""},
		{"纯 ASCII", "attachment; filename=\"ok.pdf\""},
		{"NUL 与 DEL", "a\x00b\x7fc"},
		{"合法多字节与 emoji", "中文\U0001F600"},
		{"孤立 continuation 字节", "a\x80b"},
		{"连续两个非法字节", "\xff\xfe"},
		{"latin1 文件名", "attachment; filename=\"caf\xe9.pdf\""},
		{"截断的多字节序列", "x\xe4\xb8"},
		{"过长编码", "\xc0\x80"},
		{"UTF-8 编码的代理区码点", "\xed\xa0\x80"},
		{"合法与非法交错", "\x00\xff\xfe\x41\x80"},
		{"结尾单个非法字节", "ok\xff"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			want := jsonRoundTrip(t, c.in)
			got := SanitizeJSONString(c.in)
			if got != want {
				t.Fatalf("SanitizeJSONString(%q) = %q (% x), encoding/json 得到 %q (% x)", c.in, got, got, want, want)
			}
			if !utf8.ValidString(got) {
				t.Fatalf("结果仍含非法 UTF-8: % x", got)
			}
		})
	}
}

// 连续非法字节分别替换为 U+FFFD，与 encoding/json 的逐字节规则一致。
func TestSanitizeJSONStringReplacesPerByte(t *testing.T) {
	const in = "\x00\xff\xfe\x41\x80"
	const want = "\x00\uFFFD\uFFFD\x41\uFFFD"

	if got := SanitizeJSONString(in); got != want {
		t.Fatalf("SanitizeJSONString(% x) = %q (% x), 期望三个替换符 %q", in, got, got, want)
	}
	if folded := strings.ToValidUTF8(in, "\uFFFD"); folded == want {
		t.Fatal("strings.ToValidUTF8 与逐字节替换给出了相同结果,本用例已失去区分力,请换一组更强的料")
	}
}

// 合法 UTF-8 输入使用零分配快路径。
func TestSanitizeJSONStringZeroAllocOnValid(t *testing.T) {
	s := strings.Repeat("x-forwarded-for: 1.1.1.1, ", 8) + "中文"
	var sink string
	if n := testing.AllocsPerRun(100, func() { sink = SanitizeJSONString(s) }); n != 0 {
		t.Fatalf("合法输入每次调用分配了 %v 次", n)
	}
	if sink != s {
		t.Fatalf("合法输入被改写: %q", sink)
	}
}

var sinkString string

// BenchmarkSanitizeJSONString 覆盖 ASCII、合法多字节和非法字节三类头值。
func BenchmarkSanitizeJSONString(b *testing.B) {
	cases := []struct {
		name string
		s    string
	}{
		{"ASCII", "bench-value-User-Agent; q=0.9"},
		{"CJK", "中文头值中文头值中文头值; q=0.9"},
		{"Invalid", "attachment; filename=\"\xe4\xf6\xfc\xff.txt\""},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				sinkString = SanitizeJSONString(c.s)
			}
		})
	}
}
