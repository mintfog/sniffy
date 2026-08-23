// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package http

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/mintfog/sniffy/capture/types"
	"github.com/mintfog/sniffy/internal/flow"
)

// 本文件实现「双向流」(SSE / gRPC / 通用分块流)的无缓冲转发。
//
// 解决:检测到流后改走增量「中继」——逐消息读出、过插件钩子(OnStreamMessage)、记录到
// StreamSession(供 UI 实时展示),再立刻写回并 flush。仿照 WebSocket 子系统的帧级代理。

// ---- StreamSink:把流会话写入 service(消费者定义接口,避免反向依赖) ----

// StreamSink 由 service 实现,处理器经此记录/更新一条流会话。
// added 是本次新增的消息,仅元数据变化(建会话 / 记状态码 / 关闭)时为 nil。
type StreamSink interface {
	RecordStreamSession(ss *flow.StreamSession, added *flow.StreamMessage)
}

var streamSink StreamSink

// SetStreamSink 注入流会话接收器(装配层调用)。
func SetStreamSink(s StreamSink) { streamSink = s }

// errStreamAbort 表示插件 abort 了某条流消息,应提前终止该流。
var errStreamAbort = errors.New("stream aborted by plugin")

// grpcMaxMessage 限制单条 gRPC 消息的解析缓冲上限,超过则放弃逐消息观察、原样透传,
// 避免畸形长度前缀导致 OOM(正常 gRPC 消息远小于此)。
const grpcMaxMessage = 16 << 20

// ============================ 检测 ============================

// isGRPCContentType 判断 Content-Type 是否为 gRPC(application/grpc 及其子类型)。
func isGRPCContentType(ct string) bool {
	b := flow.ContentTypeBase(ct)
	return b == "application/grpc" || strings.HasPrefix(b, "application/grpc+") || strings.HasPrefix(b, "application/grpc-")
}

// grpcRequest 判断请求是否为 gRPC(据请求 Content-Type)。
func grpcRequest(h http.Header) bool { return isGRPCContentType(h.Get("Content-Type")) }

// detectResponseStream 据响应头判定是否为流式响应及其类型(空串表示非流)。
func detectResponseStream(resp *http.Response) string {
	ct := flow.ContentTypeBase(resp.Header.Get("Content-Type"))
	switch {
	case ct == "text/event-stream":
		return flow.StreamSSE
	case isGRPCContentType(ct):
		return flow.StreamGRPC
	case ct == "application/x-ndjson" || ct == "application/stream+json" || ct == "application/x-json-stream":
		return flow.StreamChunk
	}
	return ""
}

// streamingIntent 据请求头预判该请求可能产出/承载流(用于选用无总超时的上游客户端)。
// 命中 gRPC 或客户端显式 Accept: text/event-stream(EventSource 必带)。
func streamingIntent(req *http.Request) bool {
	if grpcRequest(req.Header) {
		return true
	}
	return strings.Contains(strings.ToLower(req.Header.Get("Accept")), "text/event-stream")
}

// ============================ 解析器 ============================

// grpcFrame 是一条 gRPC length-prefixed 帧:Raw 含 5 字节前缀,Payload 为去前缀的消息。
type grpcFrame struct {
	Raw        []byte
	Payload    []byte
	Compressed bool
}

// grpcScanner 增量解析 gRPC 帧流(1 字节压缩标志 + 4 字节大端长度 + 消息)。
type grpcScanner struct {
	buf      []byte
	overflow bool // 命中超大长度:放弃逐帧解析,转为原样透传
}

func (s *grpcScanner) push(p []byte) []grpcFrame {
	s.buf = append(s.buf, p...)
	if s.overflow {
		return nil
	}
	var out []grpcFrame
	for {
		if len(s.buf) < 5 {
			break
		}
		n := binary.BigEndian.Uint32(s.buf[1:5])
		if int(n) > grpcMaxMessage {
			s.overflow = true // 异常/超大:停止解析,后续 flush 原样透传
			break
		}
		total := 5 + int(n)
		if len(s.buf) < total {
			break
		}
		raw := append([]byte(nil), s.buf[:total]...)
		out = append(out, grpcFrame{Raw: raw, Payload: raw[5:], Compressed: raw[0] != 0})
		s.buf = s.buf[total:]
	}
	return out
}

