// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package update

import "testing"

func TestCompareSemverOrdering(t *testing.T) {
	t.Parallel()
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.0.1", "1.0.0", 1},
		{"1.1.0", "1.0.9", 1},
		{"2.0.0", "1.99.99", 1},
		{"v1.2.3", "1.2.3", 0},
		{"1.2.3+build.9", "1.2.3+build.1", 0},
		{"1.2", "1.2.0", 0},
		// 先行版本低于同号正式版。
		{"1.0.0-beta.1", "1.0.0", -1},
		{"1.0.0-beta.2", "1.0.0-beta.1", 1},
		{"1.0.0-beta.10", "1.0.0-beta.9", 1},
		{"1.0.0-alpha", "1.0.0-beta", -1},
		{"1.0.0-beta", "1.0.0-beta.1", -1},
		{"1.0.0-rc.1", "1.0.0-beta.9", 1},
		{"1.0.0-beta.1", "1.0.0-beta.alpha", -1},
		{"1.2.3.1", "1.2.3", 1},
	}
	for _, c := range cases {
		a, ok := parseSemver(c.a)
		if !ok {
			t.Fatalf("parseSemver(%q) 解析失败", c.a)
		}
		b, ok := parseSemver(c.b)
		if !ok {
			t.Fatalf("parseSemver(%q) 解析失败", c.b)
		}
		if got := compareSemver(a, b); got != c.want {
			t.Errorf("compareSemver(%q, %q) = %d,期望 %d", c.a, c.b, got, c.want)
		}
		if got := compareSemver(b, a); got != -c.want {
			t.Errorf("compareSemver(%q, %q) = %d,期望 %d(与反向比较不一致)", c.b, c.a, got, -c.want)
		}
	}
}

func TestParseSemverRejectsNonVersions(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"", "  ", "v", "latest", "1.x.0", "nightly-20260918", "-1.0.0"} {
		if _, ok := parseSemver(s); ok {
			t.Errorf("parseSemver(%q) 不该解析成功", s)
		}
	}
}

func TestIsNewer(t *testing.T) {
	t.Parallel()
	cases := []struct {
		candidate, current string
		want               bool
	}{
		{"1.1.0", "1.0.0", true},
		{"1.0.0", "1.0.0", false},
		{"1.0.0", "1.1.0", false},
		{"1.0.0", "1.0.0-beta.1", true},
		// 无法解析的一侧一律不提醒,免得自定义版本号天天误报。
		{"1.0.0", "内部构建", false},
		{"内部构建", "1.0.0", false},
	}
	for _, c := range cases {
		if got := IsNewer(c.candidate, c.current); got != c.want {
			t.Errorf("IsNewer(%q, %q) = %v,期望 %v", c.candidate, c.current, got, c.want)
		}
	}
}

func TestIsDevBuild(t *testing.T) {
	t.Parallel()
	for _, v := range []string{"", "  ", "0.0.0-dev"} {
		if !IsDevBuild(v) {
			t.Errorf("IsDevBuild(%q) = false,期望 true", v)
		}
	}
	if IsDevBuild("1.2.3") {
		t.Error("IsDevBuild(\"1.2.3\") = true,期望 false")
	}
}
