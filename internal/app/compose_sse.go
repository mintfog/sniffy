// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/internal/service"
)

// 流式客户端没有总超时，普通响应体须单独限制读取时间。使用 var 供测试缩短等待。
var composeFallbackTimeout = 10 * time.Minute

var errComposeBodyTimeout = errors.New("响应体读取超时")

// SSE 没有读超时和总超时，须限制并发数以约束窗口异常关闭时的资源占用。
const maxComposeStreams = 16

// errComposeStreamAbort 将消息钩子的中止映射为 Flow 的 blocked 状态。
var errComposeStreamAbort = errors.New("插件中止了流")

// SSEScanner 的未成块缓冲超限时中止读取，限制内存占用。
var errComposeSSEOverflow = fmt.Errorf("上游单个 SSE 事件超过 %d 字节(或始终未发送空行),已中止", flow.MaxSSEEventBytes)

// composeStreamRegistry 在响应类型确定前就登记取消函数，保证关闭页签能取消等待中的请求。
type composeStreamRegistry struct {
	mu      sync.Mutex
	items   map[string]context.CancelFunc
	streams map[string]struct{} // 显式 SSE 请求及已按响应头识别的 SSE 共用名额
}

func (r *composeStreamRegistry) add(id string, cancel context.CancelFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.items == nil {
		r.items = make(map[string]context.CancelFunc)
	}
	r.items[id] = cancel
}

// reserveStream 为仍登记的请求预留 SSE 名额，重复调用复用已有名额。
func (r *composeStreamRegistry) reserveStream(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.items[id]; !ok {
		return context.Canceled
	}
	if _, ok := r.streams[id]; ok {
		return nil
	}
	if len(r.streams) >= maxComposeStreams {
		return fmt.Errorf("出站流数量已达上限 %d,请先停止一些流", maxComposeStreams)
	}
	if r.streams == nil {
		r.streams = make(map[string]struct{})
	}
	r.streams[id] = struct{}{}
	return nil
}

func (r *composeStreamRegistry) remove(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.items, id)
	delete(r.streams, id)
}

func (r *composeStreamRegistry) stop(id string) bool {
	r.mu.Lock()
	cancel, ok := r.items[id]
	delete(r.items, id)
	delete(r.streams, id)
	r.mu.Unlock()
	if !ok {
		return false
	}
	cancel()
	return true
}

func (r *composeStreamRegistry) stopAll() {
	r.mu.Lock()
	items := r.items
	r.items = nil
	r.streams = nil
	r.mu.Unlock()
	for _, cancel := range items {
		cancel()
	}
}

// StopStream 取消构造器请求，返回是否命中登记中的请求；返回时请求可能仍在收尾。
// 已进入 SSE 读取阶段的请求按 completed 收尾；响应头等待或普通正文读取被中断时记 errored，
// 普通响应中已读到的正文仍会保留。
func (a *App) StopStream(id string) bool { return a.outStreams.stop(id) }

// StopAllStreams 取消全部构造器请求，包括尚未收到响应头的请求。
func (a *App) StopAllStreams() { a.outStreams.stopAll() }

// composeStreamRecorder 向 service 交付快照，隔离消息列表的追加、裁剪与并发读取。
type composeStreamRecorder struct {
	svc     *service.Service
	mu      sync.Mutex
	session *flow.StreamSession
	seq     int
}

func newComposeStreamRecorder(svc *service.Service, f *flow.Flow, kind string) *composeStreamRecorder {
	if svc == nil {
		return nil
	}
	url, method := "", ""
	if f.Request != nil {
		url = f.Request.URL
		method = f.Request.Method
	}
	r := &composeStreamRecorder{
		svc: svc,
		session: &flow.StreamSession{
			ID:        f.ID,
			URL:       url,
			Kind:      kind,
			Method:    method,
			Status:    "open",
			StartTime: time.Now(),
			Messages:  make([]flow.StreamMessage, 0, 16),
		},
	}
	svc.ImportStreamSession(r.snapshotLocked(), nil)
	return r
}

func (r *composeStreamRecorder) setStatus(code int) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.session.StatusCode = code
	snap := r.snapshotLocked()
	r.mu.Unlock()
	r.svc.ImportStreamSession(snap, nil)
}

func (r *composeStreamRecorder) nextSeq() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	n := r.seq
	r.seq++
	return n
}

// add 使用调用方预分配的 seq，使钩子观察到的序号与存储记录一致。
func (r *composeStreamRecorder) add(seq int, eventType, sseType string, data []byte) {
	if r == nil {
		return
	}
	r.mu.Lock()
	s := r.session
	payload, size := flow.RetainPayload(data)
	s.MessageCount++
	s.TotalSize += size
	m := flow.StreamMessage{
		ID:        flow.NewID(),
		FlowID:    s.ID,
		URL:       s.URL,
		Direction: flow.WSServerToClient,
		Kind:      s.Kind,
		EventType: eventType,
		SSEType:   sseType,
		Data:      payload,
		Timestamp: time.Now(),
		Seq:       seq,
		Size:      size,
	}
	s.Messages = flow.TrimStreamMessages(append(s.Messages, m))
	snap := r.snapshotLocked()
	r.mu.Unlock()
	r.svc.ImportStreamSession(snap, &m)
}

