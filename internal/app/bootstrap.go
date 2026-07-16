// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

// Package app 把引擎、服务、插件管道装配在一起,供 headless 与桌面两种入口复用。
package app

import (
	"github.com/mintfog/sniffy/capture/types"
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
	Logger    *Logger
}

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
		configDir = ""
	}

	engine, err := core.NewEngine(cfg, core.WithLogger(logger))
	if err != nil {
		return nil, err
	}

	svc := service.New(engine.CA(), engine.Bus(), configDir)

	// 上游代理:把 service 的配置变更接到引擎,并应用一次持久化的初始值。
	svc.SetUpstreamApplier(engine.SetUpstreamProxy)
	if err := engine.SetUpstreamProxy(svc.Config().EffectiveUpstream()); err != nil {
		logger.Error("应用上游代理失败: %v", err)
	}

	// HTTPS 解密范围:同样接到引擎并应用一次持久化初始值。
	svc.SetDecryptScopeApplier(engine.SetDecryptScope)
	initCfg := svc.Config()
	if err := engine.SetDecryptScope(initCfg.EnableHTTPS, initCfg.DecryptScope, initCfg.DecryptAllow, initCfg.DecryptDeny); err != nil {
		logger.Error("应用解密范围失败: %v", err)
	}

	// 事件适配器:pipeline 不直接依赖 core,经函数把事件投递到总线。
	emit := func(t string, payload any) {
		engine.Bus().Emit(core.EventType(t), payload)
	}
	pipe := pipeline.New(emit, logger)

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
	err := a.Engine.Stop()
	FlushLogs()
	return err
}
