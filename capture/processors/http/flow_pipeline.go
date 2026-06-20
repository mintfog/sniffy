// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package http

import (
	"context"
	"net"
	"net/http"
	"time"

	"github.com/mintfog/sniffy/capture/types"
	"github.com/mintfog/sniffy/internal/flow"
)

// clientResponder 抽象「把处理后的 Flow 写回客户端」,使同一套 flow 管道既能服务
// HTTP/1.x(经 bufio/conn 写 resp.Write),也能服务 HTTP/2(经 http.ResponseWriter)。
//
// 约定:三个方法都只负责「写」,不负责记录 Flow 完成 —— 完成记录(finishFlow)
// 由 runFlowPipeline 统一负责,从而无论走哪条线缆,生命周期都一致。
//   - writeFlowResponse:写回正常 / mock 响应。
//   - writeAbort:写回阻断响应;StatusOnAbort==0 时由实现决定如何「直接中断」
//     (h1 直接关连接,h2 以 RST_STREAM 中断本流)。
//   - writeBadGateway:上游请求失败时写回 502。
type clientResponder interface {
	writeFlowResponse(f *flow.Flow, req *http.Request) error
	writeAbort(d flow.Decision)
	writeBadGateway() error
}

// runFlowPipeline 是协议无关的请求处理核心:构造 Flow → 请求插件 →
// 转发 / mock / abort / 断点 → 响应插件 → 经 responder 写回客户端。
//
// 它被 HTTP/1.x 处理器(connResponder)与 HTTP/2 处理器(h2Responder)共用:
// HTTP/2 下每个 stream 都是一次独立调用,共享同一连接但各自一条 Flow,
// 由 http2.Server 的多路复用并发驱动 —— 管道(pipeline.go)以 RWMutex 快照
// 实现,Flow 互不共享,故并发安全。
//
// activePipeline 为 nil(独立测试 / 未装配管道)时按 Continue 处理,退化为纯转发。
func runFlowPipeline(server types.Server, request *http.Request, protocol string, clientAddr, proxyAddr net.Addr, r clientResponder) error {
	f := flow.BuildRequestFlow(request, protocol)
	if clientAddr != nil {
		f.Request.ClientIP = clientAddr.String()
	}

	ctx := context.Background()
	if flowSink != nil {
		flowSink.RecordFlowStarted(f)
	}
	asyncResolveProcess(f, clientAddr, proxyAddr)

	// 请求阶段插件。
	reqDecision := flow.ContinueDecision()
	if activePipeline != nil {
		reqDecision = activePipeline.OnRequest(ctx, f)
	}
	switch reqDecision.Kind {
	case flow.Abort:
		f.State = flow.StateBlocked
		finishFlow(f) // 先记录:h2 的 writeAbort 可能以 panic(ErrAbortHandler) 中断本流
		r.writeAbort(reqDecision)
		return nil
	case flow.Mock:
		f.State = flow.StateMocked
		f.Timing.DurationMs = time.Since(f.Timing.RequestAt).Milliseconds()
		if activePipeline != nil {
			activePipeline.OnResponse(ctx, f) // 让插件 / 抓包看到 mock 响应
		}
		err := r.writeFlowResponse(f, request)
		finishFlow(f)
		return err
	}

	// 继续:把(可能被插件改过的)Flow 应用回 request,修正长度 / 编码,转发上游。
	// 返回值携带「保真头序列」ctx,务必改用它转发。
	request = flow.ApplyRequestToHTTP(f, request)

	f.State = flow.StateAwaitingResponse
	resp, err := sharedHttpClient.Do(request)
	if err != nil {
		server.LogError("请求失败: %v", err)
		f.State = flow.StateErrored
		f.Error = err.Error()
		finishFlow(f)
		return r.writeBadGateway()
	}
	defer resp.Body.Close()

	f.Timing.ResponseAt = time.Now()
	flow.CaptureResponseToFlow(f, resp)
	f.Timing.DurationMs = time.Since(f.Timing.RequestAt).Milliseconds()
	f.State = flow.StateCompleted

	// 响应阶段插件。
	respDecision := flow.ContinueDecision()
	if activePipeline != nil {
		respDecision = activePipeline.OnResponse(ctx, f)
	}
	if respDecision.Kind == flow.Abort {
		f.State = flow.StateBlocked
		finishFlow(f)
		r.writeAbort(respDecision)
		return nil
	}

	err = r.writeFlowResponse(f, request)
	finishFlow(f)
	return err
}

// finishFlow 记录 flow 完成(容忍 flowSink 为 nil 的独立测试场景)。
func finishFlow(f *flow.Flow) {
	if f.Timing.CompletedAt.IsZero() {
		f.Timing.CompletedAt = time.Now()
	}
	if flowSink != nil {
		flowSink.RecordFlowCompleted(f)
	}
}

// asyncResolveProcess 在独立 goroutine 中解析发起进程并挂到 flow 上(best-effort,
// 不阻塞 flow 处理),成功后经 RecordFlowUpdated 推送到 UI。失败时静默跳过。
func asyncResolveProcess(f *flow.Flow, clientAddr, proxyAddr net.Addr) {
	if processResolver == nil || flowSink == nil || clientAddr == nil {
		return
	}
	go func() {
		if pi := processResolver.Resolve(clientAddr, proxyAddr); pi != nil {
			f.SetProcess(pi)
			flowSink.RecordFlowUpdated(f)
		}
	}()
}

// connResponder 是 HTTP/1.x 的 responder:经处理器持有的 bufio / 裸连接写回。
type connResponder struct {
	p      *Processor
	server types.Server
}

func (c *connResponder) writeFlowResponse(f *flow.Flow, req *http.Request) error {
	return c.p.writeFlowResponse(c.server, f, req)
}

func (c *connResponder) writeAbort(d flow.Decision) { c.p.writeAbort(d) }

func (c *connResponder) writeBadGateway() error {
	writer := c.p.conn.GetWriter()
	_, _ = writer.WriteString(BadGatewayResponse)
	return writer.Flush()
}
