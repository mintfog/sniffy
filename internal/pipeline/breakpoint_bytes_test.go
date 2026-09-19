// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package pipeline

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mintfog/sniffy/internal/flow"
)

// uiBytesView 表示界面读取的有序头及值字节旁路。
type uiBytesView struct {
	RequestHeaders     [][2]string `json:"requestHeaders"`
	RequestHeadersB64  []string    `json:"requestHeadersB64"`
	ResponseHeaders    [][2]string `json:"responseHeaders"`
	ResponseHeadersB64 []string    `json:"responseHeadersB64"`
}

// editPayload 与 RequestEdit / ResponseEdit 的 JSON 形状一致。
type editPayload struct {
	Headers    [][2]string `json:"headers"`
	HeadersB64 []string    `json:"headersB64,omitempty"`
}

type resumePayload struct {
	Request  *editPayload `json:"request,omitempty"`
	Response *editPayload `json:"response,omitempty"`
}

// pauseForBytes 暂停 flow，并返回 JSON 边界后的断点载荷及顶层键集合。
func pauseForBytes(t *testing.T, f *flow.Flow, phase flow.Phase) (*BreakpointManager, <-chan bool, uiBytesView, map[string]json.RawMessage) {
	t.Helper()
	sink := &eventSink{}
	bm := NewBreakpointManager(sink.emit)
	done := make(chan bool, 1)
	go func() { done <- bm.Pause(f, phase) }()
	waitPaused(t, bm, f.ID)
	sink.waitTypes(t, "breakpoint_hit")

	hit, ok := sink.snapshot()[0].payload.(*BreakpointFlow)
	if !ok {
		t.Fatalf("hit 载荷类型 = %T, want *BreakpointFlow", sink.snapshot()[0].payload)
	}
	raw, err := json.Marshal(hit)
	if err != nil {
		t.Fatalf("断点载荷必须能序列化: %v", err)
	}
	var view uiBytesView
	if err := json.Unmarshal(raw, &view); err != nil {
		t.Fatalf("断点载荷解码失败: %v", err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatalf("断点载荷应是 JSON 对象: %v", err)
	}
	return bm, done, view, keys
}

// resumeWith 按界面补丁放行并等待处理 goroutine 返回。
func resumeWith(t *testing.T, bm *BreakpointManager, f *flow.Flow, done <-chan bool, p resumePayload) {
	t.Helper()
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("用例载荷必须能序列化: %v", err)
	}
	resumeEdited(t, bm, f, done, decodeEdit(t, string(raw)))
}

// resumeRawExpectingError 提交畸形载荷，验证错误及暂停状态，并返回错误。
func resumeRawExpectingError(t *testing.T, bm *BreakpointManager, f *flow.Flow, payload string) error {
	t.Helper()
	err := bm.Resume(f.ID, decodeEdit(t, payload))
	if err == nil {
		t.Fatal("畸形旁路必须被拒绝")
	}
	still := false
	for _, item := range bm.List() {
		if item.ID == f.ID {
			still = true
		}
	}
	if !still {
		t.Fatal("被拒绝的放行不应把 flow 从断点上放走")
	}
	return err
}

// decodeSlot 解码标准 base64 旁路项。
func decodeSlot(t *testing.T, enc string) string {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		t.Fatalf("旁路项应是标准 base64: %v", err)
	}
	return string(raw)
}

