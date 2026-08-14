// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package service

import (
	"encoding/base64"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mintfog/sniffy/internal/flow"
)

func TestRFC3339PreservesFractionalSeconds(t *testing.T) {
	t.Parallel()
	// 同一毫秒内可能有多条会话,时间戳需保留小数位以便排序。
	got := rfc3339(time.Date(2026, time.July, 29, 12, 34, 56, 123456789, time.UTC))
	want := "2026-07-29T12:34:56.123456789Z"
	if got != want {
		t.Fatalf("rfc3339() = %q, want %q", got, want)
	}
	// 零值时间(如尚未拿到响应)应留空。
	if got := rfc3339(time.Time{}); got != "" {
		t.Fatalf("零值时间应为空串,实际 %q", got)
	}
}

// TestStateToStatus 守住 Flow 状态机到前端联合类型(pending/completed/error)的收敛映射。
func TestStateToStatus(t *testing.T) {
	t.Parallel()
	tests := []struct {
		state flow.FlowState
		want  string
	}{
		{flow.StatePending, "pending"},
		{flow.StateAwaitingResponse, "pending"},
		{flow.StateCompleted, "completed"},
		{flow.StateMocked, "completed"},
		{flow.StateBlocked, "error"},
		{flow.StateErrored, "error"},
		{flow.StatePausedAtBreakpoint, "error"},
		{flow.FlowState("未来新增的状态"), "error"},
	}
	for _, tt := range tests {
		t.Run(string(tt.state), func(t *testing.T) {
			t.Parallel()
			if got := stateToStatus(tt.state); got != tt.want {
				t.Errorf("stateToStatus(%q) = %q, want %q", tt.state, got, tt.want)
			}
		})
	}
}

