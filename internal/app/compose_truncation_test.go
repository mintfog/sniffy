// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package app

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/internal/service"
)

// 裸 TCP 用于控制 chunked 终止块的缺失，模拟响应体截断或悬挂。
func rawTruncatingServer(t *testing.T, contentType string, tail func(net.Conn)) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
		br := bufio.NewReader(conn)
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				return
			}
			if strings.TrimRight(line, "\r\n") == "" {
				break
			}
		}
		_, _ = conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Type: " + contentType + "\r\nTransfer-Encoding: chunked\r\n\r\n"))
		_, _ = conn.Write([]byte("5\r\nhello\r\n"))
		tail(conn)
	}()
	return ln.Addr().String()
}

func TestSSEFallbackTruncatedBodyIsErrored(t *testing.T) {
	app := newComposeApp(t)
	addr := rawTruncatingServer(t, "application/json", func(c net.Conn) { _ = c.Close() })
	events, unsubscribe := app.Engine.Bus().Subscribe()
	t.Cleanup(unsubscribe)

	id, err := app.SendRequest(sseSpec("http://"+addr+"/events", [][2]string{{"Accept", "text/event-stream"}}))
	if err != nil {
		t.Fatal(err)
	}
	waitFlowSettled(t, events, id)

	f, _ := app.Service.RawFlow(id)
	if f.State != flow.StateErrored {
		t.Fatalf("终态 = %q,期望 %q —— 截断的响应体不能记成成功", f.State, flow.StateErrored)
	}
	if !strings.Contains(f.Error, "不完整") {
		t.Fatalf("错误信息未说明内容不完整: %q", f.Error)
	}
	if _, ok := app.Service.StreamSession(id); ok {
		t.Fatal("退化路径不该产生流会话")
	}
}

func TestSSEFallbackTimeoutIsErrored(t *testing.T) {
	if !inAppTestSubprocess(t) {
		if out, err := appTestSubprocess(t); err != nil {
			t.Fatalf("全局参数隔离子进程失败: %v\n%s", err, out)
		}
		return
	}
	app := newComposeApp(t)
	prev := composeFallbackTimeout
	composeFallbackTimeout = 150 * time.Millisecond
	t.Cleanup(func() { composeFallbackTimeout = prev })

	hang := make(chan struct{})
	t.Cleanup(func() { close(hang) })
	addr := rawTruncatingServer(t, "application/json", func(net.Conn) { <-hang })

	events, unsubscribe := app.Engine.Bus().Subscribe()
	t.Cleanup(unsubscribe)

	id, err := app.SendRequest(sseSpec("http://"+addr+"/events", [][2]string{{"Accept", "text/event-stream"}}))
	if err != nil {
		t.Fatal(err)
	}
	waitFlowSettled(t, events, id)

	f, _ := app.Service.RawFlow(id)
	if f.State != flow.StateErrored {
		t.Fatalf("终态 = %q,期望 %q —— 超时兜底不是正常终点", f.State, flow.StateErrored)
	}
	if !strings.Contains(f.Error, "未结束响应体") {
		t.Fatalf("错误信息未指出是超时兜底: %q", f.Error)
	}
	// 保留已读内容，便于定位上游中断的位置。
	if string(f.Response.Body) != "hello" {
		t.Fatalf("已读到的部分应保留,实际 = %q", string(f.Response.Body))
	}
}

func bulkServer(t *testing.T, contentType string, size int) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentType)
		_, _ = w.Write(bytes.Repeat([]byte("x"), size))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// 构造器将完整响应读入内存，需要独立于抓包旁路缓存的体积限制。
func TestResendResponseBodyCapped(t *testing.T) {
	if !inAppTestSubprocess(t) {
		if out, err := appTestSubprocess(t); err != nil {
			t.Fatalf("全局参数隔离子进程失败: %v\n%s", err, out)
		}
		return
	}
	app := newComposeApp(t)
	prev := maxComposeResponseBytes
	maxComposeResponseBytes = 4096
	t.Cleanup(func() { maxComposeResponseBytes = prev })

	events, unsubscribe := app.Engine.Bus().Subscribe()
	t.Cleanup(unsubscribe)

	url := bulkServer(t, "application/json", int(maxComposeResponseBytes)+1)
	id, err := app.SendRequest(flow.RequestSpec{Method: "GET", URL: url})
	if err != nil {
		t.Fatal(err)
	}
	waitFlowSettled(t, events, id)

	f, _ := app.Service.RawFlow(id)
	if f.State != flow.StateErrored {
		t.Fatalf("终态 = %q,期望 %q —— 被主动收掉的响应不是完整响应", f.State, flow.StateErrored)
	}
	if !strings.Contains(f.Error, "超过上限") {
		t.Fatalf("错误信息未说明是超上限: %q", f.Error)
	}
}

