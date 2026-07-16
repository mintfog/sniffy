// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package http

import (
	"net"
	"regexp"
	"strings"
	"sync/atomic"
)

// decryptScope 决定某个 CONNECT 目标主机是否需要 MITM 解密。allow/deny 为按主机模式
// 预编译的正则,匹配在每个 CONNECT 的热路径上执行,故编译一次、原子读取。
type decryptScope struct {
	enabled bool // 对应「启用 HTTPS MITM」总开关:关闭时一律直通不解密
	mode    string
	allow   []*regexp.Regexp
	deny    []*regexp.Regexp
}

// allows 报告目标主机(已去端口、小写)是否应解密。
func (s *decryptScope) allows(host string) bool {
	if !s.enabled {
		return false
	}
	switch s.mode {
	case "allow":
		return matchAnyHost(s.allow, host)
	case "deny":
		return !matchAnyHost(s.deny, host)
	default: // "all" 及未知值:全部解密
		return true
	}
}

// decryptScopePtr 持有当前解密范围(nil = 未配置,退化为全部解密以保持历史行为与独立测试)。
// 由 SetDecryptScope 原子写入,handleConnect 并发读取。
var decryptScopePtr atomic.Pointer[decryptScope]

// SetDecryptScope 由引擎层下发解密范围:enabled 为 MITM 总开关,mode 取
// "all"/"allow"/"deny",allow/deny 为主机通配模式(支持 * 与 *.domain 匹配裸域+子域)。
// 运行时即时生效、并发安全。
func SetDecryptScope(enabled bool, mode string, allow, deny []string) {
	decryptScopePtr.Store(&decryptScope{
		enabled: enabled,
		mode:    mode,
		allow:   compileHostPatterns(allow),
		deny:    compileHostPatterns(deny),
	})
}

// shouldDecrypt 报告某个 CONNECT 目标(host:port)是否应被 MITM 解密。
func shouldDecrypt(hostport string) bool {
	sc := decryptScopePtr.Load()
	if sc == nil {
		return true
	}
	return sc.allows(hostOnly(hostport))
}

// hostOnly 去掉端口并小写,得到用于匹配的主机名。
func hostOnly(hostport string) string {
	h, _, err := net.SplitHostPort(hostport)
	if err != nil {
		h = hostport
	}
	return strings.ToLower(h)
}

func matchAnyHost(res []*regexp.Regexp, host string) bool {
	for _, re := range res {
		if re.MatchString(host) {
			return true
		}
	}
	return false
}

func compileHostPatterns(pats []string) []*regexp.Regexp {
	out := make([]*regexp.Regexp, 0, len(pats))
	for _, p := range pats {
		if re := compileHostPattern(p); re != nil {
			out = append(out, re)
		}
	}
	return out
}

// compileHostPattern 把单条主机模式编译为锚定的正则:字面部分转义,* 通配为 .*;
// 前缀 *. 特殊处理为「同时匹配裸域与任意层级子域」(*.example.com → example.com、a.example.com)。
// 非法/空模式返回 nil。
func compileHostPattern(p string) *regexp.Regexp {
	p = strings.ToLower(strings.TrimSpace(p))
	if p == "" {
		return nil
	}
	var expr string
	if rest, ok := strings.CutPrefix(p, "*."); ok {
		expr = `^(.*\.)?` + globToRegex(rest) + `$`
	} else {
		expr = `^` + globToRegex(p) + `$`
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return nil
	}
	return re
}

// globToRegex 把仅含 * 通配的字面串转为正则片段:各字面段转义,段间以 .* 连接。
func globToRegex(s string) string {
	parts := strings.Split(s, "*")
	for i, part := range parts {
		parts[i] = regexp.QuoteMeta(part)
	}
	return strings.Join(parts, ".*")
}