// encSlot 使用线上契约的标准 base64 编码。
func encSlot(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// slotsFor 为每个头值生成对齐的字节旁路数组。
func slotsFor(pairs [][2]string) []string {
	out := make([]string, len(pairs))
	for i, kv := range pairs {
		out[i] = encSlot(kv[1])
	}
	return out
}

// 全部合法头值时省略两个旁路字段。
func TestBreakpointFlowOmitsHeadersB64WhenAllValid(t *testing.T) {
	f := newRespFlow()
	f.Request.Header = map[string][]string{"Accept": {"*/*"}}
	f.Request.RawHeaders = [][2]string{{"Host", "x.com"}, {"Accept", "*/*"}}
	f.Response.Header = map[string][]string{"Content-Type": {"text/plain"}}
	f.Response.RawHeaders = [][2]string{{"Content-Type", "text/plain"}}

	bm, done, _, keys := pauseForBytes(t, f, flow.PhaseResponse)
	defer resumeEdited(t, bm, f, done, nil)

	if _, found := keys["requestHeadersB64"]; found {
		t.Error("全合法请求头不应出现 requestHeadersB64 键")
	}
	if _, found := keys["responseHeadersB64"]; found {
		t.Error("全合法响应头不应出现 responseHeadersB64 键")
	}
}

// 旁路与有序头按下标对齐，非法值携带原始字节，合成 Host 行同样参与对齐。
func TestBreakpointFlowCarriesHeaderBytesSidecar(t *testing.T) {
	f := newRespFlow()
	f.Request.Header = map[string][]string{"Content-Disposition": {latin1Filename}, "Accept": {"*/*"}}
	f.Request.RawHeaders = [][2]string{{"Content-Disposition", latin1Filename}, {"Accept", "*/*"}}
	f.Response.Header = map[string][]string{"Content-Disposition": {latin1Filename}}
	f.Response.RawHeaders = [][2]string{{"Content-Disposition", latin1Filename}}

	bm, done, view, _ := pauseForBytes(t, f, flow.PhaseResponse)
	defer resumeEdited(t, bm, f, done, nil)

	if len(view.RequestHeadersB64) != len(view.RequestHeaders) {
		t.Fatalf("请求旁路长度 = %d, want %d", len(view.RequestHeadersB64), len(view.RequestHeaders))
	}
	if len(view.ResponseHeadersB64) != len(view.ResponseHeaders) {
		t.Fatalf("响应旁路长度 = %d, want %d", len(view.ResponseHeadersB64), len(view.ResponseHeaders))
	}
	for i, kv := range view.RequestHeaders {
		slot := view.RequestHeadersB64[i]
		if kv[0] == "Content-Disposition" {
			if decodeSlot(t, slot) != latin1Filename {
				t.Errorf("第 %d 行旁路 = %q, want 原始字节", i, slot)
			}
			continue
		}
		if slot != "" {
			t.Errorf("合法的第 %d 行(%s)不该有旁路项: %q", i, kv[0], slot)
		}
	}
	if decodeSlot(t, view.ResponseHeadersB64[0]) != latin1Filename {
		t.Error("响应头旁路未带回原始字节")
	}
}

// 原样回传明文与旁路时保留原始字节，flow 保持未修改状态。
func TestResumeWithSidecarEchoKeepsBytesAndCleanFlag(t *testing.T) {
	f := newReqFlow()
	f.Request.Header = map[string][]string{"Content-Disposition": {latin1Filename}, "Accept": {"*/*"}}
	f.Request.RawHeaders = [][2]string{{"Host", "x.com"}, {"Content-Disposition", latin1Filename}, {"Accept", "*/*"}}
	rawWant := append([][2]string(nil), f.Request.RawHeaders...)

	bm, done, view, _ := pauseForBytes(t, f, flow.PhaseRequest)
	resumeWith(t, bm, f, done, resumePayload{
		Request: &editPayload{Headers: view.RequestHeaders, HeadersB64: view.RequestHeadersB64},
	})

	if f.Modified {
		t.Error("原样回传的头不是改动,不应标记 Modified")
	}
	if got := f.Request.Header["Content-Disposition"]; !wantStrings(got, []string{latin1Filename}) {
		t.Errorf("请求头值 = %q, want %q", got, latin1Filename)
	}
	if !sameHeaderPairs(f.Request.RawHeaders, rawWant) {
		t.Errorf("写线序列 = %q, want %q", f.Request.RawHeaders, rawWant)
	}
}

// 响应侧原样回传旁路时保留字节并保持未修改状态。
func TestResumeWithSidecarEchoKeepsResponseBytes(t *testing.T) {
	f := newRespFlow()
	f.Response.Header = map[string][]string{"Content-Disposition": {latin1Filename}, "Content-Type": {"text/plain"}}
	f.Response.RawHeaders = [][2]string{{"Content-Type", "text/plain"}, {"Content-Disposition", latin1Filename}}

	bm, done, view, _ := pauseForBytes(t, f, flow.PhaseResponse)
	resumeWith(t, bm, f, done, resumePayload{
		Response: &editPayload{Headers: view.ResponseHeaders, HeadersB64: view.ResponseHeadersB64},
	})

	if f.Modified {
		t.Error("原样回传的头不是改动,不应标记 Modified")
	}
	if got := f.Response.Header["Content-Disposition"]; !wantStrings(got, []string{latin1Filename}) {
		t.Errorf("响应头值 = %q, want %q", got, latin1Filename)
	}
}

// 逐行旁路支持单行修改，其余行保留原始字节。
func TestResumeWithSidecarEditsOnlyOneRow(t *testing.T) {
	f := newReqFlow()
	f.Request.Header = map[string][]string{"Content-Disposition": {latin1Filename}, "X-Trace": {"1"}}
	f.Request.RawHeaders = [][2]string{{"Host", "x.com"}, {"Content-Disposition", latin1Filename}, {"X-Trace", "1"}}

	bm, done, view, _ := pauseForBytes(t, f, flow.PhaseRequest)
	pairs := append([][2]string(nil), view.RequestHeaders...)
	slots := append([]string(nil), view.RequestHeadersB64...)
	for i := range pairs {
		if pairs[i][0] == "X-Trace" {
			pairs[i][1] = "2"
			// 空旁路项使用该行的明文值。
			slots[i] = ""
		}
	}

	resumeWith(t, bm, f, done, resumePayload{Request: &editPayload{Headers: pairs, HeadersB64: slots}})

	if got := f.Request.Header["X-Trace"]; !wantStrings(got, []string{"2"}) {
		t.Errorf("被改的头 X-Trace = %q, want [2]", got)
	}
	if got := f.Request.Header["Content-Disposition"]; !wantStrings(got, []string{latin1Filename}) {
		t.Errorf("未改的头丢了原始字节: %q", got)
	}
	if !f.Modified {
		t.Error("用户改过 X-Trace,应标记 Modified")
	}
}

// 同名多值头换序、删行和插行时，旁路按行保持对应关系。
func TestResumeWithSidecarSurvivesRowSurgery(t *testing.T) {
	tests := []struct {
		name  string
		build func(rows [][2]string, slots []string) ([][2]string, []string)
		want  []string
	}{
		{
			name: "换序",
			build: func(rows [][2]string, slots []string) ([][2]string, []string) {
				return [][2]string{{"Host", "x.com"}, rows[2], rows[1]}, []string{"", slots[2], slots[1]}
			},
			want: []string{tagTwo, tagOne},
		},
		{
			name: "删掉第一条",
			build: func(rows [][2]string, slots []string) ([][2]string, []string) {
				return [][2]string{{"Host", "x.com"}, rows[2]}, []string{"", slots[2]}
			},
			want: []string{tagTwo},
		},
		{
			name: "中间插一条纯文本",
			build: func(rows [][2]string, slots []string) ([][2]string, []string) {
				return [][2]string{{"Host", "x.com"}, rows[1], {"X-Tag", "plain"}, rows[2]},
					[]string{"", slots[1], "", slots[2]}
			},
			want: []string{tagOne, "plain", tagTwo},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newReqFlow()
			f.Request.Header = map[string][]string{"X-Tag": {tagOne, tagTwo}}
			f.Request.RawHeaders = [][2]string{{"Host", "x.com"}, {"X-Tag", tagOne}, {"X-Tag", tagTwo}}

			bm, done, view, _ := pauseForBytes(t, f, flow.PhaseRequest)
			if len(view.RequestHeaders) != 3 {
				t.Fatalf("界面应看到 3 行头, got %q", view.RequestHeaders)
			}
			pairs, slots := tt.build(view.RequestHeaders, view.RequestHeadersB64)
			resumeWith(t, bm, f, done, resumePayload{Request: &editPayload{Headers: pairs, HeadersB64: slots}})

			if got := f.Request.Header["X-Tag"]; !wantStrings(got, tt.want) {
				t.Errorf("X-Tag = %q, want %q", got, tt.want)
			}
		})
	}
}

