// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package http

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/internal/pipeline"
)

// gapFlowSink 记录 flow 生命周期回调,用于断言「开始」确实被上报。
type gapFlowSink struct {
	mu        sync.Mutex
	started   []*flow.Flow
	completed []*flow.Flow
}

func (s *gapFlowSink) RecordFlowStarted(f *flow.Flow) {
	s.mu.Lock()
	s.started = append(s.started, f)
	s.mu.Unlock()
}

func (s *gapFlowSink) RecordFlowCompleted(f *flow.Flow) {
	s.mu.Lock()
	s.completed = append(s.completed, f)
	s.mu.Unlock()
}

func (s *gapFlowSink) RecordFlowUpdated(*flow.Flow) {}

func (s *gapFlowSink) firstStarted() *flow.Flow {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.started) == 0 {
		return nil
	}
	return s.started[0]
}

// TestConnBodyStreamerReplaysRawHead 上游给了原始头序列时,透传旁路必须按原顺序/原大小写
// 回放,只替换帧头:逐跳头去掉、Content-Length 按实际长度写死。
func TestConnBodyStreamerReplaysRawHead(t *testing.T) {
	raw := newMockConn("")
	w := newConnBodyStreamer(raw, 0)

	header := http.Header{"X-Trace": {"abc"}, "Content-Type": {"video/mp4"}, "Connection": {"keep-alive"}}
	rawHead := [][2]string{{"x-trace", "abc"}, {"content-type", "video/mp4"}, {"Connection", "keep-alive"}}
	if err := w.writeHead("HTTP/1.1 206 Partial Content", http.StatusPartialContent, header, rawHead, 5); err != nil {
		t.Fatalf("writeHead: %v", err)
	}
	if n, err := w.Write(nil); n != 0 || err != nil {
		t.Fatalf("空写入应是空操作,实得 (%d, %v)", n, err)
	}
	if n, err := w.Write([]byte("bytes")); n != 5 || err != nil {
		t.Fatalf("Write = (%d, %v)", n, err)
	}
	if err := w.close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	out := raw.WrittenData()
	if !strings.HasPrefix(out, "HTTP/1.1 206 Partial Content\r\n") {
		t.Fatalf("状态行未沿用上游: %q", out)
	}
	if !strings.Contains(out, "x-trace: abc\r\ncontent-type: video/mp4\r\n") {
		t.Fatalf("原始头顺序/大小写未保留: %q", out)
	}
	if strings.Contains(strings.ToLower(out), "connection:") {
		t.Fatalf("逐跳头未剔除: %q", out)
	}
	if !strings.Contains(out, "Content-Length: 5\r\n") {
		t.Fatalf("缺少按长度写死的 Content-Length: %q", out)
	}
	if !strings.HasSuffix(out, "\r\n\r\nbytes") {
		t.Fatalf("长度已知时不应分块: %q", out)
	}
}

// TestConnBodyStreamerWriteFailures 客户端断开后,写头/写体/收尾都要把错误如实上抛,
// 让上层把 flow 标成 errored 并关掉连接。
func TestConnBodyStreamerWriteFailures(t *testing.T) {
	wantErr := errors.New("客户端已断开")
	newFailing := func() *connBodyStreamer {
		return newConnBodyStreamer(&failWriteConn{mockConn: newMockConn(""), err: wantErr}, 0)
	}

	// 头块超过 64KiB 缓冲 → bufio 直写底层连接,错误在 Write 处就冒出来。
	oversized := newFailing()
	bigHeader := http.Header{"X-Big": {strings.Repeat("v", 70*1024)}}
	if err := oversized.writeHead("", http.StatusOK, bigHeader, nil, 10); !errors.Is(err, wantErr) {
		t.Fatalf("超大响应头写失败 = %v, want %v", err, wantErr)
	}

	// 长度已知(非 chunked):写头在 Flush 处失败,后续写体/收尾沿用同一个错误。
	sized := newFailing()
	if err := sized.writeHead("", http.StatusOK, http.Header{}, nil, 4); !errors.Is(err, wantErr) {
		t.Fatalf("定长响应头写失败 = %v, want %v", err, wantErr)
	}
	if _, err := sized.Write([]byte("data")); !errors.Is(err, wantErr) {
		t.Fatalf("定长响应体写失败 = %v, want %v", err, wantErr)
	}
	if err := sized.close(); !errors.Is(err, wantErr) {
		t.Fatalf("定长响应收尾 = %v, want %v", err, wantErr)
	}

	// 长度未知(chunked):写体要走组帧分支,收尾要写终止 chunk,两处都应报错。
	chunked := newFailing()
	if err := chunked.writeHead("", http.StatusOK, http.Header{}, nil, -1); !errors.Is(err, wantErr) {
		t.Fatalf("分块响应头写失败 = %v, want %v", err, wantErr)
	}
	if _, err := chunked.Write([]byte("chunk")); !errors.Is(err, wantErr) {
		t.Fatalf("分块响应体写失败 = %v, want %v", err, wantErr)
	}
	if err := chunked.close(); !errors.Is(err, wantErr) {
		t.Fatalf("分块响应终止帧 = %v, want %v", err, wantErr)
	}
}