func TestDetectMIME(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		header map[string][]string
		body   []byte
		want   string
	}{
		{"取自 Content-Type", map[string][]string{"Content-Type": {"application/json"}}, nil, "application/json"},
		{"剥掉参数", map[string][]string{"Content-Type": {"text/html; charset=utf-8"}}, nil, "text/html"},
		{"两侧空白", map[string][]string{"Content-Type": {"  image/png  "}}, nil, "image/png"},
		{"非规范键名", map[string][]string{"content-type": {"image/webp"}}, nil, "image/webp"},
		{"空 Content-Type 回退嗅探", map[string][]string{"Content-Type": {" ; charset=utf-8"}}, []byte("<html>"), "text/html; charset=utf-8"},
		{"无头时按内容嗅探", nil, []byte("\x89PNG\r\n\x1a\n"), "image/png"},
		{"无头无体时兜底", nil, nil, "application/octet-stream"},
		{"头值为空列表", map[string][]string{"Content-Type": {}}, nil, "application/octet-stream"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := detectMIME(tt.header, tt.body); got != tt.want {
				t.Errorf("detectMIME() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFirstHeaderValue(t *testing.T) {
	t.Parallel()
	header := map[string][]string{
		"Content-Type":    {"application/json", "text/plain"},
		"x-custom-header": {"v1"},
		"Empty":           {},
	}
	tests := []struct {
		name string
		key  string
		want string
	}{
		{"精确命中取首值", "Content-Type", "application/json"},
		{"大小写不敏感兜底", "X-Custom-Header", "v1"},
		{"空值列表视为缺失", "Empty", ""},
		{"缺失键", "Nope", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := firstHeaderValue(header, tt.key); got != tt.want {
				t.Errorf("firstHeaderValue(%q) = %q, want %q", tt.key, got, tt.want)
			}
		})
	}
}

// TestFlattenHeaders 前端头表是一维的:多值头取首值,空值列表整项跳过。
func TestFlattenHeaders(t *testing.T) {
	t.Parallel()
	got := flattenHeaders(map[string][]string{
		"Set-Cookie":   {"a=1", "b=2"},
		"Content-Type": {"text/plain"},
		"Empty":        {},
	})
	want := map[string]string{"Set-Cookie": "a=1", "Content-Type": "text/plain"}
	if !maps.Equal(got, want) {
		t.Fatalf("flattenHeaders() = %v, want %v", got, want)
	}
	if got := flattenHeaders(nil); len(got) != 0 {
		t.Fatalf("空头应转出空表,实际 %v", got)
	}
}

// TestSessionDTOMapsFlow 会话列表的每一列都取自这里,逐字段守住映射。
func TestSessionDTOMapsFlow(t *testing.T) {
	t.Parallel()
	f := newFlow("flow-1",
		withRequest(http.MethodPost, "https://api.example.com/v1/login?x=1"),
		withRequestHeader("User-Agent", "sniffy-test/1.0"),
		withRequestHeader("Content-Type", "application/json"),
		withRequestBody([]byte(`{"user":"a"}`)),
		withResponse(http.StatusCreated, "application/json", []byte(`{"ok":true}`)),
		withResponseHeader("X-Trace", "abc"),
		withDuration(42),
		withModified(),
		withProcess(&flow.ProcessInfo{
			PID: 4242, Name: "curl", Path: "/usr/bin/curl", User: "tester",
			HasIcon: true, IconData: "aW1n", IconType: "png", IconCategory: "cli",
		}),
	)
	f.Timing.ResponseAt = f.Timing.RequestAt.Add(42 * time.Millisecond)

	dto := SessionDTO(f)

	if dto.ID != "flow-1" || dto.Status != "completed" || dto.Duration != 42 || !dto.Modified || dto.Blocked {
		t.Fatalf("会话字段 = %+v", dto)
	}
	req := dto.Request
	if req.ID != "flow-1" || req.Method != http.MethodPost || req.URL != "https://api.example.com/v1/login?x=1" {
		t.Errorf("请求行 = %+v", req)
	}
	if req.Host != "api.example.com" || req.Path != "/v1/login" || req.Protocol != flow.ProtoHTTPS {
		t.Errorf("请求定位字段 = %+v", req)
	}
	if req.UserAgent != "sniffy-test/1.0" || req.Headers["Content-Type"] != "application/json" {
		t.Errorf("请求头 = %+v", req)
	}
	if req.Body != `{"user":"a"}` || req.Timestamp == "" {
		t.Errorf("请求体/时间戳 = %q / %q", req.Body, req.Timestamp)
	}

	resp := dto.Response
	if resp == nil {
		t.Fatal("应带响应 DTO")
	}
	// 响应 DTO 自带 ID:前端把响应当独立实体缓存,与请求靠 RequestID 关联。
	if resp.ID != "flow-1-resp" || resp.RequestID != "flow-1" {
		t.Errorf("响应标识 = %+v", resp)
	}
	if resp.Status != http.StatusCreated || resp.Body != `{"ok":true}` || resp.Headers["X-Trace"] != "abc" {
		t.Errorf("响应内容 = %+v", resp)
	}
	if resp.Size != int64(len(`{"ok":true}`)) || resp.ResponseTime != 42 || resp.Timestamp == "" {
		t.Errorf("响应元信息 = %+v", resp)
	}

	if dto.ProcessName != "curl" || dto.ProcessID != 4242 || dto.ProcessPath != "/usr/bin/curl" || dto.ProcessUser != "tester" {
		t.Errorf("进程字段 = %+v", dto)
	}
	if !dto.HasIcon || dto.IconData != "aW1n" || dto.IconType != "png" || dto.IconCategory != "cli" {
		t.Errorf("图标字段 = %+v", dto)
	}
}

func TestSessionDTOPartialFlows(t *testing.T) {
	t.Parallel()

	t.Run("无响应", func(t *testing.T) {
		t.Parallel()
		f := newFlow("pending-1")
		dto := SessionDTO(f)
		if dto.Response != nil {
			t.Errorf("未拿到响应时不应造一个空响应: %+v", dto.Response)
		}
		if ResponseDTO(f) != nil {
			t.Error("ResponseDTO 在无响应时应为 nil,否则会广播出状态码 0 的假响应")
		}
		if dto.Status != "pending" || dto.Request.Method != http.MethodGet {
			t.Errorf("会话 = %+v", dto)
		}
	})

	t.Run("无请求", func(t *testing.T) {
		t.Parallel()
		f := newFlow("no-req", withResponse(http.StatusBadGateway, "", nil))
		f.Request = nil
		dto := SessionDTO(f)
		if dto.Request.Method != "" || dto.Request.ID != "" {
			t.Errorf("无请求时请求 DTO 应为零值: %+v", dto.Request)
		}
		if dto.Response == nil || dto.Response.Status != http.StatusBadGateway {
			t.Errorf("响应仍应转换: %+v", dto.Response)
		}
	})

	t.Run("被阻断", func(t *testing.T) {
		t.Parallel()
		dto := SessionDTO(newFlow("blocked", withState(flow.StateBlocked)))
		if !dto.Blocked || dto.Status != "error" {
			t.Errorf("阻断会话 = %+v", dto)
		}
	})

	t.Run("处理出错", func(t *testing.T) {
		t.Parallel()
		dto := SessionDTO(newFlow("errored", withError("TLS 握手失败")))
		if dto.Status != "error" || dto.Error != "TLS 握手失败" {
			t.Errorf("出错会话 = %+v", dto)
		}
	})
}

// TestSessionDTODropsBinaryBody 列表里的 body 只是预览,二进制内容一律丢空,由前端按需走 MessageBody 取。
func TestSessionDTODropsBinaryBody(t *testing.T) {
	t.Parallel()
	binary := []byte{0x89, 'P', 'N', 'G', 0x00, 0x00, 0x00, 0x01, 0x02, 0x03}
	dto := SessionDTO(newFlow("bin",
		withRequestBody(binary),
		withResponse(http.StatusOK, "image/png", binary),
	))
	if dto.Request.Body != "" || dto.Response.Body != "" {
		t.Fatalf("二进制体不应出现在预览里: req=%q resp=%q", dto.Request.Body, dto.Response.Body)
	}
	// 但大小仍要如实上报,前端据此显示"另存为"。
	if dto.Response.Size != int64(len(binary)) {
		t.Errorf("响应大小 = %d, want %d", dto.Response.Size, len(binary))
	}
}

func TestWSSessionDTOEncodesFrames(t *testing.T) {
	t.Parallel()
	longText := strings.Repeat("a", bodyPreviewLimit+10)
	longBinary := append([]byte{0x00}, []byte(strings.Repeat("b", bodyPreviewLimit+10))...)

	tests := []struct {
		name       string
		msg        flow.WSMessage
		wantType   string
		wantBinary bool
		wantData   string
	}{
		{
			name:     "文本帧原样展示",
			msg:      flow.WSMessage{Type: flow.WSText, Data: []byte("你好 websocket")},
			wantType: flow.WSText,
			wantData: "你好 websocket",
		},
		{
			name:       "二进制帧转 base64",
			msg:        flow.WSMessage{Type: flow.WSBinary, Data: []byte{0x01, 0x02, 0x03}},
			wantType:   "binary",
			wantBinary: true,
			wantData:   base64.StdEncoding.EncodeToString([]byte{0x01, 0x02, 0x03}),
		},
		{
			// 历史缺陷:非 UTF-8 的文本帧曾被当纯文本处理,展示为空。
			name:       "非 UTF-8 的文本帧按二进制处理",
			msg:        flow.WSMessage{Type: flow.WSText, Data: []byte{0xff, 0xfe, 0x00}},
			wantType:   "binary",
			wantBinary: true,
			wantData:   base64.StdEncoding.EncodeToString([]byte{0xff, 0xfe, 0x00}),
		},
		{
			name:     "超长文本截断到预览上限",
			msg:      flow.WSMessage{Type: flow.WSText, Data: []byte(longText)},
			wantType: flow.WSText,
			wantData: longText[:bodyPreviewLimit],
		},
		{
			name:       "超长二进制先截断再编码",
			msg:        flow.WSMessage{Type: flow.WSBinary, Data: longBinary},
			wantType:   "binary",
			wantBinary: true,
			wantData:   base64.StdEncoding.EncodeToString(longBinary[:bodyPreviewLimit]),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			msg := tt.msg
			msg.ID = "m1"
			msg.Direction = flow.WSClientToServer
			msg.Timestamp = time.Unix(0, 0).UTC()

			end := time.Unix(10, 0).UTC()
			dto := WSSessionDTO(&flow.WSSession{
				ID: "ws-1", URL: "wss://x/ws", Status: "closed",
				StartTime: time.Unix(0, 0).UTC(), EndTime: &end,
				MessageCount: 1, TotalSize: int64(len(tt.msg.Data)),
				Messages: []flow.WSMessage{msg},
			})

			if len(dto.Messages) != 1 {
				t.Fatalf("消息条数 = %d, want 1", len(dto.Messages))
			}
			got := dto.Messages[0]
			if got.Type != tt.wantType || got.Binary != tt.wantBinary {
				t.Errorf("类型/二进制标记 = %q/%v, want %q/%v", got.Type, got.Binary, tt.wantType, tt.wantBinary)
			}
			if got.Data != tt.wantData {
				t.Errorf("载荷 = %s, want %s", elide(got.Data), elide(tt.wantData))
			}
			// Size 是帧的真实长度,不受预览截断影响。
			if got.Size != int64(len(tt.msg.Data)) {
				t.Errorf("帧大小 = %d, want %d", got.Size, len(tt.msg.Data))
			}
			if got.SessionID != "ws-1" || got.Direction != "outbound" {
				t.Errorf("消息归属/方向 = %+v", got)
			}
			if dto.EndTime == "" || dto.Status != "closed" {
				t.Errorf("会话字段 = %+v", dto)
			}
		})
	}
}

func TestWSSessionDTOSessionFields(t *testing.T) {
	t.Parallel()
	// 未结束的会话不该有 EndTime,前端据此显示"进行中"。
	dto := WSSessionDTO(&flow.WSSession{
		ID: "ws-open", URL: "wss://x/ws", Status: "open", StartTime: time.Unix(0, 0).UTC(),
		Process: &flow.ProcessInfo{PID: 7, Name: "chrome", HasIcon: true, IconData: "d", IconType: "png", IconCategory: "browser"},
	})
	if dto.EndTime != "" {
		t.Errorf("进行中的会话不应有结束时间: %q", dto.EndTime)
	}
	if dto.Messages == nil || len(dto.Messages) != 0 {
		t.Errorf("无消息时应是空数组而非 null(前端直接 map 遍历): %#v", dto.Messages)
	}
	if dto.ProcessName != "chrome" || dto.ProcessID != 7 || !dto.HasIcon || dto.IconCategory != "browser" {
		t.Errorf("进程字段 = %+v", dto)
	}
}

func TestStreamSessionDTOEncodesMessages(t *testing.T) {
	t.Parallel()
	longBinary := append([]byte{0xff}, []byte(strings.Repeat("d", bodyPreviewLimit+5))...)

	tests := []struct {
		name       string
		data       []byte
		wantBinary bool
		wantData   string
	}{
		{"UTF-8 文本原样展示", []byte("data: hello\n\n"), false, "data: hello\n\n"},
		{"二进制载荷转 base64", []byte{0xff, 0x00, 0x01}, true, base64.StdEncoding.EncodeToString([]byte{0xff, 0x00, 0x01})},
		{"超长文本截断", []byte(strings.Repeat("c", bodyPreviewLimit+5)), false, strings.Repeat("c", bodyPreviewLimit)},
		{"超长二进制先截断再编码", longBinary, true, base64.StdEncoding.EncodeToString(longBinary[:bodyPreviewLimit])},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			end := time.Unix(20, 0).UTC()
			dto := StreamSessionDTO(&flow.StreamSession{
				ID: "st-1", URL: "https://x/sse", Kind: flow.StreamSSE, Method: http.MethodGet,
				StatusCode: http.StatusOK, Status: "closed",
				StartTime: time.Unix(0, 0).UTC(), EndTime: &end,
				MessageCount: 1, TotalSize: int64(len(tt.data)),
				Messages: []flow.StreamMessage{{
					ID: "m1", Direction: flow.WSServerToClient, Kind: flow.StreamSSE,
					EventType: "ping", Data: tt.data, Seq: 3, Timestamp: time.Unix(1, 0).UTC(),
				}},
				Process: &flow.ProcessInfo{PID: 9, Name: "node"},
			})

			if dto.Kind != flow.StreamSSE || dto.Method != http.MethodGet || dto.StatusCode != http.StatusOK {
				t.Errorf("会话字段 = %+v", dto)
			}
			if dto.EndTime == "" || dto.ProcessName != "node" || dto.ProcessID != 9 {
				t.Errorf("会话补充字段 = %+v", dto)
			}
			got := dto.Messages[0]
			if got.Binary != tt.wantBinary || got.Data != tt.wantData {
				t.Errorf("载荷 = %v/%s, want %v/%s",
					got.Binary, elide(got.Data), tt.wantBinary, elide(tt.wantData))
			}
			if got.SessionID != "st-1" || got.Direction != "inbound" || got.EventType != "ping" || got.Seq != 3 {
				t.Errorf("消息元信息 = %+v", got)
			}
			if got.Size != int64(len(tt.data)) {
				t.Errorf("消息大小 = %d, want %d", got.Size, len(tt.data))
			}
		})
	}
}

