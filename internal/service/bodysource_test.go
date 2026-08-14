// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package service

import (
	"net/http"
	"os"
	"testing"
)

// TestMessageBodySourceUsesFile 落盘的响应体应以「路径」形式交给另存,而不是读进内存。
func TestMessageBodySourceUsesFile(t *testing.T) {
	t.Parallel()
	svc, c := newSpillService(t)
	data := []byte("视频字节视频字节")
	f := spilledFlow(t, c, "flow-a", data)
	svc.RecordFlowCompleted(f)

	src, ok := svc.MessageBodySource("flow-a", "response")
	if !ok {
		t.Fatal("应能取到消息体来源")
	}
	if src.Path == "" {
		t.Fatal("落盘的响应体应返回路径而非内存字节")
	}
	if len(src.Data) != 0 {
		t.Fatalf("返回路径时不该同时把字节读进内存: %d 字节", len(src.Data))
	}
	if src.Size != int64(len(data)) || src.Mime != "video/mp4" {
		t.Fatalf("元信息不对: size=%d mime=%q", src.Size, src.Mime)
	}
	got, err := os.ReadFile(src.Path)
	if err != nil || string(got) != string(data) {
		t.Fatalf("副本内容不对: err=%v got=%q", err, got)
	}
}

// TestMessageBodySourceFromMemory 未走旁路的体直接给字节,不带路径。
func TestMessageBodySourceFromMemory(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	svc.RecordFlowCompleted(newFlow("mem",
		withRequestHeader("Content-Type", "application/json"),
		withRequestBody([]byte(`{"a":1}`)),
		withResponse(http.StatusOK, "text/plain", []byte("hello")),
	))
	svc.RecordFlowStarted(newFlow("empty"))
	// 204 一类的响应存在但没有体,另存同样判定不可用。
	svc.RecordFlowCompleted(newFlow("no-content", withResponse(http.StatusNoContent, "text/plain", nil)))

	tests := []struct {
		name     string
		id       string
		source   string
		wantOK   bool
		wantData string
		wantMime string
	}{
		{"响应体", "mem", "response", true, "hello", "text/plain"},
		{"请求体", "mem", "request", true, `{"a":1}`, "application/json"},
		{"无响应判定不可用", "empty", "response", false, "", ""},
		{"空请求体判定不可用", "empty", "request", false, "", ""},
		{"响应体为空判定不可用", "no-content", "response", false, "", ""},
		{"未知会话", "missing", "response", false, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			src, ok := svc.MessageBodySource(tt.id, tt.source)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if src.Path != "" {
				t.Errorf("内存中的体不应带路径: %q", src.Path)
			}
			if string(src.Data) != tt.wantData || src.Mime != tt.wantMime {
				t.Errorf("来源 = %q/%q, want %q/%q", src.Data, src.Mime, tt.wantData, tt.wantMime)
			}
			if src.Size != int64(len(tt.wantData)) {
				t.Errorf("大小 = %d, want %d", src.Size, len(tt.wantData))
			}
		})
	}
}

// TestSessionDTOSizeFromSpilledBody 大小取自 BodyLen,不因 Body 为空而显示 0 ——
// 前端的另存按钮可用性据此判断。
func TestSessionDTOSizeFromSpilledBody(t *testing.T) {
	t.Parallel()
	svc, c := newSpillService(t)
	data := make([]byte, 1234)
	svc.RecordFlowCompleted(spilledFlow(t, c, "flow-size", data))

	dto, ok := svc.Session("flow-size")
	if !ok || dto.Response == nil {
		t.Fatal("应能取到会话")
	}
	if dto.Response.Size != int64(len(data)) {
		t.Fatalf("大小应为 %d,实际 %d", len(data), dto.Response.Size)
	}
}

// TestEvictionRemovesSpilledBody 会话被删除 / 淘汰后,它的副本就再没人能取到,必须删掉。
func TestEvictionRemovesSpilledBody(t *testing.T) {
	t.Parallel()
	svc, c := newSpillService(t)
	f := spilledFlow(t, c, "flow-evict", []byte("待回收"))
	path, _ := f.Response.BodyFile()
	svc.RecordFlowCompleted(f)

	svc.DeleteSession("flow-evict")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("删除会话后副本应被回收: %v", err)
	}
	if c.Total() != 0 {
		t.Fatalf("缓存记账应清零: got %d", c.Total())
	}

	// 清空会话同样要回收。
	f2 := spilledFlow(t, c, "flow-clear", []byte("也待回收"))
	path2, _ := f2.Response.BodyFile()
	svc.RecordFlowCompleted(f2)
	svc.ClearSessions()
	if _, err := os.Stat(path2); !os.IsNotExist(err) {
		t.Fatalf("清空会话后副本应被回收: %v", err)
	}
}

// TestCapEvictionRemovesSpilledBody 会话环满而淘汰最旧记录时,其副本同样要回收。
func TestCapEvictionRemovesSpilledBody(t *testing.T) {
	t.Parallel()
	svc, c := newSpillService(t)
	svc.sessions.setCap(2)

	oldest := spilledFlow(t, c, "flow-1", []byte("最旧"))
	oldPath, _ := oldest.Response.BodyFile()
	svc.RecordFlowCompleted(oldest)
	svc.RecordFlowCompleted(spilledFlow(t, c, "flow-2", []byte("其次")))
	svc.RecordFlowCompleted(spilledFlow(t, c, "flow-3", []byte("最新")))

	if _, ok := svc.sessions.get("flow-1"); ok {
		t.Fatal("最旧的会话应已被淘汰")
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("被淘汰会话的副本应被回收: %v", err)
	}
}

// TestEvictionToleratesFlowsWithoutSpill 淘汰回调会收到无响应、响应在内存等各类会话,不应误删他人副本。
func TestEvictionToleratesFlowsWithoutSpill(t *testing.T) {
	t.Parallel()
	svc, c := newSpillService(t)
	svc.sessions.setCap(1)

	svc.RecordFlowStarted(newFlow("no-resp"))
	svc.RecordFlowCompleted(newFlow("mem-body", withResponse(http.StatusOK, "text/plain", []byte("x"))))

	spill := spilledFlow(t, c, "spill", []byte("落盘内容"))
	path, _ := spill.Response.BodyFile()
	svc.RecordFlowCompleted(spill)

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("仍在库的会话副本不该被回收: %v", err)
	}
	if _, total := svc.Sessions(1, 10); total != 1 {
		t.Fatalf("容量为 1 时应只剩最新一条,实际 %d", total)
	}
}

// TestMessageBodyFallsBackWhenSpillGone 副本被缓存淘汰后,预览只回元信息而不是报错崩掉。
func TestMessageBodyFallsBackWhenSpillGone(t *testing.T) {
	t.Parallel()
	svc, c := newSpillService(t)
	f := spilledFlow(t, c, "flow-gone", []byte("会消失"))
	path, _ := f.Response.BodyFile()
	svc.RecordFlowCompleted(f)
	_ = os.Remove(path) // 模拟被容量上限淘汰

	dto, ok := svc.MessageBody("flow-gone", "response")
	if !ok {
		t.Fatal("副本没了也应返回元信息")
	}
	if dto.Base64 != "" {
		t.Fatal("副本已不在,不该有内容")
	}
	if dto.Mime != "video/mp4" || dto.Size == 0 {
		t.Fatalf("元信息应仍然可用: mime=%q size=%d", dto.Mime, dto.Size)
	}
	if _, ok := svc.MessageBodySource("flow-gone", "response"); ok {
		t.Fatal("副本已不在,另存应直接判定不可用")
	}
}
