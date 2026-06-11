// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build desktop

package desktop

import (
	"context"
	"net"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/mintfog/sniffy/internal/app"
	"github.com/mintfog/sniffy/internal/core"
	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/internal/pipeline"
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

// ListenInfo 是代理实际监听的绑定地址与端口(只读)。
type ListenInfo struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

// GetListenInfo 返回引擎实际监听的地址/端口。这是权威值(默认 < config.json <
// headless 命令行参数都已在启动期解析完成),供前端只读展示,不可经 UpdateConfig 修改。
func (b *Bridge) GetListenInfo() ListenInfo {
	c := b.app.Engine.Config()
	return ListenInfo{Host: c.GetAddress(), Port: c.GetPort()}
}

// GetLANIP 返回本机在内网中的首选 IPv4 地址(如 192.168.x.x),供前端在代理
// 监听地址里展示,方便同网段其它设备把代理指向本机。无可用地址时回退到回环。
func (b *Bridge) GetLANIP() string { return lanIP() }

// lanIP 选出本机的内网 IPv4。优先用"拨号到公网地址"的方式让内核按路由选出出站
// 网卡的本地地址(可避开虚拟网卡);失败时遍历网卡取第一个非回环 IPv4;再退回回环。
// 注意:UDP Dial 不会真正发包,只用于解析出站网卡。
func lanIP() string {
	if conn, err := net.Dial("udp", "8.8.8.8:80"); err == nil {
		defer conn.Close()
		if addr, ok := conn.LocalAddr().(*net.UDPAddr); ok && !addr.IP.IsLoopback() {
			if ip4 := addr.IP.To4(); ip4 != nil {
				return ip4.String()
			}
		}
	}
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
				if ip4 := ipnet.IP.To4(); ip4 != nil {
					return ip4.String()
				}
			}
		}
	}
	return "127.0.0.1"
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
func (b *Bridge) UpdateRule(id string, r *service.InterceptRule) *service.InterceptRule {
	updated, ok := b.app.Service.UpdateRule(id, r)
	if !ok {
		return nil
	}
	return updated
}
func (b *Bridge) ToggleRule(id string, enabled bool) bool {
	_, ok := b.app.Service.ToggleRule(id, enabled)
	return ok
}
func (b *Bridge) DeleteRule(id string) { b.app.Service.DeleteRule(id) }

// ---- 重发 / 证书重新生成 ----

// ResendFlow 以一条已捕获 flow 为蓝本重新发起请求(作为新 flow 记录)。返回是否找到原始 flow。
func (b *Bridge) ResendFlow(id string) bool { return b.app.ResendFlow(id) }

// RegenerateCA 重新生成根 CA 并返回新证书 PEM(失败返回空串)。
func (b *Bridge) RegenerateCA() string {
	pem, err := b.app.RegenerateCA()
	if err != nil {
		return ""
	}
	return pem
}

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

// GlobalBreakState 是全局断点开关的当前状态。
type GlobalBreakState struct {
	OnRequest  bool `json:"onRequest"`
	OnResponse bool `json:"onResponse"`
}

func (b *Bridge) GetGlobalBreak() GlobalBreakState {
	onReq, onResp := b.app.Pipeline.Breakpoints().GlobalBreak()
	return GlobalBreakState{OnRequest: onReq, OnResponse: onResp}
}

// ---- URL 断点规则 ----

func (b *Bridge) GetBreakRules() []*pipeline.BreakRule {
	return b.app.Pipeline.Breakpoints().ListRules()
}
func (b *Bridge) AddBreakRule(url string, onRequest, onResponse bool) *pipeline.BreakRule {
	return b.app.Pipeline.Breakpoints().AddRule(url, onRequest, onResponse)
}
func (b *Bridge) UpdateBreakRule(id, url string, onRequest, onResponse, enabled bool) bool {
	return b.app.Pipeline.Breakpoints().UpdateRule(id, url, onRequest, onResponse, enabled)
}
func (b *Bridge) ToggleBreakRule(id string, enabled bool) bool {
	return b.app.Pipeline.Breakpoints().ToggleRule(id, enabled)
}
func (b *Bridge) DeleteBreakRule(id string) { b.app.Pipeline.Breakpoints().DeleteRule(id) }
