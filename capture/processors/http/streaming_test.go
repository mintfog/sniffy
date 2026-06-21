// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package http

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/mintfog/sniffy/capture/types"
	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/internal/pipeline"
)

// ---- 测试替身 ----

type silentServer struct{}

func (silentServer) GetConfig() types.Config              { return nil }
func (silentServer) LogInfo(string, ...interface{})       {}
func (silentServer) LogError(string, ...interface{})      {}
func (silentServer) LogDebug(string, ...interface{})      {}
func (silentServer) FormatDataPreview(data []byte) string { return string(data) }

// captureStreamWriter 捕获写回客户端的流式响应(并可经 ch 观察增量到达)。
type captureStreamWriter struct {
	mu      sync.Mutex
	status  int
	head    http.Header
	chunks  [][]byte
	trailer http.Header
	closed  bool
	ch      chan []byte
}

func (w *captureStreamWriter) writeHead(_ string, status int, h http.Header, _ [][2]string) error {
	w.mu.Lock()
	w.status = status
	w.head = h.Clone()
	w.mu.Unlock()
	return nil
}

func (w *captureStreamWriter) writeChunk(p []byte) error {
	cp := append([]byte(nil), p...)
	w.mu.Lock()
	w.chunks = append(w.chunks, cp)
	w.mu.Unlock()
	if w.ch != nil {
		w.ch <- cp
	}
	return nil
}

func (w *captureStreamWriter) setTrailer(h http.Header) {
	w.mu.Lock()
	w.trailer = h.Clone()
	w.mu.Unlock()
}

func (w *captureStreamWriter) close() error {
	w.mu.Lock()
	w.closed = true
	w.mu.Unlock()
	return nil
}

func (w *captureStreamWriter) body() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	return bytes.Join(w.chunks, nil)
}

type fakeResponder struct {
	sw      *captureStreamWriter
	aborted *flow.Decision
}

func (f *fakeResponder) writeFlowResponse(*flow.Flow, *http.Request) error { return nil }
func (f *fakeResponder) writeAbort(d flow.Decision)                        { f.aborted = &d }
func (f *fakeResponder) writeBadGateway() error                            { return nil }
func (f *fakeResponder) streamWriter() (streamWriter, bool)                { return f.sw, true }

type fakeStreamSink struct {
	mu   sync.Mutex
	last *flow.StreamSession
}

func (s *fakeStreamSink) RecordStreamSession(ss *flow.StreamSession) {
	s.mu.Lock()
	s.last = ss
	s.mu.Unlock()
}

func (s *fakeStreamSink) snapshot() *flow.StreamSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.last
}

type testStreamHook struct {
	fn func(*flow.StreamMessage) flow.Decision
}

func (h *testStreamHook) Name() string      { return "test-stream" }
func (h *testStreamHook) Priority() int     { return 0 }
func (h *testStreamHook) Enabled() bool     { return true }
func (h *testStreamHook) Match(string) bool { return true }
func (h *testStreamHook) OnStreamMessage(_ context.Context, m *flow.StreamMessage) flow.Decision {
	return h.fn(m)
}

func grpcFrameBytes(payload []byte, compressed bool) []byte {
	out := make([]byte, 5+len(payload))
	if compressed {
		out[0] = 1
	}
	binary.BigEndian.PutUint32(out[1:5], uint32(len(payload)))
	copy(out[5:], payload)
	return out
}

func withStreamSink(t *testing.T, s StreamSink) {
	t.Helper()
	prev := streamSink
	streamSink = s
	t.Cleanup(func() { streamSink = prev })
}

func withPipeline(t *testing.T, p *pipeline.Pipeline) {
	t.Helper()
	prev := activePipeline
	activePipeline = p
	t.Cleanup(func() { activePipeline = prev })
}

// ---- 解析器 ----