func (r *composeStreamRecorder) close() {
	if r == nil {
		return
	}
	r.mu.Lock()
	now := time.Now()
	r.session.EndTime = &now
	r.session.Status = "closed"
	snap := r.snapshotLocked()
	r.mu.Unlock()
	r.svc.ImportStreamSession(snap, nil)
}

// snapshotLocked 复制可变元数据和消息列表；并发访问时须持有 mu。
// Data 已由 RetainPayload 复制，发布后按只读使用，可供各快照共享。
func (r *composeStreamRecorder) snapshotLocked() *flow.StreamSession {
	s := r.session
	cp := *s
	cp.Messages = make([]flow.StreamMessage, len(s.Messages))
	copy(cp.Messages, s.Messages)
	if s.EndTime != nil {
		t := *s.EndTime
		cp.EndTime = &t
	}
	return &cp
}

// runComposeRequest 按响应 Content-Type 选择增量事件读取或完整正文读取。
func (a *App) runComposeRequest(ctx context.Context, cancel context.CancelFunc, nf *flow.Flow, viaPipeline bool) {
	defer cancel()
	defer a.outStreams.remove(nf.ID)

	if viaPipeline {
		switch d := a.Pipeline.OnRequest(ctx, nf); d.Kind {
		case flow.Abort:
			nf.State = flow.StateBlocked
			nf.Error = d.Reason
			a.finishResend(nf)
			return
		case flow.Mock:
			// Mock 提供完整响应体，即使声明 SSE 类型也按一次性响应处理。
			nf.State = flow.StateMocked
			nf.Timing.ResponseAt = time.Now()
			a.Pipeline.OnResponse(ctx, nf)
			a.finishResend(nf)
			return
		}
	}

	req, err := http.NewRequestWithContext(ctx, nf.Request.Method, nf.Request.URL, bytes.NewReader(nf.Request.Body))
	if err != nil {
		nf.State = flow.StateErrored
		nf.Error = err.Error()
		a.finishResend(nf)
		return
	}
	req = flow.ApplyRequestToHTTP(nf, req)

	// 普通请求保留上游客户端的总超时；确认 SSE 后停止计时，让事件流持续到关闭。
	ctx, cancelRequest := context.WithCancelCause(req.Context())
	defer cancelRequest(context.Canceled)
	req = req.WithContext(ctx)
	var requestTimer *time.Timer
	var requestDeadline time.Time
	defer func() {
		if requestTimer != nil {
			requestTimer.Stop()
		}
	}()
	if timeout := a.Engine.UpstreamClient().Timeout; timeout > 0 && nf.Metadata["stream"] != flow.StreamSSE {
		requestDeadline = time.Now().Add(timeout)
		requestTimer = time.AfterFunc(time.Until(requestDeadline), func() { cancelRequest(context.DeadlineExceeded) })
	}
	nf.State = flow.StateAwaitingResponse
	resp, err := a.Engine.StreamUpstreamClient().Do(req)
	if err != nil {
		if cause := context.Cause(ctx); cause != nil {
			err = cause
		}
		nf.State = flow.StateErrored
		nf.Error = err.Error()
		a.finishResend(nf)
		return
	}

	if !flow.IsEventStream(resp.Header.Get("Content-Type")) {
		// 正文兜底只在更早到期时接管计时，普通请求仍受原有总截止时间约束。
		if requestDeadline.IsZero() || time.Until(requestDeadline) > composeFallbackTimeout {
			if requestTimer != nil {
				requestTimer.Stop()
			}
			requestTimer = time.AfterFunc(composeFallbackTimeout, func() { cancelRequest(errComposeBodyTimeout) })
		}
		a.readComposeHTTPResponse(ctx, nf, resp, viaPipeline)
		return
	}

	if requestTimer != nil {
		requestTimer.Stop()
	}
	if err := a.outStreams.reserveStream(nf.ID); err != nil {
		cancel()
		_ = resp.Body.Close()
		nf.State = flow.StateErrored
		nf.Error = err.Error()
		a.finishResend(nf)
		return
	}
	if nf.Metadata["stream"] != flow.StreamSSE {
		nf.Tags = append(nf.Tags, "sse")
		nf.Metadata["stream"] = flow.StreamSSE
	}
	nf.Timing.ResponseAt = time.Now()
	nf.Timing.TTFBMs = time.Since(nf.Timing.RequestAt).Milliseconds()
	nf.Response = &flow.Response{
		Status:     resp.StatusCode,
		StatusText: resp.Status,
		Header:     flow.FromHTTPHeader(resp.Header),
	}
	bodyReader, ceConsumed := flow.DecodeStreamBody(resp)
	if ceConsumed {
		delete(nf.Response.Header, "Content-Encoding")
	}
	// 仅关闭独立解码器；原始响应体由 finishBody 按读取结果处理。
	var decoder io.Closer
	if ceConsumed {
		decoder, _ = bodyReader.(io.Closer)
	}
	// 读到 EOF 后关闭响应体以便复用连接；提前结束时取消请求，通知传输层关闭连接。
	finishBody := func(drained bool) {
		if decoder != nil {
			_ = decoder.Close() // zstd 解码器持有 goroutine，提前结束也须释放
		}
		if drained {
			_ = resp.Body.Close()
			return
		}
		cancel()
	}

	if viaPipeline {
		if d := a.Pipeline.OnResponse(ctx, nf); d.Kind == flow.Abort {
			nf.State = flow.StateBlocked
			if d.Reason != "" {
				nf.Error = d.Reason
			}
			finishBody(false)
			a.finishResend(nf)
			return
		}
	}
	// 持续接收期间保持 awaiting_response，供界面区分进行中与已完成。
	a.Service.ImportFlowUpdated(nf.Clone())

	rec := newComposeStreamRecorder(a.Service, nf, flow.StreamSSE)
	rec.setStatus(resp.StatusCode)

	perr := a.pumpComposeSSE(ctx, rec, nf, bodyReader, viaPipeline)
	// finishBody 可能取消请求，须在清理前保存取消原因，避免把读取错误记为用户停止。
	stoppedByUser := errors.Is(context.Cause(ctx), context.Canceled)
	finishBody(perr == nil)
	rec.close()

	switch {
	case perr == nil || stoppedByUser:
		nf.State = flow.StateCompleted
	case errors.Is(perr, errComposeStreamAbort):
		nf.State = flow.StateBlocked
		nf.Error = perr.Error()
	default:
		nf.State = flow.StateErrored
		nf.Error = perr.Error()
	}
	a.finishResend(nf)
}

