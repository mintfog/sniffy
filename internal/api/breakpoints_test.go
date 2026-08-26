// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/internal/pipeline"
)

func breakpointRequest(mux *http.ServeMux, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "http://127.0.0.1:8888"+path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// pauseOne 把一条 flow 按到断点上,返回它的 id 与「等 Pause 收尾」的函数。
// 必须等自己这一条进列表:等「列表非空」在多条并发时会立刻被前一条满足,
// 后面的 flow 还没挂上就去放行,少放的那条会把用例卡满整个断点超时。
func pauseOne(t *testing.T, bp *pipeline.BreakpointManager) (string, func()) {
	t.Helper()
	f := flow.New(flow.ProtoHTTP)
	f.Request = &flow.Request{Method: "GET", URL: "https://x.com/", Host: "x.com", Path: "/", Header: map[string][]string{}}
	done := make(chan bool, 1)
	go func() { done <- bp.Pause(f, flow.PhaseRequest) }()

	wait := func() {
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("Pause 未在超时前返回")
		}
	}
	// 兜底:用例中途失败时也要把 flow 放走,否则它会一直占着 goroutine 与名额。
	t.Cleanup(func() { _ = bp.Abort(f.ID) })

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, item := range bp.List() {
			if item.ID == f.ID {
				return f.ID, wait
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("flow 未进入断点暂停")
	return "", wait
}

// 放行端点在改包落地后开始承载整份消息体,超限必须回 413 而不是含糊的 400 ——
// 400 会让调用方去改 JSON,而真正的问题是这份 JSON 太大了。
func TestBreakpointResumeRejectsOversizedBody(t *testing.T) {
	s, mux := wiredServer()
	id, wait := pauseOne(t, s.pipe.Breakpoints())
	defer wait()

	huge := `{"request":{"body":"` + strings.Repeat("a", maxBreakpointBody+1024) + `"}}`
	rec := breakpointRequest(mux, http.MethodPost, "/api/breakpoints/"+id+"/resume", huge)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("超限放行 = %d, want 413", rec.Code)
	}
	if len(s.pipe.Breakpoints().List()) != 1 {
		t.Error("被拒绝的放行不应把 flow 从断点上放走")
	}
	if rec := breakpointRequest(mux, http.MethodPost, "/api/breakpoints/"+id+"/abort", ""); rec.Code != http.StatusOK {
		t.Fatalf("收尾阻断 = %d", rec.Code)
	}
}

// 编辑不合法是 400 且 flow 继续被按住;flow 已不在暂停中才是 404。
// 两者混成一个码,客户端就分不清「改回来还能重来」和「这一条已经走了」。
func TestBreakpointResumeSeparatesInvalidFromMissing(t *testing.T) {
	s, mux := wiredServer()
	id, wait := pauseOne(t, s.pipe.Breakpoints())

	bad := `{"request":{"headers":[["X-Evil","a\r\nX-Injected: 1"]]}}`
	rec := breakpointRequest(mux, http.MethodPost, "/api/breakpoints/"+id+"/resume", bad)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("非法编辑 = %d, want 400", rec.Code)
	}
	if len(s.pipe.Breakpoints().List()) != 1 {
		t.Fatal("校验失败不应放行 flow")
	}

	if rec := breakpointRequest(mux, http.MethodPost, "/api/breakpoints/"+id+"/resume", ""); rec.Code != http.StatusOK {
		t.Fatalf("改回来后放行 = %d", rec.Code)
	}
	wait()

	rec = breakpointRequest(mux, http.MethodPost, "/api/breakpoints/"+id+"/resume", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("已解除的断点 = %d, want 404", rec.Code)
	}
}

// 续期把自动放行时刻整体推后,并如实回给调用方。
func TestBreakpointExtendPushesDeadline(t *testing.T) {
	s, mux := wiredServer()
	id, wait := pauseOne(t, s.pipe.Breakpoints())
	defer wait()

	before := s.pipe.Breakpoints().List()[0].PausedUntil
	rec := breakpointRequest(mux, http.MethodPost, "/api/breakpoints/"+id+"/extend", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("续期 = %d", rec.Code)
	}
	var body struct {
		Data breakpointDeadline `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("续期返回体不可解析: %v", err)
	}
	if !body.Data.PausedUntil.After(before) {
		t.Errorf("续期后的截止时刻 %v 不晚于原值 %v", body.Data.PausedUntil, before)
	}
	if got := s.pipe.Breakpoints().List()[0].PausedUntil; !got.Equal(body.Data.PausedUntil) {
		t.Errorf("列表里的截止时刻 %v 与返回值 %v 不一致", got, body.Data.PausedUntil)
	}
	_ = s.pipe.Breakpoints().Abort(id)
}

// 批量放行一次清空全部暂停项:全局断点一开,几十条并发请求同时被按住,逐条点不是操作。
func TestBreakpointResumeAllClearsQueue(t *testing.T) {
	s, mux := wiredServer()
	bp := s.pipe.Breakpoints()
	waits := make([]func(), 0, 3)
	for i := 0; i < 3; i++ {
		_, wait := pauseOne(t, bp)
		waits = append(waits, wait)
	}

	rec := breakpointRequest(mux, http.MethodPost, "/api/breakpoints/resume-all", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("批量放行 = %d", rec.Code)
	}
	var body struct {
		Data map[string]int `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("批量放行返回体不可解析: %v", err)
	}
	if body.Data["resolved"] != 3 {
		t.Errorf("处置条数 = %d, want 3", body.Data["resolved"])
	}
	for _, wait := range waits {
		wait()
	}
	if n := len(bp.List()); n != 0 {
		t.Errorf("批量放行后仍剩 %d 条", n)
	}
}
