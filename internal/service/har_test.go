// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mintfog/sniffy/internal/flow"
)

const browserHAR = `{"log":{"version":"1.2","creator":{"name":"Chrome","version":"130"},"pages":[],"entries":[{
  "startedDateTime":"2026-09-17T10:00:00.125Z","time":42.5,
  "request":{"method":"POST","url":"https://example.com/form?q=a+b&q=c","httpVersion":"HTTP/2.0",
    "headers":[{"name":"content-type","value":"application/x-www-form-urlencoded"},{"name":"Cookie","value":"sid=123"},{"name":"X-Repeat","value":"a"},{"name":"x-repeat","value":"b"}],
    "cookies":[{"name":"sid","value":"123"}],"queryString":[],"headersSize":-1,"bodySize":-1,
    "postData":{"mimeType":"application/x-www-form-urlencoded","text":"q=a+b&q=c","params":[{"name":"q","value":"a b"},{"name":"q","value":"c"}]}},
  "response":{"status":302,"statusText":"Found","httpVersion":"HTTP/1.1",
    "headers":[{"name":"Set-Cookie","value":"a=1; HttpOnly"},{"name":"Set-Cookie","value":"b=2; Secure"}],
    "cookies":[],"content":{"size":4,"mimeType":"image/png","text":"iVBORw==","encoding":"base64"},
    "redirectURL":"https://example.com/next","headersSize":-1,"bodySize":4},
  "cache":{},"timings":{"blocked":-1,"dns":-1,"connect":-1,"ssl":-1,"send":1,"wait":30,"receive":11.5},"_resourceType":"image"
}]}}`

func TestImportBrowserHAR(t *testing.T) {
	t.Parallel()
	svc := New(nil, nil, "", "")
	svc.StopRecording()
	result, err := svc.ImportSessionArchive(strings.NewReader(browserHAR))
	if err != nil || result.Imported != 1 || result.HTTP != 1 {
		t.Fatalf("导入结果 = %+v, %v", result, err)
	}
	ids := svc.SessionIDs()
	if !reflect.DeepEqual(result.IDs, ids) {
		t.Fatalf("返回的 ID = %v，存储中为 %v", result.IDs, ids)
	}
	f, _ := svc.RawFlow(ids[0])
	if f.State != flow.StateCompleted || f.Request.Proto != "HTTP/2.0" || f.Timing.TTFBMs != 31 || f.Timing.DurationMs != 42 {
		t.Fatalf("导入状态或时间错误: %+v", f)
	}
	if f.Response.Proto != "HTTP/1.1" {
		t.Fatalf("导入响应 HTTP 版本 = %q，期望 HTTP/1.1", f.Response.Proto)
	}
	if string(f.Request.Body) != "q=a+b&q=c" || http.Header(f.Request.Header).Get("Cookie") != "sid=123" {
		t.Fatalf("表单或 Cookie 丢失: %+v", f.Request)
	}
	if !reflect.DeepEqual(f.Request.Header["X-Repeat"], []string{"a", "b"}) || len(f.Response.Header["Set-Cookie"]) != 2 {
		t.Fatalf("重复头丢失: %+v / %+v", f.Request.Header, f.Response.Header)
	}
	if !bytes.Equal(f.Response.Body, []byte{0x89, 'P', 'N', 'G'}) || f.Response.Header["Location"][0] != "https://example.com/next" {
		t.Fatalf("二进制正文或重定向错误: %+v", f.Response)
	}
	if svc.Statistics().TotalRequests != 0 {
		t.Fatal("离线导入不应计入实时抓包统计")
	}
	var output bytes.Buffer
	if err := svc.WriteSessionArchive(context.Background(), &output, ids, true, true); err != nil {
		t.Fatal(err)
	}
	var archive HARArchive
	if err := json.Unmarshal(output.Bytes(), &archive); err != nil {
		t.Fatal(err)
	}
	e := archive.Log.Entries[0]
	if e.Response.Content.Encoding != "base64" || e.Request.HTTPVersion != "HTTP/2.0" || e.Response.HTTPVersion != "HTTP/1.1" || len(e.Response.Cookies) != 2 {
		t.Fatalf("HAR 内容错误: %+v", e)
	}
	if !reflect.DeepEqual(e.Request.QueryString, []HARPair{{Name: "q", Value: "a b"}, {Name: "q", Value: "c"}}) {
		t.Fatalf("查询参数错误: %+v", e.Request.QueryString)
	}
	if e.Request.PostData.MimeType != "application/x-www-form-urlencoded" || e.Response.Content.MimeType != "image/png" {
		t.Fatal("请求与响应 MIME 应分别保留")
	}
}