func (s *grpcScanner) flush() []byte {
	b := s.buf
	s.buf = nil
	return b
}

// reframeGRPC 在插件改动了未压缩消息载荷时重建一帧(压缩帧不支持改写,调用方应回退原样)。
func reframeGRPC(payload []byte) []byte {
	out := make([]byte, 5+len(payload))
	out[0] = 0
	binary.BigEndian.PutUint32(out[1:5], uint32(len(payload)))
	copy(out[5:], payload)
	return out
}

// ============================ 会话记录器 ============================

// streamRecorder 维护一条 StreamSession,并在每次变化时向 streamSink 推送深拷贝快照。
// 双向 gRPC 下请求/响应两个方向的 goroutine 共享同一 recorder,故以 mu 串行化。
type streamRecorder struct {
	mu      sync.Mutex
	session *flow.StreamSession
	seq     int
}

// newStreamRecorder 登记一条 open 状态的流会话(ID == flow.ID)。streamSink 未注入时返回 nil。
func newStreamRecorder(f *flow.Flow, kind string) *streamRecorder {
	if streamSink == nil {
		return nil
	}
	url, method := "", ""
	if f.Request != nil {
		url = f.Request.URL
		method = f.Request.Method
	}
	r := &streamRecorder{session: &flow.StreamSession{
		ID:        f.ID,
		URL:       url,
		Kind:      kind,
		Method:    method,
		Status:    "open",
		StartTime: time.Now(),
		Messages:  make([]flow.StreamMessage, 0, 16),
	}}
	if p := f.Process(); p != nil {
		r.session.Process = p
	}
	r.push()
	return r
}

func (r *streamRecorder) nextSeq() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	n := r.seq
	r.seq++
	return n
}

// add 追加一条消息并推送更新。
func (r *streamRecorder) add(m *flow.StreamMessage) {
	if r == nil {
		return
	}
	r.mu.Lock()
	s := r.session
	cp := *m
	cp.Data, cp.Size = flow.RetainPayload(m.Data)
	s.MessageCount++
	s.TotalSize += cp.Size
	s.Messages = flow.TrimStreamMessages(append(s.Messages, cp))
	snap := r.snapshotLocked()
	r.mu.Unlock()
	streamSink.RecordStreamSession(snap, &cp)
}

// setStatus 记录响应状态码并推送。
func (r *streamRecorder) setStatus(code int) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.session.StatusCode = code
	snap := r.snapshotLocked()
	r.mu.Unlock()
	streamSink.RecordStreamSession(snap, nil)
}

// close 标记会话关闭并推送最终状态。
func (r *streamRecorder) close() {
	if r == nil {
		return
	}
	r.mu.Lock()
	now := time.Now()
	r.session.EndTime = &now
	r.session.Status = "closed"
	snap := r.snapshotLocked()
	r.mu.Unlock()
	streamSink.RecordStreamSession(snap, nil)
}

func (r *streamRecorder) push() {
	if r == nil || streamSink == nil {
		return
	}
	r.mu.Lock()
	snap := r.snapshotLocked()
	r.mu.Unlock()
	streamSink.RecordStreamSession(snap, nil)
}

func (r *streamRecorder) snapshotLocked() *flow.StreamSession {
	s := r.session
	cp := *s
	cp.Messages = make([]flow.StreamMessage, len(s.Messages))
	copy(cp.Messages, s.Messages)
	if s.Process != nil {
		p := *s.Process
		cp.Process = &p
	}
	if s.EndTime != nil {
		t := *s.EndTime
		cp.EndTime = &t
	}
	return &cp
}

// ============================ 客户端写入器 ============================