func TestSSEScanner(t *testing.T) {
	s := &sseScanner{}
	// 分片喂入,跨片的事件应在边界齐全后才产出。
	var events []sseEvent
	events = append(events, s.push([]byte("event: greet\nda"))...)
	if len(events) != 0 {
		t.Fatalf("不应在未见空行前产出事件,得 %d", len(events))
	}
	events = append(events, s.push([]byte("ta: hello\n\n: keep-alive\n\ndata: a\ndata: b\n\n"))...)
	if len(events) != 3 {
		t.Fatalf("期望 3 个事件块(greet / 注释 / 多行 data),得 %d", len(events))
	}
	if events[0].Event != "greet" || string(events[0].Data) != "hello" {
		t.Fatalf("事件0解析错误: event=%q data=%q", events[0].Event, events[0].Data)
	}
	if string(events[1].Data) != "" {
		t.Fatalf("注释块不应有 data,得 %q", events[1].Data)
	}
	if string(events[2].Data) != "a\nb" {
		t.Fatalf("多行 data 应拼接为 a\\nb,得 %q", events[2].Data)
	}
	// Raw 应可保真回放(拼起来等于原始输入)。
	if got := string(events[0].Raw) + string(events[1].Raw) + string(events[2].Raw); got != "event: greet\ndata: hello\n\n: keep-alive\n\ndata: a\ndata: b\n\n" {
		t.Fatalf("Raw 回放不保真: %q", got)
	}
}

func TestSSEScannerCRLF(t *testing.T) {
	s := &sseScanner{}
	ev := s.push([]byte("data: x\r\n\r\n"))
	if len(ev) != 1 || string(ev[0].Data) != "x" {
		t.Fatalf("CRLF 边界解析失败: %+v", ev)
	}
}

func TestGRPCScanner(t *testing.T) {
	s := &grpcScanner{}
	f1 := grpcFrameBytes([]byte("one"), false)
	f2 := grpcFrameBytes([]byte("two"), true)
	stream := append(append([]byte(nil), f1...), f2...)
	// 分两片喂入,切点落在帧2 内部(帧1=8字节,切到第10字节即帧2 头部中段)。
	got := s.push(stream[:10])
	if len(got) != 1 || string(got[0].Payload) != "one" || got[0].Compressed {
		t.Fatalf("帧1解析错误: %+v", got)
	}
	got = s.push(stream[10:])
	if len(got) != 1 || string(got[0].Payload) != "two" || !got[0].Compressed {
		t.Fatalf("帧2解析错误: %+v", got)
	}
	if lo := s.flush(); len(lo) != 0 {
		t.Fatalf("不应有残留: %v", lo)
	}
}

func TestGRPCScannerOverflow(t *testing.T) {
	s := &grpcScanner{}
	hdr := make([]byte, 5)
	binary.BigEndian.PutUint32(hdr[1:5], uint32(grpcMaxMessage+1))
	got := s.push(append(hdr, []byte("partial")...))
	if len(got) != 0 || !s.overflow {
		t.Fatalf("超大长度应触发 overflow 且不产出帧")
	}
	if lo := s.flush(); string(lo) != string(append(hdr, []byte("partial")...)) {
		t.Fatalf("overflow 后应原样透传残留")
	}
}

func TestDetectResponseStream(t *testing.T) {
	cases := map[string]string{
		"text/event-stream":            flow.StreamSSE,
		"text/event-stream; charset=u": flow.StreamSSE,
		"application/grpc":             flow.StreamGRPC,
		"application/grpc+proto":       flow.StreamGRPC,
		"application/x-ndjson":         flow.StreamChunk,
		"application/json":             "",
		"text/html":                    "",
	}
	for ct, want := range cases {
		resp := &http.Response{Header: http.Header{"Content-Type": {ct}}}
		if got := detectResponseStream(resp); got != want {
			t.Errorf("detectResponseStream(%q)=%q want %q", ct, got, want)
		}
	}
}

func TestStreamingIntent(t *testing.T) {
	mk := func(h http.Header) *http.Request { return &http.Request{Header: h} }
	if !streamingIntent(mk(http.Header{"Accept": {"text/event-stream"}})) {
		t.Error("Accept: text/event-stream 应判为流式意图")
	}
	if !streamingIntent(mk(http.Header{"Content-Type": {"application/grpc"}})) {
		t.Error("gRPC 请求应判为流式意图")
	}
	if streamingIntent(mk(http.Header{"Accept": {"application/json"}})) {
		t.Error("普通请求不应判为流式意图")
	}
}

// ---- gRPC 出站请求保真 ----