func TestImportArchiveFormParams(t *testing.T) {
	t.Parallel()
	text := "original=raw%20text"
	params := []HARPostParam{
		{Name: "z", Value: "a b"},
		{Name: "q", Value: "中文&"},
		{Name: "z", Value: "c"},
	}
	tests := []struct {
		name    string
		post    HARPostData
		want    string
		wantErr bool
	}{
		{
			name: "参数顺序、重复名称和转义",
			post: HARPostData{MimeType: "application/x-www-form-urlencoded; charset=UTF-8", Params: params},
			want: "z=a+b&q=%E4%B8%AD%E6%96%87%26&z=c",
		},
		{
			name: "优先保留原始文本",
			post: HARPostData{MimeType: "application/x-www-form-urlencoded", Text: &text, Params: params},
			want: text,
		},
		{
			name:    "multipart 参数缺少原始正文",
			post:    HARPostData{MimeType: "multipart/form-data; boundary=original", Params: params},
			wantErr: true,
		},
		{
			name:    "文件参数缺少原始正文",
			post:    HARPostData{MimeType: "application/x-www-form-urlencoded", Params: []HARPostParam{{Name: "file", FileName: "data.txt"}}},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var archive HARArchive
			if err := json.Unmarshal([]byte(browserHAR), &archive); err != nil {
				t.Fatal(err)
			}
			archive.Log.Entries[0].Request.PostData = &tt.post
			data, err := json.Marshal(archive)
			if err != nil {
				t.Fatal(err)
			}
			svc := New(nil, nil, "", "")
			result, err := svc.ImportSessionArchive(bytes.NewReader(data))
			if (err != nil) != tt.wantErr || (tt.wantErr && len(svc.SessionIDs()) != 0) {
				t.Fatalf("导入结果 = %+v，错误 = %v，期望失败 %v", result, err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if len(result.IDs) != 1 {
				t.Fatalf("导入结果 = %+v，错误 = %v", result, err)
			}
			f, _ := svc.RawFlow(result.IDs[0])
			if got := string(f.Request.Body); got != tt.want {
				t.Fatalf("表单正文 = %q，期望 %q", got, tt.want)
			}
		})
	}
}

func TestArchiveRequestBodySize(t *testing.T) {
	t.Parallel()
	text := "decoded request body"
	tests := []struct {
		name       string
		size       int64
		text       *string
		compressed bool
	}{
		{name: "缺失正文保留已知大小", size: 9},
		{name: "缺失正文保留未知大小", size: -1},
		{name: "空正文大小", size: 0},
		{name: "压缩正文保留线缆大小", size: 9, text: &text, compressed: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var archive HARArchive
			if err := json.Unmarshal([]byte(browserHAR), &archive); err != nil {
				t.Fatal(err)
			}
			req := &archive.Log.Entries[0].Request
			req.BodySize, req.PostData = tt.size, nil
			if tt.text != nil {
				req.PostData = &HARPostData{MimeType: "text/plain", Text: tt.text}
			}
			if tt.compressed {
				req.Headers = append(req.Headers, HARPair{Name: "Content-Encoding", Value: "gzip"})
			}
			data, err := json.Marshal(archive)
			if err != nil {
				t.Fatal(err)
			}
			svc := New(nil, nil, "", "")
			result, err := svc.ImportSessionArchive(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			if err := svc.WriteSessionArchive(context.Background(), &output, result.IDs, true, true); err != nil {
				t.Fatal(err)
			}
			var exported HARArchive
			if err := json.Unmarshal(output.Bytes(), &exported); err != nil {
				t.Fatal(err)
			}
			got := exported.Log.Entries[0].Request
			if got.BodySize != tt.size || (got.PostData != nil) != (tt.text != nil) {
				t.Fatalf("导出请求 = %+v，期望正文大小 %d、正文存在 %v", got, tt.size, tt.text != nil)
			}
		})
	}
}

func TestImportArchivePreservesHost(t *testing.T) {
	t.Parallel()
	for _, host := range []string{"", "virtual.example:8443"} {
		t.Run(host, func(t *testing.T) {
			var archive HARArchive
			if err := json.Unmarshal([]byte(browserHAR), &archive); err != nil {
				t.Fatal(err)
			}
			if host != "" {
				archive.Log.Entries[0].Request.Headers = append(archive.Log.Entries[0].Request.Headers, HARPair{Name: "host", Value: host})
			}
			data, err := json.Marshal(archive)
			if err != nil {
				t.Fatal(err)
			}
			svc := New(nil, nil, "", "")
			if _, err := svc.ImportSessionArchive(bytes.NewReader(data)); err != nil {
				t.Fatal(err)
			}
			want := host
			if want == "" {
				want = "example.com"
			}
			id := svc.SessionIDs()[0]
			f, _ := svc.RawFlow(id)
			seed, ok := svc.ComposeSeed(id)
			if !ok || f.Request.Host != want {
				t.Fatalf("Host = %q, 期望 %q；构造器快照存在 = %v", f.Request.Host, want, ok)
			}
			seedHeaders := make(http.Header)
			for _, pair := range seed.Headers {
				seedHeaders.Add(pair[0], pair[1])
			}
			if got := seedHeaders.Get("Host"); got != want {
				t.Fatalf("构造器 Host = %q, 期望 %q", got, want)
			}
			entry, err := svc.archiveEntry(id, true, true)
			if err != nil {
				t.Fatal(err)
			}
			headers, _, err := importArchiveHeaders(entry.Request.Headers)
			if err != nil || headers.Get("Host") != want {
				t.Fatalf("再次导出 Host = %q, 期望 %q；错误 = %v", headers.Get("Host"), want, err)
			}
		})
	}
}

func TestArchiveResponseBodyAvailability(t *testing.T) {
	t.Parallel()
	empty := ""
	tests := []struct {
		name string
		size int64
		text *string
	}{
		{name: "缺失正文保留大小", size: 4096},
		{name: "已保存空正文", size: 0, text: &empty},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var archive HARArchive
			if err := json.Unmarshal([]byte(browserHAR), &archive); err != nil {
				t.Fatal(err)
			}
			archive.Log.Entries[0].Response.Content = HARContent{Size: tt.size, MimeType: "text/plain", Text: tt.text}
			data, err := json.Marshal(archive)
			if err != nil {
				t.Fatal(err)
			}
			svc := New(nil, nil, "", "")
			if _, err := svc.ImportSessionArchive(bytes.NewReader(data)); err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			if err := svc.WriteSessionArchive(context.Background(), &output, svc.SessionIDs(), true, true); err != nil {
				t.Fatal(err)
			}
			var exported HARArchive
			if err := json.Unmarshal(output.Bytes(), &exported); err != nil {
				t.Fatal(err)
			}
			got := exported.Log.Entries[0].Response.Content
			if got.Size != tt.size || (got.Text != nil) != (tt.text != nil) {
				t.Fatalf("content = %+v，期望 size=%d、正文存在=%v", got, tt.size, tt.text != nil)
			}
		})
	}
}

