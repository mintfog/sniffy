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
	"sync/atomic"
	"time"

	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/internal/service"
)

// composeFallbackTimeout 是「点了 SSE 但上游没按 SSE 应答」时退化到缓冲读取所用的总超时。
// 与 capture 侧的 httpproc.ClientTimeout 对齐(10 分钟),在这里另立常量是为了不让 app 依赖
// capture 子包;流式客户端本身不设总超时,退化路径必须自己把这道保险找回来,
// 否则遇上「Content-Type 不是 SSE 又不结束」的上游,io.ReadAll 会永久阻塞。
// 是 var 而非 const 只为可测:超时分支正是「本该 errored 却记成 completed」的那条,
// 不让测试压缩它就等于不测。生产代码不改它。
var composeFallbackTimeout = 10 * time.Minute

// maxComposeStreams 是构造器可同时保持的出站流数量上限,与 maxComposeWS 同理:
// UI 窗口可能在不通知 Go 的情况下被销毁,而 SSE 既无读超时也无总超时,
// 没有上限就只剩进程退出这一个收口点。
const maxComposeStreams = 16

// errComposeStreamAbort 标记「插件在流消息钩子里中止了这条流」,与读错误区分开:
// 它对应 Flow 的 blocked 而不是 errored。
var errComposeStreamAbort = errors.New("插件中止了流")

// errComposeSSEOverflow 标记「单个 SSE 事件超过 flow.MaxSSEEventBytes」。
// 抓包侧遇到这种上游会转为原样中继(下游客户端还等着数据),而构造器这边没有下游要喂,
// 继续读只是替对端把内存吃光,故直接收掉这条流并把原因写进 Flow.Error。
var errComposeSSEOverflow = fmt.Errorf("上游单个 SSE 事件超过 %d 字节(或始终未发送空行),已中止", flow.MaxSSEEventBytes)

// composeStreamRegistry 记录进行中的出站流,零值可用(App 以值字段持有,测试直接构造字面量)。
type composeStreamRegistry struct {
	mu    sync.Mutex
	items map[string]context.CancelFunc
}

// add 登记一条进行中的流;已达上限时不登记并返回错误,调用方据此放弃发起。
func (r *composeStreamRegistry) add(id string, cancel context.CancelFunc) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.items == nil {
		r.items = make(map[string]context.CancelFunc)
	}
	if len(r.items) >= maxComposeStreams {
		return fmt.Errorf("出站流数量已达上限 %d,请先停止一些流", maxComposeStreams)
	}
	r.items[id] = cancel
	return nil
}

func (r *composeStreamRegistry) remove(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.items, id)
}

// stop 取消一条流并摘除条目,返回是否命中。
func (r *composeStreamRegistry) stop(id string) bool {
	r.mu.Lock()
	cancel, ok := r.items[id]
	delete(r.items, id)
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
	r.mu.Unlock()
	for _, cancel := range items {
		cancel()
	}
}

// StopStream 主动结束一条由构造器发起的流,返回是否找到了进行中的流。
// 取消会关掉底层连接,读循环随即退出并按「正常终点」收尾(Flow 记 completed 而非 errored)。
func (a *App) StopStream(id string) bool { return a.outStreams.stop(id) }

// StopAllStreams 结束全部进行中的出站流,与 CloseAllWebSockets 对称:
// 构造器窗口一关就没人看这些流了,而它们既无读超时也无总超时,不主动收就只能等进程退出。
func (a *App) StopAllStreams() { a.outStreams.stopAll() }

// composeStreamRecorder 维护一条由构造器发起的流会话,每次变化向 service 推一份深拷贝快照。
//
// 不复用 capture 的 streamRecorder:后者未导出,且它的 add/close 直接写包级 streamSink 与
// activePipeline 两个全局变量,而这里手上就有 a.Service / a.Pipeline,绕过去只会和捕获侧抢同一个全局。
//
// 必须交深拷贝:Service.ImportStreamSession 把指针本身存进 store,并把 DTO 丢进 EventBus
// 让别的 goroutine 序列化;交出活对象后本 goroutine 继续 append(Messages) 就是切片并发读写。
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
	r := &composeStreamRecorder{svc: svc, session: &flow.StreamSession{
		ID:        f.ID,
		URL:       url,
		Kind:      kind,
		Method:    method,
		Status:    "open",
		StartTime: time.Now(),
		Messages:  make([]flow.StreamMessage, 0, 16),
	}}
	r.push()
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

// nextSeq 取本会话内的下一个消息序号(供 ViaPipeline 构造 StreamMessage)。
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

