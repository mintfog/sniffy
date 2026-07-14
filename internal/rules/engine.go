// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

// Package rules 把 service 中持久化的重写规则(InterceptRule)转成 pipeline 钩子,
// 在请求/响应阶段真正应用到流量上——这是"规则被存下来却不生效"链路的补全。
//
// 它作为常驻核心钩子(pipeline.RegisterCore)注册,每次都从 service 实时读取规则,
// 因此前端增删改规则后立即对新流量生效,无需重新注册。
//
// 规则的条件类型 / 操作符 / 动作类型 / 参数键与前端 web/src/types(InterceptRule)
// 及 web/src/workbench/lib/rulesMap.ts 严格对齐;改一处需同步另一处。
package rules

import (
	"context"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/internal/service"
)

// Provider 提供当前规则列表(由 service.Service 实现:Rules())。
type Provider func() []*service.InterceptRule

// Engine 是规则引擎,实现 pipeline.RequestHook + pipeline.ResponseHook。
type Engine struct {
	provider Provider
}

// New 创建规则引擎。
func New(p Provider) *Engine { return &Engine{provider: p} }

// ---- pipeline.Hook ----

func (e *Engine) Name() string      { return "rules-engine" }
func (e *Engine) Priority() int     { return 0 }
func (e *Engine) Enabled() bool     { return true }
func (e *Engine) Match(string) bool { return true }