func TestArchiveFullBodiesAndRepeatedImports(t *testing.T) {
	t.Parallel()
	svc := New(nil, nil, "", "")
	file := filepath.Join(t.TempDir(), "body")
	large := bytes.Repeat([]byte("正文"), (1<<20)/6+1)
	if err := os.WriteFile(file, large, 0600); err != nil {
		t.Fatal(err)
	}
	f := newFlow("original", withRequestBody([]byte{0x00, 0xff, 0x01}))
	f.Request.Header["X-Raw"] = []string{string([]byte{0xe9})}
	f.Response = &flow.Response{Status: 200, Header: map[string][]string{"Content-Type": {"text/plain"}}}
	f.Response.SetPassthroughBody(file, int64(len(large)))
	svc.ImportFlowUpdated(f)
	var output bytes.Buffer
	if err := svc.WriteSessionArchive(context.Background(), &output, []string{f.ID}, true, true); err != nil {
		t.Fatal(err)
	}
	target := New(nil, nil, "", "")
	for i := 0; i < 2; i++ {
		if _, err := target.ImportSessionArchive(bytes.NewReader(output.Bytes())); err != nil {
			t.Fatal(err)
		}
	}
	ids := target.SessionIDs()
	if len(ids) != 2 || ids[0] == ids[1] || ids[0] == f.ID {
		t.Fatalf("导入必须生成独立 ID: %v", ids)
	}
	restored, _ := target.RawFlow(ids[0])
	if !bytes.Equal(restored.Request.Body, f.Request.Body) || !bytes.Equal(restored.Response.Body, large) || restored.Request.Header["X-Raw"][0] != f.Request.Header["X-Raw"][0] {
		t.Fatal("完整正文或头部字节丢失")
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := svc.WriteSessionArchive(context.Background(), &bytes.Buffer{}, []string{f.ID}, true, true); err == nil {
		t.Fatal("缓存丢失应使导出失败")
	}
	if err := svc.WriteSessionArchive(context.Background(), &bytes.Buffer{}, []string{f.ID}, false, false); err != nil {
		t.Fatal("排除正文后不应读取缓存", err)
	}
}

func TestImportArchiveRejectsWholeFile(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, document string }{
		{"缺少档案", "{}"},
		{"空条目值", `{"log":{"entries":null}}`},
		{"版本非法", strings.Replace(browserHAR, `"version":"1.2"`, `"version":"2.0"`, 1)},
		{"相对URL", strings.Replace(browserHAR, "https://example.com/form?q=a+b&q=c", "/form", 1)},
		{"错误时间", strings.Replace(browserHAR, "2026-09-17T10:00:00.125Z", "yesterday", 1)},
		{"负耗时", strings.Replace(browserHAR, `"time":42.5`, `"time":-1`, 1)},
		{"请求正文大小非法", strings.Replace(browserHAR, `"bodySize":-1`, `"bodySize":-2`, 1)},
		{"非法编码", strings.Replace(browserHAR, "iVBORw==", "***", 1)},
		{"头部注入", strings.Replace(browserHAR, `"value":"a"`, `"value":"a\r\nInjected: b"`, 1)},
		{"尾随JSON", browserHAR + " {}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := New(nil, nil, "", "")
			if _, err := svc.ImportSessionArchive(strings.NewReader(tt.document)); err == nil {
				t.Fatal("应拒绝非法档案")
			}
			if len(svc.SessionIDs()) != 0 {
				t.Fatal("失败导入留下了记录")
			}
		})
	}
	var archive HARArchive
	if err := json.Unmarshal([]byte(browserHAR), &archive); err != nil {
		t.Fatal(err)
	}
	bad := archive.Log.Entries[0]
	bad.Request.URL = "file:///etc/passwd"
	archive.Log.Entries = append(archive.Log.Entries, bad)
	data, _ := json.Marshal(archive)
	svc := New(nil, nil, "", "")
	if _, err := svc.ImportSessionArchive(bytes.NewReader(data)); err == nil || len(svc.SessionIDs()) != 0 {
		t.Fatal("后续条目非法时，前面条目也不应写入")
	}
}

