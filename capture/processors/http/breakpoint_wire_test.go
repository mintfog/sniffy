// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package http

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// 断点/插件改过状态码之后,逐字回放上游那行会把改动吃掉:界面上显示 503、线上还是 200。
func TestStreamWriteHeadRebuildsEditedStatusLine(t *testing.T) {
	raw := newMockConn("")
	w := newConnStreamWriter(raw, 50*time.Millisecond)

	if err := w.writeHead("HTTP/1.1 200 OK", http.StatusServiceUnavailable, http.Header{}, nil); err != nil {
		t.Fatalf("writeHead: %v", err)
	}
	if got := raw.WrittenData(); !strings.HasPrefix(got, "HTTP/1.1 503 Service Unavailable\r\n") {
		t.Fatalf("状态行 = %q", strings.SplitN(got, "\r\n", 2)[0])
	}
}

// 无体状态码是例外:这条路的 body 是逐块中继出去的,写出「204 + 分块数据」会让客户端
// 把那些字节当成同一连接上的下一个响应,整条长连接错位。宁可保留上游那行。
func TestStreamWriteHeadKeepsUpstreamLineForBodylessStatus(t *testing.T) {
	raw := newMockConn("")
	w := newConnStreamWriter(raw, 50*time.Millisecond)

	if err := w.writeHead("HTTP/1.1 200 OK", http.StatusNoContent, http.Header{}, nil); err != nil {
		t.Fatalf("writeHead: %v", err)
	}
	if got := raw.WrittenData(); !strings.HasPrefix(got, "HTTP/1.1 200 OK\r\n") {
		t.Fatalf("无体状态码不该被写到线上, 状态行 = %q", strings.SplitN(got, "\r\n", 2)[0])
	}
}

func TestPassthroughWriteHeadRebuildsEditedStatusLine(t *testing.T) {
	raw := newMockConn("")
	w := newConnBodyStreamer(raw, 50*time.Millisecond)

	if err := w.writeHead("HTTP/1.1 200 OK", http.StatusNotFound, http.Header{}, nil, 5); err != nil {
		t.Fatalf("writeHead: %v", err)
	}
	if got := raw.WrittenData(); !strings.HasPrefix(got, "HTTP/1.1 404 Not Found\r\n") {
		t.Fatalf("状态行 = %q", strings.SplitN(got, "\r\n", 2)[0])
	}
}

// 理由同 TestStreamWriteHeadKeepsUpstreamLineForBodylessStatus:透传旁路照样要把 body 发完。
func TestPassthroughWriteHeadKeepsUpstreamLineForBodylessStatus(t *testing.T) {
	raw := newMockConn("")
	w := newConnBodyStreamer(raw, 50*time.Millisecond)

	if err := w.writeHead("HTTP/1.1 200 OK", http.StatusNotModified, http.Header{}, nil, 5242880); err != nil {
		t.Fatalf("writeHead: %v", err)
	}
	if got := raw.WrittenData(); !strings.HasPrefix(got, "HTTP/1.1 200 OK\r\n") {
		t.Fatalf("无体状态码不该被写到线上, 状态行 = %q", strings.SplitN(got, "\r\n", 2)[0])
	}
}
