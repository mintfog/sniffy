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
	"strings"
	"testing"
	"time"

	"github.com/mintfog/sniffy/internal/flow"
)

func exportArchive(t *testing.T, svc *Service, ids []string, opts ArchiveOptions) (HARArchive, SanitizeStats, string) {
	t.Helper()
	var output bytes.Buffer
	stats, err := svc.WriteSessionArchive(context.Background(), &output, ids, opts)
	if err != nil {
		t.Fatal(err)
	}
	var archive HARArchive
	if err := json.Unmarshal(output.Bytes(), &archive); err != nil {
		t.Fatalf("HAR 无法解析: %v\n%s", err, output.String())
	}
	return archive, stats, output.String()
}

func harHeader(pairs []HARPair, name string) string {
	for _, p := range pairs {
		if strings.EqualFold(p.Name, name) {
			return p.Value
		}
	}
	return ""
}

func redactOptions(fields ...string) ArchiveOptions {
	opts := fullBodies
	opts.Sanitize = SanitizeOptions{Redact: true, Fields: fields}
	return opts
}

func TestSanitizeArchiveRedactsCredentials(t *testing.T) {
	t.Parallel()
	svc := New(nil, nil, "", "")
	reqBody := `{"user":"alice","password":"p@ss","profile":{"phone":"13800000000"},"max_tokens":128,"auth":{"kind":"x"}}`
	resBody := "{\n  \"access_token\": \"eyJ.secret\",\n  \"usage\": {\"prompt_tokens\": 12},\n  \"name\": \"ok\"\n}"
	f := newFlow("creds",
		withRequest(http.MethodPost, "https://bob:hunter2@api.example.com/login?q=1&api_key=k-123&page=2#access_token=frag&state=s"),
		withRequestHeader("Authorization", "Bearer abc.def"),
		withRequestHeader("Cookie", "sid=s1; theme=dark"),
		withRequestHeader("X-Api-Key", "key-1"),
		withRequestHeader("X-Raw-Token", string([]byte{0xe9, 'x'})),
		withRequestHeader("Content-Type", "application/json"),
		withRequestHeader("Accept", "application/json"),
		withRequestBody([]byte(reqBody)),
		withResponse(http.StatusFound, "application/json", []byte(resBody)),
		withResponseHeader("Set-Cookie", "session=xyz; Path=/; HttpOnly"),
		withResponseHeader("Location", "https://app.example.com/cb?code=c&token=t"),
	)
	svc.ImportFlowUpdated(f)

	archive, stats, raw := exportArchive(t, svc, []string{f.ID}, redactOptions("Phone"))
	for _, secret := range []string{"hunter2", "bob:", "k-123", "frag", "abc.def", "s1", "key-1", "p@ss", "13800000000", "eyJ.secret", "xyz", "token=t", `"kind"`} {
		if strings.Contains(raw, secret) {
			t.Errorf("导出仍含敏感值 %q", secret)
		}
	}
	e := archive.Log.Entries[0]
	if want := "https://api.example.com/login?q=1&api_key=%5BREDACTED%5D&page=2#access_token=%5BREDACTED%5D&state=s"; e.Request.URL != want {
		t.Errorf("URL = %q\n期望 %q", e.Request.URL, want)
	}
	if got := e.Request.QueryString; len(got) != 3 || got[1].Value != redactedValue || got[2].Value != "2" {
		t.Errorf("queryString 未随 URL 同步: %+v", got)
	}
	if got := harHeader(e.Request.Headers, "Authorization"); got != "Bearer "+redactedValue {
		t.Errorf("Authorization = %q，应保留认证方案", got)
	}
	if got := harHeader(e.Request.Headers, "Cookie"); got != "sid=[REDACTED]; theme=[REDACTED]" {
		t.Errorf("Cookie = %q", got)
	}
	for _, p := range e.Request.Headers {
		if p.Name == "X-Raw-Token" && (p.Value != redactedValue || p.ValueBase64 != "") {
			t.Errorf("原始字节副本未清除: %+v", p)
		}
	}
	if harHeader(e.Request.Headers, "Accept") != "application/json" {
		t.Error("非敏感头被改写")
	}
	for _, c := range append(e.Request.Cookies, e.Response.Cookies...) {
		if c.Value != redactedValue {
			t.Errorf("cookies 数组未脱敏: %+v", c)
		}
	}
	if got := harHeader(e.Response.Headers, "Set-Cookie"); got != "session=[REDACTED]; Path=/; HttpOnly" {
		t.Errorf("Set-Cookie = %q，应保留属性", got)
	}
	if want := "https://app.example.com/cb?code=c&token=%5BREDACTED%5D"; e.Response.RedirectURL != want || harHeader(e.Response.Headers, "Location") != want {
		t.Errorf("Location / redirectURL = %q / %q", harHeader(e.Response.Headers, "Location"), e.Response.RedirectURL)
	}
	wantReq := `{"user":"alice","password":"[REDACTED]","profile":{"phone":"[REDACTED]"},"max_tokens":128,"auth":"[REDACTED]"}`
	if got := *e.Request.PostData.Text; got != wantReq {
		t.Errorf("请求正文 = %s\n期望 %s", got, wantReq)
	}
	wantRes := "{\n  \"access_token\": \"[REDACTED]\",\n  \"usage\": {\"prompt_tokens\": 12},\n  \"name\": \"ok\"\n}"
	if got := *e.Response.Content.Text; got != wantRes || e.Response.Content.Size != int64(len(wantRes)) {
		t.Errorf("响应正文 = %q (size %d)", got, e.Response.Content.Size)
	}
	// URL 3（账号密码、查询、片段）+ 请求头 5（Cookie 两个值）+ 响应头 2 + 请求正文 3 + 响应正文 1；
	// cookies 数组、queryString 与 redirectURL 是副本，不重复计数。
	if stats != (SanitizeStats{Redacted: 14}) {
		t.Errorf("统计 = %+v", stats)
	}

	target := New(nil, nil, "", "")
	if _, err := target.ImportSessionArchive(strings.NewReader(raw)); err != nil {
		t.Fatalf("脱敏后的 HAR 应能导入: %v", err)
	}
	restored, _ := target.RawFlow(target.SessionIDs()[0])
	if string(restored.Response.Body) != wantRes {
		t.Errorf("导入后响应正文 = %q", restored.Response.Body)
	}
}

