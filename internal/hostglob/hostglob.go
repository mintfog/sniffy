// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

// Package hostglob 编译用户填写的主机通配模式。
package hostglob

import (
	"regexp"
	"strings"
)

// Compile 把单条主机模式编译为锚定的正则:字面部分转义,* 通配为 .*;
// 前缀 *. 特殊处理为「同时匹配裸域与任意层级子域」(*.example.com → example.com、a.example.com)。
// 模式按小写匹配,调用方需传入小写主机。非法/空模式返回 nil。
func Compile(p string) *regexp.Regexp {
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

// Clean 把用户填写的主机规整成模式:去空白、转小写,并兼容直接粘贴的 URL——
// 去掉 scheme:// 前缀与路径、查询、片段,保留端口。
func Clean(p string) string {
	p = strings.ToLower(strings.TrimSpace(p))
	if _, rest, ok := strings.Cut(p, "://"); ok {
		p = rest
	}
	if i := strings.IndexAny(p, "/?#"); i >= 0 {
		p = p[:i]
	}
	return p
}
