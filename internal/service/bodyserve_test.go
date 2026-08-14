// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package service

import (
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"testing"
)

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
	t.Parallel()
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
// 否则 <video> 只能从头顺放。落盘与内存两条路径都要支持。
func TestServeMessageBodyRange(t *testing.T) {
	t.Parallel()
	const data = "0123456789abcdef"

	tests := []struct {
		name      string
		spilled   bool
		mime      string
		rangeHdr  string
		wantCode  int
		wantBody  string
		wantRange string
	}{
		{"落盘体取中段", true, "video/mp4", "bytes=4-7", http.StatusPartialContent, "4567", "bytes 4-7/16"},
		{"落盘体取到结尾", true, "video/mp4", "bytes=12-", http.StatusPartialContent, "cdef", "bytes 12-15/16"},
		{"内存体取中段", false, "audio/mpeg", "bytes=6-9", http.StatusPartialContent, "6789", "bytes 6-9/16"},
		{"内存体无 Range 整块发出", false, "audio/mpeg", "", http.StatusOK, data, ""},
		// 起点越界按 RFC 7233 回 416,而不是静默回整块。
		{"起点越界", false, "audio/mpeg", "bytes=99-", http.StatusRequestedRangeNotSatisfiable, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			svc, c := newSpillService(t)
			if tt.spilled {
				svc.RecordFlowCompleted(spilledFlow(t, c, "flow", []byte(data)))
			} else {
				svc.RecordFlowCompleted(newFlow("flow", withResponse(http.StatusOK, tt.mime, []byte(data))))
			}

			rec := serveBody(svc, "flow", "response", tt.rangeHdr)
			if rec.Code != tt.wantCode {
				t.Fatalf("状态码 = %d, want %d", rec.Code, tt.wantCode)
			}
			if tt.wantCode >= http.StatusBadRequest {
				return
			}
			if got := rec.Body.String(); got != tt.wantBody {
				t.Fatalf("内容 = %q, want %q", got, tt.wantBody)
			}
			if cr := rec.Header().Get("Content-Range"); cr != tt.wantRange {
				t.Fatalf("Content-Range = %q, want %q", cr, tt.wantRange)
			}
			if ct := rec.Header().Get("Content-Type"); ct != tt.mime {
				t.Fatalf("MIME = %q, want %q", ct, tt.mime)
			}
			if ar := rec.Header().Get("Accept-Ranges"); ar != "bytes" {
				t.Fatalf("应声明支持 Range,实际 %q", ar)
			}
		})
	}
}

// TestServeMessageBodyServesRequestSide 请求体同样可经这条路径取(如上传的大文件)。
func TestServeMessageBodyServesRequestSide(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	svc.RecordFlowCompleted(newFlow("upload",
		withRequestHeader("Content-Type", "application/octet-stream"),
		withRequestBody([]byte("payload")),
		withResponse(http.StatusOK, "text/plain", []byte("ok")),
	))

	rec := serveBody(svc, "upload", "request", "")
	if rec.Code != http.StatusOK || rec.Body.String() != "payload" {
		t.Fatalf("请求体 = %d/%q", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Fatalf("MIME = %q", ct)
	}
}

// TestServeMessageBodyMissing 会话不存在、或副本已被缓存淘汰时回 404,而不是空 200 ——
// 前端据此显示「副本已清理」而非放一个永远转圈的播放器。
func TestServeMessageBodyMissing(t *testing.T) {
	t.Parallel()
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

// TestServeMessageBodyUnreadableSpill 副本存在但打不开(权限变更 / stat 之后被删)时同样回 404。
func TestServeMessageBodyUnreadableSpill(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("依赖 POSIX 文件权限,且 root 会绕过权限检查")
	}
	t.Parallel()
	svc, c := newSpillService(t)
	f := spilledFlow(t, c, "flow-locked", []byte("读不到"))
	path, _ := f.Response.BodyFile()
	svc.RecordFlowCompleted(f)
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}

	if rec := serveBody(svc, "flow-locked", "response", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("副本不可读应回 404,实际 %d", rec.Code)
	}
}

// TestMessageBodyInfoNoBytes 元信息查询不该把内容读进来(大体积媒体只取头信息)。
func TestMessageBodyInfoNoBytes(t *testing.T) {
	t.Parallel()
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