// streamWriter 把流式响应增量写回客户端。HTTP/1.x 用 chunked 写裸连接;HTTP/2 经
// ResponseWriter + Flush。约定:writeHead 仅调用一次,且先于任何 writeChunk。
type streamWriter interface {
	writeHead(statusLine string, status int, header http.Header, rawHead [][2]string) error
	writeChunk(p []byte) error
	setTrailer(h http.Header)
	close() error
}

// streamRespHeader 为流式响应裁剪响应头:去逐跳头与 Content-Length(改写为 chunked / h2 帧)。
// Content-Encoding 由调用方按是否做了流式解码自行决定保留/删除(见 flow.DecodeStreamBody)。
func streamRespHeader(h http.Header) http.Header {
	out := flow.ToHTTPHeader(flow.FromHTTPHeader(h))
	flow.StripHopByHop(out)
	out.Del("Content-Length")
	return out
}

// --- HTTP/1.x:chunked 裸连接写入器 ---

type connStreamWriter struct {
	conn net.Conn
	bw   *bufio.Writer
	// writeTimeout 是单条消息写出的停滞期限,每条前续期。流可以开很久,故不能用一个
	// 覆盖整条流的绝对期限;但不设期限,停止读取的客户端会把本 goroutine 永久挂住。
	writeTimeout time.Duration
	chunkBuf     bytes.Buffer // 复用的单 chunk 组装缓冲
}

func newConnStreamWriter(conn net.Conn, writeTimeout time.Duration) *connStreamWriter {
	return &connStreamWriter{conn: conn, bw: bufio.NewWriter(conn), writeTimeout: writeTimeout}
}

// armWrite 把写期限续到 now+writeTimeout(未配置期限时不设)。
func (w *connStreamWriter) armWrite() {
	if w.writeTimeout > 0 {
		_ = w.conn.SetWriteDeadline(time.Now().Add(w.writeTimeout))
	}
}

func (w *connStreamWriter) writeHead(statusLine string, status int, header http.Header, rawHead [][2]string) error {
	// 流式长连接:清除握手期设置的绝对超时,改由每条消息续期的写期限兜底。
	_ = w.conn.SetDeadline(time.Time{})
	w.armWrite()

	hdr := streamRespHeader(header)
	hdr.Set("Transfer-Encoding", "chunked") // Go 客户端已脱 chunk,这里对客户端重新分块

	if statusLine == "" {
		text := http.StatusText(status)
		statusLine = fmt.Sprintf("HTTP/1.1 %d %s", status, text)
	}
	var b bytes.Buffer
	b.WriteString(statusLine)
	b.WriteString("\r\n")
	if len(rawHead) > 0 {
		// 尽量保真:沿用上游原始头顺序/大小写,仅替换/补齐分块相关头。
		for _, kv := range reconcileStreamHead(rawHead, hdr) {
			b.WriteString(kv[0])
			b.WriteString(": ")
			b.WriteString(kv[1])
			b.WriteString("\r\n")
		}
	} else {
		_ = hdr.Write(&b)
	}
	b.WriteString("\r\n")
	if _, err := w.bw.Write(b.Bytes()); err != nil {
		return err
	}
	return w.bw.Flush()
}

func (w *connStreamWriter) writeChunk(p []byte) error {
	if len(p) == 0 {
		return nil
	}
	w.armWrite()
	// 一个 chunk 的「长度行 + 数据 + CRLF」整体写出,避免分多次 Write 在中途出错时残留半截 chunk。
	fmt.Fprintf(&w.chunkBuf, "%x\r\n", len(p))
	w.chunkBuf.Write(p)
	w.chunkBuf.WriteString("\r\n")
	_, err := w.bw.Write(w.chunkBuf.Bytes())
	w.chunkBuf.Reset()
	if err != nil {
		return err
	}
	return w.bw.Flush()
}

func (w *connStreamWriter) setTrailer(http.Header) {} // h1 chunked trailer 较罕见,从略

func (w *connStreamWriter) close() error {
	w.armWrite()
	if _, err := w.bw.WriteString("0\r\n\r\n"); err != nil {
		return err
	}
	return w.bw.Flush()
}