func TestH2BodyStreamerWriteFailure(t *testing.T) {
	wantErr := errors.New("stream reset")
	w := newH2BodyStreamer(&errorResponseWriter{err: wantErr})
	if n, err := w.Write([]byte("data")); n != 0 || !errors.Is(err, wantErr) {
		t.Fatalf("h2 透传写失败 = (%d, %v), want (0, %v)", n, err, wantErr)
	}
}

// TestConnStreamWriterArmsDeadlineAndDefaultHead 未捕获到原始头序列时按标准头写出;
// 配置了写超时则每次写出前续期(长流不能用一个绝对期限)。
func TestConnStreamWriterArmsDeadlineAndDefaultHead(t *testing.T) {
	raw := newMockConn("")
	w := newConnStreamWriter(raw, 50*time.Millisecond)

	before := time.Now()
	if err := w.writeHead("", http.StatusOK, http.Header{"Content-Type": {"text/event-stream"}}, nil); err != nil {
		t.Fatalf("writeHead: %v", err)
	}
	if len(raw.writeDeadlines) == 0 {
		t.Fatal("配置了写超时却没有设置写期限")
	}
	armed := raw.writeDeadlines[len(raw.writeDeadlines)-1]
	if !armed.After(before) || armed.After(before.Add(time.Second)) {
		t.Fatalf("写期限 %v 不在 %v 之后的 50ms 量级", armed, before)
	}
	if len(raw.connectionDeadlines) == 0 || !raw.connectionDeadlines[0].IsZero() {
		t.Fatalf("流式写回前应清掉握手期的绝对超时: %v", raw.connectionDeadlines)
	}

	out := raw.WrittenData()
	if !strings.HasPrefix(out, "HTTP/1.1 200 OK\r\n") {
		t.Fatalf("状态行 = %q", out)
	}
	if !strings.Contains(out, "Content-Type: text/event-stream\r\n") {
		t.Fatalf("标准头写出丢了 Content-Type: %q", out)
	}
	if !strings.Contains(strings.ToLower(out), "transfer-encoding: chunked\r\n") {
		t.Fatalf("流式写回必须改 chunked: %q", out)
	}
}

// TestConnStreamWriterWriteFailures 流式写回的三个出错点都要上抛。
func TestConnStreamWriterWriteFailures(t *testing.T) {
	wantErr := errors.New("客户端已断开")
	newFailing := func() *connStreamWriter {
		return newConnStreamWriter(&failWriteConn{mockConn: newMockConn(""), err: wantErr}, 0)
	}

	// 头块超过 bufio 默认 4KiB 缓冲 → 直写底层连接。
	oversized := newFailing()
	if err := oversized.writeHead("", http.StatusOK, http.Header{"X-Big": {strings.Repeat("v", 8*1024)}}, nil); !errors.Is(err, wantErr) {
		t.Fatalf("超大流式响应头 = %v, want %v", err, wantErr)
	}

	w := newFailing()
	if err := w.writeHead("", http.StatusOK, http.Header{}, nil); !errors.Is(err, wantErr) {
		t.Fatalf("流式响应头 = %v, want %v", err, wantErr)
	}
	if err := w.writeChunk([]byte("hello")); !errors.Is(err, wantErr) {
		t.Fatalf("流式消息 = %v, want %v", err, wantErr)
	}
	if err := w.close(); !errors.Is(err, wantErr) {
		t.Fatalf("流式终止帧 = %v, want %v", err, wantErr)
	}
}

