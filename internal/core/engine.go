// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

// Package core 是抓包引擎层:它只负责监听 / 协议处理 / TLS MITM / 运行插件管道,
// 并通过 EventBus 向上层广播事件。它不关心管理 API、UI 或持久化。
//
// Engine 持有抓包所需的共享资源(CA、上游 HTTP 客户端、TCP 监听器、事件总线),
// 是 service 层与两种 transport(headless / 桌面)共同依赖的底座。
//
// 注:P1 阶段 Engine 通过 setter 把 CA 与上游客户端注入到 http 处理器包,
// 取代其包级 init() 全局变量的所有权;处理器内部仍保留默认值以兼容独立测试。
package core

import (
	"crypto/tls"
	"net/http"

	"github.com/mintfog/sniffy/ca"
	"github.com/mintfog/sniffy/capture"
	httpproc "github.com/mintfog/sniffy/capture/processors/http"
	"github.com/mintfog/sniffy/capture/types"
	"github.com/mintfog/sniffy/internal/pipeline"
	"github.com/mintfog/sniffy/plugins"
)

// Engine 抓包引擎。
type Engine struct {
	config   types.Config
	ca       ca.CA
	upstream *http.Client
	listener *capture.TCPListener
	bus      *EventBus
	logger   types.Logger
}

// NewEngine 构造引擎:创建/注入 CA 与上游客户端,把它们交给 http 处理器,
// 并基于给定配置创建 TCP 监听器。
func NewEngine(config types.Config, opts ...Option) (*Engine, error) {
	e := &Engine{
		config: config,
		bus:    NewEventBus(),
	}
	for _, o := range opts {
		o(e)
	}

	if e.ca == nil {
		c, err := ca.NewSelfSignedCA()
		if err != nil {
			return nil, err
		}
		e.ca = c
	}
	if e.upstream == nil {
		e.upstream = defaultUpstreamClient()
	}

	// 把引擎拥有的 CA 与上游客户端注入处理器,确立所有权。
	httpproc.SetCA(e.ca)
	httpproc.SetUpstreamClient(e.upstream)

	e.listener = capture.NewTCPListener(config)
	if e.logger != nil {
		e.listener.SetLogger(e.logger)
	}
	return e, nil
}

// defaultUpstreamClient 复刻历史上 http 处理器 init() 中的连接池配置。
func defaultUpstreamClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig:       &tls.Config{InsecureSkipVerify: true},
			MaxIdleConns:          httpproc.MaxIdleConns,
			MaxIdleConnsPerHost:   httpproc.MaxIdleConnsPerHost,
			MaxConnsPerHost:       httpproc.MaxConnsPerHost,
			IdleConnTimeout:       httpproc.IdleConnTimeout,
			DisableKeepAlives:     false,
			ResponseHeaderTimeout: httpproc.ResponseHeaderTimeout,
			ExpectContinueTimeout: httpproc.ExpectContinueTimeout,
		},
		Timeout: httpproc.ClientTimeout,
	}
}

// SetHookExecutor 注入插件钩子执行器到监听器与数据包处理器。
// (沿用历史 main.go 的注入逻辑,集中到引擎。)
func (e *Engine) SetHookExecutor(h *plugins.HookExecutor) {
	if h == nil {
		return
	}
	e.listener.SetHookExecutor(h)
	if handler := e.listener.GetHandler(); handler != nil {
		if simple, ok := handler.(*capture.SimplePacketHandler); ok {
			simple.SetHookExecutor(h)
		}
	}
}

// SetPipeline 注入插件管道到 HTTP 处理器。
func (e *Engine) SetPipeline(p *pipeline.Pipeline) { httpproc.SetPipeline(p) }

// SetFlowSink 注入 flow 接收器(由 service 实现)到 HTTP 处理器。
func (e *Engine) SetFlowSink(s httpproc.FlowSink) { httpproc.SetFlowSink(s) }

// Start 启动抓包监听。
func (e *Engine) Start() error { return e.listener.Start() }

// Stop 停止抓包监听。
func (e *Engine) Stop() error { return e.listener.Stop() }

// Bus 返回事件总线,供 service 层订阅。
func (e *Engine) Bus() *EventBus { return e.bus }

// CA 返回引擎持有的 CA,供 service 层导出证书等。
func (e *Engine) CA() ca.CA { return e.ca }

// UpstreamClient 返回引擎持有的上游 HTTP 客户端。
func (e *Engine) UpstreamClient() *http.Client { return e.upstream }

// Listener 返回底层 TCP 监听器(过渡期暴露,后续逐步收敛)。
func (e *Engine) Listener() *capture.TCPListener { return e.listener }

// Config 返回引擎配置。
func (e *Engine) Config() types.Config { return e.config }
