// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package http

import "testing"

func TestCompileHostPatternMatch(t *testing.T) {
	cases := []struct {
		pattern string
		host    string
		want    bool
	}{
		// 精确匹配(去端口、大小写不敏感由 hostOnly 负责,这里直接给小写)
		{"example.com", "example.com", true},
		{"example.com", "api.example.com", false},
		{"example.com", "notexample.com", false},
		// *.domain 同时匹配裸域与任意层级子域
		{"*.example.com", "example.com", true},
		{"*.example.com", "api.example.com", true},
		{"*.example.com", "a.b.example.com", true},
		{"*.example.com", "example.com.evil.com", false},
		{"*.example.com", "evilexample.com", false},
		// 中缀通配
		{"api.*.com", "api.foo.com", true},
		{"api.*.com", "api.foo.bar.com", true},
		{"api.*.com", "api.com", false},
		// 点号需按字面转义,不能被正则当作任意字符
		{"a.b.com", "axb.com", false},
	}
	for _, c := range cases {
		re := compileHostPattern(c.pattern)
		if re == nil {
			t.Fatalf("pattern %q 编译为 nil", c.pattern)
		}
		if got := re.MatchString(c.host); got != c.want {
			t.Errorf("pattern %q host %q: got %v want %v", c.pattern, c.host, got, c.want)
		}
	}
}

func TestCompileHostPatternEmpty(t *testing.T) {
	for _, p := range []string{"", "   ", "\t"} {
		if re := compileHostPattern(p); re != nil {
			t.Errorf("空模式 %q 应返回 nil", p)
		}
	}
}

func TestDecryptScopeAllows(t *testing.T) {
	cases := []struct {
		name    string
		enabled bool
		mode    string
		allow   []string
		deny    []string
		host    string
		want    bool
	}{
		{"mitm 关闭一律不解密", false, "all", nil, nil, "example.com", false},
		{"all 模式全部解密", true, "all", nil, nil, "example.com", true},
		{"未知模式按 all", true, "", nil, nil, "example.com", true},
		{"allow 命中白名单", true, "allow", []string{"*.example.com"}, nil, "api.example.com", true},
		{"allow 未命中直通", true, "allow", []string{"*.example.com"}, nil, "other.com", false},
		{"allow 空白名单不解密", true, "allow", nil, nil, "example.com", false},
		{"deny 命中黑名单直通", true, "deny", nil, []string{"*.example.com"}, "api.example.com", false},
		{"deny 未命中照常解密", true, "deny", nil, []string{"*.example.com"}, "other.com", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sc := &decryptScope{
				enabled: c.enabled,
				mode:    c.mode,
				allow:   compileHostPatterns(c.allow),
				deny:    compileHostPatterns(c.deny),
			}
			if got := sc.allows(c.host); got != c.want {
				t.Errorf("allows(%q) = %v, want %v", c.host, got, c.want)
			}
		})
	}
}

func TestShouldDecryptStripsPortAndCase(t *testing.T) {
	SetDecryptScope(true, "allow", []string{"example.com"}, nil)
	t.Cleanup(func() { decryptScopePtr.Store(nil) })

	if !shouldDecrypt("EXAMPLE.com:443") {
		t.Error("大小写/端口应被规整后命中白名单")
	}
	if shouldDecrypt("other.com:443") {
		t.Error("非白名单主机应直通")
	}
}

func TestShouldDecryptNilScopeDefaultsAll(t *testing.T) {
	decryptScopePtr.Store(nil)
	if !shouldDecrypt("anything.com:443") {
		t.Error("未配置解密范围时应默认全部解密")
	}
}
