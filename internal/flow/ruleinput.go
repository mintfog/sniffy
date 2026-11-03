// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package flow

// 钩子会就地改写 flow，会话里只留最终状态。从会话生成的规则排在最前，按请求进入规则引擎时的
// 方法、主机与路径匹配；mock 原样回放客户端收到的响应，不再经过响应钩子。
// 结果不确定的插件与断点里的手工编辑无法复现。

// RequestTarget 是规则条件比对的请求字段。
type RequestTarget struct {
	Method string
	Host   string
	Path   string
}

// SetRuleInput 由规则引擎在请求进入时调用，只记第一次：重复送入钩子链时请求已被改写过。
// 记录位只在 New 中创建，在这里补建会与其他 goroutine 上的 RuleInput 读取竞争。
func (f *Flow) SetRuleInput(r *Request) {
	if f.ruleInput == nil {
		return
	}
	f.ruleInput.CompareAndSwap(nil, &RequestTarget{Method: r.Method, Host: r.Host, Path: r.Path})
}

// RuleInput 返回 SetRuleInput 记下的字段，可并发读取；请求未经过规则引擎（如导入的会话）时为 nil。
func (f *Flow) RuleInput() *RequestTarget {
	if f.ruleInput == nil {
		return nil
	}
	return f.ruleInput.Load()
}

// MarkFinalResponse 标记 Response 是原样回放的 mock，响应钩子跳过它。
func (f *Flow) MarkFinalResponse() { f.finalResponse = true }

// FinalResponse 报告 Response 是否为原样回放的 mock。
func (f *Flow) FinalResponse() bool { return f.finalResponse }