func TestBuildOutboundGRPCRequestFaithful(t *testing.T) {
	// 客户端未带 User-Agent:出站应以空值哨兵阻止 net/http 注入 Go-http-client。
	req, _ := http.NewRequest("POST", "https://host/svc/Method", nil)
	req.Header.Set("Content-Type", "application/grpc+proto")
	req.Header.Set("TE", "trailers")
	req.Header.Set("Connection", "keep-alive") // 逐跳头,应被剔除
	f := buildStreamRequestFlow(req, flow.ProtoHTTPS)

	out, err := buildOutboundGRPCRequest(context.Background(), req, f)
	if err != nil {
		t.Fatal(err)
	}
	if out.Method != "POST" {
		t.Fatalf("method=%q", out.Method)
	}
	if got := out.Header.Get("Content-Type"); got != "application/grpc+proto" {
		t.Fatalf("Content-Type 未保真: %q", got)
	}
	if got := out.Header.Get("TE"); got != "trailers" {
		t.Fatalf("TE: trailers 应保留,得 %q", got)
	}
	if got := out.Header.Get("Connection"); got != "" {
		t.Fatalf("逐跳头 Connection 应被剔除,得 %q", got)
	}
	if ua, ok := out.Header["User-Agent"]; !ok || len(ua) != 1 || ua[0] != "" {
		t.Fatalf("未带 UA 时应置空值哨兵阻止注入,得 %v (ok=%v)", ua, ok)
	}

	// 客户端带了 User-Agent:出站应原样保留。
	req2, _ := http.NewRequest("POST", "https://host/svc/Method", nil)
	req2.Header.Set("Content-Type", "application/grpc")
	req2.Header.Set("User-Agent", "grpc-go/1.60.0")
	f2 := buildStreamRequestFlow(req2, flow.ProtoHTTPS)
	out2, err := buildOutboundGRPCRequest(context.Background(), req2, f2)
	if err != nil {
		t.Fatal(err)
	}
	if got := out2.Header.Get("User-Agent"); got != "grpc-go/1.60.0" {
		t.Fatalf("客户端 UA 应保真,得 %q", got)
	}
}

// ---- 中继(pump 级) ----

func TestPumpResponseStreamSSE(t *testing.T) {
	sink := &fakeStreamSink{}
	withStreamSink(t, sink)
	f := flow.New(flow.ProtoHTTP)
	f.Request = &flow.Request{URL: "http://x/sse", Method: "GET"}
	rec := newStreamRecorder(f, flow.StreamSSE)

	body := bytes.NewReader([]byte("data: one\n\nevent: e\ndata: two\n\n"))
	sw := &captureStreamWriter{}
	if err := pumpResponseStream(silentServer{}, rec, "http://x/sse", flow.StreamSSE, body, sw); err != nil {
		t.Fatal(err)
	}
	rec.close()

	if got := string(sw.body()); got != "data: one\n\nevent: e\ndata: two\n\n" {
		t.Fatalf("客户端收到的字节不保真: %q", got)
	}
	ss := sink.snapshot()
	if ss == nil || len(ss.Messages) != 2 {
		t.Fatalf("应记录 2 条消息,得 %v", ss)
	}
	if string(ss.Messages[0].Data) != "one" || ss.Messages[1].EventType != "e" || string(ss.Messages[1].Data) != "two" {
		t.Fatalf("消息解析错误: %+v", ss.Messages)
	}
}

func TestPumpResponseStreamChunk(t *testing.T) {
	body := bytes.NewReader([]byte(`{"a":1}` + "\n" + `{"b":2}` + "\n"))
	sw := &captureStreamWriter{}
	if err := pumpResponseStream(silentServer{}, nil, "u", flow.StreamChunk, body, sw); err != nil {
		t.Fatal(err)
	}
	if got := string(sw.body()); got != `{"a":1}`+"\n"+`{"b":2}`+"\n" {
		t.Fatalf("chunk 透传不保真: %q", got)
	}
}

func TestPumpResponseStreamSSEAbort(t *testing.T) {
	p := pipeline.New(nil, nil)
	p.Register(&testStreamHook{fn: func(m *flow.StreamMessage) flow.Decision {
		if string(m.Data) == "two" {
			return flow.AbortDecision(0, "blocked")
		}
		return flow.ContinueDecision()
	}})
	withPipeline(t, p)

	body := bytes.NewReader([]byte("data: one\n\ndata: two\n\ndata: three\n\n"))
	sw := &captureStreamWriter{}
	err := pumpResponseStream(silentServer{}, nil, "u", flow.StreamSSE, body, sw)
	if err == nil {
		t.Fatal("abort 应返回错误")
	}
	if got := string(sw.body()); got != "data: one\n\n" {
		t.Fatalf("abort 后只应写出第一个事件,得 %q", got)
	}
}

