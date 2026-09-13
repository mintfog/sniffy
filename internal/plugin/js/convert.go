// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package js

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/mintfog/sniffy/internal/flow"
)

// 进出 VM 的 flow 视图（请求字段扁平到顶层，响应位于 response 下）。
type jsFlow struct {
	ID       string            `json:"id"`
	Method   string            `json:"method,omitempty"`
	URL      string            `json:"url,omitempty"`
	Host     string            `json:"host,omitempty"`
	Path     string            `json:"path,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
	Body     string            `json:"body,omitempty"`
	BodyB64  string            `json:"bodyB64,omitempty"`
	Response *jsResponse       `json:"response,omitempty"`
	Process  *jsProcess        `json:"process,omitempty"`

	// WS / 流（SSE、gRPC、分块）专用字段。
	Direction string `json:"direction,omitempty"`
	Type      string `json:"type,omitempty"`
	Data      string `json:"data,omitempty"`
	DataB64   string `json:"dataB64,omitempty"`
	Kind      string `json:"kind,omitempty"`      // 流类型:sse|grpc|chunk
	EventType string `json:"eventType,omitempty"` // SSE 的 event 名
}

type jsResponse struct {
	Status     int               `json:"status"`
	StatusText string            `json:"statusText,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
	Body       string            `json:"body,omitempty"`
	BodyB64    string            `json:"bodyB64,omitempty"`
	Reason     string            `json:"reason,omitempty"`
}

type jsProcess struct {
	Name string `json:"name,omitempty"`
	PID  uint32 `json:"pid,omitempty"`
	Path string `json:"path,omitempty"`
}

type jsDecision struct {
	Kind   string `json:"kind"`
	Status int    `json:"status"`
	Reason string `json:"reason"`
}

type jsOut struct {
	Flow     jsFlow     `json:"flow"`
	Decision jsDecision `json:"decision"`
}

// ---- 转换 ----

// payloadToJS 将合法 UTF-8 载荷放入文本字段，其余载荷放入标准 base64 字段。
func payloadToJS(b []byte) (text, b64 string) {
	if len(b) == 0 {
		return "", ""
	}
	if utf8.Valid(b) {
		return string(b), ""
	}
	return "", base64.StdEncoding.EncodeToString(b)
}

// payloadFromJS 按文本优先级还原脚本回传的载荷字节；文本为空时解码 b64。
// ok 表示载荷是否成功解析。
func payloadFromJS(text, b64 string) ([]byte, bool) {
	if text == "" && b64 != "" {
		raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
		if err != nil {
			return nil, false
		}
		return raw, true
	}
	return []byte(text), true
}

// resolvePayload 按通道优先级解析载荷，并记录字段冲突与解码错误。
func resolvePayload(text, b64, field, failMsg string, logf func(level, msg string)) ([]byte, bool) {
	if text != "" && b64 != "" {
		emitLog(logf, "error", field+" 与 "+field+"B64 同时有值,已按 "+field+" 为准;"+
			"要走二进制通道请先把 "+field+" 置为空串")
	}
	b, ok := payloadFromJS(text, b64)
	if !ok {
		emitLog(logf, "error", failMsg)
	}
	return b, ok
}

func emitLog(logf func(level, msg string), level, msg string) {
	if logf != nil {
		logf(level, msg)
	}
}

// 严格解码下各字段要求的 JSON 值形态。
// 's' 字符串、'n' 数字、'h' 字符串值对象（扁平头视图）、'o' 对象。
var (
	jsFlowFieldKinds = map[string]byte{
		"id": 's', "method": 's', "url": 's', "host": 's', "path": 's',
		"headers": 'h', "body": 's', "bodyB64": 's', "response": 'o', "process": 'o',
		"direction": 's', "type": 's', "data": 's', "dataB64": 's', "kind": 's', "eventType": 's',
	}
	jsResponseFieldKinds = map[string]byte{
		"status": 'n', "statusText": 's', "headers": 'h', "body": 's', "bodyB64": 's', "reason": 's',
	}
	jsProcessFieldKinds  = map[string]byte{"name": 's', "pid": 'n', "path": 's'}
	jsDecisionFieldKinds = map[string]byte{"kind": 's', "status": 'n', "reason": 's'}
)