func TestSanitizeArchiveBodies(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		mime     string
		body     string
		want     *string
		unparsed int
	}{
		{"表单", "application/x-www-form-urlencoded", "user=a&pass_word=b&note=c", ptr("user=a&pass_word=%5BREDACTED%5D&note=c"), 0},
		{"JSON 解析失败整段省略", "application/json", `{"token":"abc"`, nil, 1},
		{"表单字段名转义非法整段省略", "application/x-www-form-urlencoded", "p%61ssword%ZZ=secret&a=1", nil, 1},
		{"NDJSON", "application/x-ndjson", "{\"token\":\"a\"}\n{\"n\":1}\n", ptr("{\"token\":\"[REDACTED]\"}\n{\"n\":1}\n"), 0},
		{"multipart 无法按字段识别", "multipart/form-data; boundary=x", "--x\r\n\r\nsecret\r\n--x--", nil, 1},
		{"非结构化正文原样保留", "text/html", "<p>token=abc</p>", ptr("<p>token=abc</p>"), 0},
		{"未声明 JSON 的文本按内容识别", "text/plain;charset=UTF-8", ` {"password":"p"}`, ptr(` {"password":"[REDACTED]"}`), 0},
		{"未声明格式的非法 JSON 原样保留", "text/plain", `{token=abc`, ptr(`{token=abc`), 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			svc := New(nil, nil, "", "")
			f := newFlow("body", withResponse(http.StatusOK, tt.mime, []byte(tt.body)))
			svc.ImportFlowUpdated(f)
			archive, stats, _ := exportArchive(t, svc, []string{f.ID}, redactOptions())
			content := archive.Log.Entries[0].Response.Content
			switch {
			case tt.want == nil && content.Text != nil:
				t.Fatalf("正文应省略，实际 %q", *content.Text)
			case tt.want != nil && (content.Text == nil || *content.Text != *tt.want):
				t.Fatalf("正文 = %v，期望 %q", content.Text, *tt.want)
			}
			if tt.want == nil && content.Size != int64(len(tt.body)) {
				t.Errorf("省略正文后应保留原始大小，实际 %d", content.Size)
			}
			if stats.Unparsed != tt.unparsed {
				t.Errorf("Unparsed = %d，期望 %d", stats.Unparsed, tt.unparsed)
			}
		})
	}
}

func ptr(s string) *string { return &s }

