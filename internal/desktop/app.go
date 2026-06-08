// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build desktop

package desktop

import (
	"context"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/mintfog/sniffy/internal/app"
	"github.com/mintfog/sniffy/internal/core"
	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/internal/service"
)

// Bridge 是桌面(Wails v3)transport:把 service 的领域方法绑定给前端,并用 Wails 事件做实时推送。
// 它与 internal/api(headless)平行,共享同一个 service。
//
// 作为 Wails v3 Service 注册(application.NewService),实现可选的
// ServiceStartup/ServiceShutdown 生命周期钩子。前端通过 Call.ByName 调用以下导出方法,
// 完整方法名为 "github.com/mintfog/sniffy/internal/desktop.Bridge.<方法名>"。
type Bridge struct {
	app    *app.App
	cancel func()
}

// New 创建桥接对象。
func New(a *app.App) *Bridge { return &Bridge{app: a} }

// ServiceName 用于日志/调试。
func (b *Bridge) ServiceName() string { return "sniffy.Bridge" }

// ServiceStartup 由 Wails v3 在启动时调用:订阅引擎事件总线并转发为 Wails 事件。
func (b *Bridge) ServiceStartup(_ context.Context, _ application.ServiceOptions) error {
	ch, cancel := b.app.Service.Bus().Subscribe()
	b.cancel = cancel
	go b.forwardEvents(ch)
	return nil
}

// ServiceShutdown 由 Wails v3 在退出时调用:停止转发并关闭引擎。
func (b *Bridge) ServiceShutdown() error {
	if b.cancel != nil {
		b.cancel()
	}
	return b.app.Stop()
}

// forwardEvents 把引擎事件总线的事件转发为 Wails 事件(事件名 = 事件类型字符串,如 flow_started)。
func (b *Bridge) forwardEvents(ch <-chan core.Event) {
	wapp := application.Get()
	for e := range ch {
		if wapp == nil {
			wapp = application.Get()
		}
		if wapp != nil {
			wapp.Event.Emit(string(e.Type), e.Payload)
		}
	}
}

// ---- 绑定给前端的方法(返回值由 Wails 序列化为 JSON) ----

// SessionPage 是分页会话返回。
type SessionPage struct {
	Data  []service.HTTPSessionDTO `json:"data"`
	Total int                      `json:"total"`
}

func (b *Bridge) GetSessions(page, pageSize int) SessionPage {
	list, total := b.app.Service.Sessions(page, pageSize)
	return SessionPage{Data: list, Total: total}
}

func (b *Bridge) GetSession(id string) *service.HTTPSessionDTO {
	s, ok := b.app.Service.Session(id)
	if !ok {
		return nil
	}
	return &s
}

func (b *Bridge) DeleteSession(id string) { b.app.Service.DeleteSession(id) }
func (b *Bridge) ClearSessions()          { b.app.Service.ClearSessions() }

func (b *Bridge) GetStatistics() service.StatisticsDTO { return b.app.Service.Statistics() }

func (b *Bridge) GetConfig() service.AppConfig { return b.app.Service.Config() }
func (b *Bridge) UpdateConfig(patch map[string]any) service.AppConfig {
	return b.app.Service.UpdateConfig(patch)
}

func (b *Bridge) StartRecording()   { b.app.Service.StartRecording() }
func (b *Bridge) StopRecording()    { b.app.Service.StopRecording() }
func (b *Bridge) IsRecording() bool { return b.app.Service.IsRecording() }
func (b *Bridge) GetCertificatePEM() string {
	return string(b.app.Service.CertificatePEM())
}

func (b *Bridge) GetRules() []*service.InterceptRule { return b.app.Service.Rules() }
func (b *Bridge) CreateRule(r *service.InterceptRule) *service.InterceptRule {
	return b.app.Service.CreateRule(r)
}
func (b *Bridge) ToggleRule(id string, enabled bool) bool {
	_, ok := b.app.Service.ToggleRule(id, enabled)
	return ok
}
func (b *Bridge) DeleteRule(id string) { b.app.Service.DeleteRule(id) }

// ---- 插件 ----

func (b *Bridge) GetPlugins() []map[string]any {
	if b.app.Plugins == nil {
		return nil
	}
	return b.app.Plugins.ListPlugins()
}
func (b *Bridge) EnablePlugin(id string, enabled bool) error {
	return b.app.Plugins.EnablePlugin(id, enabled)
}
func (b *Bridge) GetPluginSource(id string) string {
	src, _ := b.app.Plugins.GetPluginSource(id)
	return src
}
func (b *Bridge) SavePluginSource(id, source string) error {
	return b.app.Plugins.SavePluginSource(id, source)
}

// ---- 断点 ----

func (b *Bridge) GetBreakpoints() []*flow.Flow {
	return b.app.Pipeline.Breakpoints().List()
}
func (b *Bridge) ResumeBreakpoint(id string, edited *flow.Flow) bool {
	return b.app.Pipeline.Breakpoints().Resume(id, edited)
}
func (b *Bridge) AbortBreakpoint(id string) bool {
	return b.app.Pipeline.Breakpoints().Abort(id)
}
func (b *Bridge) SetGlobalBreak(onRequest, onResponse bool) {
	b.app.Pipeline.Breakpoints().SetGlobalBreak(onRequest, onResponse)
}