func TestImportArchiveWithoutContentAndCapacity(t *testing.T) {
	t.Parallel()
	var archive HARArchive
	if err := json.Unmarshal([]byte(browserHAR), &archive); err != nil {
		t.Fatal(err)
	}
	archive.Log.Entries[0].Response.Content.Text = nil
	archive.Log.Entries[0].Response.Content.Size = 4096
	data, _ := json.Marshal(archive)
	svc := New(nil, nil, "", "")
	svc.sessions.setCap(1)
	for i := 0; i < 2; i++ {
		if _, err := svc.ImportSessionArchive(bytes.NewReader(data)); err != nil {
			t.Fatal(err)
		}
	}
	list, total := svc.Sessions(1, 10)
	if total != 1 || list[0].Response.Size != 4096 || list[0].Response.Body != "" {
		t.Fatalf("容量或正文元信息错误: %+v", list)
	}
}

func TestArchiveWebSocketMessages(t *testing.T) {
	t.Parallel()
	svc := New(nil, nil, "", "")
	start := time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC)
	ws := &flow.WSSession{
		ID:        "socket",
		URL:       "wss://example.com/ws",
		Status:    "open",
		StartTime: start,
		Messages: []flow.WSMessage{
			{ID: "1", Direction: flow.WSClientToServer, Type: flow.WSText, Data: []byte("你好"), Timestamp: start},
			{ID: "2", Direction: flow.WSServerToClient, Type: flow.WSBinary, Data: []byte{0, 0xff}, Timestamp: start.Add(time.Second)},
		},
	}
	svc.ImportWSSession(ws, nil)
	var output bytes.Buffer
	if err := svc.WriteSessionArchive(context.Background(), &output, []string{ws.ID}, true, true); err != nil {
		t.Fatal(err)
	}
	var archive HARArchive
	if err := json.Unmarshal(output.Bytes(), &archive); err != nil {
		t.Fatal(err)
	}
	if len(archive.Log.Entries) != 1 {
		t.Fatalf("HAR 条目数 = %d，期望 1", len(archive.Log.Entries))
	}
	entry := archive.Log.Entries[0]
	if entry.Request.HTTPVersion != "HTTP/1.1" || entry.Response.HTTPVersion != "HTTP/1.1" {
		t.Fatalf("WebSocket 握手 HTTP 版本 = %q / %q，期望 HTTP/1.1", entry.Request.HTTPVersion, entry.Response.HTTPVersion)
	}
	target := New(nil, nil, "", "")
	result, err := target.ImportSessionArchive(bytes.NewReader(output.Bytes()))
	if err != nil || result.WebSocket != 1 {
		t.Fatalf("WS 导入: %+v, %v", result, err)
	}
	list, total := target.ws.list(1, 10)
	if total != 1 || list[0].Status != "closed" || len(list[0].Messages) != 2 || list[0].ID == ws.ID {
		t.Fatalf("WS 会话错误: %+v", list)
	}
	if !bytes.Equal(list[0].Messages[1].Data, ws.Messages[1].Data) || list[0].Messages[0].Direction != flow.WSClientToServer {
		t.Fatal("WS 消息还原错误")
	}
	// Chromium 导出二进制消息时省略自定义编码字段。
	chrome := strings.Replace(output.String(), `,"_encoding":"base64"`, "", 1)
	if _, err := target.ImportSessionArchive(strings.NewReader(chrome)); err != nil {
		t.Fatal(err)
	}
}