func TestStreamSessionDTOEmptyMessages(t *testing.T) {
	t.Parallel()
	dto := StreamSessionDTO(&flow.StreamSession{ID: "st-empty", Kind: flow.StreamGRPC, Status: "open"})
	if dto.Messages == nil || len(dto.Messages) != 0 {
		t.Errorf("无消息时应是空数组而非 null: %#v", dto.Messages)
	}
	if dto.EndTime != "" {
		t.Errorf("进行中的流不应有结束时间: %q", dto.EndTime)
	}
}

func TestWSDirectionToFrontend(t *testing.T) {
	t.Parallel()
	// 方向是从"客户端视角"翻译的:客户端发出 = outbound,其余(含未知值)按收到处理。
	tests := map[string]string{
		flow.WSClientToServer: "outbound",
		flow.WSServerToClient: "inbound",
		"":                    "inbound",
	}
	for in, want := range tests {
		if got := wsDirectionToFrontend(in); got != want {
			t.Errorf("wsDirectionToFrontend(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBodyDTOEncodesAndDetects(t *testing.T) {
	t.Parallel()
	body := []byte{0x89, 'P', 'N', 'G'}

	got := bodyDTO(body, nil)
	if got.Size != len(body) || got.TooLarge {
		t.Fatalf("元信息 = %+v", got)
	}
	if got.Base64 != base64.StdEncoding.EncodeToString(body) {
		t.Errorf("Base64 = %q", got.Base64)
	}
	if got.Mime == "" {
		t.Error("无 Content-Type 时应嗅探出 MIME")
	}
	// 空体也要给出可渲染的元信息,而不是 nil。
	if empty := bodyDTO(nil, nil); empty == nil || empty.Size != 0 || empty.Mime != "application/octet-stream" {
		t.Errorf("空体 = %+v", empty)
	}
}

// TestBodyDTOFromFileSkipsHugeCopy 落盘副本超过预览上限时不读盘,只回元信息。
func TestBodyDTOFromFileSkipsHugeCopy(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "spill")
	if err := os.WriteFile(path, []byte("真实内容很小,大小以旁路记账为准"), 0o600); err != nil {
		t.Fatal(err)
	}

	header := map[string][]string{"Content-Type": {"video/mp4"}}
	got := bodyDTOFromFile(path, maxRawBodyBytes+1, header)
	if !got.TooLarge || got.Base64 != "" {
		t.Fatalf("超限副本不应被读取编码: %+v", got)
	}
	if got.Mime != "video/mp4" || got.Size != maxRawBodyBytes+1 {
		t.Errorf("元信息 = %+v", got)
	}

	// 未超限时正常读盘,MIME 仍取自响应头(落盘字节不参与嗅探)。
	small := bodyDTOFromFile(path, 4, header)
	if small.TooLarge || small.Base64 == "" || small.Mime != "video/mp4" {
		t.Errorf("未超限副本 = %+v", small)
	}
}

func BenchmarkSessionDTO(b *testing.B) {
	f := newFlow("bench",
		withRequestHeader("User-Agent", "sniffy-bench/1.0"),
		withRequestHeader("Accept", "*/*"),
		withRequestBody([]byte(`{"user":"a"}`)),
		withResponse(http.StatusOK, "application/json", []byte(strings.Repeat("x", 2048))),
		withDuration(12),
	)
	b.ReportAllocs()
	for b.Loop() {
		_ = SessionDTO(f)
	}
}

// FuzzWSMessageData 帧数据来自网络对端,展示编码对任意字节都应成立:
// 要么是原文前缀(文本),要么是可解码的 base64(二进制)。
func FuzzWSMessageData(f *testing.F) {
	f.Add(flow.WSText, []byte("hello"))
	f.Add(flow.WSText, []byte{0xff, 0xfe})
	f.Add(flow.WSBinary, []byte{0x00, 0x01, 0x02})
	f.Add(flow.WSPing, []byte(""))
	f.Fuzz(func(t *testing.T, typ string, data []byte) {
		got, binary := wsMessageData(flow.WSMessage{Type: typ, Data: data})
		if binary {
			decoded, err := base64.StdEncoding.DecodeString(got)
			if err != nil {
				t.Fatalf("binary 帧的 Data 应可 base64 解码: %v", err)
			}
			if len(decoded) > bodyPreviewLimit {
				t.Fatalf("解码后长度 %d 超过预览上限", len(decoded))
			}
			if !strings.HasPrefix(string(data), string(decoded)) {
				t.Fatal("解码结果应是原始字节的前缀")
			}
			return
		}
		if !strings.HasPrefix(string(data), got) {
			t.Fatal("文本帧展示串应是原文前缀")
		}
		if len(got) > bodyPreviewLimit {
			t.Fatalf("文本长度 %d 超过预览上限", len(got))
		}
	})
}

// FuzzBodyDTORoundTrip 消息体同样来自网络:未超上限时 base64 应能原样解回。
func FuzzBodyDTORoundTrip(f *testing.F) {
	f.Add([]byte("hello"), "text/plain")
	f.Add([]byte{0x00, 0xff}, "")
	f.Fuzz(func(t *testing.T, body []byte, contentType string) {
		header := map[string][]string{}
		if contentType != "" {
			header["Content-Type"] = []string{contentType}
		}
		got := bodyDTO(body, header)
		if got.Size != len(body) {
			t.Fatalf("Size = %d, want %d", got.Size, len(body))
		}
		if got.Mime == "" {
			t.Fatal("MIME 不应为空")
		}
		if got.TooLarge {
			return
		}
		decoded, err := base64.StdEncoding.DecodeString(got.Base64)
		if err != nil {
			t.Fatalf("Base64 应可解码: %v", err)
		}
		if string(decoded) != string(body) {
			t.Fatal("解码结果应与原始字节一致")
		}
	})
}
