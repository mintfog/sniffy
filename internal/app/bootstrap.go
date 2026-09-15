// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

// Package app 把引擎、服务、插件管道装配在一起,供 headless 与桌面两种入口复用。
package app

import (
	"fmt"
	"sync"

	"github.com/mintfog/sniffy/ca"
	"github.com/mintfog/sniffy/capture/types"
	"github.com/mintfog/sniffy/internal/bodycache"
	"github.com/mintfog/sniffy/internal/core"
	"github.com/mintfog/sniffy/internal/pipeline"
	"github.com/mintfog/sniffy/internal/platform"
	"github.com/mintfog/sniffy/internal/plugin"
	"github.com/mintfog/sniffy/internal/plugin/native"
	"github.com/mintfog/sniffy/internal/procinfo"
	"github.com/mintfog/sniffy/internal/rules"
	"github.com/mintfog/sniffy/internal/service"
)

// App 聚合一次运行所需的核心组件。
type App struct {
	Engine    *core.Engine
	Service   *service.Service
	Pipeline  *pipeline.Pipeline
	Plugins   *plugin.Manager
	ConfigDir string
	CertDir   string
	Logger    *Logger
	caMu      sync.Mutex

	// outStreams 是构造器发起、仍在进行中的出站流(SSE)。键是 flow.ID —— 前端 SendRequest
	// 拿到的就是它,故 UI 无需第二套句柄即可停止一条流。
	outStreams composeStreamRegistry
	// outWS 是构造器打开的出站 WebSocket 连接表,键是 WSSession.ID(即 OpenWebSocket 的返回值)。
	outWS composeWSRegistry
}

// newBodyCache 允许隔离测试模拟缓存初始化时的文件系统故障。
var newBodyCache = bodycache.New

// Build 装配核心组件:引擎 → 服务 → 管道 → 插件,并完成注入。
// 调用方负责随后 Start() 引擎与所选 transport。
func Build(cfg types.Config, verbose bool) (*App, error) {
	logger := NewLogger(verbose)

	// 日志落盘:控制台 + 按天滚动的日志文件。失败时降级为仅控制台,不影响启动。
	if logsDir, err := EnableFileLogging(); err != nil {
		logger.Warn("日志落盘不可用,仅输出到控制台: %v", err)
	} else {
		logger.Info("日志写入目录: %s", logsDir)
	}

	configDir, err := platform.ConfigDir()
	if err != nil {
		return nil, fmt.Errorf("创建配置目录失败: %w", err)
	}
	certDir, err := platform.CertificatesDir()
	if err != nil {
		return nil, fmt.Errorf("创建证书目录失败: %w", err)
	}
	rootCA, err := ca.NewSelfSignedCA(certDir)
	if err != nil {
		return nil, fmt.Errorf("加载根 CA 失败: %w", err)
	}

	engine, err := core.NewEngine(cfg, core.WithCA(rootCA), core.WithLogger(logger))
	if err != nil {
		return nil, err
	}

	svc := service.New(engine.CA(), engine.Bus(), configDir, certDir)
	svc.SetTLSInsecureHostsApplier(engine.SetTLSInsecureHosts)

	// 上游代理:把 service 的配置变更接到引擎,并应用一次持久化的初始值。
	svc.SetUpstreamApplier(engine.SetUpstreamProxy)
	if err := engine.SetUpstreamProxy(svc.Config().EffectiveUpstream()); err != nil {
		logger.Error("应用上游代理失败: %v", service.RedactUpstreamError(err))
	}
	// 监听端可能绑定 0.0.0.0，必须在接受外部流量前恢复持久化凭据。凭据不全时的
	// fail-closed（见 SetProxyAuth）表现为「代理突然全不通」，故告警指出原因。
	svc.SetProxyAuthApplier(func(enabled bool, username, password string) error {
		if enabled && (username == "" || password == "") {
			logger.Warn("已开启本地代理认证但账号或密码为空，所有客户端请求都会被 407 拒绝")
		}
		return engine.SetProxyAuth(enabled, username, password)
	})

	// HTTPS 解密范围:同样接到引擎并应用一次持久化初始值。
	svc.SetDecryptScopeApplier(engine.SetDecryptScope)
	initCfg := svc.Config()
	if err := engine.SetDecryptScope(initCfg.EnableHTTPS, initCfg.DecryptScope, initCfg.DecryptAllow, initCfg.DecryptDeny); err != nil {
		logger.Error("应用解密范围失败: %v", err)
	}

	// 网络限速:前端开关写入持久化配置,这里启动时恢复并接入运行时热切换。
	svc.SetThrottleApplier(engine.SetThrottle)
	if err := engine.SetThrottle(initCfg.Throttle, initCfg.ThrottleKiBps); err != nil {
		logger.Error("应用网络限速失败: %v", err)
	}

	// 大体积 / 媒体响应的透传旁路与其落盘缓存:缓存建不起来(盘满 / 权限)不影响抓包,
	// 只是详情页取不到这类响应体,故降级继续。
	if cacheDir, err := platform.CacheDir(); err != nil {
		logger.Warn("缓存目录不可用,大体积响应体将不留副本: %v", err)
	} else if cache, err := newBodyCache(cacheDir, bodycache.DefaultBudget); err != nil {
		logger.Warn("响应体缓存不可用,大体积响应体将不留副本: %v", err)
	} else {
		engine.SetBodyCache(cache)
		svc.SetBodyCache(cache)
		logger.Info("响应体缓存目录: %s", cacheDir)
	}
	// SetPassthroughApplier 内部即以持久化值应用一次。
	svc.SetPassthroughApplier(engine.SetPassthrough)

	// 导入的服务端证书(应对固定证书场景):接到引擎,SetServerCertsApplier 内部即以持久化值应用一次。
	svc.SetServerCertsApplier(engine.SetImportedServerCerts)

	// 事件适配器:pipeline 不直接依赖 core,经函数把事件投递到总线。
	emit := func(t string, payload any) {
		engine.Bus().Emit(core.EventType(t), payload)
	}
	pipe := pipeline.New(emit, logger)

	// URL 断点规则:启动恢复一次,此后每次 CRUD 写时落盘(回调在断点管理器的锁外调用,
	// 不把磁盘延迟摊到每请求的 ShouldBreakFor 上)。全局"断在请求/响应"开关刻意不持久化 ——
	// 它会把全部流量按住,恢复它等于让 App 在用户看到界面之前就静默冻结所有请求。
	restoreBreakRules(pipe, svc, logger)

	// 规则引擎作为常驻核心钩子:把 service 持久化的重写规则实时应用到流量上。
	// 用 RegisterCore 注册,使其不被插件热重载(pipe.Clear)清掉。
	pipe.RegisterCore(rules.New(svc.Rules))

	// Go 原生(编译进二进制)插件:同样用 RegisterCore,避免 JS 热重载把它们清掉。
	for _, h := range native.All() {
		pipe.RegisterCore(h)
	}

	// 加载用户 JS 插件(目录见 platform.PluginsDir);emit 用于把插件日志实时推到 UI。
	pluginsDir, _ := platform.PluginsDir()
	mgr := plugin.NewManager(pipe, pluginsDir, logger, emit)
	if err := mgr.LoadAll(); err != nil {
		logger.Error("加载插件失败: %v", err)
	}

	engine.SetPipeline(pipe)
	engine.SetFlowSink(svc)
	engine.SetStreamSink(svc)

	// 进程解析器(best-effort):创建失败则跳过进程补全,不影响抓包。
	if resolver := procinfo.NewResolver(); resolver != nil {
		engine.SetProcessResolver(resolver)
	} else {
		logger.Debug("进程解析器不可用,会话将不含进程信息")
	}

	return &App{
		Engine:    engine,
		Service:   svc,
		Pipeline:  pipe,
		Plugins:   mgr,
		ConfigDir: configDir,
		CertDir:   certDir,
		Logger:    logger,
	}, nil
}

