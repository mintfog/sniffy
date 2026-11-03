// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package service

import (
	"errors"
	"maps"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/mintfog/sniffy/internal/flow"
)

// 从抓到的 flow 一键生成的规则种类，与前端右键菜单一一对应。
const (
	FlowRuleMock         = "mock"
	FlowRuleBlock        = "block"
	FlowRuleDelay        = "delay"
	FlowRuleRedirect     = "redirect"
	FlowRuleSetReqHeader = "setReqHeader"
	FlowRuleCustom       = "custom"
)

// 错误文本是前端据以本地化的稳定代码，不要改动。
var (
	ErrFlowNotFound        = errors.New("flow_not_found")
	ErrUnknownFlowRule     = errors.New("unknown_rule_kind")
	ErrMockBodyTooLarge    = errors.New("mock_body_too_large")
	ErrMockBodyBinary      = errors.New("mock_body_binary")
	ErrMockBodyUnavailable = errors.New("mock_body_unavailable")
	ErrMockFlowPending     = errors.New("mock_flow_pending")
	ErrMockHasTrailers     = errors.New("mock_has_trailers")
	ErrFlowTargetBinary    = errors.New("rule_target_binary")
)

// maxMockBody 限制固化进 rules.json 的 mock 正文。
const maxMockBody = 1 << 20

const defaultFlowRuleDelayMs = 1000

// mockDroppedHeaders 不随 mock 回放：Content-Type 走单独参数；mock 正文是 identity 字节，
// 长度与编码由出线侧重算（HEAD 例外，见 mockActionFromFlow）；其余是逐跳头。
var mockDroppedHeaders = map[string]bool{
	"content-type":      true,
	"content-length":    true,
	"content-encoding":  true,
	"transfer-encoding": true,
	"connection":        true,
	"keep-alive":        true,
	"proxy-connection":  true,
	"te":                true,
	"trailer":           true,
	"upgrade":           true,
}

// CreateRuleFromFlow 按会话请求的「方法 + 主机 + 路径」精确匹配新建规则，排在所有规则之前，
// 复现契约见 flow/ruleinput.go。进行中的会话仍由转发 goroutine 改写，只读并发安全的字段，也不能生成 mock。
func (s *Service) CreateRuleFromFlow(id, kind, name string) (*InterceptRule, error) {
	f, snapshot, pending, ok := s.sessions.stableView(id)
	if !ok {
		return nil, ErrFlowNotFound
	}
	target := f.RuleInput()
	// 请求未经过规则引擎（尚未到达或是导入的会话）时取录下的请求。
	switch {
	case target != nil:
	case pending:
		target = &flow.RequestTarget{Method: snapshot.Request.Method, Host: snapshot.Request.Host, Path: snapshot.Request.Path}
	case f.Request != nil:
		target = &flow.RequestTarget{Method: f.Request.Method, Host: f.Request.Host, Path: f.Request.Path}
	default:
		return nil, ErrFlowNotFound
	}
	var actions []InterceptAction
	switch kind {
	case FlowRuleMock:
		if pending {
			return nil, ErrMockFlowPending
		}
		a, err := mockActionFromFlow(f)
		if err != nil {
			return nil, err
		}
		actions = []InterceptAction{a}
	case FlowRuleBlock:
		actions = []InterceptAction{{Type: "block", Parameters: map[string]any{}, Enabled: true}}
	case FlowRuleDelay:
		actions = []InterceptAction{{Type: "delay", Parameters: map[string]any{"milliseconds": defaultFlowRuleDelayMs}, Enabled: true}}
	case FlowRuleRedirect:
		// 参数留空：空目标 / 空头名在引擎里是空操作，规则启用也不会改动流量，等用户补全。
		actions = []InterceptAction{{Type: "redirect", Parameters: map[string]any{"url": ""}, Enabled: true}}
	case FlowRuleSetReqHeader:
		actions = []InterceptAction{{Type: "modify_headers", Parameters: map[string]any{"name": "", "value": ""}, Enabled: true}}
	case FlowRuleCustom:
		actions = []InterceptAction{}
	default:
		return nil, ErrUnknownFlowRule
	}
	return s.createFlowRule(name, target, actions)
}

func (s *Service) createFlowRule(name string, t *flow.RequestTarget, actions []InterceptAction) (*InterceptRule, error) {
	// 条件值以 JSON 字符串落盘，非 UTF-8 字节重载后变成 U+FFFD，规则不再命中。
	if !utf8.ValidString(t.Method) || !utf8.ValidString(t.Host) || !utf8.ValidString(t.Path) {
		return nil, ErrFlowTargetBinary
	}
	// 构造器里省略路径的 URL 记成空串，实际出线的路径是 /。
	path := t.Path
	if path == "" {
		path = "/"
	}
	rule := &InterceptRule{
		Name:          name,
		Enabled:       true,
		LogicOperator: "AND",
		Conditions: []InterceptCondition{
			// 引擎默认忽略大小写；方法与路径按原样区分，主机名本就不分大小写。
			{Type: "method", Operator: "equals", Value: t.Method, CaseSensitive: true},
			{Type: "url_host", Operator: "equals", Value: t.Host},
			{Type: "url_path", Operator: "equals", Value: path, CaseSensitive: true},
		},
		Actions: actions,
	}
	return s.rules.createFirst(rule), nil
}

func mockActionFromFlow(f *flow.Flow) (InterceptAction, error) {
	resp := f.Response
	// 流式与透传响应的正文不在 Body 里，截断的只有前半段。
	if resp == nil || resp.BodyUnbuffered || resp.Truncated() {
		return InterceptAction{}, ErrMockBodyUnavailable
	}
	// auto_respond 没有响应尾部可回放，丢掉 Digest / 校验和之类的 Trailer 会让客户端校验失败。
	if len(resp.Trailer) > 0 {
		return InterceptAction{}, ErrMockHasTrailers
	}
	if len(resp.Body) > maxMockBody {
		return InterceptAction{}, ErrMockBodyTooLarge
	}
	// rules.json 以 JSON 字符串保存正文，非 UTF-8 字节无法原样往返。
	if !utf8.Valid(resp.Body) {
		return InterceptAction{}, ErrMockBodyBinary
	}
	// 插件可能追加仅大小写不同的同名头，引擎回放时会规范成同一键相互覆盖，故按规范名合并；
	// 排序遍历让合并顺序与 Content-Type 取值稳定。HEAD 无正文，Content-Length / Content-Encoding 原样保留。
	head := f.Request != nil && f.Request.Method == http.MethodHead
	headers := map[string]any{}
	contentType := ""
	for _, name := range slices.Sorted(maps.Keys(resp.Header)) {
		values := resp.Header[name]
		lower := strings.ToLower(name)
		if lower == "content-type" && contentType == "" && len(values) > 0 {
			contentType = values[0]
		}
		keep := head && (lower == "content-length" || lower == "content-encoding")
		if (mockDroppedHeaders[lower] && !keep) || len(values) == 0 {
			continue
		}
		key := http.CanonicalHeaderKey(name)
		kept, _ := headers[key].([]string)
		headers[key] = append(kept, values...)
	}
	response := map[string]any{
		"status":  resp.Status,
		"headers": headers,
		"body":    string(resp.Body),
		// 原响应没有 Content-Type 时写空串：省略会被引擎补成 application/json。
		"contentType": contentType,
	}
	return InterceptAction{Type: "auto_respond", Parameters: map[string]any{"response": response, "final": true}, Enabled: true}, nil
}
