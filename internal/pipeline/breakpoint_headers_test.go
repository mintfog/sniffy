// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package pipeline

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/mintfog/sniffy/internal/flow"
)

// latin1Filename 模拟含 Latin-1 文件名的 Content-Disposition 值。
const latin1Filename = "attachment; filename=\"caf\xe9.pdf\""

// 同名多值测试使用可区分的原始值。
const (
	tagOne = "tag-\xe9-one"
	tagTwo = "tag-\xe9-two"
)

// uiHeaders 是界面从断点载荷里读到的有序头。
type uiHeaders struct {
	Request  [][2]string `json:"requestHeaders"`
	Response [][2]string `json:"responseHeaders"`
}

// pauseForUI 暂停 flow，并返回 JSON 边界后的有序头。
func pauseForUI(t *testing.T, f *flow.Flow, phase flow.Phase) (*BreakpointManager, <-chan bool, uiHeaders) {
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
	var view uiHeaders
	if err := json.Unmarshal(raw, &view); err != nil {
		t.Fatalf("断点载荷解码失败: %v", err)
	}
	return bm, done, view
}

// resumeEdited 提交编辑并等待处理 goroutine 返回。
func resumeEdited(t *testing.T, bm *BreakpointManager, f *flow.Flow, done <-chan bool, edit *BreakpointEdit) {
	t.Helper()
	if err := bm.Resume(f.ID, edit); err != nil {
		t.Fatalf("Resume 应成功: %v", err)
	}
	select {
	case abort := <-done:
		if abort {
			t.Fatal("Resume 不应返回阻断")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Pause 未在超时前返回")
	}
}

// requireSanitized 检查界面读取到 JSON 边界后的字符串形态。
func requireSanitized(t *testing.T, pairs [][2]string, name, original string) {
	t.Helper()
	got := headerRowValues(pairs, name)
	if len(got) == 0 {
		t.Fatalf("导出给界面的头里没有 %s: %v", name, pairs)
	}
	if got[0] != flow.SanitizeJSONString(original) || got[0] == original {
		t.Fatalf("界面读到的 %s = %q, want 原值经 JSON 出境后的 U+FFFD 形态", name, got[0])
	}
}

// headerRowValues 取有序头里某个头名(大小写不敏感的精确名)的全部值。
func headerRowValues(pairs [][2]string, name string) []string {
	var out []string
	for _, kv := range pairs {
		if kv[0] == name {
			out = append(out, kv[1])
		}
	}
	return out
}

// replaceRowValue 模拟用户在编辑器里改动某一行的值。
func replaceRowValue(pairs [][2]string, name, value string) [][2]string {
	out := make([][2]string, len(pairs))
	copy(out, pairs)
	for i := range out {
		if out[i][0] == name {
			out[i][1] = value
		}
	}
	return out
}

func wantStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// 请求头原样回传时恢复原始字节并保持 flow 未修改。
func TestResumeEchoedRequestHeadersKeepRawBytes(t *testing.T) {
	f := newReqFlow()
	f.Request.Header = map[string][]string{"Content-Disposition": {latin1Filename}, "Accept": {"*/*"}}
	f.Request.RawHeaders = [][2]string{{"Host", "x.com"}, {"Content-Disposition", latin1Filename}, {"Accept", "*/*"}}
	rawWant := append([][2]string(nil), f.Request.RawHeaders...)

	bm, done, view := pauseForUI(t, f, flow.PhaseRequest)
	requireSanitized(t, view.Request, "Content-Disposition", latin1Filename)

	resumeEdited(t, bm, f, done, &BreakpointEdit{Request: &RequestEdit{Headers: view.Request}})

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

// 响应头原样回传时恢复原始字节并保持 flow 未修改。
func TestResumeEchoedResponseHeadersKeepRawBytes(t *testing.T) {
	f := newRespFlow()
	f.Response.Header = map[string][]string{"Content-Disposition": {latin1Filename}, "Content-Type": {"text/plain"}}
	f.Response.RawHeaders = [][2]string{{"Content-Type", "text/plain"}, {"Content-Disposition", latin1Filename}}
	rawWant := append([][2]string(nil), f.Response.RawHeaders...)

	bm, done, view := pauseForUI(t, f, flow.PhaseResponse)
	requireSanitized(t, view.Response, "Content-Disposition", latin1Filename)

	resumeEdited(t, bm, f, done, &BreakpointEdit{Response: &ResponseEdit{Headers: view.Response}})

	if f.Modified {
		t.Error("原样回传的头不是改动,不应标记 Modified")
	}
	if got := f.Response.Header["Content-Disposition"]; !wantStrings(got, []string{latin1Filename}) {
		t.Errorf("响应头值 = %q, want %q", got, latin1Filename)
	}
	if !sameHeaderPairs(f.Response.RawHeaders, rawWant) {
		t.Errorf("写线序列 = %q, want %q", f.Response.RawHeaders, rawWant)
	}
}

// 用户编辑非法字节头时采用用户提供的新值。
func TestResumeEditedIllegalHeaderTakesUserValue(t *testing.T) {
	f := newReqFlow()
	f.Request.Header = map[string][]string{"Content-Disposition": {latin1Filename}}
	f.Request.RawHeaders = [][2]string{{"Host", "x.com"}, {"Content-Disposition", latin1Filename}}

	bm, done, view := pauseForUI(t, f, flow.PhaseRequest)
	const edited = `attachment; filename="cafe.pdf"`
	pairs := replaceRowValue(view.Request, "Content-Disposition", edited)

	resumeEdited(t, bm, f, done, &BreakpointEdit{Request: &RequestEdit{Headers: pairs}})

	if got := f.Request.Header["Content-Disposition"]; !wantStrings(got, []string{edited}) {
		t.Errorf("请求头值 = %q, want %q", got, edited)
	}
	if !f.Modified {
		t.Error("用户改过头值,应标记 Modified")
	}
}

// 单独编辑一个头时，其他头保留各自的原始字节。
func TestResumeEditOfOtherHeaderKeepsIllegalBytes(t *testing.T) {
	f := newReqFlow()
	f.Request.Header = map[string][]string{"Content-Disposition": {latin1Filename}, "X-Trace": {"1"}}
	f.Request.RawHeaders = [][2]string{{"Host", "x.com"}, {"Content-Disposition", latin1Filename}, {"X-Trace", "1"}}

	bm, done, view := pauseForUI(t, f, flow.PhaseRequest)
	pairs := replaceRowValue(view.Request, "X-Trace", "2")

	resumeEdited(t, bm, f, done, &BreakpointEdit{Request: &RequestEdit{Headers: pairs}})

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

// 同名多值头换序后，各值按用户顺序写线并保留对应原始字节。
func TestResumeReordersMultiValueHeaderWithoutMisplacingBytes(t *testing.T) {
	f := newReqFlow()
	f.Request.Header = map[string][]string{"X-Tag": {tagOne, tagTwo}}
	f.Request.RawHeaders = [][2]string{{"Host", "x.com"}, {"X-Tag", tagOne}, {"X-Tag", tagTwo}}

	bm, done, view := pauseForUI(t, f, flow.PhaseRequest)
	rows := headerRowValues(view.Request, "X-Tag")
	if len(rows) != 2 {
		t.Fatalf("界面应看到两行 X-Tag, got %q", rows)
	}
	pairs := [][2]string{{"Host", "x.com"}, {"X-Tag", rows[1]}, {"X-Tag", rows[0]}}

	resumeEdited(t, bm, f, done, &BreakpointEdit{Request: &RequestEdit{Headers: pairs}})

	if got := f.Request.Header["X-Tag"]; !wantStrings(got, []string{tagTwo, tagOne}) {
		t.Errorf("X-Tag = %q, want %q", got, []string{tagTwo, tagOne})
	}
	want := [][2]string{{"Host", "x.com"}, {"X-Tag", tagTwo}, {"X-Tag", tagOne}}
	if !sameHeaderPairs(f.Request.RawHeaders, want) {
		t.Errorf("写线序列 = %q, want %q", f.Request.RawHeaders, want)
	}
}

// 删除同名多值头中的一行后，保留行继续使用对应的原始字节。
func TestResumeDropsOneMultiValueHeaderRow(t *testing.T) {
	f := newReqFlow()
	f.Request.Header = map[string][]string{"X-Tag": {tagOne, tagTwo}}
	f.Request.RawHeaders = [][2]string{{"Host", "x.com"}, {"X-Tag", tagOne}, {"X-Tag", tagTwo}}

	bm, done, view := pauseForUI(t, f, flow.PhaseRequest)
	rows := headerRowValues(view.Request, "X-Tag")
	pairs := [][2]string{{"Host", "x.com"}, {"X-Tag", rows[1]}}

	resumeEdited(t, bm, f, done, &BreakpointEdit{Request: &RequestEdit{Headers: pairs}})

	if got := f.Request.Header["X-Tag"]; !wantStrings(got, []string{tagTwo}) {
		t.Errorf("X-Tag = %q, want %q", got, []string{tagTwo})
	}
	if !f.Modified {
		t.Error("删掉一行头是改动,应标记 Modified")
	}
}

// 在同名多值头中插入新行时，新值与两侧原始值分别写线。
func TestResumeInsertsRowAmongMultiValueHeader(t *testing.T) {
	f := newReqFlow()
	f.Request.Header = map[string][]string{"X-Tag": {tagOne, tagTwo}}
	f.Request.RawHeaders = [][2]string{{"Host", "x.com"}, {"X-Tag", tagOne}, {"X-Tag", tagTwo}}

	bm, done, view := pauseForUI(t, f, flow.PhaseRequest)
	rows := headerRowValues(view.Request, "X-Tag")
	pairs := [][2]string{{"Host", "x.com"}, {"X-Tag", rows[0]}, {"X-Tag", "plain"}, {"X-Tag", rows[1]}}

	resumeEdited(t, bm, f, done, &BreakpointEdit{Request: &RequestEdit{Headers: pairs}})

	want := []string{tagOne, "plain", tagTwo}
	if got := f.Request.Header["X-Tag"]; !wantStrings(got, want) {
		t.Errorf("X-Tag = %q, want %q", got, want)
	}
	wantRaw := [][2]string{{"Host", "x.com"}, {"X-Tag", tagOne}, {"X-Tag", "plain"}, {"X-Tag", tagTwo}}
	if !sameHeaderPairs(f.Request.RawHeaders, wantRaw) {
		t.Errorf("写线序列 = %q, want %q", f.Request.RawHeaders, wantRaw)
	}
}

// 缺少原始头序列时，按规范化头表恢复原始值。
func TestResumeEchoedHeadersKeepRawBytesWithoutRawHeaders(t *testing.T) {
	f := newReqFlow()
	f.Request.Header = map[string][]string{"Content-Disposition": {latin1Filename}}
	f.Request.RawHeaders = nil

	bm, done, view := pauseForUI(t, f, flow.PhaseRequest)
	requireSanitized(t, view.Request, "Content-Disposition", latin1Filename)

	resumeEdited(t, bm, f, done, &BreakpointEdit{Request: &RequestEdit{Headers: view.Request}})

	if got := f.Request.Header["Content-Disposition"]; !wantStrings(got, []string{latin1Filename}) {
		t.Errorf("请求头值 = %q, want %q", got, latin1Filename)
	}
	if got := headerRowValues(f.Request.RawHeaders, "Content-Disposition"); !wantStrings(got, []string{latin1Filename}) {
		t.Errorf("写线序列里的值 = %q, want %q", got, latin1Filename)
	}
}

// Host 头同样按原始字节恢复，并保持 flow 未修改。
func TestResumeEchoedHostKeepsRawBytes(t *testing.T) {
	const host = "caf\xe9.example"
	f := newReqFlow()
	f.Request.Host = host
	f.Request.Header = map[string][]string{"Accept": {"*/*"}}
	f.Request.RawHeaders = [][2]string{{"Host", host}, {"Accept", "*/*"}}

	bm, done, view := pauseForUI(t, f, flow.PhaseRequest)
	requireSanitized(t, view.Request, "Host", host)

	resumeEdited(t, bm, f, done, &BreakpointEdit{Request: &RequestEdit{Headers: view.Request}})

	if f.Request.Host != host {
		t.Errorf("Host = %q, want %q", f.Request.Host, host)
	}
	if f.Modified {
		t.Error("Host 原样回传不是改动,不应标记 Modified")
	}
}

// 用户编辑 Host 行时采用用户提供的新值。
func TestResumeEditedHostTakesUserValue(t *testing.T) {
	f := newReqFlow()
	f.Request.Host = "caf\xe9.example"
	f.Request.RawHeaders = [][2]string{{"Host", f.Request.Host}}

	bm, done, view := pauseForUI(t, f, flow.PhaseRequest)
	pairs := replaceRowValue(view.Request, "Host", "proxy.internal")

	resumeEdited(t, bm, f, done, &BreakpointEdit{Request: &RequestEdit{Headers: pairs}})

	if f.Request.Host != "proxy.internal" {
		t.Errorf("Host = %q, want proxy.internal", f.Request.Host)
	}
	if !f.Modified {
		t.Error("改过 Host 应标记 Modified")
	}
}
