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

// rawTruncatingServer 应答一个「声明了 chunked 却没写完」的非 SSE 响应:先发头 + 一个数据块,
// 再由 tail 决定怎么收场(立即断开 / 一直挂着)。用裸 TCP 是因为 httptest 的 Server
// 会替我们把响应补完整,而这组测试要的正是「补不完整」。
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
		_, _ = conn.Write([]byte("5\r\nhello\r\n")) // 一个完整数据块,但整条响应没有终止块
		tail(conn)
	}()
	return ln.Addr().String()
}

// 上游在响应体中途断开:Flow.Body 只有半截,记成 completed 就等于告诉用户
// 「这就是完整响应」。这条路径没有增量呈现,Flow.Body 就是界面上的全部内容。
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

// 上游既不是 SSE 又永不结束:退化路径靠自己的总超时兜底。超时后 io.ReadAll 拿到的是
// 半截 body,此时必须记 errored 并说明是超时,否则就是一条「成功但被截断」的 flow。
func TestSSEFallbackTimeoutIsErrored(t *testing.T) {
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
	// 已读到的部分仍要留着:排查「上游卡在哪」时那半截就是线索。
	if string(f.Response.Body) != "hello" {
		t.Fatalf("已读到的部分应保留,实际 = %q", string(f.Response.Body))
	}
}

// bulkServer 应答一个指定字节数的普通响应。
func bulkServer(t *testing.T, contentType string, size int) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentType)
		_, _ = w.Write(bytes.Repeat([]byte("x"), size))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// 一次性往返把整个响应体读进内存,还要随快照再复制一份并长期留在会话存储里。上游给多少就
// 吃多少的话,重发一个下载链接就能在超时之前把进程撑爆 —— 这条路径绕开了抓包侧的旁路与落盘。
func TestResendResponseBodyCapped(t *testing.T) {
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

// endlessBulkServer 应答一个永不结束的 chunked 响应,直到被关连接。
// bulkServer 的响应只比上限多一字节,收口是瞬时的,盖不住「触顶之后还得等多久」。
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

// 上限的意义是「立刻收手」,不是「少存一点」:触顶之后必须当场断开,而不是陪着上游把剩下的
// 传完 —— 慢速或不结束的下载会让这条 flow 永远停在 pending(界面上就是主按钮一直锁着)。
//
// 必须带一条头:没有头就不进保真写线路径,响应体改由标准 Transport 包装,收口方式完全不同,
// 盖不到 forward.pooledBody 那条真正要钉的路径(见 splitComposedHeaders)。
func TestResendCapStopsTransferPromptly(t *testing.T) {
	app := newComposeApp(t)
	prev := maxComposeResponseBytes
	maxComposeResponseBytes = 4096
	t.Cleanup(func() { maxComposeResponseBytes = prev })

	events, unsubscribe := app.Engine.Bus().Subscribe()
	t.Cleanup(unsubscribe)

	id, err := app.SendRequest(flow.RequestSpec{
		Method:  "GET",
		URL:     endlessBulkServer(t) + "/big",
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

// 恰好等于上限的响应必须照常收下:上限判定差一字节,就会把正常内容判成超限。
func TestResendResponseBodyAtLimitSucceeds(t *testing.T) {
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

// SSE 退化路径与一次性往返同为「整块进内存」:总超时只拦得住不结束的上游,拦不住
// 十分钟内就送来一个大文件的上游,故上限必须同样生效。
func TestSSEFallbackResponseBodyCapped(t *testing.T) {
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

// 上游声明了 SSE 却始终不发空行:事件切不出来,缓冲只会一直涨。构造器这边没有下游
// 客户端要喂,必须直接收掉,而不是替对端把内存吃光。
func TestSSEOverflowAbortsStream(t *testing.T) {
	app := newComposeApp(t)
	url, emit, _ := sseServer(t, 200, "text/event-stream")
	events, unsubscribe := app.Engine.Bus().Subscribe()
	t.Cleanup(unsubscribe)

	id, err := app.SendRequest(sseSpec(url, [][2]string{{"Accept", "text/event-stream"}}))
	if err != nil {
		t.Fatal(err)
	}
	// 一路 data: 不带空行,凑过单事件上限。分多块发,顺带覆盖跨 Push 累积的路径。
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

// gzipBombServer 应答一个「压缩后远小于上限、解压后远大于上限」的响应。
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

// 上游客户端设了 DisableCompression,resp.Body 是压缩字节:读取侧的上限只卡得住传输量,
// 而 CaptureResponseToFlow 随后才解压。上限落错一层,半兆的 gzip 就能在 Flow.Body 里
// 变成几百兆,还会记成 completed —— 界面上是绿的、内容却是错的。
func TestResendGzipBombCapped(t *testing.T) {
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

// SSE 退化路径与一次性往返共用同一段读取逻辑,压缩炸弹同样要拦下。
func TestSSEFallbackGzipBombCapped(t *testing.T) {
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

// 压缩响应没超限时必须照常解码收下:上限不该把正常的 gzip 响应一并判死。
func TestResendGzipUnderLimitSucceeds(t *testing.T) {
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
