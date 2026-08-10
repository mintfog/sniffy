// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package http

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mintfog/sniffy/internal/bodycache"
	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/internal/pipeline"
)

type failingCloseBodyStreamer struct {
	bytes.Buffer
	closeErr error
	closed   bool
}

func (w *failingCloseBodyStreamer) writeHead(string, int, http.Header, [][2]string, int64) error {
	return nil
}
func (w *failingCloseBodyStreamer) setTrailer(http.Header) {}
func (w *failingCloseBodyStreamer) close() error {
	w.closed = true
	return w.closeErr
}

func TestPassthroughFinalChunkErrorPropagates(t *testing.T) {
	wantErr := errors.New("final chunk failed")
	req, _ := http.NewRequest(http.MethodGet, "http://x/video", nil)
	resp := &http.Response{
		StatusCode:    http.StatusOK,
		Status:        "200 OK",
		Header:        http.Header{"Content-Type": {"video/mp4"}},
		Body:          io.NopCloser(strings.NewReader("video")),
		ContentLength: -1,
		Request:       req,
	}
	f := flow.New(flow.ProtoHTTP)
	f.Request = &flow.Request{URL: req.URL.String(), Method: http.MethodGet}
	w := &failingCloseBodyStreamer{closeErr: wantErr}
	r := &fakeResponder{}

	err := runPassthroughResponse(silentServer{}, f, resp, req, r, w)
	if !errors.Is(err, wantErr) {
		t.Fatalf("透传最终 chunk 写错误未传播: %v", err)
	}
	if f.State != flow.StateErrored || !r.reuseDisabled {
		t.Fatalf("透传收尾失败应标记 errored 并禁用复用: state=%s disabled=%v", f.State, r.reuseDisabled)
	}
}

// timedConn 是记录「首次写入时刻」的连接:透传旁路的意义就在于首字节不再等到上游传完。
type timedConn struct {
	readBuf *bytes.Buffer

	mu      sync.Mutex
	written bytes.Buffer
	firstAt time.Time
	start   time.Time
	closed  bool
}

func newTimedConn(req string) *timedConn {
	return &timedConn{readBuf: bytes.NewBufferString(req), start: time.Now()}
}

func (c *timedConn) Read(b []byte) (int, error) {
	if c.closed {
		return 0, io.EOF
	}
	return c.readBuf.Read(b)
}

func (c *timedConn) Write(b []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.firstAt.IsZero() {
		c.firstAt = time.Now()
	}
	return c.written.Write(b)
}

func (c *timedConn) Close() error { c.closed = true; return nil }
func (c *timedConn) LocalAddr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8080}
}
func (c *timedConn) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 9090}
}
func (c *timedConn) SetDeadline(time.Time) error      { return nil }
func (c *timedConn) SetReadDeadline(time.Time) error  { return nil }
func (c *timedConn) SetWriteDeadline(time.Time) error { return nil }

func (c *timedConn) ttfb() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.firstAt.Sub(c.start)
}

func (c *timedConn) bytes() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.written.Bytes()...)
}

// runThroughProxy 让一条 h1 请求走完整处理器,返回写回客户端的原始字节与记录到的 Flow。
func runThroughProxy(t *testing.T, rawReq string) (*timedConn, *collectingSink) {
	t.Helper()
	sink := &collectingSink{}
	defer func(p *pipeline.Pipeline, s FlowSink) { activePipeline, flowSink = p, s }(activePipeline, flowSink)
	activePipeline = pipeline.New(nil, nil)
	flowSink = sink

	tc := newTimedConn(rawReq)
	srv := newMockServer()
	conn := &mockConnection{conn: tc, reader: bufio.NewReader(tc), writer: bufio.NewWriter(tc), server: srv}
	p := New(conn).(*Processor)
	if err := p.handleHttpProtocol(srv, conn.GetReader(), conn.GetWriter()); err != nil {
		t.Fatalf("handleHttpProtocol: %v", err)
	}
	return tc, sink
}