// add 追加一条 server->client 消息并推送。seq 由调用方经 nextSeq 预先取得 ——
// 过管道时钩子要先看到它,不能等到入库才编号。
func (r *composeStreamRecorder) add(seq int, eventType string, data []byte) {
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

func (r *composeStreamRecorder) push() {
	r.mu.Lock()
	snap := r.snapshotLocked()
	r.mu.Unlock()
	r.svc.ImportStreamSession(snap, nil)
}

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

// runComposeSSE 执行一次由构造器发起的 SSE 往返:请求管道 → 发起 → 增量读取事件 → 逐条记录。
// 与 runResend 的关键差别是响应体不进 Flow.Body(与捕获侧流式路径同契约),
// 逐条事件只经 StreamSession 呈现。
//
// ctx / cancel 由 SendRequest 创建并已登记进 outStreams:名额检查与占用必须是同一次
// 加锁操作,挪进本 goroutine 就成了 check-then-act 竞态。
func (a *App) runComposeSSE(ctx context.Context, cancel context.CancelFunc, nf *flow.Flow, viaPipeline bool) {
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
			// mock 的响应是一次性的完整体,没有可增量呈现的事件,不进流路径。
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

	nf.State = flow.StateAwaitingResponse
	resp, err := a.Engine.StreamUpstreamClient().Do(req)
	if err != nil {
		// 连响应头都没拿到,建一条零消息的会话对 UI 只是噪音,错误信息在 Flow 上已经完整。
		nf.State = flow.StateErrored
		nf.Error = err.Error()
		a.finishResend(nf)
		return
	}

	if !flow.IsEventStream(resp.Header.Get("Content-Type")) {
		a.composeSSEFallback(ctx, cancel, nf, resp, viaPipeline)
		return
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
	// ceConsumed 为假时 bodyReader 就是 resp.Body 本身,没有独立解码器要关。
	var decoder io.Closer
	if ceConsumed {
		decoder, _ = bodyReader.(io.Closer)
	}
	// finishBody 收口上游响应体。drained 为真(已读到 EOF)时正常 Close,连接可回池;
	// 未读尽时只能取消 ctx —— 保真转发器(internal/forward)的 Close 会为复用连接把剩余字节
	// 抽干,而一条还在推事件的流永远抽不完,只有连接守护(随 ctx 取消)能把底层连接关掉。
	finishBody := func(drained bool) {
		if decoder != nil {
			_ = decoder.Close() // zstd 解码器持有 goroutine,读没读尽都要释放
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
	// 状态刻意停在 awaiting_response:流还在跑,提前置 completed 会让构造器窗口渲染成绿色「已完成」。
	a.Service.ImportFlowUpdated(nf.Clone())

	rec := newComposeStreamRecorder(a.Service, nf, flow.StreamSSE)
	rec.setStatus(resp.StatusCode)

	perr := a.pumpComposeSSE(ctx, rec, nf, bodyReader, viaPipeline)
	// 必须在 finishBody 之前取:未读尽的收口会取消 ctx,之后再看 ctx.Err() 就分不出
	// 「用户停止」与「上游出错」了。
	stoppedByUser := ctx.Err() != nil
	finishBody(perr == nil)
	rec.close()

	switch {
	case perr == nil || stoppedByUser:
		// 用户主动 StopStream 与上游读尽都是这条流的正常终点。
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

// pumpComposeSSE 增量读取上游 body,逐事件过管道并记录。返回 nil 表示读到了 EOF。
func (a *App) pumpComposeSSE(ctx context.Context, rec *composeStreamRecorder, nf *flow.Flow, body io.Reader, viaPipeline bool) error {
	sc := &flow.SSEScanner{}
	buf := make([]byte, 32*1024)
	url := ""
	if nf.Request != nil {
		url = nf.Request.URL
	}
	for {
		n, rerr := body.Read(buf)
		if n > 0 {
			for _, ev := range sc.Push(buf[:n]) {
				data := append([]byte(nil), ev.Data...)
				eventType := ev.Event
				seq := rec.nextSeq()
				if viaPipeline {
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
				rec.add(seq, eventType, data)
			}
			if sc.Overflowed() {
				return errComposeSSEOverflow
			}
		}
		if rerr != nil {
			// 出站方向没有下游客户端要喂,Flush 的残留字节(不成块的尾巴)直接丢弃。
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

// composeSSEFallback 处理「点了 SSE 但上游回了普通响应」:整体读进 Flow.Body 按一次性往返收尾,
// 不产生 StreamSession。401 鉴权页一类是常见场景,故不视为错误。
func (a *App) composeSSEFallback(ctx context.Context, cancel context.CancelFunc, nf *flow.Flow, resp *http.Response, viaPipeline bool) {
	// 流式客户端不设总超时:退化路径要自己给「不结束的上游」兜底,否则 io.ReadAll 永久阻塞。
	// timedOut 单独记一笔:超时与 StopStream 都是取消 ctx,事后看 ctx.Err() 分不出是哪一个。
	var timedOut atomic.Bool
	timer := time.AfterFunc(composeFallbackTimeout, func() {
		timedOut.Store(true)
		cancel()
	})
	defer timer.Stop()
	defer resp.Body.Close()

	nf.Timing.ResponseAt = time.Now()
	nf.Timing.TTFBMs = time.Since(nf.Timing.RequestAt).Milliseconds()
	// 这条路径没有增量呈现,Flow.Body 就是用户看到的全部响应。读不尽即内容不完整,
	// 四种成因(超上限 / 超时兜底 / 用户停止 / 上游出错)都得记 errored ——「completed 但少了一截」
	// 是最坏的结果:界面上是绿的,内容却是错的。
	//
	// 上限先于超时:退化路径与 runResend 同为「整块进内存」,超时只能拦住不结束的上游,
	// 拦不住十分钟内就送来一个大文件的上游。
	capResponseBody(resp, maxComposeResponseBytes)
	readErr := flow.CaptureResponseToFlowLimit(nf, resp, maxComposeResponseBytes)
	switch msg, limited := composeSizeLimitError(readErr); {
	case readErr == nil:
		nf.State = flow.StateCompleted
	case limited:
		nf.State = flow.StateErrored
		nf.Error = msg
	case timedOut.Load():
		nf.State = flow.StateErrored
		nf.Error = fmt.Sprintf("上游在 %s 内未结束响应体,已中止(内容不完整)", composeFallbackTimeout)
	case ctx.Err() != nil:
		nf.State = flow.StateErrored
		nf.Error = "已停止(响应体不完整)"
	default:
		nf.State = flow.StateErrored
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