func TestPumpResponseStreamSSEModify(t *testing.T) {
	p := pipeline.New(nil, nil)
	p.Register(&testStreamHook{fn: func(m *flow.StreamMessage) flow.Decision {
		m.Data = []byte("REDACTED")
		return flow.ContinueDecision()
	}})
	withPipeline(t, p)

	body := bytes.NewReader([]byte("event: x\ndata: secret\n\n"))
	sw := &captureStreamWriter{}
	if err := pumpResponseStream(silentServer{}, nil, "u", flow.StreamSSE, body, sw); err != nil {
		t.Fatal(err)
	}
	if got := string(sw.body()); got != "event: x\ndata: REDACTED\n\n" {
		t.Fatalf("改写后应重建 SSE 事件,得 %q", got)
	}
}

func TestPumpResponseStreamGRPCModify(t *testing.T) {
	p := pipeline.New(nil, nil)
	p.Register(&testStreamHook{fn: func(m *flow.StreamMessage) flow.Decision {
		m.Data = []byte("XX")
		return flow.ContinueDecision()
	}})
	withPipeline(t, p)

	body := bytes.NewReader(grpcFrameBytes([]byte("orig"), false))
	sw := &captureStreamWriter{}
	if err := pumpResponseStream(silentServer{}, nil, "u", flow.StreamGRPC, body, sw); err != nil {
		t.Fatal(err)
	}
	want := grpcFrameBytes([]byte("XX"), false)
	if !bytes.Equal(sw.body(), want) {
		t.Fatalf("改写后应重建 gRPC 帧: got=%v want=%v", sw.body(), want)
	}
}

func TestGRPCCompressedFrameNotModified(t *testing.T) {
	p := pipeline.New(nil, nil)
	p.Register(&testStreamHook{fn: func(m *flow.StreamMessage) flow.Decision {
		m.Data = []byte("XX") // 试图改写压缩帧
		return flow.ContinueDecision()
	}})
	withPipeline(t, p)

	orig := grpcFrameBytes([]byte("compressed"), true)
	body := bytes.NewReader(orig)
	sw := &captureStreamWriter{}
	if err := pumpResponseStream(silentServer{}, nil, "u", flow.StreamGRPC, body, sw); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(sw.body(), orig) {
		t.Fatalf("压缩帧不应被改写,应原样透传")
	}
}

// ---- 集成:SSE 增量中继(证明不缓冲) ----

func TestRunResponseStreamSSEIncremental(t *testing.T) {
	sink := &fakeStreamSink{}
	withStreamSink(t, sink)

	released := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl := w.(http.Flusher)
		io.WriteString(w, "data: one\n\n")
		fl.Flush()
		<-released // 仅在测试确认收到事件一后,才发事件二 —— 缓冲实现会在此死锁
		io.WriteString(w, "event: two\ndata: 2\n\n")
		fl.Flush()
	}))
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	f := flow.New(flow.ProtoHTTP)
	f.Request = &flow.Request{URL: srv.URL, Method: "GET"}

	sw := &captureStreamWriter{ch: make(chan []byte, 8)}
	resp2 := &fakeResponder{sw: sw}

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = runResponseStream(silentServer{}, f, flow.StreamSSE, resp, resp.Request, resp2, sw)
	}()

	// 事件一应在事件二发送之前就到达客户端。
	select {
	case chunk := <-sw.ch:
		if string(chunk) != "data: one\n\n" {
			t.Fatalf("首个增量应为事件一,得 %q", chunk)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("超时:未实时收到事件一(疑似缓冲)")
	}
	close(released)
	select {
	case chunk := <-sw.ch:
		if string(chunk) != "event: two\ndata: 2\n\n" {
			t.Fatalf("第二个增量应为事件二,得 %q", chunk)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("超时:未收到事件二")
	}
	<-done

	ss := sink.snapshot()
	if ss == nil || ss.Status != "closed" || ss.MessageCount != 2 {
		t.Fatalf("会话应有 2 条消息且已关闭,得 %+v", ss)
	}
}

// ---- 集成:带 Content-Encoding 的 SSE 流应被解码 ----

func TestRunResponseStreamSSEGzip(t *testing.T) {
	sink := &fakeStreamSink{}
	withStreamSink(t, sink)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Content-Encoding", "gzip")
		w.WriteHeader(http.StatusOK)
		gz := gzip.NewWriter(w)
		_, _ = io.WriteString(gz, "data: hello\n\ndata: world\n\n")
		_ = gz.Close()
	}))
	defer srv.Close()

	// 关键:用 DisableCompression 的客户端(与代理上游一致),使响应保留 gzip 而不被自动解压。
	client := &http.Client{Transport: &http.Transport{DisableCompression: true}}
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("测试前提:响应应带 Content-Encoding: gzip,得 %q", resp.Header.Get("Content-Encoding"))
	}

	f := flow.New(flow.ProtoHTTP)
	f.Request = &flow.Request{URL: srv.URL, Method: "GET"}
	sw := &captureStreamWriter{}
	if err := runResponseStream(silentServer{}, f, flow.StreamSSE, resp, resp.Request, &fakeResponder{sw: sw}, sw); err != nil {
		t.Fatal(err)
	}

	// 客户端应收到解码后的 SSE 事件,且响应头不再带 Content-Encoding。
	if got := string(sw.body()); got != "data: hello\n\ndata: world\n\n" {
		t.Fatalf("gzip 流未被解码: %q", got)
	}
	if ce := sw.head.Get("Content-Encoding"); ce != "" {
		t.Fatalf("解码后应删除 Content-Encoding 头,得 %q", ce)
	}
	if ss := sink.snapshot(); ss == nil || ss.MessageCount != 2 {
		t.Fatalf("应记录 2 条消息,得 %+v", ss)
	}
}