func TestSanitizeArchiveOmitBodiesAndClientInfo(t *testing.T) {
	t.Parallel()
	svc := New(nil, nil, "", "")
	file := filepath.Join(t.TempDir(), "body")
	if err := os.WriteFile(file, []byte("cached"), 0600); err != nil {
		t.Fatal(err)
	}
	f := newFlow("omit",
		withRequestBody([]byte("secret-request")),
		withProcess(&flow.ProcessInfo{PID: 7, Name: "curl", Path: "/usr/bin/curl"}),
	)
	f.Request.ClientIP = "10.0.0.8"
	f.Response = &flow.Response{Status: http.StatusOK, Header: map[string][]string{}}
	f.Response.SetPassthroughBody(file, 6)
	svc.ImportFlowUpdated(f)
	start := time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC)
	ws := &flow.WSSession{
		ID: "socket", URL: "wss://example.com/ws", StartTime: start,
		Messages: []flow.WSMessage{{ID: "1", Direction: flow.WSClientToServer, Type: flow.WSText, Data: []byte("hello"), Timestamp: start}},
	}
	svc.ImportWSSession(ws, nil)

	full, _, _ := exportArchive(t, svc, []string{f.ID}, fullBodies)
	if p := full.Log.Entries[0].Process; p == nil || p.Name != "curl" || p.PID != 7 {
		t.Fatalf("应导出发起进程: %+v", p)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	opts := fullBodies
	opts.Sanitize = SanitizeOptions{OmitBodies: true, OmitClientInfo: true}
	archive, _, raw := exportArchive(t, svc, []string{f.ID, ws.ID}, opts)
	e := archive.Log.Entries[0]
	if e.Request.PostData != nil || e.Response.Content.Text != nil || e.Response.Content.Size != 6 {
		t.Errorf("正文未省略或大小丢失: %+v / %+v", e.Request.PostData, e.Response.Content)
	}
	if e.ClientIP != "" || e.Process != nil || strings.Contains(raw, "curl") {
		t.Errorf("客户端信息未省略: %q %+v", e.ClientIP, e.Process)
	}
	if m := archive.Log.Entries[1].WebSocketMessages[0]; m.Data != "" || m.Size != 5 {
		t.Errorf("WS 消息内容未省略或大小丢失: %+v", m)
	}
}

func TestImportArchiveRestoresProcess(t *testing.T) {
	t.Parallel()
	svc := New(nil, nil, "", "")
	f := newFlow("proc", withProcess(&flow.ProcessInfo{PID: 42, Name: "app.exe", Path: `C:\app.exe`}))
	svc.ImportFlowUpdated(f)
	var output bytes.Buffer
	if _, err := svc.WriteSessionArchive(context.Background(), &output, []string{f.ID}, fullBodies); err != nil {
		t.Fatal(err)
	}
	target := New(nil, nil, "", "")
	if _, err := target.ImportSessionArchive(&output); err != nil {
		t.Fatal(err)
	}
	restored, _ := target.RawFlow(target.SessionIDs()[0])
	if p := restored.Process(); p == nil || p.PID != 42 || p.Name != "app.exe" || p.Path != `C:\app.exe` {
		t.Fatalf("导入未还原进程: %+v", p)
	}
}

func TestSanitizeWebSocketJSONMessages(t *testing.T) {
	t.Parallel()
	z := newSanitizer(SanitizeOptions{Redact: true})
	messages := []HARWSMessage{
		{Opcode: 1, Data: `{"type":"auth","token":"t-1"}`},
		{Opcode: 1, Data: `{"token":`},
		{Opcode: 1, Data: `42["event",{"token":"keep"}]`},
		{Opcode: 2, Data: "e3t9", Encoding: "base64"},
	}
	z.entry(&HAREntry{WebSocketMessages: messages})
	if messages[0].Data != `{"type":"auth","token":"[REDACTED]"}` {
		t.Errorf("JSON 消息 = %s", messages[0].Data)
	}
	if messages[1].Data != "" {
		t.Errorf("无法解析的 JSON 消息应省略，实际 %q", messages[1].Data)
	}
	if messages[2].Data != `42["event",{"token":"keep"}]` || messages[3].Data != "e3t9" {
		t.Error("非 JSON 文本与二进制消息应原样保留")
	}
	if z.stats != (SanitizeStats{Redacted: 1, Unparsed: 1}) {
		t.Errorf("统计 = %+v", z.stats)
	}
}

