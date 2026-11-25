// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package process

import (
	"fmt"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"unicode"
)

const (
	// MaxAppEntries 是应用名单的条目上限。
	MaxAppEntries = 256
	// maxAppEntryLen 是单项的字节数上限，覆盖各平台的路径长度上限（Linux PATH_MAX、Windows 长路径）。
	maxAppEntryLen = 32767
)

// NormalizeAppEntries 校验并规范化应用名单：每项是程序名或本机写法的绝对路径，首尾空白去掉，空项
// 跳过，重复的只留第一个，路径经 filepath.Clean 规范化。Windows 以外的平台不接受反斜杠：那是 Windows
// 的路径写法，当成程序名保存的话永远匹配不上。有不合要求的项时返回错误，指出是哪一项。
func NormalizeAppEntries(entries []string) ([]string, error) {
	out := []string{}
	for _, e := range entries {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		switch {
		case len(e) > maxAppEntryLen || strings.ContainsFunc(e, unicode.IsControl):
			return nil, fmt.Errorf("应用名单里的 %q 不是有效的程序名或路径", e)
		case !hostAppRules.isSeparator('\\') && strings.Contains(e, `\`):
			return nil, fmt.Errorf("应用名单里的 %q 是 Windows 的路径写法，本机用不了", e)
		// 带盘符而不带分隔符的（C:app.exe）是 Windows 上相对于该盘当前目录的路径，不是程序名。
		case strings.ContainsAny(e, hostAppRules.separators) || filepath.VolumeName(e) != "":
			if !filepath.IsAbs(e) {
				return nil, fmt.Errorf("应用名单里的 %q 不是绝对路径：填程序名，或从根目录写起的完整路径", e)
			}
			e = filepath.Clean(e)
		case e == "." || e == "..":
			return nil, fmt.Errorf("应用名单里的 %q 不是有效的程序名", e)
		}
		if !slices.Contains(out, e) {
			out = append(out, e)
		}
	}
	if len(out) > MaxAppEntries {
		return nil, fmt.Errorf("应用名单有 %d 项，超过上限 %d", len(out), MaxAppEntries)
	}
	return out, nil
}

// AppSet 是按程序选取进程的名单，由 NewAppSet 创建，创建后只读、可并发使用。条目有两种：
//
//   - 程序名（不含路径分隔符）：与可执行文件的文件名比较，不区分大小写，Windows 上可以省略 .exe。
//     macOS 上还与可执行文件所在的最外层 .app 包名比较，写不写 .app 都行：「Google Chrome」覆盖包内的
//     Google Chrome Helper，Chrome 的网络请求正是由它发出的。
//   - 绝对路径：选中这个可执行文件，或这个目录下的全部可执行文件。Windows、macOS 上不区分大小写，
//     与两者文件系统的默认行为一致。
type AppSet struct {
	rules appRules
	names map[string]struct{}
	// paths 是折叠后的路径条目，连同符号链接解析后的路径，不带末尾的分隔符（根目录除外）。
	paths []string
}

// appRules 是各平台的匹配规则，测试据此在一个平台上覆盖全部规则。
type appRules struct {
	separators string
	foldPaths  bool
	// trimSuffix 是程序名比较时去掉的后缀：Windows 上的 .exe，macOS 上的 .app（包名常带着它写）。
	trimSuffix string
	appBundles bool
}

var hostAppRules = func() appRules {
	switch runtime.GOOS {
	case "windows":
		return appRules{separators: `\/`, foldPaths: true, trimSuffix: ".exe"}
	case "darwin":
		return appRules{separators: "/", foldPaths: true, trimSuffix: ".app", appBundles: true}
	}
	return appRules{separators: "/"}
}()

// NewAppSet 用规范化的条目（见 NormalizeAppEntries）创建名单。路径条目是符号链接或经过符号链接时，
// 解析后的路径一并加入：Linux 上进程的可执行文件路径是解析过的，/usr/bin/python3 实际是
// /usr/bin/python3.12。
func NewAppSet(entries []string) *AppSet {
	return newAppSet(entries, hostAppRules, filepath.EvalSymlinks)
}

func newAppSet(entries []string, rules appRules, resolve func(string) (string, error)) *AppSet {
	s := &AppSet{rules: rules, names: make(map[string]struct{})}
	for _, e := range entries {
		if !strings.ContainsAny(e, rules.separators) {
			s.names[rules.foldName(e)] = struct{}{}
			continue
		}
		s.addPath(e)
		if resolved, err := resolve(e); err == nil && resolved != e {
			s.addPath(resolved)
		}
	}
	return s
}

func (s *AppSet) addPath(p string) {
	p = s.rules.foldPath(p)
	if len(p) > 1 && s.rules.isSeparator(p[len(p)-1]) {
		p = p[:len(p)-1]
	}
	if !slices.Contains(s.paths, p) {
		s.paths = append(s.paths, p)
	}
}

// Empty 报告名单是否为空，nil 也是空的。
func (s *AppSet) Empty() bool { return s == nil || len(s.names) == 0 && len(s.paths) == 0 }

// Match 报告可执行文件为 path 的进程是否在名单里。name 是进程名的别名，不是可执行文件名时才有意义
// （Linux 上的 comm：会话列表显示的就是它，python3 的可执行文件却是 python3.12），没有时传空。
func (s *AppSet) Match(path, name string) bool {
	return !s.Empty() && (s.matchName(path, name) || s.matchPath(path))
}

// matchName 拿程序名条目比较别名、可执行文件的文件名，以及 macOS 上所在的最外层 .app 包名。
func (s *AppSet) matchName(path, name string) bool {
	if s.hasName(name) {
		return true
	}
	if path == "" {
		return false
	}
	if s.hasName(path[strings.LastIndexAny(path, s.rules.separators)+1:]) {
		return true
	}
	return s.rules.appBundles && s.hasName(s.rules.outerBundle(path))
}

// matchPath 拿路径条目比较：可执行文件就是条目本身，或在条目这个目录下。
func (s *AppSet) matchPath(path string) bool {
	if path == "" {
		return false
	}
	folded := s.rules.foldPath(path)
	for _, p := range s.paths {
		if folded == p {
			return true
		}
		// 只有根目录以分隔符结尾，其余目录要求前缀之后紧跟分隔符，/opt/app 不选中 /opt/application。
		if strings.HasPrefix(folded, p) && (s.rules.isSeparator(p[len(p)-1]) || s.rules.isSeparator(folded[len(p)])) {
			return true
		}
	}
	return false
}

func (s *AppSet) hasName(name string) bool {
	if name == "" {
		return false
	}
	_, ok := s.names[s.rules.foldName(name)]
	return ok
}

func (r appRules) isSeparator(c byte) bool { return strings.IndexByte(r.separators, c) >= 0 }

func (r appRules) foldName(name string) string {
	name = strings.ToLower(name)
	if r.trimSuffix != "" && len(name) > len(r.trimSuffix) {
		name = strings.TrimSuffix(name, r.trimSuffix)
	}
	return name
}

func (r appRules) foldPath(p string) string {
	if r.foldPaths {
		return strings.ToLower(p)
	}
	return p
}

// outerBundle 返回 path 所在的最外层 .app 包的名字（不带 .app），不在包里时为空。
func (r appRules) outerBundle(path string) string {
	for part := range strings.FieldsFuncSeq(path, func(c rune) bool { return c < 0x80 && r.isSeparator(byte(c)) }) {
		if len(part) > len(".app") && strings.EqualFold(part[len(part)-len(".app"):], ".app") {
			return part[:len(part)-len(".app")]
		}
	}
	return ""
}