// reconcileStreamHead 用 hdr(已裁剪 + 含 chunked)回填到原始顺序,丢弃 hdr 里已无的头,
// hdr 多出的(如 Transfer-Encoding: chunked)追加在尾部。
func reconcileStreamHead(rawHead [][2]string, hdr http.Header) [][2]string {
	remaining := flow.FromHTTPHeader(hdr)
	out := make([][2]string, 0, len(rawHead)+1)
	for _, kv := range rawHead {
		ck := http.CanonicalHeaderKey(kv[0])
		q := remaining[ck]
		if len(q) == 0 {
			continue
		}
		out = append(out, [2]string{kv[0], q[0]})
		remaining[ck] = q[1:]
	}
	for ck, q := range remaining {
		for _, v := range q {
			out = append(out, [2]string{ck, v})
		}
	}
	return out
}

// --- HTTP/2:ResponseWriter 写入器 ---

type h2StreamWriter struct {
	w http.ResponseWriter
}

func newH2StreamWriter(w http.ResponseWriter) *h2StreamWriter {
	return &h2StreamWriter{w: w}
}

func (w *h2StreamWriter) writeHead(_ string, status int, header http.Header, _ [][2]string) error {
	dst := w.w.Header()
	for k, vs := range streamRespHeader(header) {
		dst[k] = append([]string(nil), vs...)
	}
	w.w.WriteHeader(status)
	return http.NewResponseController(w.w).Flush()
}

func (w *h2StreamWriter) writeChunk(p []byte) error {
	if len(p) == 0 {
		return nil
	}
	if _, err := w.w.Write(p); err != nil {
		return err
	}
	return http.NewResponseController(w.w).Flush()
}

func (w *h2StreamWriter) setTrailer(h http.Header) {
	dst := w.w.Header()
	for k, vs := range h {
		for _, v := range vs {
			dst.Add(http.TrailerPrefix+k, v)
		}
	}
}

func (w *h2StreamWriter) close() error { return nil } // h2 由框架在 handler 返回时收尾

// ============================ 中继引擎 ============================

// emitStreamMessage 过插件钩子 + 记录,返回应写到客户端的字节(raw 表未改动时的原样回放)。
// 插件 abort 时返回 errStreamAbort。
func emitStreamMessage(rec *streamRecorder, url, direction, kind, eventType string, payload, raw []byte) ([]byte, error) {
	out := raw
	data := payload
	seq := rec.nextSeq()
	if activePipeline != nil {
		m := &flow.StreamMessage{
			ID:        flow.NewID(),
			FlowID:    rec.flowID(),
			URL:       url,
			Direction: direction,
			Kind:      kind,
			EventType: eventType,
			Data:      append([]byte(nil), payload...),
			Timestamp: time.Now(),
			Seq:       seq,
		}
		d := activePipeline.OnStreamMessage(context.Background(), m)
		if d.Kind == flow.Abort {
			return nil, errStreamAbort
		}
		if !bytes.Equal(m.Data, payload) {
			// 插件改写了载荷:按类型重建线缆字节。
			switch kind {
			case flow.StreamSSE:
				out = flow.ReserializeSSE(eventType, m.Data)
			case flow.StreamGRPC:
				out = reframeGRPC(m.Data) // 注:压缩帧由调用方保证不传入改写路径
			default:
				out = m.Data
			}
		}
		data = m.Data
	}
	rec.add(&flow.StreamMessage{
		ID:        flow.NewID(),
		FlowID:    rec.flowID(),
		URL:       url,
		Direction: direction,
		Kind:      kind,
		EventType: eventType,
		Data:      data,
		Timestamp: time.Now(),
		Seq:       seq,
	})
	return out, nil
}

func (r *streamRecorder) flowID() string {
	if r == nil {
		return ""
	}
	return r.session.ID
}

