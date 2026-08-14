// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package service

import (
	"encoding/base64"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/mintfog/sniffy/ca"
	"github.com/mintfog/sniffy/internal/core"
	"github.com/mintfog/sniffy/internal/flow"
)

func TestNewWiresPersistencePaths(t *testing.T) {
	t.Parallel()
	rootCA, err := ca.NewInMemorySelfSignedCA()
	if err != nil {
		t.Fatalf("创建 CA 失败: %v", err)
	}
	configDir, certDir := t.TempDir(), t.TempDir()
	svc := New(rootCA, core.NewEventBus(), configDir, certDir)

	// 私钥落在 certDir(0700),不跟着配置走,见 servercert.go 的文件名注释。
	if want := filepath.Join(certDir, serverCertFileName); svc.serverCerts.path != want {
		t.Errorf("服务端证书路径 = %q, want %q", svc.serverCerts.path, want)
	}
	if want := filepath.Join(configDir, "rules.json"); svc.rules.path != want {
		t.Errorf("规则路径 = %q, want %q", svc.rules.path, want)
	}
	if want := filepath.Join(configDir, configFileName); svc.cfg.path != want {
		t.Errorf("配置路径 = %q, want %q", svc.cfg.path, want)
	}

	// 目录为空表示"仅内存",各 store 都不该猜一个路径出来。
	mem := New(rootCA, nil, "", "")
	if mem.rules.path != "" || mem.cfg.path != "" || mem.serverCerts.path != "" {
		t.Errorf("目录为空时不应有持久化路径: rules=%q cfg=%q certs=%q",
			mem.rules.path, mem.cfg.path, mem.serverCerts.path)
	}
}