// slowOrigin 起一个按片发送、片间有间隔的上游,用于观察首字节是否被代理攒住。
func slowOrigin(t *testing.T, contentType string, chunk []byte, chunks int, gap time.Duration, declareLength bool) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		if declareLength {
			w.Header().Set("Content-Length", strconv.Itoa(len(chunk)*chunks))
		}
		w.WriteHeader(http.StatusOK)
		f, _ := w.(http.Flusher)
		for i := 0; i < chunks; i++ {
			_, _ = w.Write(chunk)
			if f != nil {
				f.Flush()
			}
			time.Sleep(gap)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

// splitHead 把写回客户端的字节切成头块与 body。
func splitHead(raw []byte) (string, []byte) {
	if i := bytes.Index(raw, []byte("\r\n\r\n")); i >= 0 {
		return string(raw[:i]), raw[i+4:]
	}
	return string(raw), nil
}

// TestPassthroughStreamsVideo 锁定核心行为:视频响应的首字节不再等到上游传完,
// 字节完整无损,且 body 不进 Flow 而是落到缓存文件。
func TestPassthroughStreamsVideo(t *testing.T) {
	const (
		chunks = 16
		gap    = 8 * time.Millisecond
	)
	chunk := bytes.Repeat([]byte{0xDE, 0xAD, 0xBE, 0xEF}, 4096) // 16KiB/片,共 256KiB
	origin := slowOrigin(t, "video/mp4", chunk, chunks, gap, true)

	cache, err := bodycache.New(t.TempDir(), 0)
	if err != nil {
		t.Fatalf("bodycache.New: %v", err)
	}
	defer func(prev *bodycache.Cache) { SetBodyCache(prev) }(bodyCache.Load())
	SetBodyCache(cache)

	req := "GET " + origin.URL + "/v.mp4 HTTP/1.1\r\nHost: " + origin.Listener.Addr().String() + "\r\n\r\n"
	tc, sink := runThroughProxy(t, req)

	total := time.Duration(chunks) * gap
	if ttfb := tc.ttfb(); ttfb > total/2 {
		t.Fatalf("首字节被攒住了: TTFB=%v,上游总传输约 %v(应远小于它)", ttfb, total)
	}

	head, body := splitHead(tc.bytes())
	if !strings.Contains(head, "200") {
		t.Fatalf("状态行不对: %q", head)
	}
	want := bytes.Repeat(chunk, chunks)
	if !bytes.Equal(body, want) {
		t.Fatalf("转发字节不一致: want %d 字节, got %d 字节", len(want), len(body))
	}
	// 上游给了 Content-Length,就该原样保留而不是改成 chunked。
	if !strings.Contains(head, "Content-Length: "+strconv.Itoa(len(want))) {
		t.Fatalf("应保留上游 Content-Length,实际头块: %q", head)
	}
	if strings.Contains(strings.ToLower(head), "transfer-encoding") {
		t.Fatalf("长度已知时不应改 chunked: %q", head)
	}

	f := sink.last()
	if f == nil || f.Response == nil {
		t.Fatal("未记录完成的 Flow")
	}
	if len(f.Response.Body) != 0 {
		t.Fatalf("旁路下 body 不应进内存,实际 %d 字节", len(f.Response.Body))
	}
	if f.Response.BodyLen() != int64(len(want)) {
		t.Fatalf("BodyLen 不对: want %d, got %d", len(want), f.Response.BodyLen())
	}
	path, size := f.Response.BodyFile()
	if path == "" {
		t.Fatal("应留下落盘副本")
	}
	if size != int64(len(want)) {
		t.Fatalf("副本字节数不对: want %d, got %d", len(want), size)
	}
	spilled, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读副本失败: %v", err)
	}
	if !bytes.Equal(spilled, want) {
		t.Fatal("落盘副本与转发字节不一致")
	}
}