// pumpComposeSSE 按完整 SSE 块记录消息；结尾不足一个块的字节丢弃。
func (a *App) pumpComposeSSE(ctx context.Context, rec *composeStreamRecorder, nf *flow.Flow, body io.Reader, viaPipeline bool) error {
	sc := &flow.SSEScanner{}
	buf := make([]byte, 32*1024)
	url := ""
	if nf.Request != nil {
		url = nf.Request.URL
	}
	for {
		n, rerr := body.Read(buf)
		for _, ev := range sc.Push(buf[:n]) {
			data := ev.Data
			if ev.Type != "" {
				data = ev.Raw
			}
			eventType := ev.Event
			seq := rec.nextSeq()
			// 注释和控制块按原文记录，消息钩子只处理 data 事件。
			if viaPipeline && ev.Type == "" {
				m := &flow.StreamMessage{
					ID:        flow.NewID(),
					FlowID:    nf.ID,
					URL:       url,
					Direction: flow.WSServerToClient,
					Kind:      flow.StreamSSE,
					EventType: eventType,
					Data:      data,
					Timestamp: time.Now(),
					Seq:       seq,
				}
				if d := a.Pipeline.OnStreamMessage(ctx, m); d.Kind == flow.Abort {
					return errComposeStreamAbort
				}
				data, eventType = m.Data, m.EventType
			}
			rec.add(seq, eventType, ev.Type, data)
		}
		if sc.Overflowed() {
			return errComposeSSEOverflow
		}
		if rerr != nil {
			if errors.Is(rerr, io.EOF) {
				return nil
			}
			return rerr
		}
		if err := ctx.Err(); err != nil {
			return nil
		}
	}
}

func (a *App) readComposeHTTPResponse(ctx context.Context, nf *flow.Flow, resp *http.Response, viaPipeline bool) {
	defer resp.Body.Close()

	nf.Timing.ResponseAt = time.Now()
	nf.Timing.TTFBMs = time.Since(nf.Timing.RequestAt).Milliseconds()
	// 响应体整体存入内存，传输和解压后的字节数都须限制；读取不完整时保留正文并标记错误。
	capResponseBody(resp, maxComposeResponseBytes)
	readErr := flow.CaptureResponseToFlowLimit(nf, resp, maxComposeResponseBytes)
	cause := context.Cause(ctx)
	nf.State = flow.StateErrored
	switch msg, limited := composeSizeLimitError(readErr); {
	case readErr == nil:
		nf.State = flow.StateCompleted
	case limited:
		nf.Error = msg
	case errors.Is(cause, errComposeBodyTimeout):
		nf.Error = fmt.Sprintf("上游在 %s 内未结束响应体,已中止(内容不完整)", composeFallbackTimeout)
	case errors.Is(cause, context.DeadlineExceeded):
		nf.Error = "请求超时(响应体不完整)"
	case ctx.Err() != nil:
		nf.Error = "已停止(响应体不完整)"
	default:
		nf.Error = fmt.Sprintf("响应体读取失败(内容不完整): %v", readErr)
	}
	if viaPipeline {
		if d := a.Pipeline.OnResponse(ctx, nf); d.Kind == flow.Abort {
			nf.State = flow.StateBlocked
			if d.Reason != "" {
				nf.Error = d.Reason
			}
		}
	}
	a.finishResend(nf)
}