// pumpResponseStream 单向中继上游响应体到客户端(SSE / chunk / gRPC 服务端方向)。
// 逐消息解析、过钩子、记录、写回并 flush。读尽后回填响应尾部。
func pumpResponseStream(server types.Server, rec *streamRecorder, url, kind string, body io.Reader, sw streamWriter) error {
	sse := &flow.SSEScanner{}
	grpc := &grpcScanner{}
	buf := make([]byte, 32*1024)
	for {
		n, rerr := body.Read(buf)
		if n > 0 {
			if err := dispatchChunk(rec, url, flow.WSServerToClient, kind, sse, grpc, buf[:n], sw); err != nil {
				return err
			}
		}
		if rerr != nil {
			// 把本次已读到但尚未组成完整消息的字节也交给客户端；若随后不是正常 EOF，
			// 调用方会关闭 H1 连接或复位 H2 stream，客户端仍能察觉响应不完整。
			if lo := leftover(kind, sse, grpc); len(lo) > 0 {
				if err := sw.writeChunk(lo); err != nil {
					return err
				}
			}
			if errors.Is(rerr, io.EOF) {
				return nil
			}
			server.LogDebug("流式响应读取结束: %v", rerr)
			return rerr
		}
	}
}

// dispatchChunk 把一段新读入的字节按 kind 切成消息并逐条 emit + 写回。
func dispatchChunk(rec *streamRecorder, url, direction, kind string, sse *flow.SSEScanner, grpc *grpcScanner, p []byte, sw streamWriter) error {
	switch kind {
	case flow.StreamSSE:
		for _, ev := range sse.Push(p) {
			out, err := emitStreamMessage(rec, url, direction, kind, ev.Event, ev.Data, ev.Raw)
			if err != nil {
				return err
			}
			if err := sw.writeChunk(out); err != nil {
				return err
			}
		}
		if sse.Overflowed() { // 超大事件:停止解析,原样透传剩余(与 gRPC 分支同一处置)
			if err := sw.writeChunk(sse.Flush()); err != nil {
				return err
			}
		}
	case flow.StreamGRPC:
		for _, fr := range grpc.push(p) {
			// 压缩帧不参与改写(避免破坏 protobuf/压缩),仍记录并尊重 abort。
			payload := fr.Payload
			raw := fr.Raw
			out, err := emitStreamMessageGRPC(rec, url, direction, fr, payload, raw)
			if err != nil {
				return err
			}
			if err := sw.writeChunk(out); err != nil {
				return err
			}
		}
		if grpc.overflow { // 超大消息:停止解析,原样透传剩余
			if err := sw.writeChunk(grpc.flush()); err != nil {
				return err
			}
		}
	default: // chunk:原样按读入粒度透传并记录
		out, err := emitStreamMessage(rec, url, direction, kind, "", p, p)
		if err != nil {
			return err
		}
		if err := sw.writeChunk(out); err != nil {
			return err
		}
	}
	return nil
}

// emitStreamMessageGRPC 处理一条 gRPC 帧:压缩帧仅观察(不改写),非压缩帧可被插件改写。
func emitStreamMessageGRPC(rec *streamRecorder, url, direction string, fr grpcFrame, payload, raw []byte) ([]byte, error) {
	if fr.Compressed {
		// 压缩帧:仅记录与 abort,不改写(避免破坏压缩消息)。
		seq := rec.nextSeq()
		if activePipeline != nil {
			hm := &flow.StreamMessage{
				ID: flow.NewID(), FlowID: rec.flowID(), URL: url, Direction: direction,
				Kind: flow.StreamGRPC, Data: append([]byte(nil), payload...), Timestamp: time.Now(), Seq: seq,
			}
			if d := activePipeline.OnStreamMessage(context.Background(), hm); d.Kind == flow.Abort {
				return nil, errStreamAbort
			}
		}
		rec.add(&flow.StreamMessage{
			ID: flow.NewID(), FlowID: rec.flowID(), URL: url, Direction: direction,
			Kind: flow.StreamGRPC, Data: payload, Timestamp: time.Now(), Seq: seq,
		})
		return raw, nil
	}
	return emitStreamMessage(rec, url, direction, flow.StreamGRPC, "", payload, raw)
}

