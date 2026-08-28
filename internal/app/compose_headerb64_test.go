// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package app

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/internal/service"
)

// encHeaderSlot 使用线上契约的标准 base64 编码。
func encHeaderSlot(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// seedThroughJSON 返回构造器经过 JSON 边界后的蓝本。
func seedThroughJSON(t *testing.T, app *App, id string) service.ComposeSeedDTO {
	t.Helper()
	seed, ok := app.Service.ComposeSeed(id)
	if !ok {
		t.Fatal("取不到蓝本")
	}
	raw, err := json.Marshal(seed)
	if err != nil {
		t.Fatal(err)
	}
	var out service.ComposeSeedDTO
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// wantWire 等待上游原始报文并检查指定片段。
func wantWire(t *testing.T, got <-chan string, want string) {
	t.Helper()
	select {
	case wire := <-got:
		if !strings.Contains(wire, want) {
			t.Fatalf("出线报文里找不到 %q:\n%q", want, wire)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("上游未收到请求")
	}
}

// 验证 Latin-1 文件名经过构造器闭环后保留原始字节。
func TestSendRequestSidecarWritesExactBytesEndToEnd(t *testing.T) {
	app := newComposeApp(t)
	addr, got := rawEchoServer(t)

	src := flow.New(flow.ProtoHTTP)
	src.Request = &flow.Request{
		Method: "GET",
		URL:    "http://" + addr + "/report",
		Host:   addr,
		Path:   "/report",
		Header: map[string][]string{
			"Content-Disposition": {latin1Disposition},
			"Accept":              {"*/*"},
		},
		RawHeaders: [][2]string{
			{"Host", addr},
			{"Content-Disposition", latin1Disposition},
			{"Accept", "*/*"},
		},
	}
	app.Service.ImportFlowStarted(src)

	seen := seedThroughJSON(t, app, src.ID)
	if headerRowValue(t, seen.Headers, "Content-Disposition") == latin1Disposition {
		t.Fatal("蓝本经 JSON 出境后仍是原始字节,本用例已失去区分力")
	}
	if len(seen.HeadersB64) != len(seen.Headers) {
		t.Fatalf("旁路长度 = %d, want %d", len(seen.HeadersB64), len(seen.Headers))
	}

	if _, err := app.SendRequest(flow.RequestSpec{
		Method:     "GET",
		URL:        "http://" + addr + "/report",
		Headers:    seen.Headers,
		HeadersB64: seen.HeadersB64,
		FromID:     src.ID,
	}); err != nil {
		t.Fatal(err)
	}
	wantWire(t, got, "Content-Disposition: "+latin1Disposition)
}

// 验证旁路携带完整字节值时可独立完成发送。
func TestSendRequestSidecarNeedsNoBasis(t *testing.T) {
	app := newComposeApp(t)
	addr, got := rawEchoServer(t)

	if _, err := app.SendRequest(flow.RequestSpec{
		Method: "GET",
		URL:    "http://" + addr + "/report",
		Headers: [][2]string{
			{"Host", addr},
			{"Content-Disposition", "attachment; filename=\"caf\uFFFD.pdf\""},
		},
		HeadersB64: []string{"", encHeaderSlot(latin1Disposition)},
	}); err != nil {
		t.Fatal(err)
	}
	wantWire(t, got, "Content-Disposition: "+latin1Disposition)
}

// 验证空旁路项使用明文值，非空旁路项使用字节值。
func TestSendRequestSidecarEmptySlotSendsPlainValue(t *testing.T) {
	app := newComposeApp(t)
	addr, got := rawEchoServer(t)

	if _, err := app.SendRequest(flow.RequestSpec{
		Method: "GET",
		URL:    "http://" + addr + "/report",
		Headers: [][2]string{
			{"Host", addr},
			{"Content-Disposition", "inline"},
			{"X-Note", "unused"},
		},
		HeadersB64: []string{"", "", encHeaderSlot("raw-\xe9")},
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case wire := <-got:
		if !strings.Contains(wire, "Content-Disposition: inline") {
			t.Errorf("空旁路项的行应按明文出线:\n%q", wire)
		}
		if !strings.Contains(wire, "X-Note: raw-\xe9") {
			t.Errorf("非空旁路项的行应按字节出线:\n%q", wire)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("上游未收到请求")
	}
}

// 验证畸形旁路返回错误且不创建 flow。
func TestSendRequestRejectsMalformedSidecar(t *testing.T) {
	tests := []struct {
		name    string
		headers [][2]string
		b64     []string
		wantIn  string
	}{
		{
			name:    "旁路项不是标准 base64",
			headers: [][2]string{{"Host", "x.com"}, {"X-Note", "shown"}},
			b64:     []string{"", "not base64!!"},
			wantIn:  "X-Note",
		},
		{
			name:    "旁路与明文长度对不上",
			headers: [][2]string{{"Host", "x.com"}},
			b64:     []string{"", encHeaderSlot("a")},
			wantIn:  "对不上",
		},
		{
			name:    "只发旁路不发明文",
			headers: nil,
			b64:     []string{encHeaderSlot("raw-\xe9")},
			wantIn:  "对不上",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := newComposeApp(t)
			id, err := app.SendRequest(flow.RequestSpec{
				Method:     "GET",
				URL:        "https://127.0.0.1:1/never",
				Headers:    tt.headers,
				HeadersB64: tt.b64,
			})
			if err == nil {
				t.Fatalf("畸形旁路应拒发, got flow %s", id)
			}
			if !strings.Contains(err.Error(), tt.wantIn) {
				t.Errorf("错误信息 = %v, want 含 %q", err, tt.wantIn)
			}
			if id != "" {
				t.Errorf("被拒的请求不应建 flow: %s", id)
			}
		})
	}
}

// 验证解码后的旁路字节经过头部字符校验。
func TestSendRequestRejectsCRLFSmuggledThroughSidecar(t *testing.T) {
	app := newComposeApp(t)
	id, err := app.SendRequest(flow.RequestSpec{
		Method:     "GET",
		URL:        "https://127.0.0.1:1/never",
		Headers:    [][2]string{{"Host", "x.com"}, {"X-Note", "clean"}},
		HeadersB64: []string{"", encHeaderSlot("a\r\nX-Injected: 1")},
	})
	if err == nil {
		t.Fatalf("经 base64 走私的 CR/LF 应被拦下, got flow %s", id)
	}
	if !strings.Contains(err.Error(), "CR/LF") {
		t.Errorf("错误信息 = %v, want 指出 CR/LF", err)
	}
	if id != "" {
		t.Errorf("被拒的请求不应建 flow: %s", id)
	}
}

// 验证无旁路请求使用蓝本头部恢复原始字节。
func TestSendRequestWithoutSidecarStillUsesBasis(t *testing.T) {
	app := newComposeApp(t)
	addr, got := rawEchoServer(t)

	src := flow.New(flow.ProtoHTTP)
	src.Request = &flow.Request{
		Method:     "GET",
		URL:        "http://" + addr + "/report",
		Host:       addr,
		Path:       "/report",
		Header:     map[string][]string{"Content-Disposition": {latin1Disposition}},
		RawHeaders: [][2]string{{"Host", addr}, {"Content-Disposition", latin1Disposition}},
	}
	app.Service.ImportFlowStarted(src)

	seen := seedThroughJSON(t, app, src.ID)
	if _, err := app.SendRequest(flow.RequestSpec{
		Method:  "GET",
		URL:     "http://" + addr + "/report",
		Headers: seen.Headers,
		FromID:  src.ID,
	}); err != nil {
		t.Fatal(err)
	}
	wantWire(t, got, "Content-Disposition: "+latin1Disposition)
}

// 验证 WebSocket 握手按 RequestSpec 解析值字节旁路。
func TestOpenWebSocketSidecarWritesExactBytes(t *testing.T) {
	app := newComposeApp(t)
	srv := newWSTestServer(t, false)

	if _, err := app.OpenWebSocket(flow.RequestSpec{
		Kind:       flow.SpecKindWS,
		URL:        srv.url,
		Headers:    [][2]string{{"X-Note", "attachment; filename=\"caf\uFFFD.pdf\""}},
		HeadersB64: []string{encHeaderSlot(latin1Disposition)},
	}); err != nil {
		t.Fatal(err)
	}
	if got := srv.nextFrameHeader(t).Get("X-Note"); got != latin1Disposition {
		t.Fatalf("握手头值 = %q, want %q", got, latin1Disposition)
	}
}

// 验证 WebSocket 握手的畸形旁路返回错误。
func TestOpenWebSocketRejectsMalformedSidecar(t *testing.T) {
	app := newComposeApp(t)
	srv := newWSTestServer(t, false)

	id, err := app.OpenWebSocket(flow.RequestSpec{
		Kind:       flow.SpecKindWS,
		URL:        srv.url,
		Headers:    [][2]string{{"X-Note", "shown"}},
		HeadersB64: []string{"not base64!!"},
	})
	if err == nil {
		t.Fatalf("畸形旁路应拒绝拨号, got session %s", id)
	}
	if !strings.Contains(err.Error(), "X-Note") {
		t.Errorf("错误信息 = %v, want 指明是哪个头", err)
	}
}
