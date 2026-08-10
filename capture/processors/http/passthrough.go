// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package http

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mintfog/sniffy/capture/types"
	"github.com/mintfog/sniffy/internal/bodycache"
	"github.com/mintfog/sniffy/internal/flow"
)

// 本文件实现「大体积 / 媒体响应」的透传旁路:响应头写回后即 io.Copy 到客户端,同时
// tee 一份到 bodycache,body 不进 Flow,详情页的预览与「另存为」按需读盘。
//
// 约束:走旁路的响应上插件只能改头、拿不到完整 body,与 SSE / gRPC 一致。

// DefaultPassthroughThreshold 是「按大小」触发旁路的默认阈值。
const DefaultPassthroughThreshold int64 = 2 << 20 // 2MiB

var (
	passthroughEnabled   atomic.Bool
	passthroughThreshold atomic.Int64
	bodyCache            atomic.Pointer[bodycache.Cache]
)

func init() {
	passthroughEnabled.Store(true)
	passthroughThreshold.Store(DefaultPassthroughThreshold)
}

// SetPassthrough 下发透传旁路的开关与大小阈值(bytes<=0 时取默认阈值)。
// 运行时即时生效、并发安全;关闭后新响应回到缓冲路径。
func SetPassthrough(enabled bool, thresholdBytes int64) {
	if thresholdBytes <= 0 {
		thresholdBytes = DefaultPassthroughThreshold
	}
	passthroughEnabled.Store(enabled)
	passthroughThreshold.Store(thresholdBytes)
}

// SetBodyCache 注入落盘缓存(装配层调用)。为 nil 时旁路照常生效,只是不留副本。
func SetBodyCache(c *bodycache.Cache) { bodyCache.Store(c) }

// mediaContentTypes 是无视大小阈值、一律走旁路的响应类型前缀 / 全名。
// 不含 m3u8 / mpd 等播放列表:它们是小体积文本,留在内存里才能在详情页直接看。
var mediaContentTypes = []string{"video/", "audio/", "application/octet-stream", "application/mp4"}

func isMediaContentType(ct string) bool {
	b := contentTypeBase(ct)
	for _, m := range mediaContentTypes {
		if b == m || (strings.HasSuffix(m, "/") && strings.HasPrefix(b, m)) {
			return true
		}
	}
	return false
}

// shouldPassthroughResponse 判断该响应是否应绕过缓冲、走增量转发。
func shouldPassthroughResponse(req *http.Request, resp *http.Response) bool {
	if !passthroughEnabled.Load() {
		return false
	}
	// HEAD 的响应带 Content-Length 却无 body:走旁路会写出一个永远填不满的长度,客户端干等。
	if req != nil && req.Method == http.MethodHead {
		return false
	}
	// Flow.Body 的契约是 identity 字节,线上要发的却是编码后的字节,单次拷贝无法同时满足
	// 两者,故带 Content-Encoding 的响应一律交回缓冲路径。
	if resp.Header.Get("Content-Encoding") != "" {
		return false
	}
	if bodylessResponseStatus(resp.StatusCode) {
		return false
	}
	if isMediaContentType(resp.Header.Get("Content-Type")) {
		return true
	}
	// 206 基本只出现在拖进度条 / 分片下载上,按大体积处理。
	if resp.StatusCode == http.StatusPartialContent {
		return true
	}
	// ContentLength 为 -1(chunked / 未知长度)时不据大小判定,交回缓冲路径。
	return resp.ContentLength >= passthroughThreshold.Load()
}

// bodylessResponseStatus 报告该状态码按 RFC 不携带 body(旁路对其无意义)。
func bodylessResponseStatus(status int) bool {
	return status == http.StatusNoContent || status == http.StatusNotModified ||
		(status >= 100 && status < 200)
}

// largeBodyIntent 据请求头预判该请求会取回大体积内容:命中 Range 或 Accept 媒体类型。
// 调用方据此改用无总超时的上游客户端,绕开默认客户端 10 分钟的总超时(含读 body)。
func largeBodyIntent(req *http.Request) bool {
	if req.Header.Get("Range") != "" {
		return true
	}
	accept := strings.ToLower(req.Header.Get("Accept"))
	return strings.Contains(accept, "video/") || strings.Contains(accept, "audio/")
}