// leftover 取扫描器结尾残留(EOF 时原样透传)。
func leftover(kind string, sse *flow.SSEScanner, grpc *grpcScanner) []byte {
	switch kind {
	case flow.StreamSSE:
		return sse.Flush()
	case flow.StreamGRPC:
		return grpc.flush()
	}
	return nil
}

// pumpGRPCFrames 把一段新读入字节切成 gRPC 帧并 emit + 写到 w(请求方向用)。
func pumpGRPCFrames(rec *streamRecorder, url, direction string, gc *grpcScanner, p []byte, w io.Writer) error {
	for _, fr := range gc.push(p) {
		out, err := emitStreamMessageGRPC(rec, url, direction, fr, fr.Payload, fr.Raw)
		if err != nil {
			return err
		}
		if _, werr := w.Write(out); werr != nil {
			return werr
		}
	}
	if gc.overflow {
		if _, werr := w.Write(gc.flush()); werr != nil {
			return werr
		}
	}
	return nil
}

// ============================ 编排 ============================

// runResponseStream 处理「响应是流」的情形(SSE / chunk / gRPC 服务端流):请求阶段已正常
// 走过 runFlowPipeline(请求体已缓冲,对这些场景无害),此处接管响应:头级响应插件 →
// 写回响应头 → 增量中继响应体。f 已 RecordFlowStarted 且 request 插件已应用。
func runResponseStream(server types.Server, f *flow.Flow, kind string, resp *http.Response, request *http.Request, r clientResponder, sw streamWriter) error {
	// 捕获响应头到 Flow(供 UI 的 HTTP 行展示);body 留空,逐条消息走 StreamSession。
	f.Timing.ResponseAt = time.Now()
	f.Response = &flow.Response{
		Status:     resp.StatusCode,
		StatusText: resp.Status,
		Header:     flow.FromHTTPHeader(resp.Header),
	}
	f.Metadata["stream"] = kind
	f.State = flow.StateCompleted

	// 上游客户端 DisableCompression,不会自动解压;若响应带 Content-Encoding 则流式解码,
	// 并据此删除/保留 Content-Encoding 头(decoded → 客户端收 identity 的 chunked 流)。
	bodyReader, ceConsumed := flow.DecodeStreamBody(resp)
	if ceConsumed {
		delete(f.Response.Header, "Content-Encoding")
	}

	var statusLine string
	var rawHead [][2]string
	if rc, ok := flow.ResponseCaptureFrom(request.Context()); ok && len(rc.Headers) > 0 {
		statusLine, rawHead = rc.StatusLine, rc.Headers
		f.Response.RawHeaders = rc.Headers
	}

	// 响应阶段插件(头部级:可改头 / abort;此时无完整 body)。
	if activePipeline != nil {
		if d := activePipeline.OnResponse(context.Background(), f); d.Kind == flow.Abort {
			f.State = flow.StateBlocked
			finishFlow(f)
			return r.writeAbort(d)
		}
	}

	if err := sw.writeHead(statusLine, f.Response.Status, flow.ToHTTPHeader(f.Response.Header), rawHead); err != nil {
		f.State = flow.StateErrored
		f.Error = err.Error()
		r.disableReuse()
		finishFlow(f)
		return err
	}

	rec := newStreamRecorder(f, kind)
	rec.setStatus(resp.StatusCode)
	url := ""
	if f.Request != nil {
		url = f.Request.URL
	}

	perr := pumpResponseStream(server, rec, url, kind, bodyReader, sw)
	if c, ok := bodyReader.(io.Closer); ok {
		_ = c.Close() // 释放解码器资源(zstd 解码器持有 goroutine);resp.Body 的二次关闭是安全的
	}
	// 只有正常读尽上游 body 才写尾部与终止帧；失败时正常收尾会掩盖截断。
	if perr == nil {
		if len(resp.Trailer) > 0 {
			sw.setTrailer(resp.Trailer)
			f.Response.Trailer = flow.FromHTTPHeader(resp.Trailer)
		}
		perr = sw.close()
	}
	rec.close()

	switch {
	case perr == nil:
		f.State = flow.StateCompleted
	case errors.Is(perr, errStreamAbort):
		f.State = flow.StateBlocked
		r.disableReuse()
	default:
		f.State = flow.StateErrored
		f.Error = perr.Error()
		r.disableReuse()
	}
	f.Timing.DurationMs = time.Since(f.Timing.RequestAt).Milliseconds()
	finishFlow(f)
	return perr
}