// parseOut 先按字段类型严格解码；类型不匹配时清理对应字段并记录，其余字段继续生效。
func parseOut(out []byte, logf func(level, msg string)) (jsOut, dropSet, bool) {
	var res jsOut
	if json.Unmarshal(out, &res) == nil {
		return res, nil, true
	}
	var doc map[string]any
	if json.Unmarshal(out, &doc) != nil {
		emitLog(logf, "error", "插件返回的 flow 无法解析,本次改动与处置已忽略")
		return jsOut{}, nil, false
	}
	dropped := dropSet{}
	if fl, ok := doc["flow"].(map[string]any); ok {
		pruneJSObject(fl, jsFlowFieldKinds, "flow", dropped, logf)
		if r, ok := fl["response"].(map[string]any); ok {
			pruneJSObject(r, jsResponseFieldKinds, "flow.response", dropped, logf)
		}
		if pr, ok := fl["process"].(map[string]any); ok {
			pruneJSObject(pr, jsProcessFieldKinds, "flow.process", dropped, logf)
		}
	} else {
		// flow 需要对象形态；其他形态的可编辑字段沿用原值。
		if v, present := doc["flow"]; present && v != nil {
			emitLog(logf, "error", "插件把 flow 写成了非法类型,本次 flow 改动已忽略")
		}
		delete(doc, "flow")
		markFlowDropped(dropped)
	}
	if d, present := doc["decision"]; present && d != nil {
		if dm, ok := d.(map[string]any); ok {
			pruneJSObject(dm, jsDecisionFieldKinds, "decision", dropped, logf)
		} else {
			// 非对象 decision 采用 Continue，并记录字段错误。
			delete(doc, "decision")
			emitLog(logf, "error", "插件把 decision 写成了非法类型,本次处置按放行处理")
		}
	}
	b, err := json.Marshal(doc)
	if err != nil {
		return jsOut{}, nil, false
	}
	// 使用新对象承接清理后的 JSON。
	var clean jsOut
	if json.Unmarshal(b, &clean) != nil {
		return jsOut{}, nil, false
	}
	return clean, dropped, true
}

// dropSet 记录解析时跳过的字段路径，写回逻辑据此保留对应原值。
type dropSet map[string]bool

func (d dropSet) has(paths ...string) bool {
	for _, p := range paths {
		if d[p] {
			return true
		}
	}
	return false
}

// markFlowDropped 标记 flow 中的可编辑字段，供非法 flow 形态保持原值。
func markFlowDropped(dropped dropSet) {
	for _, k := range []string{
		"flow.method", "flow.url", "flow.body", "flow.bodyB64",
		"flow.data", "flow.dataB64", "flow.response.body", "flow.response.bodyB64",
	} {
		dropped[k] = true
	}
}

// pruneJSObject 删除类型与 kinds 声明不符的字段；头对象包含非字符串值时整体跳过。
func pruneJSObject(obj map[string]any, kinds map[string]byte, path string, dropped dropSet, logf func(level, msg string)) {
	for k, v := range obj {
		kind, known := kinds[k]
		if !known {
			delete(obj, k) // 未知字段不属于脚本输出契约
			continue
		}
		if v == nil {
			continue
		}
		ok := false
		msg := "插件把 " + path + "." + k + " 写成了非法类型,该字段已忽略"
		switch kind {
		case 's':
			_, ok = v.(string)
		case 'n':
			_, ok = v.(float64)
		case 'o':
			_, ok = v.(map[string]any)
		case 'h':
			var hm map[string]any
			if hm, ok = v.(map[string]any); ok {
				for hk, hv := range hm {
					if _, isStr := hv.(string); !isStr && hv != nil {
						ok = false
						msg = "插件把 " + path + "." + k + "['" + hk + "'] 写成了非字符串,本次头改动已忽略"
						break
					}
				}
			}
		}
		if !ok {
			delete(obj, k)
			dropped[path+"."+k] = true
			emitLog(logf, "error", msg)
		}
	}
}

func requestToJS(f *flow.Flow) jsFlow {
	v := jsFlow{ID: f.ID}
	if f.Request != nil {
		// 标量字段按 JSON 出境后的字符串形态构造，作为回程比较基准。
		v.Method = flow.SanitizeJSONString(f.Request.Method)
		v.URL = flow.SanitizeJSONString(f.Request.URL)
		v.Host = flow.SanitizeJSONString(f.Request.Host)
		v.Path = flow.SanitizeJSONString(f.Request.Path)
		v.Headers = flatten(f.Request.Header)
		v.Body, v.BodyB64 = payloadToJS(f.Request.Body)
	}
	if f.Response != nil {
		body, b64 := payloadToJS(f.Response.Body)
		v.Response = &jsResponse{
			Status:     f.Response.Status,
			StatusText: flow.SanitizeJSONString(f.Response.StatusText),
			Headers:    flatten(f.Response.Header),
			Body:       body,
			BodyB64:    b64,
		}
	}
	if p := f.Process(); p != nil {
		v.Process = &jsProcess{Name: p.Name, PID: p.PID, Path: p.Path}
	}
	return v
}