// sortedRules 返回启用的规则,按 Priority 升序(数值小者先)。
func (e *Engine) sortedRules() []*service.InterceptRule {
	if e.provider == nil {
		return nil
	}
	all := e.provider()
	out := make([]*service.InterceptRule, 0, len(all))
	for _, r := range all {
		if r != nil && r.Enabled {
			out = append(out, r)
		}
	}
	// 稳定插入排序(N 很小,避免引入 sort 依赖的同时保持稳定)。
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Priority < out[j-1].Priority; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// ---- pipeline.RequestHook ----

// OnRequest 在请求阶段按优先级应用匹配规则的请求类动作。
// 遇到 block→Abort、auto_respond→Mock 时短路返回。
func (e *Engine) OnRequest(_ context.Context, f *flow.Flow) flow.Decision {
	if f.Request == nil {
		return flow.ContinueDecision()
	}
	for _, r := range e.sortedRules() {
		if !e.matches(r, f, flow.PhaseRequest) {
			continue
		}
		for _, a := range r.Actions {
			switch a.Type {
			case "block":
				status := getInt(a.Parameters, "statusCode")
				if status == 0 {
					status = 403 // 默认返回 403 阻断页(而非裸关连接),符合"阻断"语义且更易诊断
				}
				return flow.AbortDecision(status, firstNonEmpty(getStr(a.Parameters, "message"), "blocked by rule: "+r.Name))
			case "auto_respond":
				e.applyAutoRespond(f, a.Parameters)
				f.Modified = true
				return flow.MockDecision("mocked by rule: " + r.Name)
			case "redirect":
				if applyRedirect(f, getStr(a.Parameters, "url")) {
					f.Modified = true
				}
			case "modify_url":
				if applyModifyURL(f, getStr(a.Parameters, "urlPattern"), getStr(a.Parameters, "replacement")) {
					f.Modified = true
				}
			case "modify_method":
				if m := getStr(a.Parameters, "newMethod"); m != "" {
					f.Request.Method = m
					f.Modified = true
				}
			case "modify_headers":
				if applyHeaderOps(f.Request.Header, a.Parameters, "headers") {
					f.Modified = true
					// 设置 Host 头时需同步到 Request.Host:net/http 从 req.Host 取 Host 行,
					// 忽略 Header["Host"],否则覆盖 Host 会静默失效。
					if strings.EqualFold(getStr(a.Parameters, "name"), "Host") {
						if v := getStr(a.Parameters, "value"); v != "" {
							f.Request.Host = v
						}
					}
				}
			case "modify_body":
				if applyBodyReplace(&f.Request.Body, getStr(a.Parameters, "bodyPattern"), getStr(a.Parameters, "bodyReplacement")) {
					f.Modified = true
				}
			case "replace_body":
				f.Request.Body = []byte(getStr(a.Parameters, "body"))
				// 含 CR/LF 的值经保真写线逐字节透传会造成头注入,故非法 Content-Type 丢弃不改原头。
				if ct := getStr(a.Parameters, "contentType"); ct != "" && !strings.ContainsAny(ct, "\r\n") {
					if f.Request.Header == nil {
						f.Request.Header = map[string][]string{}
					}
					f.Request.Header["Content-Type"] = []string{ct}
				}
				f.Modified = true
			case "delay":
				applyDelay(a.Parameters)
			}
		}
	}
	return flow.ContinueDecision()
}

// ---- pipeline.ResponseHook ----

// OnResponse 在响应阶段按优先级应用匹配规则的响应类动作。
func (e *Engine) OnResponse(_ context.Context, f *flow.Flow) flow.Decision {
	if f.Response == nil {
		return flow.ContinueDecision()
	}
	for _, r := range e.sortedRules() {
		if !e.matches(r, f, flow.PhaseResponse) {
			continue
		}
		for _, a := range r.Actions {
			switch a.Type {
			case "modify_status":
				if s := getInt(a.Parameters, "statusCode"); s > 0 {
					f.Response.Status = s
					f.Response.StatusText = ""
					f.Modified = true
				}
			case "modify_response_headers":
				if applyHeaderOps(f.Response.Header, a.Parameters, "responseHeaders") {
					f.Modified = true
				}
			case "modify_response_body":
				if applyBodyReplace(&f.Response.Body, getStr(a.Parameters, "responseBodyPattern"), getStr(a.Parameters, "responseBodyReplacement")) {
					f.Modified = true
				}
			}
		}
	}
	return flow.ContinueDecision()
}

// ---- 条件匹配 ----

func (e *Engine) matches(r *service.InterceptRule, f *flow.Flow, phase flow.Phase) bool {
	if len(r.Conditions) == 0 {
		return true // 无条件:匹配所有流量
	}
	or := strings.EqualFold(r.LogicOperator, "OR")
	for _, c := range r.Conditions {
		ok := evalCondition(c, f, phase)
		if or {
			if ok {
				return true
			}
		} else if !ok {
			return false
		}
	}
	return !or // AND 全过 → true;OR 全不过 → false
}

func evalCondition(c service.InterceptCondition, f *flow.Flow, _ flow.Phase) bool {
	field, ok := fieldValue(c, f)
	if !ok {
		return false // 字段在当前阶段不可用(如请求阶段取 response_status)
	}
	res := matchOp(c, field)
	if c.Negate {
		res = !res
	}
	return res
}

// matchOp 处理需要 Value2 / 数值 / 存在性 / 列表语义的操作符,其余委托给字符串 compare。
func matchOp(c service.InterceptCondition, field string) bool {
	val := stringify(c.Value)
	switch c.Operator {
	case "exists":
		return field != ""
	case "not_exists":
		return field == ""
	case "greater_than", "less_than":
		fn, e1 := strconv.ParseFloat(strings.TrimSpace(field), 64)
		vn, e2 := strconv.ParseFloat(strings.TrimSpace(val), 64)
		if e1 != nil || e2 != nil {
			return false
		}
		if c.Operator == "greater_than" {
			return fn > vn
		}
		return fn < vn
	case "between":
		fn, e1 := strconv.ParseFloat(strings.TrimSpace(field), 64)
		lo, e2 := strconv.ParseFloat(strings.TrimSpace(val), 64)
		hi, e3 := strconv.ParseFloat(strings.TrimSpace(stringify(c.Value2)), 64)
		if e1 != nil || e2 != nil || e3 != nil {
			return false
		}
		return fn >= lo && fn <= hi
	case "in_list", "not_in_list":
		in := false
		for _, item := range strings.Split(val, ",") {
			if strings.TrimSpace(item) == field {
				in = true
				break
			}
		}
		if c.Operator == "not_in_list" {
			return !in
		}
		return in
	default:
		return compare(c.Operator, field, val, c.CaseSensitive)
	}
}

func fieldValue(c service.InterceptCondition, f *flow.Flow) (string, bool) {
	req := f.Request
	switch c.Type {
	case "url":
		return req.URL, true
	case "url_host":
		return req.Host, true
	case "url_path":
		return req.Path, true
	case "url_query":
		if u, err := url.Parse(req.URL); err == nil {
			return u.RawQuery, true
		}
		return "", true
	case "method":
		return req.Method, true
	case "scheme":
		if u, err := url.Parse(req.URL); err == nil {
			return u.Scheme, true
		}
		return "", true
	case "request_header":
		return headerValue(req.Header, c.HeaderName), true
	case "user_agent":
		return headerValue(req.Header, "User-Agent"), true
	case "content_type":
		if f.Response != nil {
			return headerValue(f.Response.Header, "Content-Type"), true
		}
		return headerValue(req.Header, "Content-Type"), true
	case "response_status":
		if f.Response == nil {
			return "", false
		}
		return strconv.Itoa(f.Response.Status), true
	case "response_header":
		if f.Response == nil {
			return "", false
		}
		return headerValue(f.Response.Header, c.HeaderName), true
	default:
		return "", false
	}
}

func compare(op, field, val string, caseSensitive bool) bool {
	switch op {
	case "regex", "not_regex":
		pat := val
		if !caseSensitive {
			pat = "(?i)" + pat
		}
		re, err := regexp.Compile(pat)
		if err != nil {
			return false
		}
		m := re.MatchString(field)
		if op == "not_regex" {
			return !m
		}
		return m
	}
	if !caseSensitive {
		field = strings.ToLower(field)
		val = strings.ToLower(val)
	}
	switch op {
	case "equals":
		return field == val
	case "not_equals":
		return field != val
	case "contains":
		return strings.Contains(field, val)
	case "not_contains":
		return !strings.Contains(field, val)
	case "starts_with":
		return strings.HasPrefix(field, val)
	case "ends_with":
		return strings.HasSuffix(field, val)
	case "is_empty":
		return field == ""
	case "not_empty":
		return field != ""
	default:
		return false
	}
}

// ---- 动作应用 ----

// applyRedirect 把请求重定向到 target。target 仅含 scheme://host 时保留原 path/query;
// 含 path 时整体替换。返回是否改动。
func applyRedirect(f *flow.Flow, target string) bool {
	target = strings.TrimSpace(target)
	if target == "" {
		return false
	}
	u, err := url.Parse(f.Request.URL)
	if err != nil {
		return false
	}
	// 无 scheme 的目标(如 "localhost:3000" / "example.com" / "127.0.0.1:3000")会被 url.Parse
	// 误判为 scheme/opaque 或 path,导致 host 没被替换。补上原请求的 scheme 再解析,按 host[:port] 处理。
	if !strings.Contains(target, "://") {
		scheme := u.Scheme
		if scheme == "" {
			scheme = "http"
		}
		target = scheme + "://" + target
	}
	t, err := url.Parse(target)
	if err != nil {
		return false
	}
	if t.Scheme != "" {
		u.Scheme = t.Scheme
	}
	if t.Host != "" {
		u.Host = t.Host
	}
	if t.Path != "" && t.Path != "/" {
		u.Path = t.Path
	}
	if t.RawQuery != "" {
		u.RawQuery = t.RawQuery
	}
	f.Request.URL = u.String()
	f.Request.Host = u.Host
	f.Request.Path = u.Path
	return true
}

// applyModifyURL 用正则 pattern→replacement 改写整条 URL,并回填 Host/Path。
func applyModifyURL(f *flow.Flow, pattern, replacement string) bool {
	if pattern == "" {
		return false
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return false
	}
	newURL := re.ReplaceAllString(f.Request.URL, replacement)
	if newURL == f.Request.URL {
		return false
	}
	f.Request.URL = newURL
	if u, err := url.Parse(newURL); err == nil {
		f.Request.Host = u.Host
		f.Request.Path = u.Path
	}
	return true
}

// applyHeaderOps 应用头操作。支持两种形态:
//   - 扁平:parameters.name + parameters.value(设置单个头)
//   - 结构化:parameters[structKey].{modify|add}(map) / .remove([])
//
// structKey 区分请求头("headers")与响应头("responseHeaders"),与 web/src/types
// 的 ActionParameters 形状对齐。
func applyHeaderOps(h map[string][]string, params map[string]any, structKey string) bool {
	if h == nil || params == nil {
		return false
	}
	changed := false

	// 扁平单头。
	if name := getStr(params, "name"); name != "" {
		h[canonicalHeaderKey(name)] = []string{getStr(params, "value")}
		changed = true
	}

	hops, _ := params[structKey].(map[string]any)
	if hops == nil {
		return changed
	}
	for _, key := range []string{"add", "modify"} {
		if m, ok := hops[key].(map[string]any); ok {
			for k, v := range m {
				h[canonicalHeaderKey(k)] = []string{stringify(v)}
				changed = true
			}
		}
	}
	if rm, ok := hops["remove"].([]any); ok {
		for _, name := range rm {
			delete(h, canonicalHeaderKey(stringify(name)))
			changed = true
		}
	}
	return changed
}

// applyBodyReplace 在 body 上做字面子串替换 pattern→replacement。
func applyBodyReplace(body *[]byte, pattern, replacement string) bool {
	if pattern == "" || body == nil {
		return false
	}
	s := string(*body)
	ns := strings.ReplaceAll(s, pattern, replacement)
	if ns == s {
		return false
	}
	*body = []byte(ns)
	return true
}

// applyAutoRespond 用 parameters.response 直接构造响应(mock,不打上游)。
func (e *Engine) applyAutoRespond(f *flow.Flow, params map[string]any) {
	resp, _ := params["response"].(map[string]any)
	status := 200
	body := ""
	contentType := "application/json"
	if resp != nil {
		if s := getInt(resp, "status"); s > 0 {
			status = s
		}
		body = getStr(resp, "body")
		if ct := getStr(resp, "contentType"); ct != "" {
			contentType = ct
		}
	}
	header := map[string][]string{"Content-Type": {contentType}}
	if resp != nil {
		if hs, ok := resp["headers"].(map[string]any); ok {
			for k, v := range hs {
				header[canonicalHeaderKey(k)] = []string{stringify(v)}
			}
		}
	}
	f.Response = &flow.Response{
		Status: status,
		Header: header,
		Body:   []byte(body),
	}
}

// applyDelay 按 parameters.milliseconds 阻塞当前处理 goroutine。
func applyDelay(params map[string]any) {
	ms := getInt(params, "milliseconds")
	if ms <= 0 {
		return
	}
	if ms > 60000 {
		ms = 60000 // 上限 60s,防止误配置长时间占用连接
	}
	time.Sleep(time.Duration(ms) * time.Millisecond)
}

// ---- 工具 ----

func headerValue(h map[string][]string, name string) string {
	if h == nil || name == "" {
		return ""
	}
	ck := canonicalHeaderKey(name)
	if v, ok := h[ck]; ok {
		return strings.Join(v, ", ")
	}
	// 大小写不敏感回退。
	for k, v := range h {
		if strings.EqualFold(k, name) {
			return strings.Join(v, ", ")
		}
	}
	return ""
}

// canonicalHeaderKey 规范化头名(首字母大写,连字符分段),与 net/http 一致。
func canonicalHeaderKey(name string) string {
	parts := strings.Split(name, "-")
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + strings.ToLower(p[1:])
	}
	return strings.Join(parts, "-")
}

func stringify(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case float64:
		// JSON 数字统一为 float64;整数去掉小数点。
		if x == float64(int64(x)) {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	case int:
		return strconv.Itoa(x)
	default:
		return ""
	}
}

func getStr(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	return stringify(m[key])
}

func getInt(m map[string]any, key string) int {
	if m == nil {
		return 0
	}
	switch x := m[key].(type) {
	case float64:
		return int(x)
	case int:
		return x
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(x))
		return n
	default:
		return 0
	}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
