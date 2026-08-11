// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/internal/service"
)

// audioFlow 造一条体在内存的音频响应。
func audioFlow(id string, data []byte) *flow.Flow {
	f := flow.New(flow.ProtoHTTPS)
	f.ID = id
	f.State = flow.StateCompleted
	f.Request = &flow.Request{Method: "GET", URL: "https://x/a.mp3", Header: map[string][]string{}}
	f.Response = &flow.Response{
		Status: 200,
		Header: map[string][]string{"Content-Type": {"audio/mpeg"}},
		Body:   data,
	}
	return f
}

// TestSessionBodyRawRoute /body/raw 走流式原始字节(支持 Range),不能被 /body 的
// base64 分支截走 —— 两条路径同前缀,顺序错了音视频就永远拿不到可播放的字节。
func TestSessionBodyRawRoute(t *testing.T) {
	svc := service.New(nil, nil, "", "")
	svc.RecordFlowCompleted(audioFlow("flow-raw", []byte("0123456789")))
	server := &Server{svc: svc}

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/flow-raw/body/raw?source=response", nil)
	req.Header.Set("Range", "bytes=2-5")
	rec := httptest.NewRecorder()
	server.handleSession(rec, req)

	if rec.Code != http.StatusPartialContent {
		t.Fatalf("状态码应为 206,实际 %d,响应: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); got != "2345" {
		t.Fatalf("分片内容不对: %q", got)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "audio/mpeg" {
		t.Fatalf("MIME 不对: %q", ct)
	}
}

// TestSessionBodyBase64RouteUnchanged /body 仍回 base64 元信息(图片预览依赖它)。
func TestSessionBodyBase64RouteUnchanged(t *testing.T) {
	svc := service.New(nil, nil, "", "")
	svc.RecordFlowCompleted(audioFlow("flow-b64", []byte("0123456789")))
	server := &Server{svc: svc}

	rec := httptest.NewRecorder()
	server.handleSession(rec, httptest.NewRequest(http.MethodGet, "/api/sessions/flow-b64/body", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码应为 200,实际 %d", rec.Code)
	}
	var resp struct {
		Data service.BodyDTO `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v (%s)", err, rec.Body.String())
	}
	if resp.Data.Mime != "audio/mpeg" || resp.Data.Base64 == "" {
		t.Fatalf("base64 分支不该被改变: %+v", resp.Data)
	}
}
