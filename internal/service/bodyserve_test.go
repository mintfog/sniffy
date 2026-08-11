// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package service

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/mintfog/sniffy/internal/flow"
)

// memFlow 造一条「体在内存」的 Flow(未走透传旁路的小体积响应)。
func memFlow(id string, data []byte, mime string) *flow.Flow {
	f := flow.New(flow.ProtoHTTPS)
	f.ID = id
	f.State = flow.StateCompleted
	f.Request = &flow.Request{Method: "GET", URL: "https://x/a.mp3", Header: map[string][]string{}}
	f.Response = &flow.Response{
		Status: 200,
		Header: map[string][]string{"Content-Type": {mime}},
		Body:   data,
	}
	return f
}

func serveBody(svc *Service, id, source, rangeHdr string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/body/"+id+"?source="+source, nil)
	if rangeHdr != "" {
		req.Header.Set("Range", rangeHdr)
	}
	rec := httptest.NewRecorder()
	svc.ServeMessageBody(rec, req, id, source)
	return rec
}

// TestServeMessageBodyFromSpill 落盘的响应体应整块流式发出,并带上响应头里的 MIME。
func TestServeMessageBodyFromSpill(t *testing.T) {
	svc, c := newSpillService(t)
	data := []byte("0123456789abcdef")
	svc.RecordFlowCompleted(spilledFlow(t, c, "flow-play", data))

	rec := serveBody(svc, "flow-play", "response", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码应为 200,实际 %d", rec.Code)
	}
	if got := rec.Body.String(); got != string(data) {
		t.Fatalf("内容不对: %q", got)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "video/mp4" {
		t.Fatalf("MIME 应取自响应头,实际 %q", ct)
	}
}

// TestServeMessageBodyRange 播放器拖进度条靠 Range:必须回 206 与正确的 Content-Range,
// 否则 <video> 只能从头顺放。
func TestServeMessageBodyRange(t *testing.T) {
	svc, c := newSpillService(t)
	data := []byte("0123456789abcdef")
	svc.RecordFlowCompleted(spilledFlow(t, c, "flow-seek", data))

	rec := serveBody(svc, "flow-seek", "response", "bytes=4-7")
	if rec.Code != http.StatusPartialContent {
		t.Fatalf("状态码应为 206,实际 %d", rec.Code)
	}
	if got := rec.Body.String(); got != "4567" {
		t.Fatalf("分片内容不对: %q", got)
	}
	if cr := rec.Header().Get("Content-Range"); cr != "bytes 4-7/16" {
		t.Fatalf("Content-Range 不对: %q", cr)
	}
	if ar := rec.Header().Get("Accept-Ranges"); ar != "bytes" {
		t.Fatalf("应声明支持 Range,实际 %q", ar)
	}
}

// TestServeMessageBodyFromMemory 未走旁路的体在内存里,同样要支持 Range。
func TestServeMessageBodyFromMemory(t *testing.T) {
	svc, _ := newSpillService(t)
	f := memFlow("flow-mem", []byte("audio-bytes"), "audio/mpeg")
	svc.RecordFlowCompleted(f)

	rec := serveBody(svc, "flow-mem", "response", "bytes=6-")
	if rec.Code != http.StatusPartialContent {
		t.Fatalf("状态码应为 206,实际 %d", rec.Code)
	}
	if got := rec.Body.String(); got != "bytes" {
		t.Fatalf("分片内容不对: %q", got)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "audio/mpeg" {
		t.Fatalf("MIME 不对: %q", ct)
	}
}

// TestServeMessageBodyMissing 会话不存在、或副本已被缓存淘汰时回 404,而不是空 200 ——
// 前端据此显示「副本已清理」而非放一个永远转圈的播放器。
func TestServeMessageBodyMissing(t *testing.T) {
	svc, c := newSpillService(t)
	if rec := serveBody(svc, "flow-none", "response", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("未知会话应回 404,实际 %d", rec.Code)
	}

	f := spilledFlow(t, c, "flow-dropped", []byte("会消失"))
	path, _ := f.Response.BodyFile()
	svc.RecordFlowCompleted(f)
	_ = os.Remove(path)

	if rec := serveBody(svc, "flow-dropped", "response", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("副本已淘汰应回 404,实际 %d", rec.Code)
	}
}

// TestMessageBodyInfoNoBytes 元信息查询不该把内容读进来(大体积媒体只取头信息)。
func TestMessageBodyInfoNoBytes(t *testing.T) {
	svc, c := newSpillService(t)
	data := make([]byte, 4096)
	svc.RecordFlowCompleted(spilledFlow(t, c, "flow-info", data))

	info, ok := svc.MessageBodyInfo("flow-info", "response")
	if !ok {
		t.Fatal("应能取到元信息")
	}
	if info.Mime != "video/mp4" || info.Size != int64(len(data)) {
		t.Fatalf("元信息不对: %+v", info)
	}
	if _, ok := svc.MessageBodyInfo("flow-info", "request"); ok {
		t.Fatal("请求体为空时应判定不可用")
	}
}