// Start 启动抓包引擎。
func (a *App) Start() error { return a.Engine.Start() }

// Stop 停止抓包引擎与插件,并把缓冲中的日志落盘。
func (a *App) Stop() error {
	if a.Plugins != nil {
		a.Plugins.Close()
	}
	// 出站长连接不归引擎管,进程退出前显式收口,免得留下悬挂的连接与 open 状态的会话。
	a.outStreams.stopAll()
	a.outWS.closeAll()
	err := a.Engine.Stop()
	FlushLogs()
	return err
}

// restoreBreakRules 把持久化的 URL 断点规则灌回断点管理器,并接上写时落盘。
// service 与 pipeline 各用自己的规则类型,转换只发生在装配层,两边互不依赖。
func restoreBreakRules(pipe *pipeline.Pipeline, svc *service.Service, logger *Logger) {
	bp := pipe.Breakpoints()
	stored := svc.BreakRules()
	restored := make([]*pipeline.BreakRule, 0, len(stored))
	for _, r := range stored {
		restored = append(restored, &pipeline.BreakRule{
			ID:         r.ID,
			URL:        r.URL,
			OnRequest:  r.OnRequest,
			OnResponse: r.OnResponse,
			Enabled:    r.Enabled,
		})
	}
	bp.RestoreRules(restored)

	bp.SetPersist(func(rules []*pipeline.BreakRule) error {
		specs := make([]service.BreakRuleSpec, 0, len(rules))
		for _, r := range rules {
			specs = append(specs, service.BreakRuleSpec{
				ID:         r.ID,
				URL:        r.URL,
				OnRequest:  r.OnRequest,
				OnResponse: r.OnResponse,
				Enabled:    r.Enabled,
			})
		}
		if err := svc.SaveBreakRules(specs); err != nil {
			logger.Error("保存断点规则失败: %v", err)
			return err
		}
		return nil
	})
}