// ============================ 写入器 ============================

// bodyStreamer 把响应头与响应体增量写回客户端,并保留上游的帧头形态:长度已知时沿用
// Content-Length,未知时才改 chunked。同包的 streamWriter 服务于 SSE / gRPC,一律改
// 写成 chunked,两者不可互换。
type bodyStreamer interface {
	io.Writer
	// writeHead 写回响应头。contentLength<0 表示上游未给出长度。
	writeHead(statusLine string, status int, header http.Header, rawHead [][2]string, contentLength int64) error
	setTrailer(h http.Header)
	close() error
}

// passthroughRespHeader 裁剪响应头并让帧头自洽:去逐跳头后,长度已知则写 Content-Length,
// 未知则改 chunked(Go 客户端已脱 chunk,须对客户端重新分块)。
func passthroughRespHeader(h http.Header, contentLength int64) http.Header {
	out := flow.ToHTTPHeader(flow.FromHTTPHeader(h))
	flow.StripHopByHop(out) // 含 Transfer-Encoding,故 chunked 要在其后补
	if contentLength >= 0 {
		out.Set("Content-Length", strconv.FormatInt(contentLength, 10))
	} else {
		out.Del("Content-Length")
		out.Set("Transfer-Encoding", "chunked")
	}
	return out
}

// --- HTTP/1.x:裸连接写入器 ---

type connBodyStreamer struct {
	conn net.Conn
	bw   *bufio.Writer
	// writeTimeout 是单次写出的停滞期限,每块前续期。大文件可能传很久,故不能用一个
	// 覆盖整次传输的绝对期限;但不设期限,停止读取的客户端会把本 goroutine 永久挂住。
	writeTimeout time.Duration
	chunked      bool
	chunkBuf     bytes.Buffer
}

func newConnBodyStreamer(conn net.Conn, writeTimeout time.Duration) *connBodyStreamer {
	return &connBodyStreamer{conn: conn, bw: bufio.NewWriterSize(conn, 64*1024), writeTimeout: writeTimeout}
}

// armWrite 把写期限续到 now+writeTimeout(未配置期限时不设)。
func (w *connBodyStreamer) armWrite() {
	if w.writeTimeout > 0 {
		_ = w.conn.SetWriteDeadline(time.Now().Add(w.writeTimeout))
	}
}