// TestDispatchChunkGenericStreamAbort 通用分块流(NDJSON 等)上插件 abort 时,
// 必须在写回客户端之前就中断,而不是先把这块字节发出去。
func TestDispatchChunkGenericStreamAbort(t *testing.T) {
	preserveHTTPGlobals(t)
	p := pipeline.New(nil, nil)
	p.Register(&testStreamHook{fn: func(*flow.StreamMessage) flow.Decision {
		return flow.AbortDecision(0, "generic chunk blocked")
	}})
	activePipeline = p

	sw := &captureStreamWriter{}
	err := dispatchChunk(nil, "http://example.test/ndjson", flow.WSServerToClient, flow.StreamChunk,
		&sseScanner{}, &grpcScanner{}, []byte("{\"a\":1}\n"), sw)
	if !errors.Is(err, errStreamAbort) {
		t.Fatalf("通用分块 abort = %v, want %v", err, errStreamAbort)
	}
	if len(sw.chunks) != 0 {
		t.Fatalf("abort 后不该写回任何字节: %q", sw.body())
	}
}

func TestBuildOutboundGRPCRequestFillsSchemeAndHost(t *testing.T) {
	request := &http.Request{
		Method: http.MethodPost,
		Host:   "grpc.example:443",
		URL:    &url.URL{Path: "/pkg.Svc/Call"},
		Header: http.Header{"Content-Type": {"application/grpc"}},
	}
	f := buildStreamRequestFlow(request, flow.ProtoHTTPS)

	out, err := buildOutboundGRPCRequest(context.Background(), request, f)
	if err != nil {
		t.Fatalf("buildOutboundGRPCRequest: %v", err)
	}
	if out.URL.Scheme != "https" || out.URL.Host != "grpc.example:443" {
		t.Fatalf("出站 URL = %q,scheme/host 未按请求补全", out.URL.String())
	}
	if out.Host != "grpc.example:443" {
		t.Fatalf("出站 Host = %q", out.Host)
	}
	// 客户端没带 UA 时用空值哨兵挡住 net/http 注入 Go-http-client。
	if ua, ok := out.Header["User-Agent"]; !ok || len(ua) != 1 || ua[0] != "" {
		t.Fatalf("User-Agent 哨兵 = %v", out.Header["User-Agent"])
	}

	bad := &http.Request{
		Method: "BAD METHOD",
		Host:   "grpc.example:443",
		URL:    &url.URL{Path: "/pkg.Svc/Call"},
		Header: http.Header{},
	}
	if _, err := buildOutboundGRPCRequest(context.Background(), bad, buildStreamRequestFlow(bad, flow.ProtoHTTPS)); err == nil {
		t.Fatal("非法方法应构造失败")
	}
}

// TestRunGRPCStreamOutboundBuildFailure 出站请求构造不出来时回 502,并把 flow 记成 errored。
func TestRunGRPCStreamOutboundBuildFailure(t *testing.T) {
	preserveHTTPGlobals(t)
	activePipeline = nil
	sink := &gapFlowSink{}
	SetFlowSink(sink)

	request := &http.Request{
		Method: "BAD METHOD",
		Host:   "grpc.example:443",
		URL:    &url.URL{Path: "/pkg.Svc/Call"},
		Header: http.Header{"Content-Type": {"application/grpc"}},
		Body:   io.NopCloser(strings.NewReader("")),
	}
	clientAddr := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 51234}
	r := &branchResponder{}

	if err := runGRPCStream(silentServer{}, request, flow.ProtoHTTPS, clientAddr, nil, r, &captureStreamWriter{}); err != nil {
		t.Fatalf("构造失败应转成 502 而非返回错误: %v", err)
	}
	if r.badGatewayCalls != 1 {
		t.Fatalf("502 次数 = %d", r.badGatewayCalls)
	}
	started := sink.firstStarted()
	if started == nil {
		t.Fatal("gRPC 流未上报 flow 开始")
	}
	if started.Request.ClientIP != clientAddr.String() {
		t.Fatalf("客户端地址 = %q, want %q", started.Request.ClientIP, clientAddr.String())
	}
	if started.State != flow.StateErrored {
		t.Fatalf("flow 状态 = %s, want %s", started.State, flow.StateErrored)
	}
}