func TestImportArchiveWebSocketRetention(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name         string
		count        int
		payloadBytes int
		messageSize  int64
		retained     int
	}{
		{name: "消息条数上限", count: flow.MaxWSMessages + 1, payloadBytes: 1, retained: flow.MaxWSMessages},
		{name: "单条正文上限", count: 1, payloadBytes: flow.MaxRetainedMessageBytes + 1, retained: 1},
		{name: "单会话字节预算", count: 9, payloadBytes: flow.MaxRetainedMessageBytes, retained: 8},
		{name: "来源已裁剪正文", count: 1, payloadBytes: 7, messageSize: 123, retained: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry := HAREntry{
				StartedDateTime: start,
				Request:         HARRequest{Method: "GET", URL: "wss://example.com/ws"},
				Response:        HARResponse{Status: http.StatusSwitchingProtocols},
			}
			payload := strings.Repeat("a", tt.payloadBytes)
			for i := 0; i < tt.count; i++ {
				entry.WebSocketMessages = append(entry.WebSocketMessages, HARWSMessage{
					Type: "receive", Opcode: 1, Data: payload, Size: tt.messageSize,
					Time: float64(start.Unix() + int64(i)),
				})
			}
			data, err := json.Marshal(HARArchive{Log: HARLog{Version: "1.2", Entries: []HAREntry{entry}}})
			if err != nil {
				t.Fatal(err)
			}
			svc := New(nil, nil, "", "")
			for i := 0; i < 2; i++ {
				if _, err := svc.ImportSessionArchive(bytes.NewReader(data)); err != nil {
					t.Fatal(err)
				}
			}
			sockets, total := svc.ws.list(1, 10)
			if total != 2 || sockets[0].ID == sockets[1].ID {
				t.Fatalf("重复导入应生成两条独立会话：总数=%d", total)
			}
			wantSize := max(tt.messageSize, int64(tt.payloadBytes))
			for _, ws := range sockets {
				if len(ws.Messages) != tt.retained || ws.MessageCount != tt.count || ws.TotalSize != int64(tt.count)*wantSize {
					t.Fatalf("留存=%d，计数=%d，总字节=%d；期望 %d / %d / %d", len(ws.Messages), ws.MessageCount, ws.TotalSize, tt.retained, tt.count, int64(tt.count)*wantSize)
				}
				if !ws.Messages[0].Timestamp.Equal(start.Add(time.Duration(tt.count-tt.retained) * time.Second)) {
					t.Error("时间线应保留最新的消息")
				}
				retainedBytes := 0
				for _, m := range ws.Messages {
					wantPayload := payload[:min(tt.payloadBytes, flow.MaxRetainedMessageBytes)]
					if string(m.Data) != wantPayload || m.PayloadSize() != wantSize || m.Truncated() != (wantSize > int64(len(wantPayload))) {
						t.Fatalf("正文留存长度=%d，真实大小=%d，裁剪标记=%v", len(m.Data), m.PayloadSize(), m.Truncated())
					}
					retainedBytes += len(m.Data)
				}
				if retainedBytes > flow.MaxSessionBytes {
					t.Errorf("留存字节=%d，超过单会话预算 %d", retainedBytes, flow.MaxSessionBytes)
				}
			}
		})
	}
}
