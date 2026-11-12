// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package service

import (
	"bytes"
	"encoding/json"
	"net"
	"regexp"
	"slices"
	"strings"

	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/internal/hostglob"
)

const (
	maxBizStatusRules  = 64
	maxBizStatusFields = 8
	maxBizSuccess      = 32
	maxBizConfigLen    = 128

	// bizScanLimit 限制单个响应体参与扫描的前缀长度。提取发生在转发 goroutine 上,
	// 关闭透传旁路后响应体可以任意大,不设上限会拖住同一连接的下一个请求。
	bizScanLimit = 4 << 20

	maxBizCodeRunes    = 64
	maxBizMessageRunes = 256
)

// BizStatusRule 描述一组主机的业务状态码取法。
type BizStatusRule struct {
	// Host 为主机通配模式(同解密范围);带端口时连端口一起匹配,否则忽略请求端口。
	Host string `json:"host"`
	// Fields 是候选的顶级字段名,取响应中第一个存在且为标量的。
	Fields []string `json:"fields"`
	// Success 为空表示只展示不判定;否则取值不在其中即视为业务失败。
	Success      []string `json:"success,omitempty"`
	MessageField string   `json:"messageField,omitempty"`
}

func cloneBizStatusRules(rules []BizStatusRule) []BizStatusRule {
	if rules == nil {
		return nil
	}
	out := make([]BizStatusRule, len(rules))
	for i, r := range rules {
		r.Fields = slices.Clone(r.Fields)
		r.Success = slices.Clone(r.Success)
		out[i] = r
	}
	return out
}

// bizStatusRulesPatch 解析配置补丁里的规则列表;形状不对时 ok 为 false,保留原规则。
func bizStatusRulesPatch(v any) (rules []BizStatusRule, ok bool) {
	if v == nil {
		return nil, false
	}
	data, err := json.Marshal(v)
	if err != nil {
		return nil, false
	}
	if err := json.Unmarshal(data, &rules); err != nil {
		return nil, false
	}
	return normalizeBizStatusRules(rules), true
}

// normalizeBizStatusRules 规整主机并去掉空白与无法生效的规则(缺主机、主机模式非法或没有字段)。
func normalizeBizStatusRules(rules []BizStatusRule) []BizStatusRule {
	var out []BizStatusRule
	for _, r := range rules {
		r.Host = hostglob.Clean(r.Host)
		r.Fields = cleanBizList(r.Fields, maxBizStatusFields)
		r.Success = cleanBizList(r.Success, maxBizSuccess)
		r.MessageField = strings.TrimSpace(r.MessageField)
		if len(r.Host) > maxBizConfigLen || len(r.MessageField) > maxBizConfigLen {
			continue
		}
		if len(r.Fields) == 0 || hostglob.Compile(r.Host) == nil {
			continue
		}
		out = append(out, r)
		if len(out) == maxBizStatusRules {
			break
		}
	}
	return out
}

func cleanBizList(in []string, limit int) []string {
	var out []string
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" || len(v) > maxBizConfigLen || slices.Contains(out, v) {
			continue
		}
		out = append(out, v)
		if len(out) == limit {
			break
		}
	}
	return out
}

// bizMatcher 是规则列表的编译结果,整体不可变,经原子指针替换。
// nil 接收者表示未配置规则。
type bizMatcher struct {
	rules []bizRule
}

type bizRule struct {
	host *regexp.Regexp
	// withPort 为真时用完整的 host:port 匹配。
	withPort bool
	fields   []string
	success  []string
	message  string
}

func newBizMatcher(rules []BizStatusRule) *bizMatcher {
	if len(rules) == 0 {
		return nil
	}
	m := &bizMatcher{rules: make([]bizRule, 0, len(rules))}
	for _, r := range rules {
		re := hostglob.Compile(r.Host)
		if re == nil || len(r.Fields) == 0 {
			continue
		}
		_, _, err := net.SplitHostPort(r.Host)
		m.rules = append(m.rules, bizRule{
			host:     re,
			withPort: err == nil,
			fields:   r.Fields,
			success:  r.Success,
			message:  r.MessageField,
		})
	}
	if len(m.rules) == 0 {
		return nil
	}
	return m
}

// match 的 hostport 取自 Host 头,默认端口通常被省略;带端口的规则按 URL scheme 补齐后比较。
func (m *bizMatcher) match(hostport, rawURL string) *bizRule {
	full := strings.ToLower(hostport)
	bare := full
	if h, _, err := net.SplitHostPort(full); err == nil {
		bare = h
	} else if port := defaultPort(rawURL); port != "" {
		full = net.JoinHostPort(strings.Trim(full, "[]"), port)
	}
	for i := range m.rules {
		r := &m.rules[i]
		target := bare
		if r.withPort {
			target = full
		}
		if r.host.MatchString(target) {
			return r
		}
	}
	return nil
}

func defaultPort(rawURL string) string {
	scheme, _, ok := strings.Cut(rawURL, "://")
	if !ok {
		return ""
	}
	switch strings.ToLower(scheme) {
	case "https", "wss":
		return "443"
	case "http", "ws":
		return "80"
	}
	return ""
}