// 旁路存在时使用用户输入的字节值，包括字面 U+FFFD。
func TestResumeWithSidecarKeepsLiteralReplacementChar(t *testing.T) {
	f := newReqFlow()
	f.Request.Header = map[string][]string{"X-Tag": {"raw-\xe9"}}
	f.Request.RawHeaders = [][2]string{{"Host", "x.com"}, {"X-Tag", "raw-\xe9"}}

	bm, done, view, _ := pauseForBytes(t, f, flow.PhaseRequest)
	pairs := append([][2]string(nil), view.RequestHeaders...)
	slots := slotsFor(pairs)
	if pairs[1][1] != flow.SanitizeJSONString("raw-\xe9") {
		t.Fatalf("界面读到的值 = %q, want U+FFFD 形态", pairs[1][1])
	}

	resumeWith(t, bm, f, done, resumePayload{Request: &editPayload{Headers: pairs, HeadersB64: slots}})

	if got := f.Request.Header["X-Tag"]; !wantStrings(got, []string{pairs[1][1]}) {
		t.Errorf("X-Tag = %q, want 用户手打的 %q", got, pairs[1][1])
	}
}

// 无旁路客户端使用基准头部恢复原始字节。
func TestResumeWithoutSidecarKeepsHeuristic(t *testing.T) {
	f := newReqFlow()
	f.Request.Header = map[string][]string{"Content-Disposition": {latin1Filename}}
	f.Request.RawHeaders = [][2]string{{"Host", "x.com"}, {"Content-Disposition", latin1Filename}}

	bm, done, view, _ := pauseForBytes(t, f, flow.PhaseRequest)
	// 仅提交明文头部，使用基准恢复原始值。
	resumeWith(t, bm, f, done, resumePayload{Request: &editPayload{Headers: view.RequestHeaders}})

	if got := f.Request.Header["Content-Disposition"]; !wantStrings(got, []string{latin1Filename}) {
		t.Errorf("请求头值 = %q, want %q", got, latin1Filename)
	}
	if f.Modified {
		t.Error("原样回传不是改动,不应标记 Modified")
	}
}