// 持续发送直到客户端断开，用于验证超限后能及时停止读取。
func endlessBulkServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		br := bufio.NewReader(conn)
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				return
			}
			if strings.TrimRight(line, "\r\n") == "" {
				break
			}
		}
		if _, err := conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nTransfer-Encoding: chunked\r\n\r\n")); err != nil {
			return
		}
		chunk := []byte("400\r\n" + strings.Repeat("x", 1024) + "\r\n")
		for {
			if _, err := conn.Write(chunk); err != nil {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()
	return "http://" + ln.Addr().String()
}

func TestResendCapStopsTransferPromptly(t *testing.T) {
	if !inAppTestSubprocess(t) {
		if out, err := appTestSubprocess(t); err != nil {
			t.Fatalf("全局参数隔离子进程失败: %v\n%s", err, out)
		}
		return
	}
	app := newComposeApp(t)
	prev := maxComposeResponseBytes
	maxComposeResponseBytes = 4096
	t.Cleanup(func() { maxComposeResponseBytes = prev })

	events, unsubscribe := app.Engine.Bus().Subscribe()
	t.Cleanup(unsubscribe)

	id, err := app.SendRequest(flow.RequestSpec{
		Method: "GET",
		URL:    endlessBulkServer(t) + "/big",
		// 显式请求头使转发进入保真路径，验证 pooledBody 在超限时能及时关闭连接。
		Headers: [][2]string{{"Accept", "*/*"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	deadline := time.After(5 * time.Second)
	for settled := false; !settled; {
		select {
		case ev := <-events:
			dto, ok := ev.Payload.(service.HTTPSessionDTO)
			settled = ok && dto.ID == id && dto.Status != "pending"
		case <-deadline:
			t.Fatal("5s 后 flow 仍未收尾 —— 触顶之后还在陪上游把 body 传完")
		}
	}

	f, _ := app.Service.RawFlow(id)
	if f.State != flow.StateErrored {
		t.Fatalf("终态 = %q,期望 %q", f.State, flow.StateErrored)
	}
}

func TestResendResponseBodyAtLimitSucceeds(t *testing.T) {
	if !inAppTestSubprocess(t) {
		if out, err := appTestSubprocess(t); err != nil {
			t.Fatalf("全局参数隔离子进程失败: %v\n%s", err, out)
		}
		return
	}
	app := newComposeApp(t)
	prev := maxComposeResponseBytes
	maxComposeResponseBytes = 4096
	t.Cleanup(func() { maxComposeResponseBytes = prev })

	events, unsubscribe := app.Engine.Bus().Subscribe()
	t.Cleanup(unsubscribe)

	url := bulkServer(t, "application/json", int(maxComposeResponseBytes))
	id, err := app.SendRequest(flow.RequestSpec{Method: "GET", URL: url})
	if err != nil {
		t.Fatal(err)
	}
	waitFlowSettled(t, events, id)

	f, _ := app.Service.RawFlow(id)
	if f.State != flow.StateCompleted {
		t.Fatalf("终态 = %q,期望 %q(错误: %q)", f.State, flow.StateCompleted, f.Error)
	}
	if int64(len(f.Response.Body)) != maxComposeResponseBytes {
		t.Fatalf("响应体 = %d 字节,期望 %d", len(f.Response.Body), maxComposeResponseBytes)
	}
}

// SSE 退化为普通响应后会缓冲完整内容，因此仍需校验体积上限。
func TestSSEFallbackResponseBodyCapped(t *testing.T) {
	if !inAppTestSubprocess(t) {
		if out, err := appTestSubprocess(t); err != nil {
			t.Fatalf("全局参数隔离子进程失败: %v\n%s", err, out)
		}
		return
	}
	app := newComposeApp(t)
	prev := maxComposeResponseBytes
	maxComposeResponseBytes = 4096
	t.Cleanup(func() { maxComposeResponseBytes = prev })

	events, unsubscribe := app.Engine.Bus().Subscribe()
	t.Cleanup(unsubscribe)

	url := bulkServer(t, "application/json", int(maxComposeResponseBytes)+1)
	id, err := app.SendRequest(sseSpec(url, [][2]string{{"Accept", "text/event-stream"}}))
	if err != nil {
		t.Fatal(err)
	}
	waitFlowSettled(t, events, id)

	f, _ := app.Service.RawFlow(id)
	if f.State != flow.StateErrored {
		t.Fatalf("终态 = %q,期望 %q", f.State, flow.StateErrored)
	}
	if !strings.Contains(f.Error, "超过上限") {
		t.Fatalf("错误信息未说明是超上限: %q", f.Error)
	}
}

// 缺少空行分隔符时 SSE 解析器会持续缓冲，必须在单事件超限时终止。
func TestSSEOverflowAbortsStream(t *testing.T) {
	app := newComposeApp(t)
	url, emit, _ := sseServer(t, 200, "text/event-stream")
	events, unsubscribe := app.Engine.Bus().Subscribe()
	t.Cleanup(unsubscribe)

	id, err := app.SendRequest(sseSpec(url, [][2]string{{"Accept", "text/event-stream"}}))
	if err != nil {
		t.Fatal(err)
	}
	// 分块发送且省略空行，验证跨次 Push 累积也受单事件上限约束。
	chunk := "data: " + strings.Repeat("x", 1<<20) + "\n"
	for sent := 0; sent <= flow.MaxSSEEventBytes; sent += len(chunk) {
		emit(chunk)
	}
	waitFlowSettled(t, events, id)

	f, _ := app.Service.RawFlow(id)
	if f.State != flow.StateErrored {
		t.Fatalf("终态 = %q,期望 %q", f.State, flow.StateErrored)
	}
	if !strings.Contains(f.Error, "已中止") {
		t.Fatalf("错误信息未说明中止原因: %q", f.Error)
	}
	ss, ok := app.Service.StreamSession(id)
	if !ok || ss.Status != "closed" {
		t.Fatalf("会话 ok=%v status=%q,期望 closed —— 悬挂的 open 会让 UI 一直转圈", ok, ss.Status)
	}
}

func gzipBombServer(t *testing.T, plainSize int) string {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(bytes.Repeat([]byte("x"), plainSize)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	gz := buf.Bytes()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(gz)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// 上游禁用自动解压，resp.Body 仍是压缩字节；体积上限还需约束解码后的 Flow.Body。
func TestResendGzipBombCapped(t *testing.T) {
	if !inAppTestSubprocess(t) {
		if out, err := appTestSubprocess(t); err != nil {
			t.Fatalf("全局参数隔离子进程失败: %v\n%s", err, out)
		}
		return
	}
	app := newComposeApp(t)
	prev := maxComposeResponseBytes
	maxComposeResponseBytes = 64 << 10
	t.Cleanup(func() { maxComposeResponseBytes = prev })

	events, unsubscribe := app.Engine.Bus().Subscribe()
	t.Cleanup(unsubscribe)

	url := gzipBombServer(t, 8<<20)
	id, err := app.SendRequest(flow.RequestSpec{Method: "GET", URL: url})
	if err != nil {
		t.Fatal(err)
	}
	waitFlowSettled(t, events, id)

	f, _ := app.Service.RawFlow(id)
	if f.State != flow.StateErrored {
		t.Fatalf("终态 = %q,期望 %q —— 压缩炸弹被当成了完整响应", f.State, flow.StateErrored)
	}
	if !strings.Contains(f.Error, "解压后") {
		t.Fatalf("错误信息未点明是解压后超限(用户在 Content-Length 里看不出来): %q", f.Error)
	}
	if int64(len(f.Response.Body)) > maxComposeResponseBytes {
		t.Fatalf("Flow.Body = %d 字节,已越过上限 %d", len(f.Response.Body), maxComposeResponseBytes)
	}
}

func TestSSEFallbackGzipBombCapped(t *testing.T) {
	if !inAppTestSubprocess(t) {
		if out, err := appTestSubprocess(t); err != nil {
			t.Fatalf("全局参数隔离子进程失败: %v\n%s", err, out)
		}
		return
	}
	app := newComposeApp(t)
	prev := maxComposeResponseBytes
	maxComposeResponseBytes = 64 << 10
	t.Cleanup(func() { maxComposeResponseBytes = prev })

	events, unsubscribe := app.Engine.Bus().Subscribe()
	t.Cleanup(unsubscribe)

	url := gzipBombServer(t, 8<<20)
	id, err := app.SendRequest(sseSpec(url, [][2]string{{"Accept", "text/event-stream"}}))
	if err != nil {
		t.Fatal(err)
	}
	waitFlowSettled(t, events, id)

	f, _ := app.Service.RawFlow(id)
	if f.State != flow.StateErrored {
		t.Fatalf("终态 = %q,期望 %q", f.State, flow.StateErrored)
	}
	if !strings.Contains(f.Error, "解压后") {
		t.Fatalf("错误信息未点明是解压后超限: %q", f.Error)
	}
}

func TestResendGzipUnderLimitSucceeds(t *testing.T) {
	if !inAppTestSubprocess(t) {
		if out, err := appTestSubprocess(t); err != nil {
			t.Fatalf("全局参数隔离子进程失败: %v\n%s", err, out)
		}
		return
	}
	app := newComposeApp(t)
	prev := maxComposeResponseBytes
	maxComposeResponseBytes = 64 << 10
	t.Cleanup(func() { maxComposeResponseBytes = prev })

	events, unsubscribe := app.Engine.Bus().Subscribe()
	t.Cleanup(unsubscribe)

	const plain = 32 << 10
	url := gzipBombServer(t, plain)
	id, err := app.SendRequest(flow.RequestSpec{Method: "GET", URL: url})
	if err != nil {
		t.Fatal(err)
	}
	waitFlowSettled(t, events, id)

	f, _ := app.Service.RawFlow(id)
	if f.State != flow.StateCompleted {
		t.Fatalf("终态 = %q,期望 %q(错误: %q)", f.State, flow.StateCompleted, f.Error)
	}
	if len(f.Response.Body) != plain {
		t.Fatalf("Flow.Body = %d 字节,期望解码后的 %d", len(f.Response.Body), plain)
	}
}