// applyHTTP 将 VM 返回的 flow 增量应用回 Go flow.Flow，并以 sent 作为改动比较基准。
// 头部保留未改键的原始多值与顺序，响应结构按字段增量更新。
func applyHTTP(f *flow.Flow, sent *jsFlow, out []byte, phase flow.Phase, logf func(level, msg string)) flow.Decision {
	res, dropped, ok := parseOut(out, logf)
	if !ok {
		return flow.ContinueDecision()
	}
	jf := res.Flow
	changed := false

	if f.Request != nil {
		r := f.Request
		// method/url 仅接受非空且相对 sent 有变化的值；host/path 使用同一比较规则。
		if !dropped.has("flow.method") && jf.Method != "" && jf.Method != sent.Method {
			r.Method = jf.Method
			changed = true
		}
		if !dropped.has("flow.url") && jf.URL != "" && jf.URL != sent.URL {
			r.URL = jf.URL
			changed = true
		}
		if jf.Host != "" && jf.Host != sent.Host {
			r.Host = jf.Host
			changed = true
		}
		if jf.Path != "" && jf.Path != sent.Path {
			r.Path = jf.Path
			changed = true
		}
		// headers 为 nil 表示未提供，空 map 表示清空全部头。
		if jf.Headers != nil {
			if nh, hChanged := mergeHeaders(r.Header, sent.Headers, jf.Headers); hChanged {
				r.Header = nh
				changed = true
			}
		}
		if !dropped.has("flow.body", "flow.bodyB64") {
			if b, ok := resolvePayload(jf.Body, jf.BodyB64, "flow.body",
				"flow.bodyB64 不是合法的标准 base64,请求体保持原值", logf); ok {
				if !bytes.Equal(b, r.Body) {
					r.Body = b
					changed = true
				}
			}
		}
	}

	if jf.Response != nil {
		if f.Response != nil {
			r := f.Response
			sr := sent.Response
			if sr == nil {
				sr = &jsResponse{} // sent 与 f 同源，此处为异常形态提供空视图
			}
			if jf.Response.Status != 0 && jf.Response.Status != r.Status {
				r.Status = jf.Response.Status
				changed = true
			}
			if jf.Response.StatusText != "" && jf.Response.StatusText != sr.StatusText {
				r.StatusText = jf.Response.StatusText
				changed = true
			}
			if jf.Response.Headers != nil {
				if nh, hChanged := mergeHeaders(r.Header, sr.Headers, jf.Response.Headers); hChanged {
					r.Header = nh
					changed = true
				}
			}
			if !dropped.has("flow.response.body", "flow.response.bodyB64") {
				if b, ok := resolvePayload(jf.Response.Body, jf.Response.BodyB64, "flow.response.body",
					"flow.response.bodyB64 不是合法的标准 base64,响应体保持原值", logf); ok {
					if !bytes.Equal(b, r.Body) {
						r.Body = b
						changed = true
					}
				}
			}
		} else {
			// 请求阶段脚本设置了 response(mock):此时无原始响应可保留,整体新建。
			// mock 载荷按文本优先级解析，bodyB64 提供二进制响应体。
			body, ok := resolvePayload(jf.Response.Body, jf.Response.BodyB64, "mock 的 body",
				"mock 的 bodyB64 不是合法的标准 base64,已按空响应体处理", logf)
			if !ok {
				body = nil
			}
			f.Response = &flow.Response{
				Status:     jf.Response.Status,
				StatusText: jf.Response.StatusText,
				Header:     unflatten(jf.Response.Headers),
				Body:       body,
			}
			changed = true
		}
	}

	if changed {
		f.Modified = true
	}
	return decisionFromJS(res.Decision, phase)
}

func decisionFromJS(d jsDecision, phase flow.Phase) flow.Decision {
	switch d.Kind {
	case "mock":
		return flow.MockDecision(d.Reason)
	case "abort":
		return flow.AbortDecision(d.Status, d.Reason)
	case "breakpoint":
		return flow.BreakpointDecision(phase, d.Reason)
	default:
		return flow.ContinueDecision()
	}
}

// flatten 生成脚本可见的首值扁平头视图，并将值归一为 JSON 出境后的字符串形态。
// 头名含非法 UTF-8 字节时跳过该键，mergeHeaders 会保留原始头。
func flatten(h map[string][]string) map[string]string {
	out := make(map[string]string, len(h))
	for k, v := range h {
		if len(v) > 0 && utf8.ValidString(k) {
			out[k] = flow.SanitizeJSONString(v[0])
		}
	}
	return out
}

func unflatten(m map[string]string) map[string][]string {
	out := make(map[string][]string, len(m))
	for k, v := range m {
		out[k] = []string{v}
	}
	return out
}

// mergeHeaders 将脚本回传的扁平头视图合并回原始多值头。
// edited 与 sent 相同的键保留 orig，多值变化或新增键写入单值，缺失键移除。
func mergeHeaders(orig map[string][]string, sent, edited map[string]string) (map[string][]string, bool) {
	if sameStringMap(sent, edited) {
		return orig, false
	}
	out := make(map[string][]string, len(edited)+2)
	// 脚本视图未覆盖的头保留原值。
	for k, v := range orig {
		if len(v) == 0 || !utf8.ValidString(k) {
			out[k] = v
		}
	}
	for k, v := range edited {
		if sv, ok := sent[k]; ok && sv == v {
			out[k] = orig[k] // 脚本没碰:保留原始多值切片
		} else {
			out[k] = []string{v}
		}
	}
	return out, true
}

func sameStringMap(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}