func TestSanitizeURL(t *testing.T) {
	t.Parallel()
	z := newSanitizer(SanitizeOptions{Redact: true})
	tests := []struct {
		in, want string
		count    int
	}{
		{"//alice:password@example.com/cb?x=1", "//example.com/cb?x=1", 1},
		{"https://alice@example.com", "https://example.com", 1},
		{"https://example.com/a?p%61ssword%ZZ=secret&q=1", "https://example.com/a?p%61ssword%ZZ=%5BREDACTED%5D&q=1", 1},
		{"https://example.com/a?pass+word=x", "https://example.com/a?pass+word=%5BREDACTED%5D", 1},
		// 路径里的 :// 不是 authority 的开头，路径中的 @ 不能被当成账号密码删掉。
		{"/go/http://user@host/x", "/go/http://user@host/x", 0},
		{"/cb#state=s&id_token=t", "/cb#state=s&id_token=%5BREDACTED%5D", 1},
	}
	for _, tt := range tests {
		if got, n := z.url(tt.in); got != tt.want || n != tt.count {
			t.Errorf("url(%q) = %q, %d\n期望 %q, %d", tt.in, got, n, tt.want, tt.count)
		}
	}
}

func TestSanitizeArchiveErrorMessage(t *testing.T) {
	t.Parallel()
	svc := New(nil, nil, "", "")
	f := newFlow("err",
		withRequest(http.MethodGet, "https://api.example.com/x?access_token=secret&q=1"),
		withError(`Get "https://alice:***@api.example.com/x?access_token=secret&q=1": dial tcp: connection refused`),
	)
	svc.ImportFlowUpdated(f)
	archive, stats, raw := exportArchive(t, svc, []string{f.ID}, redactOptions())
	want := `Get "https://api.example.com/x?access_token=%5BREDACTED%5D&q=1": dial tcp: connection refused`
	if got := archive.Log.Entries[0].Error; got != want || strings.Contains(raw, "secret") {
		t.Errorf("_error = %q\n期望 %q", got, want)
	}
	if stats.Redacted != 1 {
		t.Errorf("错误信息里的 URL 是请求 URL 的副本，不应重复计数: %+v", stats)
	}
}

func TestSanitizeErrorEmbeddedURLBoundaries(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, requestURL, in, want string }{
		{
			"与请求 URL 相同时保留包裹标点",
			"https://h/x?q=O'Reilly&access_token=secret",
			`Get "https://h/x?q=O'Reilly&access_token=secret": EOF`,
			`Get "https://h/x?q=O'Reilly&access_token=%5BREDACTED%5D": EOF`,
		},
		{
			"敏感值全是标点且与请求 URL 相同",
			"https://h/login?password=...",
			`Get "https://h/login?password=...": EOF`,
			`Get "https://h/login?password=%5BREDACTED%5D": EOF`,
		},
		{
			"敏感值全是标点且与请求 URL 不同",
			"https://h/login?password=...",
			`Get "https://h/login?user=a&password=...": EOF`,
			`Get "https://h/login?user=a&password=%5BREDACTED%5D EOF`,
		},
		{
			"敏感值以标点结尾",
			"",
			`跳转到 https://h/cb?token=abc.;) 失败`,
			`跳转到 https://h/cb?token=%5BREDACTED%5D 失败`,
		},
		{
			"请求 URL 只是前缀且敏感值继续延伸",
			"https://h/?token=abc",
			`Get "https://h/?token=abcdef": EOF`,
			`Get "https://h/?token=%5BREDACTED%5D EOF`,
		},
		{
			"请求 URL 只是前缀且敏感值经标点继续延伸",
			"https://h/?token=abc",
			`Get "https://h/?token=abc.secret": EOF`,
			`Get "https://h/?token=%5BREDACTED%5D EOF`,
		},
		{
			"请求 URL 之后多个标点视为值的一部分",
			"https://h/?token=abc",
			`Get "https://h/?token=abc...": EOF`,
			`Get "https://h/?token=%5BREDACTED%5D EOF`,
		},
		{
			"与请求 URL 相同时保留括号与句号",
			"https://h/?token=abc",
			`见 (https://h/?token=abc). 重试`,
			`见 (https://h/?token=%5BREDACTED%5D). 重试`,
		},
		{
			"单引号不截断",
			"",
			`Get "https://h/x?q=O'Reilly&access_token=secret": EOF`,
			`Get "https://h/x?q=O'Reilly&access_token=%5BREDACTED%5D EOF`,
		},
		{
			"%q 转义出的双引号不截断",
			"",
			`Get "https://h/x?q=a\"b&token=secret": EOF`,
			`Get "https://h/x?q=a\"b&token=%5BREDACTED%5D EOF`,
		},
		{
			"最后一个参数不敏感时标点原样保留",
			"",
			`重定向失败 (https://h/cb?token=secret&code=c).`,
			`重定向失败 (https://h/cb?token=%5BREDACTED%5D&code=c).`,
		},
		{
			"协议相对地址",
			"",
			`Get "//alice:pass@example.com/?token=abc&q=1": EOF`,
			`Get "//example.com/?token=%5BREDACTED%5D&q=1": EOF`,
		},
		{
			"路径中的双斜杠不受影响",
			"",
			`open /var//log/a.log: permission denied`,
			`open /var//log/a.log: permission denied`,
		},
		{
			"多个 URL",
			"",
			"a https://h/?x=1&token=s1\tb http://u:p@h2/?sig=s2",
			"a https://h/?x=1&token=%5BREDACTED%5D\tb http://h2/?sig=%5BREDACTED%5D",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			z := newSanitizer(SanitizeOptions{Redact: true})
			e := &HAREntry{Request: HARRequest{URL: tt.requestURL}, Error: tt.in}
			z.entry(e)
			if e.Error != tt.want {
				t.Errorf("_error = %q\n期望 %q", e.Error, tt.want)
			}
		})
	}
}