// TestPassthroughChunkedUpstream 上游未给长度(chunked)时,应对客户端重新分块而不是漏帧。
func TestPassthroughChunkedUpstream(t *testing.T) {
	chunk := bytes.Repeat([]byte{0x01, 0x02}, 2048)
	origin := slowOrigin(t, "video/mp4", chunk, 4, time.Millisecond, false)

	defer func(prev *bodycache.Cache) { SetBodyCache(prev) }(bodyCache.Load())
	SetBodyCache(nil) // 无缓存也应正常转发

	req := "GET " + origin.URL + "/v.mp4 HTTP/1.1\r\nHost: " + origin.Listener.Addr().String() + "\r\n\r\n"
	tc, sink := runThroughProxy(t, req)

	head, body := splitHead(tc.bytes())
	if !strings.Contains(strings.ToLower(head), "transfer-encoding: chunked") {
		t.Fatalf("长度未知时应改 chunked: %q", head)
	}
	// 按 chunked 解帧后应还原出原始字节。
	dechunked, err := io.ReadAll(httputil.NewChunkedReader(bytes.NewReader(body)))
	if err != nil {
		t.Fatalf("解 chunked 失败: %v", err)
	}
	want := bytes.Repeat(chunk, 4)
	if !bytes.Equal(dechunked, want) {
		t.Fatalf("解帧后字节不一致: want %d, got %d", len(want), len(dechunked))
	}

	f := sink.last()
	if f == nil || f.Response == nil {
		t.Fatal("未记录完成的 Flow")
	}
	// 未启用缓存:没有副本,但大小仍然可信。
	if p, _ := f.Response.BodyFile(); p != "" {
		t.Fatalf("未启用缓存时不该有副本路径: %q", p)
	}
	if f.Response.BodyLen() != int64(len(want)) {
		t.Fatalf("BodyLen 不对: want %d, got %d", len(want), f.Response.BodyLen())
	}
}

// TestPassthroughSkipsEncodedBody 带 Content-Encoding 的响应必须回到缓冲路径 ——
// Flow.Body 的契约是 identity 字节,旁路无法同时满足它与线上编码字节。
func TestPassthroughSkipsEncodedBody(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "http://x/v.mp4", nil)
	resp := &http.Response{
		StatusCode:    200,
		Header:        http.Header{"Content-Type": {"video/mp4"}, "Content-Encoding": {"gzip"}},
		ContentLength: 100 << 20,
	}
	if shouldPassthroughResponse(req, resp) {
		t.Fatal("带 Content-Encoding 的响应不应走旁路")
	}
	resp.Header.Del("Content-Encoding")
	if !shouldPassthroughResponse(req, resp) {
		t.Fatal("无编码的视频响应应走旁路")
	}
}

// TestPassthroughSkipsHeadAndBodyless HEAD 与无体状态码走旁路会写出填不满的长度。
func TestPassthroughSkipsHeadAndBodyless(t *testing.T) {
	head, _ := http.NewRequest(http.MethodHead, "http://x/v.mp4", nil)
	get, _ := http.NewRequest(http.MethodGet, "http://x/v.mp4", nil)
	video := http.Header{"Content-Type": {"video/mp4"}}

	if shouldPassthroughResponse(head, &http.Response{StatusCode: 200, Header: video, ContentLength: 1 << 30}) {
		t.Fatal("HEAD 的响应不应走旁路")
	}
	if shouldPassthroughResponse(get, &http.Response{StatusCode: 304, Header: video, ContentLength: -1}) {
		t.Fatal("304 不应走旁路")
	}
	if shouldPassthroughResponse(get, &http.Response{StatusCode: 204, Header: video, ContentLength: -1}) {
		t.Fatal("204 不应走旁路")
	}
}

// TestPassthroughThreshold 非媒体类型只按大小触发;长度未知时不猜,交回缓冲路径。
func TestPassthroughThreshold(t *testing.T) {
	defer SetPassthrough(true, DefaultPassthroughThreshold)
	SetPassthrough(true, 1<<20)

	get, _ := http.NewRequest(http.MethodGet, "http://x/a.json", nil)
	json := http.Header{"Content-Type": {"application/json"}}

	if shouldPassthroughResponse(get, &http.Response{StatusCode: 200, Header: json, ContentLength: 512 << 10}) {
		t.Fatal("小于阈值不应走旁路")
	}
	if !shouldPassthroughResponse(get, &http.Response{StatusCode: 200, Header: json, ContentLength: 4 << 20}) {
		t.Fatal("超过阈值应走旁路")
	}
	if shouldPassthroughResponse(get, &http.Response{StatusCode: 200, Header: json, ContentLength: -1}) {
		t.Fatal("长度未知的非媒体响应不应走旁路")
	}
	if shouldPassthroughResponse(get, &http.Response{StatusCode: 206, Header: json, ContentLength: 1024}) != true {
		t.Fatal("206 应按大体积处理")
	}

	SetPassthrough(false, 0)
	if shouldPassthroughResponse(get, &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"video/mp4"}}, ContentLength: 1 << 30}) {
		t.Fatal("开关关闭后不应走旁路")
	}
}