// extract 按首条命中主机的规则取出业务状态;无规则、未命中、响应体不是 JSON 对象
// 或没有可用字段时返回 nil。
func (m *bizMatcher) extract(f *flow.Flow) *flow.BizStatus {
	if m == nil || f.Request == nil || f.Response == nil || len(f.Response.Body) == 0 {
		return nil
	}
	rule := m.match(f.Request.Host, f.Request.URL)
	if rule == nil {
		return nil
	}
	body := f.Response.Body
	if len(body) > bizScanLimit {
		body = body[:bizScanLimit]
	}

	codes := make([]string, len(rule.fields))
	found := make([]bool, len(rule.fields))
	message, messageDone := "", rule.message == ""
	scanTopLevelJSON(body, func(key, raw []byte) bool {
		if !messageDone && jsonKeyEquals(key, rule.message) {
			message, _ = jsonScalarText(raw)
			messageDone = true
		}
		for i, name := range rule.fields {
			if found[i] || !jsonKeyEquals(key, name) {
				continue
			}
			codes[i], found[i] = jsonScalarText(raw)
		}
		// 首选字段与消息都到手后,后面的键不会再改变结果。
		return found[0] && messageDone
	})

	i := slices.Index(found, true)
	if i < 0 {
		return nil
	}
	return &flow.BizStatus{
		Code:    truncateRunes(codes[i], maxBizCodeRunes),
		Message: truncateRunes(message, maxBizMessageRunes),
		Failed:  len(rule.success) > 0 && !slices.Contains(rule.success, codes[i]),
	}
}

// scanTopLevelJSON 遍历 JSON 对象的顶级键值,把带引号的键与值的原始字节交给 visit;
// visit 返回 true 时停止。只做定位、不校验语法,输入不是对象或中途残缺即结束。
func scanTopLevelJSON(b []byte, visit func(key, raw []byte) (stop bool)) {
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
	i := skipJSONSpace(b, 0)
	if i >= len(b) || b[i] != '{' {
		return
	}
	i++
	for {
		i = skipJSONSpace(b, i)
		if i >= len(b) || b[i] != '"' {
			return
		}
		keyEnd := jsonStringEnd(b, i)
		if keyEnd < 0 {
			return
		}
		key := b[i:keyEnd]
		i = skipJSONSpace(b, keyEnd)
		if i >= len(b) || b[i] != ':' {
			return
		}
		i = skipJSONSpace(b, i+1)
		valueEnd := jsonValueEnd(b, i)
		if valueEnd < 0 {
			return
		}
		if visit(key, b[i:valueEnd]) {
			return
		}
		i = skipJSONSpace(b, valueEnd)
		if i >= len(b) || b[i] != ',' {
			return
		}
		i++
	}
}

func skipJSONSpace(b []byte, i int) int {
	for i < len(b) && (b[i] == ' ' || b[i] == '\n' || b[i] == '\r' || b[i] == '\t') {
		i++
	}
	return i
}

// jsonStringEnd 返回起于 b[i](引号)的字符串之后的下标,未闭合时为 -1。
func jsonStringEnd(b []byte, i int) int {
	for j := i + 1; j < len(b); j++ {
		switch b[j] {
		case '\\':
			j++
		case '"':
			return j + 1
		}
	}
	return -1
}

// jsonValueEnd 返回起于 b[i] 的值之后的下标,值残缺时为 -1。
func jsonValueEnd(b []byte, i int) int {
	if i >= len(b) {
		return -1
	}
	switch b[i] {
	case '"':
		return jsonStringEnd(b, i)
	case '{', '[':
		return jsonContainerEnd(b, i)
	}
	for j := i; j < len(b); j++ {
		switch b[j] {
		case ',', '}', ']', ' ', '\n', '\r', '\t':
			if j == i {
				return -1
			}
			return j
		}
	}
	return -1
}

func jsonContainerEnd(b []byte, i int) int {
	depth := 0
	for j := i; j < len(b); j++ {
		switch b[j] {
		case '"':
			end := jsonStringEnd(b, j)
			if end < 0 {
				return -1
			}
			j = end - 1
		case '{', '[':
			depth++
		case '}', ']':
			depth--
			if depth == 0 {
				return j + 1
			}
		}
	}
	return -1
}

// jsonKeyEquals 比较带引号的原始键与字段名;含转义的键才走完整解码。
func jsonKeyEquals(quoted []byte, name string) bool {
	inner := quoted[1 : len(quoted)-1]
	if bytes.IndexByte(inner, '\\') < 0 {
		return string(inner) == name
	}
	var s string
	return json.Unmarshal(quoted, &s) == nil && s == name
}

// jsonScalarText 把字符串、数字、布尔值转成展示文本;数字保留原始字面量,
// 避免大整数经 float64 丢精度。null、对象、数组不算可用取值。
func jsonScalarText(raw []byte) (string, bool) {
	switch c := raw[0]; {
	case c == '"':
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return "", false
		}
		return s, true
	case c == '-' || (c >= '0' && c <= '9'):
		return string(raw), true
	}
	if s := string(raw); s == "true" || s == "false" {
		return s, true
	}
	return "", false
}

func truncateRunes(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	n := 0
	for i := range s {
		if n == limit {
			return s[:i] + "…"
		}
		n++
	}
	return s
}