// runGRPCStream 处理 gRPC 双向流:在读取请求体之前接管,避免 io.ReadAll(req.Body) 死锁。
// 请求体经管道流式发往上游(逐帧过钩子/记录),响应体增量中继回客户端,两个方向并发。
func runGRPCStream(server types.Server, request *http.Request, protocol string, clientAddr, proxyAddr net.Addr, r clientResponder, sw streamWriter) error {
	f := buildStreamRequestFlow(request, protocol)
	if clientAddr != nil {
		f.Request.ClientIP = clientAddr.String()
	}
	f.Metadata["stream"] = flow.StreamGRPC
	if flowSink != nil {
		flowSink.RecordFlowStarted(f)
	}
	asyncResolveProcess(f, clientAddr, proxyAddr)

	// 请求阶段插件(头部级)。Mock 在 gRPC 下不支持,按继续处理。
	if activePipeline != nil {
		if d := activePipeline.OnRequest(context.Background(), f); d.Kind == flow.Abort {
			f.State = flow.StateBlocked
			finishFlow(f)
			return r.writeAbort(d)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rec := newStreamRecorder(f, flow.StreamGRPC)
	url := f.Request.URL

	pr, pw := io.Pipe()
	// 关闭管道读端会让阻塞在 pw.Write 的请求泵立即解除(任意提前返回路径都能回收泵 goroutine);
	// 泵若阻塞在 request.Body.Read,则随 h2 handler 返回时框架关闭请求体而解除。
	defer pr.Close()
	outReq, err := buildOutboundGRPCRequest(ctx, request, f)
	if err != nil {
		f.State = flow.StateErrored
		f.Error = err.Error()
		rec.close()
		finishFlow(f)
		return r.writeBadGateway()
	}
	outReq.Body = pr
	outReq.ContentLength = -1 // 流式 body,无 Content-Length

	// 请求泵:client->server 逐帧解析/钩子/记录,写入管道供上游 transport 发送。
	go func() {
		// 只有正常 EOF(客户端半关)才以 nil 收尾 —— 那等于告诉上游「请求流到此完整结束」。
		// 客户端读失败(连接被复位等)若也这么收尾,上游会把残缺的调用当成完整调用执行;
		// 带错关闭则让 transport 复位出站流。
		var closeErr error
		defer func() { _ = pw.CloseWithError(closeErr) }()
		gc := &grpcScanner{}
		buf := make([]byte, 32*1024)
		for {
			n, rerr := request.Body.Read(buf)
			if n > 0 {
				if perr := pumpGRPCFrames(rec, url, flow.WSClientToServer, gc, buf[:n], pw); perr != nil {
					closeErr = perr
					return
				}
			}
			if rerr != nil {
				if !errors.Is(rerr, io.EOF) {
					closeErr = rerr
					return
				}
				if lo := gc.flush(); len(lo) > 0 {
					_, _ = pw.Write(lo)
				}
				return // 客户端半关:请求流正常结束
			}
		}
	}()

	resp, err := sharedStreamClient.Do(outReq)
	if err != nil {
		cancel()
		server.LogError("gRPC 上游请求失败: %v", err)
		f.State = flow.StateErrored
		f.Error = err.Error()
		rec.close()
		finishFlow(f)
		return r.writeBadGateway()
	}
	defer resp.Body.Close()

	f.Timing.ResponseAt = time.Now()
	f.Response = &flow.Response{
		Status:     resp.StatusCode,
		StatusText: resp.Status,
		Header:     flow.FromHTTPHeader(resp.Header),
	}
	rec.setStatus(resp.StatusCode)

	if activePipeline != nil {
		if d := activePipeline.OnResponse(context.Background(), f); d.Kind == flow.Abort {
			cancel()
			f.State = flow.StateBlocked
			rec.close()
			finishFlow(f)
			return r.writeAbort(d)
		}
	}

	if err := sw.writeHead("", resp.StatusCode, flow.ToHTTPHeader(f.Response.Header), nil); err != nil {
		cancel()
		f.State = flow.StateErrored
		f.Error = err.Error()
		r.disableReuse()
		rec.close()
		finishFlow(f)
		return err
	}

	perr := pumpResponseStream(server, rec, url, flow.StreamGRPC, resp.Body, sw)
	if perr == nil {
		if len(resp.Trailer) > 0 {
			sw.setTrailer(resp.Trailer)
			f.Response.Trailer = flow.FromHTTPHeader(resp.Trailer)
		}
		perr = sw.close()
	}
	cancel() // 响应结束:停止请求泵与上游
	rec.close()

	switch {
	case perr == nil:
		f.State = flow.StateCompleted
	case errors.Is(perr, errStreamAbort):
		f.State = flow.StateBlocked
		r.disableReuse()
	default:
		f.State = flow.StateErrored
		f.Error = perr.Error()
		r.disableReuse()
	}
	f.Timing.DurationMs = time.Since(f.Timing.RequestAt).Milliseconds()
	finishFlow(f)
	return perr
}

// buildStreamRequestFlow 据请求头构造 Flow(不读 body),供流式请求(gRPC)使用。
func buildStreamRequestFlow(req *http.Request, protocol string) *flow.Flow {
	f := flow.New(protocol)
	f.Request = &flow.Request{
		Method:   req.Method,
		URL:      req.URL.String(),
		Host:     req.Host,
		Path:     req.URL.Path,
		Proto:    req.Proto,
		Header:   flow.FromHTTPHeader(req.Header),
		ClientIP: req.RemoteAddr,
	}
	return f
}

// buildOutboundGRPCRequest 据(可能被请求插件改过的)f.Request 构造出站 gRPC 请求。
// 不设 OrderedHeaders → forward.Transport 立即回退到标准 transport,经 h2 原生流式收发。
func buildOutboundGRPCRequest(ctx context.Context, request *http.Request, f *flow.Flow) (*http.Request, error) {
	u := *request.URL
	if u.Scheme == "" {
		u.Scheme = "https"
	}
	if u.Host == "" {
		u.Host = request.Host
	}
	// URL 可能已被规则 / 插件改写(redirect、modify_url)。不跟着改写走的话,出站请求的
	// :authority(取自 f.Request.Host)换了、TCP 却仍连向原站,重定向类规则对 gRPC 静默失效。
	target := u.String()
	if f.Request.URL != "" && f.Request.URL != request.URL.String() {
		if nu, err := u.Parse(f.Request.URL); err == nil {
			target = nu.String()
		}
	}
	out, err := http.NewRequestWithContext(ctx, f.Request.Method, target, nil)
	if err != nil {
		return nil, err
	}
	hdr := flow.ToHTTPHeader(f.Request.Header)
	// gRPC 要求 TE: trailers(h2 下 TE 唯一合法值)。TE 是逐跳头会被剔除,故先探测再补回。
	keepTE := headerHasToken(hdr, "TE", "trailers")
	flow.StripHopByHop(hdr)
	if keepTE {
		hdr.Set("TE", "trailers")
	}
	out.Header = hdr
	// 保真:客户端没带 User-Agent 时,用空值哨兵阻止 net/http 注入 Go-http-client
	//(置空键会让请求写出整行省略),与 ApplyRequestToHTTP 的回退路径一致。
	if out.Header.Get("User-Agent") == "" {
		out.Header["User-Agent"] = []string{""}
	}
	if f.Request.Host != "" {
		out.Host = f.Request.Host
	}
	return out, nil
}

// headerHasToken 报告 h[name] 是否(不区分大小写)含 token。
func headerHasToken(h http.Header, name, token string) bool {
	for _, v := range h.Values(name) {
		for _, part := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return true
			}
		}
	}
	return false
}