// ---- 集成:gRPC 双向流端到端 ----

func TestRunGRPCStreamEndToEnd(t *testing.T) {
	sink := &fakeStreamSink{}
	withStreamSink(t, sink)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/grpc")
		w.Header().Set("Trailer", "Grpc-Status")
		w.WriteHeader(http.StatusOK)
		fl := w.(http.Flusher)
		body, _ := io.ReadAll(r.Body) // 收齐客户端请求帧
		_, _ = w.Write(grpcFrameBytes([]byte("server-1"), false))
		fl.Flush()
		_, _ = w.Write(grpcFrameBytes([]byte(fmt.Sprintf("got-%d", len(body))), false))
		fl.Flush()
		w.Header().Set(http.TrailerPrefix+"Grpc-Status", "0")
	}))
	defer srv.Close()

	prev := sharedStreamClient
	sharedStreamClient = streamClientFrom(srv.Client())
	t.Cleanup(func() { sharedStreamClient = prev })

	reqBody := grpcFrameBytes([]byte("client-msg"), false)
	req, err := http.NewRequest("POST", srv.URL+"/svc/Method", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/grpc")

	sw := &captureStreamWriter{}
	resp := &fakeResponder{sw: sw}
	if err := runGRPCStream(silentServer{}, req, flow.ProtoHTTP, nil, nil, resp, sw); err != nil {
		t.Fatal(err)
	}

	// 客户端应收到两条服务端帧。
	sc := &grpcScanner{}
	frames := sc.push(sw.body())
	if len(frames) != 2 {
		t.Fatalf("期望 2 条服务端帧,得 %d (body=%v)", len(frames), sw.body())
	}
	if string(frames[0].Payload) != "server-1" {
		t.Fatalf("帧1=%q", frames[0].Payload)
	}
	if string(frames[1].Payload) != fmt.Sprintf("got-%d", len(reqBody)) {
		t.Fatalf("帧2=%q(应回显请求体长度 %d)", frames[1].Payload, len(reqBody))
	}
	// 尾部应被回填。
	if sw.trailer.Get("Grpc-Status") != "0" {
		t.Fatalf("应回填 grpc-status 尾部,得 %v", sw.trailer)
	}
	// 会话应记录:1 条客户端消息 + 2 条服务端消息。
	ss := sink.snapshot()
	if ss == nil || ss.MessageCount != 3 {
		t.Fatalf("会话应记录 3 条消息,得 %+v", ss)
	}
	var cli, srvCnt int
	for _, m := range ss.Messages {
		if m.Direction == flow.WSClientToServer {
			cli++
		} else {
			srvCnt++
		}
	}
	if cli != 1 || srvCnt != 2 {
		t.Fatalf("方向计数错误: client=%d server=%d", cli, srvCnt)
	}
}

// 确保 silentServer 满足接口。
var _ types.Server = silentServer{}
var _ net.Addr = (*net.TCPAddr)(nil)
