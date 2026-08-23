// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package flow

import (
	"bufio"
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 插件 / 规则往响应头里写进 CR/LF 时,逐字回放会把它拼成额外的头(响应拆分)。
// 这种响应退回标准 Write:net/http 会把换行折成空格,报文完整优先于保真。
// 注意注入发生在 Header map 上而不是 RawHeaders —— 写线的值正是从 map 取的。
func TestWriteResponseFallsBackOnInjectedCRLF(t *testing.T) {
	f := New(ProtoHTTP)
	f.Response = &Response{
		Status:     200,
		Header:     map[string][]string{"X-A": {"1\r\nX-Injected: pwned"}},
		RawHeaders: [][2]string{{"x-a", "1"}},
		Body:       []byte("hi"),
	}

	var buf bytes.Buffer
	if err := WriteResponse(&buf, f, httptest.NewRequest(http.MethodGet, "/", nil)); err != nil {
		t.Fatalf("WriteResponse: %v", err)
	}
	if strings.Contains(buf.String(), "\r\nX-Injected:") {
		t.Fatalf("线上字节里出现了注入的头行:\n%q", buf.String())
	}

	resp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(buf.Bytes())), nil)
	if err != nil {
		t.Fatalf("写出的响应无法解析: %v", err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("X-Injected"); got != "" {
		t.Fatalf("注入的头成了独立的一条: X-Injected=%q", got)
	}
}

// 干净的响应不受影响:仍逐字回放,顺序与大小写都不动。
func TestWriteResponseStaysFaithfulWhenClean(t *testing.T) {
	f := New(ProtoHTTP)
	f.Response = &Response{
		Status:     200,
		Header:     map[string][]string{"X-Zulu": {"1"}, "X-Alpha": {"2"}},
		RawHeaders: [][2]string{{"x-zulu", "1"}, {"X-ALPHA", "2"}},
		Body:       []byte("hi"),
	}

	var buf bytes.Buffer
	if err := WriteResponse(&buf, f, httptest.NewRequest(http.MethodGet, "/", nil)); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "x-zulu: 1\r\n") || !strings.Contains(out, "X-ALPHA: 2\r\n") {
		t.Fatalf("干净的响应应保持原大小写:\n%q", out)
	}
	if strings.Index(out, "x-zulu") > strings.Index(out, "X-ALPHA") {
		t.Fatalf("头顺序被改了:\n%q", out)
	}
}
