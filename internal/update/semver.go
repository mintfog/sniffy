// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package update

import (
	"cmp"
	"strconv"
	"strings"
)

// 构建元数据不参与版本优先级比较,解析时丢弃。
type semver struct {
	nums []int64
	pre  []string
}

// parseSemver 解析 "v1.2.3-beta.1+build" 形态的版本号。
// 数字段不足三节时按 0 补齐,兼容带额外数字段的版本号。
func parseSemver(s string) (semver, bool) {
	v := strings.TrimSpace(s)
	v = strings.TrimPrefix(v, "v")
	if v == "" {
		return semver{}, false
	}
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i]
	}
	core, pre := v, ""
	if i := strings.IndexByte(v, '-'); i >= 0 {
		core, pre = v[:i], v[i+1:]
	}
	parts := strings.Split(core, ".")
	nums := make([]int64, 0, max(len(parts), 3))
	for _, p := range parts {
		n, err := strconv.ParseInt(p, 10, 64)
		if err != nil || n < 0 {
			return semver{}, false
		}
		nums = append(nums, n)
	}
	for len(nums) < 3 {
		nums = append(nums, 0)
	}
	out := semver{nums: nums}
	if pre != "" {
		out.pre = strings.Split(pre, ".")
	}
	return out, true
}

func compareSemver(a, b semver) int {
	for i := 0; i < max(len(a.nums), len(b.nums)); i++ {
		if c := cmp.Compare(nthNum(a.nums, i), nthNum(b.nums, i)); c != 0 {
			return c
		}
	}
	switch {
	case len(a.pre) == 0 && len(b.pre) == 0:
		return 0
	case len(a.pre) == 0:
		return 1
	case len(b.pre) == 0:
		return -1
	}
	for i := 0; i < min(len(a.pre), len(b.pre)); i++ {
		if c := comparePreIdent(a.pre[i], b.pre[i]); c != 0 {
			return c
		}
	}
	return cmp.Compare(len(a.pre), len(b.pre))
}

// SemVer 的数字标识符按数值比较,且优先级低于非数字标识符。
func comparePreIdent(a, b string) int {
	an, aErr := strconv.ParseInt(a, 10, 64)
	bn, bErr := strconv.ParseInt(b, 10, 64)
	aNum, bNum := aErr == nil, bErr == nil
	switch {
	case aNum && bNum:
		return cmp.Compare(an, bn)
	case aNum:
		return -1
	case bNum:
		return 1
	default:
		return strings.Compare(a, b)
	}
}

func nthNum(nums []int64, i int) int64 {
	if i < len(nums) {
		return nums[i]
	}
	return 0
}

// IsNewer 判断 candidate 是否严格新于 current;任一侧无法解析时返回 false。
func IsNewer(candidate, current string) bool {
	c, ok := parseSemver(candidate)
	if !ok {
		return false
	}
	cur, ok := parseSemver(current)
	if !ok {
		return false
	}
	return compareSemver(c, cur) > 0
}