// 畸形旁路返回错误，flow 保持暂停且头部保持原值。
func TestResumeRejectsMalformedSidecar(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		wantIn  string
	}{
		{
			name:    "旁路项不是标准 base64",
			payload: `{"request":{"headers":[["Host","x.com"],["X-Tag","shown"]],"headersB64":["","not base64!!"]}}`,
			wantIn:  "X-Tag",
		},
		{
			name:    "旁路比明文多一项",
			payload: `{"request":{"headers":[["Host","x.com"]],"headersB64":["","` + encSlot("a") + `"]}}`,
			wantIn:  "对不上",
		},
		{
			name:    "只发旁路不发明文",
			payload: `{"request":{"headersB64":["` + encSlot("raw-\xe9") + `"]}}`,
			wantIn:  "对不上",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newReqFlow()
			f.Request.Header = map[string][]string{"X-Tag": {"raw-\xe9"}}
			f.Request.RawHeaders = [][2]string{{"Host", "x.com"}, {"X-Tag", "raw-\xe9"}}

			bm, done, _, _ := pauseForBytes(t, f, flow.PhaseRequest)
			err := resumeRawExpectingError(t, bm, f, tt.payload)
			if !strings.Contains(err.Error(), tt.wantIn) {
				t.Errorf("错误信息 = %v, want 含 %q", err, tt.wantIn)
			}
			if got := f.Request.Header["X-Tag"]; !wantStrings(got, []string{"raw-\xe9"}) {
				t.Errorf("被拒绝的放行动了头: %q", got)
			}
			resumeEdited(t, bm, f, done, nil)
		})
	}
}

// 旁路解码后的字节经过头部字符校验。
func TestResumeRejectsCRLFSmuggledThroughSidecar(t *testing.T) {
	f := newReqFlow()
	f.Request.Header = map[string][]string{"X-Tag": {"clean"}}
	f.Request.RawHeaders = [][2]string{{"Host", "x.com"}, {"X-Tag", "clean"}}

	bm, done, _, _ := pauseForBytes(t, f, flow.PhaseRequest)
	payload := `{"request":{"headers":[["Host","x.com"],["X-Tag","clean"]],"headersB64":["","` +
		encSlot("a\r\nX-Injected: 1") + `"]}}`
	err := resumeRawExpectingError(t, bm, f, payload)
	if !strings.Contains(err.Error(), "CR/LF") {
		t.Errorf("错误信息 = %v, want 指出 CR/LF", err)
	}
	if got := f.Request.Header["X-Tag"]; !wantStrings(got, []string{"clean"}) {
		t.Errorf("被拒绝的放行动了头: %q", got)
	}
	resumeEdited(t, bm, f, done, nil)
}

// 断点列表与 hit 事件共享同一份头部字节旁路。
func TestBreakpointListCarriesHeaderBytesSidecar(t *testing.T) {
	f := newReqFlow()
	f.Request.Header = map[string][]string{"Content-Disposition": {latin1Filename}}
	f.Request.RawHeaders = [][2]string{{"Host", "x.com"}, {"Content-Disposition", latin1Filename}}

	bm, done, _, _ := pauseForBytes(t, f, flow.PhaseRequest)
	defer resumeEdited(t, bm, f, done, nil)

	list := bm.List()
	if len(list) != 1 {
		t.Fatalf("暂停列表条目数 = %d, want 1", len(list))
	}
	found := false
	for i, kv := range list[0].RequestHeaders {
		if kv[0] != "Content-Disposition" {
			continue
		}
		found = true
		if decodeSlot(t, list[0].RequestHeadersB64[i]) != latin1Filename {
			t.Errorf("列表旁路 = %q, want 原始字节", list[0].RequestHeadersB64[i])
		}
	}
	if !found {
		t.Fatalf("列表里没有 Content-Disposition: %q", list[0].RequestHeaders)
	}
}
