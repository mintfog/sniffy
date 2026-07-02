// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build desktop

package desktop

import (
	"context"
	"errors"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/mintfog/sniffy/internal/app"
	"github.com/mintfog/sniffy/internal/core"
	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/internal/netinfo"
	"github.com/mintfog/sniffy/internal/pipeline"
	"github.com/mintfog/sniffy/internal/service"
	"github.com/mintfog/sniffy/internal/sysproxy"
)

// sysProxyHost 是系统代理指向的本机地址:引擎绑定 0.0.0.0,但系统代理走回环。
const sysProxyHost = "127.0.0.1"

// errPluginsUnavailable 在插件管理器未装配时返回给前端。
var errPluginsUnavailable = errors.New("插件子系统不可用")

// Bridge 是桌面(Wails v3)transport:把 service 的领域方法绑定给前端,并用 Wails 事件做实时推送。
// 它与 internal/api(headless)平行,共享同一个 service。
//
// 作为 Wails v3 Service 注册(application.NewService),实现可选的
// ServiceStartup/ServiceShutdown 生命周期钩子。前端通过 Call.ByName 调用以下导出方法,
// 完整方法名为 "github.com/mintfog/sniffy/internal/desktop.Bridge.<方法名>"。
type Bridge struct {
	app    *app.App
	cancel func()

	// sysProxyMu 守护 sysProxyOn:记录我们是否已接管系统代理,供退出时清理判断。
	sysProxyMu sync.Mutex
	sysProxyOn bool
}

// New 创建桥接对象,并把「应用系统代理」回调注入 service。
func New(a *app.App) *Bridge {
	b := &Bridge{app: a}
	a.Service.SetSystemProxyApplier(b.applySystemProxy)
	return b
}

// applySystemProxy 按开关把系统代理指向引擎实际监听端口(127.0.0.1:port)或释放。
// 启用时先乐观置位,确保即便部分网络服务设置失败,退出时也会尝试清理。
func (b *Bridge) applySystemProxy(enabled bool) error {
	var err error
	if enabled {
		b.setSysProxyOn(true)
		err = sysproxy.Set(sysProxyHost, b.app.Engine.Config().GetPort())
	} else {
		// 仅在确实清除成功后才置位:Clear 失败时保持 sysProxyOn=true,让退出钩子兜底重试,
		// 避免系统代理残留指向已失效端口。
		if err = sysproxy.Clear(); err == nil {
			b.setSysProxyOn(false)
		}
	}
	if err != nil {
		b.app.Logger.Warn("应用系统代理(enabled=%v)失败: %v", enabled, err)
	}
	return err
}

func (b *Bridge) setSysProxyOn(v bool) {
	b.sysProxyMu.Lock()
	b.sysProxyOn = v
	b.sysProxyMu.Unlock()
}

// ServiceName 用于日志/调试。
func (b *Bridge) ServiceName() string { return "sniffy.Bridge" }

// ServiceStartup 由 Wails v3 在启动时调用:订阅引擎事件总线并转发为 Wails 事件,
// 并按「启动时自动启用」配置决定是否接管系统代理。
func (b *Bridge) ServiceStartup(_ context.Context, _ application.ServiceOptions) error {
	ch, cancel := b.app.Service.Bus().Subscribe()
	b.cancel = cancel
	go b.forwardEvents(ch)

	// 退出时已清除系统代理,故启动后是否生效完全由「自动启用」决定:开则 Set,关则保持直连。
	auto := b.app.Service.Config().AutoProxy
	if auto {
		_ = b.applySystemProxy(true)
	} else if sysproxy.PointsTo(sysProxyHost, b.app.Engine.Config().GetPort()) {
		// 上次异常退出可能残留指向本程序的系统代理;本次不自动启用,清掉以保持一致。
		if err := sysproxy.Clear(); err != nil {
			b.app.Logger.Warn("清理残留系统代理失败: %v", err)
		}
	}
	// 把存储的当前开关对齐为实际状态,供前端启动时回读展示。
	b.app.Service.SetSystemProxyState(auto)
	return nil
}

// ServiceShutdown 由 Wails v3 在退出时调用:停止转发、释放系统代理并关闭引擎。
func (b *Bridge) ServiceShutdown() error {
	if b.cancel != nil {
		b.cancel()
	}
	b.releaseSystemProxy()
	return b.app.Stop()
}