func TestRedactJSONPreservesLayout(t *testing.T) {
	t.Parallel()
	z := newSanitizer(SanitizeOptions{Redact: true})
	tests := []struct{ in, want string }{
		{`{"b":1,"a":{"secret":[1,{"x":2}],"z":1.50}}`, `{"b":1,"a":{"secret":"[REDACTED]","z":1.50}}`},
		{`[{"password" : "x"}, {"password":null}]`, `[{"password" : "[REDACTED]"}, {"password":null}]`},
		{`{"nested":{"token":{"token":"x"}},"after":"y"}`, `{"nested":{"token":"[REDACTED]"},"after":"y"}`},
		{`"token"`, `"token"`},
	}
	for _, tt := range tests {
		got, _, err := redactJSON([]byte(tt.in), z.classify)
		if err != nil || string(got) != tt.want {
			t.Errorf("redactJSON(%s) = %s, %v\n期望 %s", tt.in, got, err, tt.want)
		}
	}
	for _, bad := range []string{`{"a":1`, `[1,2`, `{"token":"x"`} {
		if _, _, err := redactJSON([]byte(bad), z.classify); err == nil {
			t.Errorf("截断的 JSON %s 应报错", bad)
		}
	}
}

func TestRedactJSONScalarValues(t *testing.T) {
	t.Parallel()
	z := newSanitizer(SanitizeOptions{Redact: true, Fields: []string{"phone", "verified"}})
	tests := []struct {
		in, want string
		count    int
	}{
		{
			`{"phone":13800000000,"otp":123456,"password":987654}`,
			`{"phone":"[REDACTED]","otp":"[REDACTED]","password":"[REDACTED]"}`, 3,
		},
		{`{"access_token":12345,"sid":-7,"verified":true,"pin_secret":1e9}`, `{"access_token":"[REDACTED]","sid":"[REDACTED]","verified":"[REDACTED]","pin_secret":"[REDACTED]"}`, 4},
		{
			`{"max_tokens":128,"usage":{"prompt_tokens":12,"token_count":3},"tokens_truncated":false}`,
			`{"max_tokens":128,"usage":{"prompt_tokens":12,"token_count":3},"tokens_truncated":false}`, 0,
		},
		{`{"tokens":["a","b"],"token_type":"Bearer","phone":null}`, `{"tokens":"[REDACTED]","token_type":"[REDACTED]","phone":null}`, 2},
	}
	for _, tt := range tests {
		got, n, err := redactJSON([]byte(tt.in), z.classify)
		if err != nil || string(got) != tt.want || n != tt.count {
			t.Errorf("redactJSON(%s) = %s, %d, %v\n期望 %s, %d", tt.in, got, n, err, tt.want, tt.count)
		}
	}
}

func TestSanitizeKeyMatching(t *testing.T) {
	t.Parallel()
	z := newSanitizer(SanitizeOptions{Redact: true, Fields: []string{" ID-Card ", ""}})
	tests := []struct {
		level sensitivity
		keys  []string
	}{
		{sensitiveField, []string{"access_token", "X-CSRF-Token", "clientSecret", "PASSWORD", "sid", "otp", "id_card", "idCard", "x-amz-security-token", "token", "refreshToken", "key"}},
		{tokenCounter, []string{"max_tokens", "prompt_tokens", "token_count", "tokens", "token_type"}},
		{notSensitive, []string{"author", "session_count_total", "sidebar", "id", "", "content-type", "keyword", "monkey"}},
	}
	for _, tt := range tests {
		for _, key := range tt.keys {
			if got := z.classify(key); got != tt.level {
				t.Errorf("classify(%q) = %d，期望 %d", key, got, tt.level)
			}
		}
	}
}