func (w *connBodyStreamer) writeHead(statusLine string, status int, header http.Header, rawHead [][2]string, contentLength int64) error {
	// 大体积传输可能远超握手期设置的绝对超时,清掉它,改由每块续期的写期限兜底。
	_ = w.conn.SetDeadline(time.Time{})
	w.armWrite()
	w.chunked = contentLength < 0

	hdr := passthroughRespHeader(header, contentLength)
	if statusLine == "" {
		statusLine = fmt.Sprintf("HTTP/1.1 %d %s", status, http.StatusText(status))
	}
	var b bytes.Buffer
	b.WriteString(statusLine)
	b.WriteString("\r\n")
	if len(rawHead) > 0 {
		// 保真:沿用上游原始头顺序/大小写,仅替换/补齐帧头。
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

func (w *connBodyStreamer) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	w.armWrite()
	if !w.chunked {
		if _, err := w.bw.Write(p); err != nil {
			return 0, err
		}
		return len(p), w.bw.Flush()
	}
	// 一个 chunk 的「长度行 + 数据 + CRLF」整体写出,避免中途出错残留半截 chunk。
	fmt.Fprintf(&w.chunkBuf, "%x\r\n", len(p))
	w.chunkBuf.Write(p)
	w.chunkBuf.WriteString("\r\n")
	_, err := w.bw.Write(w.chunkBuf.Bytes())
	w.chunkBuf.Reset()
	if err != nil {
		return 0, err
	}
	return len(p), w.bw.Flush()
}

func (w *connBodyStreamer) setTrailer(http.Header) {} // h1 chunked trailer 罕见,从略

func (w *connBodyStreamer) close() error {
	w.armWrite()
	if w.chunked {
		if _, err := w.bw.WriteString("0\r\n\r\n"); err != nil {
			return err
		}
	}
	return w.bw.Flush()
}

// --- HTTP/2:ResponseWriter 写入器 ---

type h2BodyStreamer struct {
	w http.ResponseWriter
}

func newH2BodyStreamer(w http.ResponseWriter) *h2BodyStreamer {
	return &h2BodyStreamer{w: w}
}

func (w *h2BodyStreamer) writeHead(_ string, status int, header http.Header, _ [][2]string, contentLength int64) error {
	dst := w.w.Header()
	for k, vs := range passthroughRespHeader(header, contentLength) {
		dst[k] = append([]string(nil), vs...)
	}
	// h2 无 chunked 概念,分帧由框架负责。
	dst.Del("Transfer-Encoding")
	w.w.WriteHeader(status)
	return http.NewResponseController(w.w).Flush()
}

func (w *h2BodyStreamer) Write(p []byte) (int, error) {
	n, err := w.w.Write(p)
	if err != nil {
		return n, err
	}
	return n, http.NewResponseController(w.w).Flush()
}

func (w *h2BodyStreamer) setTrailer(h http.Header) {
	dst := w.w.Header()
	for k, vs := range h {
		for _, v := range vs {
			dst.Add(http.TrailerPrefix+k, v)
		}
	}
}

func (w *h2BodyStreamer) close() error { return nil } // h2 由框架在 handler 返回时收尾

// ============================ 编排 ============================

// copyBufPool 复用 64KiB 拷贝缓冲:转发是每请求热路径,不为每条大响应新分配一块。
var copyBufPool = sync.Pool{New: func() any {
	b := make([]byte, 64*1024)
	return &b
}}

// runPassthroughResponse 接管「大体积 / 媒体响应」:写回响应头后即增量转发 body,
// 同时 tee 一份到 bodycache 供详情页按需读取。f 已 RecordFlowStarted 且请求插件已应用。
func runPassthroughResponse(server types.Server, f *flow.Flow, resp *http.Response, request *http.Request, r clientResponder, w bodyStreamer) error {
	f.Timing.ResponseAt = time.Now()
	f.Response = &flow.Response{
		Status:     resp.StatusCode,
		StatusText: resp.Status,
		Header:     flow.FromHTTPHeader(resp.Header),
	}
	f.Metadata["passthrough"] = true
	f.State = flow.StateCompleted

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

	if err := w.writeHead(statusLine, f.Response.Status, flow.ToHTTPHeader(f.Response.Header), rawHead, resp.ContentLength); err != nil {
		f.State = flow.StateErrored
		f.Error = err.Error()
		r.disableReuse()
		finishFlow(f)
		return err
	}

	entry := bodyCache.Load().Create(f.ID)
	buf := copyBufPool.Get().(*[]byte)
	// entry 的 Write 永不返错(落盘问题不该打断转发),故 MultiWriter 只会因客户端断开而中止。
	n, cerr := io.CopyBuffer(io.MultiWriter(w, entry), resp.Body, *buf)
	copyBufPool.Put(buf)

	if cerr != nil {
		entry.Abort()
		server.LogDebug("透传转发中断(已转发 %d 字节): %v", n, cerr)
		f.State = flow.StateErrored
		f.Error = cerr.Error()
		// 响应头已按上游长度写出,body 却短了一截。此时唯一能让客户端察觉截断的方式
		// 就是关闭连接;继续复用只会让它干等到空闲超时。
		r.disableReuse()
	} else {
		path, size := entry.Commit()
		if path == "" {
			size = n // 未落盘:大小仍然可信,只是详情页取不到完整字节
		}
		f.Response.SetPassthroughBody(path, size)
	}

	// 只有正常读尽 body 才能发送尾部与最终 chunk。失败时写正常终止帧会掩盖截断，
	// 并让 H1 客户端把流水线里的下一条响应误当成本响应的数据。
	if cerr == nil {
		if len(resp.Trailer) > 0 {
			w.setTrailer(resp.Trailer)
			f.Response.Trailer = flow.FromHTTPHeader(resp.Trailer)
		}
		cerr = w.close()
		if cerr != nil {
			f.State = flow.StateErrored
			f.Error = cerr.Error()
			r.disableReuse()
		}
	}

	f.Timing.DurationMs = time.Since(f.Timing.RequestAt).Milliseconds()
	finishFlow(f)
	return cerr
}