// TestRunGRPCStreamRequestPump 请求泵把客户端方向的帧逐条过钩子后写给上游:
// 结尾残帧要原样补发,插件 abort 则要把上游请求体以该错误关闭(而不是当成正常结束)。
func TestRunGRPCStreamRequestPump(t *testing.T) {
	complete := grpcFrameBytes([]byte("ping"), false)
	partial := []byte{0x00, 0x00, 0x00, 0x00, 0x09, 'h', 'a', 'l'} // 声明 9 字节却只给了 3

	newRequest := func(body []byte) *http.Request {
		return &http.Request{
			Method: http.MethodPost,
			Host:   "grpc.example:443",
			URL:    &url.URL{Path: "/pkg.Svc/Call"},
			Header: http.Header{"Content-Type": {"application/grpc"}},
			Body:   io.NopCloser(bytes.NewReader(body)),
			Proto:  "HTTP/2.0",
		}
	}

	// 上游侧读尽请求体并把结果回报,借此观察请求泵到底发了什么。必须同步读完再返回响应:
	// runGRPCStream 一返回就会关掉管道读端,异步读只能读到"pipe closed"而非真实结果。
	installUpstream := func(t *testing.T, forwarded chan<- []byte, failed chan<- error) {
		t.Helper()
		sharedStreamClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			var buf bytes.Buffer
			if _, err := io.Copy(&buf, req.Body); err != nil {
				failed <- err
			} else {
				forwarded <- buf.Bytes()
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     http.Header{"Content-Type": {"application/grpc"}},
				Body:       io.NopCloser(strings.NewReader("")),
				Request:    req,
			}, nil
		})}
	}

	t.Run("flushes trailing partial frame", func(t *testing.T) {
		preserveHTTPGlobals(t)
		activePipeline = nil
		flowSink = nil
		forwarded := make(chan []byte, 1)
		failed := make(chan error, 1)
		installUpstream(t, forwarded, failed)

		body := append(append([]byte(nil), complete...), partial...)
		if err := runGRPCStream(silentServer{}, newRequest(body), flow.ProtoHTTPS, nil, nil, &branchResponder{}, &captureStreamWriter{}); err != nil {
			t.Fatalf("runGRPCStream: %v", err)
		}
		select {
		case got := <-forwarded:
			if !bytes.Equal(got, body) {
				t.Fatalf("转发给上游的请求体 = %x, want %x", got, body)
			}
		case err := <-failed:
			t.Fatalf("上游读请求体失败: %v", err)
		case <-time.After(3 * time.Second):
			t.Fatal("等待上游收齐请求体超时")
		}
	})

	t.Run("aborts upstream body on hook abort", func(t *testing.T) {
		preserveHTTPGlobals(t)
		flowSink = nil
		p := pipeline.New(nil, nil)
		p.Register(&testStreamHook{fn: func(m *flow.StreamMessage) flow.Decision {
			if m.Direction == flow.WSClientToServer {
				return flow.AbortDecision(0, "client frame blocked")
			}
			return flow.ContinueDecision()
		}})
		activePipeline = p
		forwarded := make(chan []byte, 1)
		failed := make(chan error, 1)
		installUpstream(t, forwarded, failed)

		if err := runGRPCStream(silentServer{}, newRequest(complete), flow.ProtoHTTPS, nil, nil, &branchResponder{}, &captureStreamWriter{}); err != nil {
			t.Fatalf("请求方向 abort 不应让响应侧报错: %v", err)
		}
		select {
		case err := <-failed:
			if !errors.Is(err, errStreamAbort) {
				t.Fatalf("上游请求体应以 abort 错误关闭,实得 %v", err)
			}
		case got := <-forwarded:
			t.Fatalf("被 abort 的帧不该转发给上游: %x", got)
		case <-time.After(3 * time.Second):
			t.Fatal("等待上游请求体出错超时")
		}
	})
}
