// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package flow

// DecisionKind 是插件对一个 Flow 作出的处置类型。
//
// 插件在钩子中"就地修改 Flow"以改写请求/响应内容,并返回一个 Decision
// 表达更进一步的控制(短路、阻断、断点)。管道按优先级合并多个插件的
// Decision,优先级:Abort > Mock > Breakpoint > Continue。
type DecisionKind int

const (
	// Continue 继续正常流程(应用已就地修改的 Flow)。
	Continue DecisionKind = iota
	// Mock 短路:直接使用 Flow.Response 返回,不打上游。
	Mock
	// Abort 阻断:丢弃连接或返回引擎错误页。
	Abort
	// Breakpoint 暂停:把 Flow 回传 UI 手动编辑,放行后继续。
	Breakpoint
)

// String 便于日志。
func (k DecisionKind) String() string {
	switch k {
	case Continue:
		return "continue"
	case Mock:
		return "mock"
	case Abort:
		return "abort"
	case Breakpoint:
		return "breakpoint"
	default:
		return "unknown"
	}
}

// Decision 是钩子返回的处置结果。
type Decision struct {
	Kind          DecisionKind
	StatusOnAbort int    // Abort 时返回给客户端的状态码;0 表示直接关闭连接
	Reason        string // 处置原因(日志 / UI 展示)
	BreakpointOn  Phase  // Breakpoint 时表示在哪个阶段暂停
}

// ContinueDecision 返回一个继续处置。
func ContinueDecision() Decision { return Decision{Kind: Continue} }

// MockDecision 返回一个 mock 短路处置。
func MockDecision(reason string) Decision { return Decision{Kind: Mock, Reason: reason} }

// AbortDecision 返回一个阻断处置。status 为 0 时表示直接关闭连接。
func AbortDecision(status int, reason string) Decision {
	return Decision{Kind: Abort, StatusOnAbort: status, Reason: reason}
}

// BreakpointDecision 返回一个断点处置。
func BreakpointDecision(phase Phase, reason string) Decision {
	return Decision{Kind: Breakpoint, BreakpointOn: phase, Reason: reason}
}

// priority 返回处置类型的优先级(数值越大越优先)。
func (k DecisionKind) priority() int {
	switch k {
	case Abort:
		return 3
	case Mock:
		return 2
	case Breakpoint:
		return 1
	default: // Continue
		return 0
	}
}

// Merge 按优先级合并两个 Decision,返回胜出者。
// Abort > Mock > Breakpoint > Continue;相等时保留已有(先到先得)。
func Merge(acc, next Decision) Decision {
	if next.Kind.priority() > acc.Kind.priority() {
		return next
	}
	return acc
}