// releaseSystemProxy 退出前确保系统代理被释放:无论内存标志如何,只要系统代理当前
// 指向本程序监听端口就清除,避免退出后(含标志漂移或清除失败的情况)用户无法上网。
func (b *Bridge) releaseSystemProxy() {
	b.sysProxyMu.Lock()
	on := b.sysProxyOn
	b.sysProxyMu.Unlock()
	if !on && !sysproxy.PointsTo(sysProxyHost, b.app.Engine.Config().GetPort()) {
		return
	}
	if err := sysproxy.Clear(); err != nil {
		b.app.Logger.Warn("退出时清除系统代理失败: %v", err)
		return
	}
	b.setSysProxyOn(false)
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

// GetSessionBody 按需拉取某会话请求/响应体的原始字节(base64)与 MIME,供前端预览图片等
// 二进制内容。source 取 "request" | "response"。会话不存在或无对应消息时返回 nil。
func (b *Bridge) GetSessionBody(id, source string) *service.BodyDTO {
	dto, ok := b.app.Service.MessageBody(id, source)
	if !ok {
		return nil
	}
	return dto
}

func (b *Bridge) DeleteSession(id string) { b.app.Service.DeleteSession(id) }
func (b *Bridge) ClearSessions()          { b.app.Service.ClearSessions() }

// WSSessionPage 是分页 WebSocket 会话返回。
type WSSessionPage struct {
	Data  []service.WSSessionDTOType `json:"data"`
	Total int                        `json:"total"`
}

// GetWSSessions 回填已捕获的 WebSocket 会话(实时帧仍经 ws_message 事件推送)。
func (b *Bridge) GetWSSessions(page, pageSize int) WSSessionPage {
	list, total := b.app.Service.WSSessions(page, pageSize)
	return WSSessionPage{Data: list, Total: total}
}

// GetWSSession 返回单个 WebSocket 会话(含全部消息)。
func (b *Bridge) GetWSSession(id string) *service.WSSessionDTOType {
	s, ok := b.app.Service.WSSession(id)
	if !ok {
		return nil
	}
	return &s
}

// StreamSessionPage 是分页流式会话返回。
type StreamSessionPage struct {
	Data  []service.StreamSessionDTOType `json:"data"`
	Total int                            `json:"total"`
}

// GetStreamSessions 回填已捕获的流式会话(SSE / gRPC / 分块流;实时消息经 stream_message 事件推送)。
func (b *Bridge) GetStreamSessions(page, pageSize int) StreamSessionPage {
	list, total := b.app.Service.StreamSessions(page, pageSize)
	return StreamSessionPage{Data: list, Total: total}
}

// GetStreamSession 返回单个流式会话(含全部消息)。
func (b *Bridge) GetStreamSession(id string) *service.StreamSessionDTOType {
	s, ok := b.app.Service.StreamSession(id)
	if !ok {
		return nil
	}
	return &s
}

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

// GetLANIPs 枚举本机所有可用内网 IPv4 候选(推荐项在前),供前端在代理监听地址里展示;
// 多网卡(同时连 WiFi 与有线、或叠加 VPN/虚拟网卡)时据此提示并让用户自选要暴露给同网段设备的地址。
func (b *Bridge) GetLANIPs() []netinfo.LANAddr { return netinfo.LANIPs() }

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

// InstallCAToSystem 把当前根 CA 写入本机信任库(macOS 用户级登录钥匙串,支持 Touch ID),
// 授权对话框由平台层弹出。
func (b *Bridge) InstallCAToSystem() error { return b.app.InstallCAToSystem() }

// ---- 插件 ----

func (b *Bridge) GetPlugins() []map[string]any {
	if b.app.Plugins == nil {
		return nil
	}
	return b.app.Plugins.ListPlugins()
}
func (b *Bridge) EnablePlugin(id string, enabled bool) error {
	if b.app.Plugins == nil {
		return errPluginsUnavailable
	}
	return b.app.Plugins.EnablePlugin(id, enabled)
}
func (b *Bridge) GetPluginSource(id string) string {
	if b.app.Plugins == nil {
		return ""
	}
	src, _ := b.app.Plugins.GetPluginSource(id)
	return src
}
func (b *Bridge) SavePluginSource(id, source string) error {
	if b.app.Plugins == nil {
		return errPluginsUnavailable
	}
	return b.app.Plugins.SavePluginSource(id, source)
}

// CreatePlugin 在用户插件目录下新建一个插件并加载,返回新建的 manifest。
func (b *Bridge) CreatePlugin(meta map[string]any, source string) (map[string]any, error) {
	if b.app.Plugins == nil {
		return nil, errPluginsUnavailable
	}
	return b.app.Plugins.CreatePlugin(meta, source)
}

// DeletePlugin 删除一个插件(实例 + 磁盘目录)。
func (b *Bridge) DeletePlugin(id string) error {
	if b.app.Plugins == nil {
		return errPluginsUnavailable
	}
	return b.app.Plugins.DeletePlugin(id)
}

// UpdatePluginManifest 更新插件 manifest 的可编辑字段并热重载。
func (b *Bridge) UpdatePluginManifest(id string, patch map[string]any) error {
	if b.app.Plugins == nil {
		return errPluginsUnavailable
	}
	return b.app.Plugins.UpdateManifest(id, patch)
}

// ClearPluginLogs 清空指定插件的日志缓冲。
func (b *Bridge) ClearPluginLogs(id string) error {
	if b.app.Plugins == nil {
		return errPluginsUnavailable
	}
	return b.app.Plugins.ClearPluginLogs(id)
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
