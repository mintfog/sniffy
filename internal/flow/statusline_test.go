// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package flow

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// respWithOrigin 造一条"上游给过原始状态行与头序列"的保真响应(h1 MITM 的常态)。
func respWithOrigin(status int, statusText, statusLine string, body string, extra ...[2]string) *Flow {
	f := New(ProtoHTTP)
	r := &Response{
		Status:     status,
		StatusText: statusText,
		Header:     map[string][]string{"Content-Type": {"text/plain"}},
		Body:       []byte(body),
		RawHeaders: append([][2]string{{"Content-Type", "text/plain"}}, extra...),
	}
	for _, kv := range extra {
		r.Header[kv[0]] = []string{kv[1]}
	}
	r.SetOriginalHead(statusLine)
	f.Response = r
	return f
}

func writeResp(t *testing.T, f *Flow, method string) string {
	t.Helper()
	var buf bytes.Buffer
	req := httptest.NewRequest(method, "http://x/", nil)
	if err := WriteResponse(&buf, f, req); err != nil {
		t.Fatalf("WriteResponse 失败: %v", err)
	}
	return buf.String()
}

// 断点把状态码改成无体码之后,不能再沿用上游宣告的 Content-Length ——
// 那会让客户端一直等一份永远不会来的 body。
func TestWriteResponseDropsUpstreamLengthWhenStatusEdited(t *testing.T) {
	f := respWithOrigin(http.StatusNoContent, "204 No Content", "HTTP/1.1 200 OK", "", [2]string{"Content-Length", "125"})
	got := writeResp(t, f, http.MethodGet)

	if !strings.HasPrefix(got, "HTTP/1.1 204 No Content\r\n") {
		t.Errorf("状态行 = %q", strings.SplitN(got, "\r\n", 2)[0])
	}
	if strings.Contains(got, "Content-Length: 125") {
		t.Errorf("改成 204 后不该再宣告上游那份长度:\n%s", got)
	}
}

// 状态码没被改过的无体响应仍沿用上游宣告的长度(HEAD 的既有语义,不能被上面那条误伤)。
func TestWriteResponseKeepsUpstreamLengthForUneditedHead(t *testing.T) {
	f := respWithOrigin(http.StatusOK, "200 OK", "HTTP/1.1 200 OK", "", [2]string{"Content-Length", "125"})
	got := writeResp(t, f, http.MethodHead)

	if !strings.Contains(got, "Content-Length: 125") {
		t.Errorf("HEAD 未被改动时应沿用上游长度:\n%s", got)
	}
}

// 只改原因短语(不动状态码)时,逐字回放原始状态行会把编辑吃掉。
func TestWriteResponseHonorsReasonPhraseEdit(t *testing.T) {
	f := respWithOrigin(http.StatusOK, "Totally Fine", "HTTP/1.1 200 OK", "hi")
	got := writeResp(t, f, http.MethodGet)

	if !strings.HasPrefix(got, "HTTP/1.1 200 Totally Fine\r\n") {
		t.Errorf("原因短语的编辑被吞掉了, 状态行 = %q", strings.SplitN(got, "\r\n", 2)[0])
	}
}

// 一处没改时仍须逐字回放上游那行(保真是这条路径存在的理由)。
func TestWriteResponseReplaysUntouchedStatusLineVerbatim(t *testing.T) {
	f := respWithOrigin(http.StatusOK, "200 OK", "HTTP/1.0 200 OK", "hi")
	got := writeResp(t, f, http.MethodGet)

	if !strings.HasPrefix(got, "HTTP/1.0 200 OK\r\n") {
		t.Errorf("未改动的状态行应原样回放, got %q", strings.SplitN(got, "\r\n", 2)[0])
	}
}

func TestStatusLineCode(t *testing.T) {
	for line, want := range map[string]int{
		"HTTP/1.1 200 OK":           200,
		"HTTP/1.0 304 Not Modified": 304,
		"":                          0,
		"garbage":                   0,
		"HTTP/1.1 abc OK":           0,
	} {
		if got := StatusLineCode(line); got != want {
			t.Errorf("StatusLineCode(%q) = %d, want %d", line, got, want)
		}
	}
}

// body 被整体换掉之后,截断标记必须失效:否则出线仍宣告上游那份更长的长度,
// 用户手写的 mock 响应对客户端就是一份读不完的短响应。
func TestClearTruncatedRestoresComputedLength(t *testing.T) {
	f := respWithOrigin(http.StatusOK, "200 OK", "HTTP/1.1 200 OK", "half", [2]string{"Content-Length", "10000"})
	f.Response.MarkTruncated()
	if got := writeResp(t, f, http.MethodGet); !strings.Contains(got, "Content-Length: 10000") {
		t.Fatalf("截断响应应沿用上游宣告的长度:\n%s", got)
	}

	f.Response.Body = []byte(`{"mocked":true}`)
	f.Response.ClearTruncated()
	got := writeResp(t, f, http.MethodGet)
	if !strings.Contains(got, "Content-Length: 15") {
		t.Errorf("换过 body 之后应按新内容重算长度:\n%s", got)
	}
}