// TestNewAdoptsPersistedRecording 录制开关是持久化配置,启动时应接上次的状态。
func TestNewAdoptsPersistedRecording(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, configFileName), []byte(`{"recording":false}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if svc := New(nil, nil, dir, ""); svc.IsRecording() {
		t.Error("配置里 recording=false,启动后不应处于录制态")
	}
	if svc := New(nil, nil, t.TempDir(), ""); !svc.IsRecording() {
		t.Error("无配置文件时应取默认值:录制开启")
	}
}

// TestRecordingGateDropsCapture 停止录制后抓到的会话一律不入库。
func TestRecordingGateDropsCapture(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		record func(*Service)
		count  func(*Service) int
	}{
		{
			name:   "HTTP 会话",
			record: func(s *Service) { s.RecordFlowStarted(newFlow("f1")) },
			count:  func(s *Service) int { _, n := s.Sessions(1, 10); return n },
		},
		{
			name:   "WebSocket 会话",
			record: func(s *Service) { s.RecordWSSession(&flow.WSSession{ID: "ws1", URL: "wss://x/ws"}) },
			count:  func(s *Service) int { _, n := s.WSSessions(1, 10); return n },
		},
		{
			name:   "流式会话",
			record: func(s *Service) { s.RecordStreamSession(&flow.StreamSession{ID: "st1", Kind: flow.StreamSSE}) },
			count:  func(s *Service) int { _, n := s.StreamSessions(1, 10); return n },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			svc := newTestService(t)

			svc.StopRecording()
			if svc.IsRecording() {
				t.Fatal("StopRecording 后 IsRecording 应为 false")
			}
			tt.record(svc)
			if got := tt.count(svc); got != 0 {
				t.Fatalf("停止录制时不应入库,实际存了 %d 条", got)
			}

			svc.StartRecording()
			tt.record(svc)
			if got := tt.count(svc); got != 1 {
				t.Fatalf("恢复录制后应入库 1 条,实际 %d 条", got)
			}
		})
	}
}

// TestRecordFlowCompletedUpdatesInFlight 录制中途被停掉时,已在库的请求仍应被更新为完成态。
func TestRecordFlowCompletedUpdatesInFlight(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)

	f := newFlow("in-flight")
	svc.RecordFlowStarted(f)
	svc.StopRecording()

	withResponse(http.StatusOK, "text/plain", []byte("ok"))(f)
	svc.RecordFlowCompleted(f)

	dto, ok := svc.Session("in-flight")
	if !ok {
		t.Fatal("在途会话应仍在库")
	}
	if dto.Status != "completed" || dto.Response == nil {
		t.Fatalf("在途会话应被更新为完成态: %+v", dto)
	}
	if got := svc.Statistics().TotalRequests; got != 1 {
		t.Fatalf("在途会话完成也应计入统计,实际 %d", got)
	}

	// 停录期间新出现的会话则整条丢弃(没有 pending 记录可对齐)。
	svc.RecordFlowCompleted(newFlow("stray", withResponse(http.StatusOK, "text/plain", []byte("x"))))
	if _, ok := svc.Session("stray"); ok {
		t.Error("停录期间新出现的会话不应入库")
	}
	if got := svc.Statistics().TotalRequests; got != 1 {
		t.Fatalf("被丢弃的会话不应计入统计,实际 %d", got)
	}
}

// TestFlowLifecycleEmitsEvents 守住三个事件的广播契约:UI 靠它们做增量刷新。
func TestFlowLifecycleEmitsEvents(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	rec := newEventRecorder(t, svc.Bus())

	f := newFlow("flow-evt")
	svc.RecordFlowStarted(f)
	if got := rec.types(); !slices.Equal(got, []core.EventType{core.EventFlowStarted}) {
		t.Fatalf("开始事件 = %v", got)
	}

	withResponse(http.StatusOK, "text/plain", []byte("hi"))(f)
	withDuration(12)(f)
	svc.RecordFlowCompleted(f)
	events := rec.drain()
	if len(events) != 2 || events[0].Type != core.EventFlowCompleted || events[1].Type != core.EventFlowUpdated {
		t.Fatalf("完成应先广播响应再广播整会话,实际 %+v", events)
	}
	resp, ok := events[0].Payload.(*HTTPResponseDTO)
	if !ok || resp.RequestID != "flow-evt" || resp.ResponseTime != 12 {
		t.Fatalf("flow_completed 载荷应为响应 DTO: %#v", events[0].Payload)
	}
	session, ok := events[1].Payload.(HTTPSessionDTO)
	if !ok || session.ID != "flow-evt" {
		t.Fatalf("flow_updated 载荷应为会话 DTO: %#v", events[1].Payload)
	}

	svc.RecordFlowUpdated(f)
	if got := rec.types(); !slices.Equal(got, []core.EventType{core.EventFlowUpdated}) {
		t.Fatalf("补充信息应只广播 flow_updated,实际 %v", got)
	}
}

// TestRecordFlowCompletedWithoutResponseSkipsResponseEvent 会话无响应时不广播 flow_completed,
// 避免前端渲染出状态码为 0 的假响应。
func TestRecordFlowCompletedWithoutResponseSkipsResponseEvent(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	rec := newEventRecorder(t, svc.Bus())

	f := newFlow("no-resp", withError("TLS 握手失败"))
	svc.RecordFlowStarted(f)
	rec.drain()
	svc.RecordFlowCompleted(f)

	if got := rec.types(); !slices.Equal(got, []core.EventType{core.EventFlowUpdated}) {
		t.Fatalf("无响应时应只广播 flow_updated,实际 %v", got)
	}
}

// TestImportFlowBypassesRecordingSwitch 重发由用户主动发起,不受录制开关约束。
func TestImportFlowBypassesRecordingSwitch(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	svc.StopRecording()
	rec := newEventRecorder(t, svc.Bus())

	f := newFlow("replay")
	svc.ImportFlowStarted(f)
	withResponse(http.StatusCreated, "application/json", []byte(`{}`))(f)
	svc.ImportFlowCompleted(f)

	if got := rec.types(); !slices.Equal(got, []core.EventType{core.EventFlowStarted, core.EventFlowUpdated}) {
		t.Fatalf("重发事件 = %v", got)
	}
	if dto, ok := svc.Session("replay"); !ok || dto.Response == nil || dto.Response.Status != http.StatusCreated {
		t.Fatalf("重发会话应入库并带响应: %+v", dto)
	}
	if got := svc.Statistics().TotalRequests; got != 1 {
		t.Fatalf("重发应计入统计,实际 %d", got)
	}
}

// TestSessionsPagination 分页按"最新优先";参数来自 URL query,越界与非法值应回退到安全值。
func TestSessionsPagination(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	for _, id := range []string{"f1", "f2", "f3", "f4", "f5"} {
		svc.RecordFlowStarted(newFlow(id))
	}

	tests := []struct {
		name     string
		page     int
		pageSize int
		want     []string
	}{
		{"首页最新优先", 1, 2, []string{"f5", "f4"}},
		{"第二页顺延", 2, 2, []string{"f3", "f2"}},
		{"末页不足一页", 3, 2, []string{"f1"}},
		{"越界页返回空而非报错", 9, 2, []string{}},
		{"页码小于 1 视为首页", 0, 2, []string{"f5", "f4"}},
		{"页大小非法时取默认值", 1, 0, []string{"f5", "f4", "f3", "f2", "f1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			list, total := svc.Sessions(tt.page, tt.pageSize)
			if total != 5 {
				t.Errorf("总数 = %d, want 5", total)
			}
			if got := sessionIDs(list); !slices.Equal(got, tt.want) {
				t.Errorf("分页结果 = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSessionLookupDeleteClear(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	svc.RecordFlowStarted(newFlow("keep"))
	svc.RecordFlowStarted(newFlow("drop"))

	if _, ok := svc.Session("missing"); ok {
		t.Error("未知 ID 不应查到会话")
	}
	if _, ok := svc.RawFlow("missing"); ok {
		t.Error("未知 ID 不应查到 Flow")
	}
	// RawFlow 必须回真正的底层对象:断点编辑要就地改写它。
	if raw, ok := svc.RawFlow("keep"); !ok || raw.ID != "keep" {
		t.Fatalf("RawFlow 应返回底层 Flow: %+v", raw)
	}

	svc.DeleteSession("drop")
	if _, ok := svc.Session("drop"); ok {
		t.Error("删除后不应还能查到")
	}
	if _, total := svc.Sessions(1, 10); total != 1 {
		t.Errorf("删除后总数 = %d, want 1", total)
	}

	svc.ClearSessions()
	if list, total := svc.Sessions(1, 10); total != 0 || len(list) != 0 {
		t.Errorf("清空后应无会话: total=%d len=%d", total, len(list))
	}
}

// TestMessageBodyBySource 按需拉取消息体的分支表:DTO 里被丢空的二进制内容靠它取回。
func TestMessageBodyBySource(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	svc.RecordFlowCompleted(newFlow("both",
		withRequestHeader("Content-Type", "application/json"),
		withRequestBody([]byte(`{"a":1}`)),
		withResponse(http.StatusOK, "text/plain", []byte("hello")),
	))
	noReq := newFlow("no-req", withResponse(http.StatusOK, "text/plain", []byte("x")))
	noReq.Request = nil
	svc.RecordFlowCompleted(noReq)
	svc.RecordFlowStarted(newFlow("no-resp"))

	tests := []struct {
		name     string
		id       string
		source   string
		wantOK   bool
		wantMime string
		wantBody string
	}{
		{"取请求体", "both", "request", true, "application/json", `{"a":1}`},
		{"取响应体", "both", "response", true, "text/plain", "hello"},
		{"source 缺省按响应处理", "both", "", true, "text/plain", "hello"},
		{"未知会话不可用", "missing", "response", false, "", ""},
		{"无请求的会话取请求体不可用", "no-req", "request", false, "", ""},
		{"无响应的会话取响应体不可用", "no-resp", "response", false, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dto, ok := svc.MessageBody(tt.id, tt.source)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if dto.Mime != tt.wantMime {
				t.Errorf("mime = %q, want %q", dto.Mime, tt.wantMime)
			}
			raw, err := base64.StdEncoding.DecodeString(dto.Base64)
			if err != nil {
				t.Fatalf("Base64 应可解码: %v", err)
			}
			if string(raw) != tt.wantBody {
				t.Errorf("内容 = %q, want %q", raw, tt.wantBody)
			}
			if dto.Size != len(tt.wantBody) {
				t.Errorf("size = %d, want %d", dto.Size, len(tt.wantBody))
			}
		})
	}
}

// TestMessageRawBodyReturnsUnencodedBytes 另存/原始查看走这条路径,拿到的必须是裸字节。
func TestMessageRawBodyReturnsUnencodedBytes(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	body := []byte{0x89, 'P', 'N', 'G', 0x00, 0x1a}
	svc.RecordFlowCompleted(newFlow("raw",
		withRequestBody([]byte("req-bytes")),
		withResponse(http.StatusOK, "image/png", body),
	))

	data, mime, ok := svc.MessageRawBody("raw", "response")
	if !ok || string(data) != string(body) || mime != "image/png" {
		t.Fatalf("响应原始体 = %q/%q/%v", data, mime, ok)
	}
	// 请求侧没有 Content-Type,MIME 只能靠内容嗅探。
	data, mime, ok = svc.MessageRawBody("raw", "request")
	if !ok || string(data) != "req-bytes" || mime == "" {
		t.Fatalf("请求原始体 = %q/%q/%v", data, mime, ok)
	}
	if _, _, ok := svc.MessageRawBody("missing", "response"); ok {
		t.Error("未知会话不应返回原始体")
	}

	// 消息不存在时判定不可用,而不是返回 0 字节内容。
	noReq := newFlow("no-req", withResponse(http.StatusOK, "text/plain", []byte("x")))
	noReq.Request = nil
	svc.RecordFlowCompleted(noReq)
	svc.RecordFlowStarted(newFlow("no-resp"))
	if _, _, ok := svc.MessageRawBody("no-req", "request"); ok {
		t.Error("无请求的会话不应返回请求原始体")
	}
	if _, _, ok := svc.MessageRawBody("no-resp", "response"); ok {
		t.Error("无响应的会话不应返回响应原始体")
	}
}

// TestMessageRawBodyReadsSpilledCopy 走过透传旁路的响应体只在磁盘上,要能读回;副本被淘汰后判定不可用。
func TestMessageRawBodyReadsSpilledCopy(t *testing.T) {
	t.Parallel()
	svc, c := newSpillService(t)
	data := []byte("落盘的视频字节")
	f := spilledFlow(t, c, "spilled", data)
	path, _ := f.Response.BodyFile()
	svc.RecordFlowCompleted(f)

	got, mime, ok := svc.MessageRawBody("spilled", "response")
	if !ok || string(got) != string(data) || mime != "video/mp4" {
		t.Fatalf("落盘体原始读取 = %q/%q/%v", got, mime, ok)
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := svc.MessageRawBody("spilled", "response"); ok {
		t.Error("副本已被淘汰时应判定不可用")
	}
}

// TestMessageBodyTooLargeSkipsEncoding 超过预览上限的体只回元信息并打上 tooLarge,不做 base64。
func TestMessageBodyTooLargeSkipsEncoding(t *testing.T) {
	if testing.Short() {
		t.Skip("需要分配 25MiB 缓冲")
	}
	t.Parallel()
	svc := newTestService(t)
	huge := make([]byte, maxRawBodyBytes+1)
	svc.RecordFlowCompleted(newFlow("huge", withResponse(http.StatusOK, "application/octet-stream", huge)))

	dto, ok := svc.MessageBody("huge", "response")
	if !ok {
		t.Fatal("超大体也应返回元信息")
	}
	if !dto.TooLarge || dto.Base64 != "" {
		t.Fatalf("超大体不应被编码: tooLarge=%v base64Len=%d", dto.TooLarge, len(dto.Base64))
	}
	if dto.Size != len(huge) {
		t.Errorf("size = %d, want %d", dto.Size, len(huge))
	}
	// 另存不受预览上限约束,拿到的仍是完整字节。
	if data, _, ok := svc.MessageRawBody("huge", "response"); !ok || len(data) != len(huge) {
		t.Fatalf("原始体应不受预览上限约束: len=%d ok=%v", len(data), ok)
	}
}

func TestWSAndStreamSessionLookup(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	end := time.Now()
	svc.RecordWSSession(&flow.WSSession{
		ID: "ws-1", URL: "wss://x/ws", Status: "closed", StartTime: time.Now(), EndTime: &end,
		MessageCount: 2, TotalSize: 10,
	})
	svc.RecordStreamSession(&flow.StreamSession{
		ID: "st-1", URL: "https://x/sse", Kind: flow.StreamSSE, Status: "open",
		StartTime: time.Now(), StatusCode: http.StatusOK,
	})

	if dto, ok := svc.WSSession("ws-1"); !ok || dto.URL != "wss://x/ws" || dto.EndTime == "" {
		t.Fatalf("WebSocket 会话 = %+v ok=%v", dto, ok)
	}
	if _, ok := svc.WSSession("nope"); ok {
		t.Error("未知 WebSocket 会话不应查到")
	}
	if dto, ok := svc.StreamSession("st-1"); !ok || dto.Kind != flow.StreamSSE || dto.StatusCode != http.StatusOK {
		t.Fatalf("流式会话 = %+v ok=%v", dto, ok)
	}
	if _, ok := svc.StreamSession("nope"); ok {
		t.Error("未知流式会话不应查到")
	}

	if list, total := svc.WSSessions(1, 10); total != 1 || len(list) != 1 {
		t.Errorf("WebSocket 分页 = %d/%d", len(list), total)
	}
	if list, total := svc.StreamSessions(1, 10); total != 1 || len(list) != 1 {
		t.Errorf("流式分页 = %d/%d", len(list), total)
	}
}

func TestUptimeSecondsCountsFromStart(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	if got := svc.UptimeSeconds(); got != 0 {
		t.Fatalf("刚启动的运行时长 = %d, want 0", got)
	}
	svc.startTime = time.Now().Add(-90 * time.Second)
	if got := svc.UptimeSeconds(); got != 90 {
		t.Fatalf("运行时长 = %d, want 90", got)
	}
}

func TestBusReturnsInjectedBus(t *testing.T) {
	t.Parallel()
	bus := core.NewEventBus()
	if svc := New(nil, bus, "", ""); svc.Bus() != bus {
		t.Error("Bus 应返回装配层注入的总线")
	}
	// 无总线时广播应为静默 no-op。
	svc := New(nil, nil, "", "")
	svc.RecordFlowStarted(newFlow("no-bus"))
	if _, ok := svc.Session("no-bus"); !ok {
		t.Error("无总线时仍应正常入库")
	}
}
