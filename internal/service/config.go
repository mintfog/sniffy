// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// configFileName 持久化配置在 configDir 下的文件名。
const configFileName = "config.json"

const (
	defaultThrottleKiBps int64 = 128
	minThrottleKiBps     int64 = 1
	maxThrottleKiBps     int64 = 1024 * 1024

	// defaultLargeBodyKiB 是透传旁路的默认大小阈值(2MiB):再大的体缓冲起来,
	// 首字节延迟与内存占用都开始明显。
	defaultLargeBodyKiB int64 = 2048
)

// AppConfig 对应前端 SniffyConfig 的核心字段(可持久化)。
type AppConfig struct {
	Port             int    `json:"port"`
	EnableHTTPS      bool   `json:"enableHTTPS"`
	Recording        bool   `json:"recording"`
	MaxFlows         int    `json:"maxFlows,omitempty"` // 会话存储容量上限;0 取默认值
	Upstream         bool   `json:"upstream"`           // 是否启用上游(二级)代理
	UpstreamAddr     string `json:"upstreamAddr"`       // 上游代理地址,如 http://host:port
	UpstreamAuth     bool   `json:"upstreamAuth"`       // 是否使用 Basic 账号密码认证
	UpstreamUsername string `json:"upstreamUsername"`
	UpstreamPassword string `json:"upstreamPassword"` // 不进对外视图,只以 upstreamPasswordSet 暴露
	ProxyAuth        bool   `json:"proxyAuth"`        // 是否要求连接 Sniffy 本地代理的客户端认证
	ProxyUsername    string `json:"proxyUsername"`
	ProxyPassword    string `json:"proxyPassword"`   // 不进对外视图,只以 proxyPasswordSet 暴露
	SystemProxy      bool   `json:"systemProxy"`     // 是否把本机系统代理指向 Sniffy 监听端口
	AutoProxy        bool   `json:"autoSystemProxy"` // 是否在每次启动时自动开启系统代理
	Throttle         bool   `json:"throttle"`        // 是否启用全局网络限速
	ThrottleKiBps    int64  `json:"throttleKiBps"`   // 每条连接每个方向的限速速率(KiB/s)
	// LargeBodyPassthrough 决定大体积 / 媒体响应是否走透传旁路:开启则边收边发、body 不进
	// 内存,此时插件只能改头、拿不到完整 body;关闭则一律走缓冲路径。
	LargeBodyPassthrough bool `json:"largeBodyPassthrough"`
	// LargeBodyKiB 是「按大小」触发旁路的阈值(KiB);媒体类型无视阈值一律走旁路。
	LargeBodyKiB int64 `json:"largeBodyKiB"`
	// RunInBackground 决定关闭主窗口的行为:true 隐藏到托盘保持后台运行(经托盘再打开),
	// false 则关闭 = 完全退出。仅桌面 transport 参考,headless 忽略。
	RunInBackground bool `json:"runInBackground"`
	// DecryptScope 为 HTTPS 解密范围:"all" 全部解密、"allow" 仅解密白名单、"deny" 白名单外全解密。
	// 空值按 "all" 处理。仅在 EnableHTTPS 为真时生效。
	DecryptScope string `json:"decryptScope,omitempty"`
	// DecryptAllow / DecryptDeny 为主机通配模式列表(支持 * 与 *.domain 匹配裸域+子域),
	// 分别在 allow / deny 模式下生效。
	DecryptAllow []string `json:"decryptAllow,omitempty"`
	DecryptDeny  []string `json:"decryptDeny,omitempty"`
	// Extra 保存前端可能附带的其它字段,原样回存。
	Extra map[string]any `json:"-"`
}

// defaultAppConfig 返回出厂配置。configStore 与 LoadSaved 都会把规范化结果整体写回
// config.json,必须以同一份默认值为底解码,否则文件里缺失的字段会被固化成零值。
func defaultAppConfig() AppConfig {
	return AppConfig{
		Port: 8080, EnableHTTPS: true, Recording: true, SystemProxy: true, AutoProxy: true,
		ThrottleKiBps: defaultThrottleKiBps, RunInBackground: true, DecryptScope: "all",
		LargeBodyPassthrough: true, LargeBodyKiB: defaultLargeBodyKiB,
	}
}

// ConfigView 是对外返回的配置视图。代理密码只在 Service 内部保存与使用,
// IPC/API 只返回是否已设置,避免把秘密复制到前端状态或网络响应中。
type ConfigView struct {
	Port                 int      `json:"port"`
	EnableHTTPS          bool     `json:"enableHTTPS"`
	Recording            bool     `json:"recording"`
	MaxFlows             int      `json:"maxFlows,omitempty"`
	Upstream             bool     `json:"upstream"`
	UpstreamAddr         string   `json:"upstreamAddr"`
	UpstreamAuth         bool     `json:"upstreamAuth"`
	UpstreamUsername     string   `json:"upstreamUsername"`
	UpstreamPasswordSet  bool     `json:"upstreamPasswordSet"`
	ProxyAuth            bool     `json:"proxyAuth"`
	ProxyUsername        string   `json:"proxyUsername"`
	ProxyPasswordSet     bool     `json:"proxyPasswordSet"`
	SystemProxy          bool     `json:"systemProxy"`
	AutoProxy            bool     `json:"autoSystemProxy"`
	Throttle             bool     `json:"throttle"`
	ThrottleKiBps        int64    `json:"throttleKiBps"`
	LargeBodyPassthrough bool     `json:"largeBodyPassthrough"`
	LargeBodyKiB         int64    `json:"largeBodyKiB"`
	RunInBackground      bool     `json:"runInBackground"`
	DecryptScope         string   `json:"decryptScope,omitempty"`
	DecryptAllow         []string `json:"decryptAllow,omitempty"`
	DecryptDeny          []string `json:"decryptDeny,omitempty"`
}

// PublicConfig 返回不含代理密码的配置视图,供 IPC/API 使用。
func PublicConfig(c AppConfig) ConfigView {
	return ConfigView{
		Port:                 c.Port,
		EnableHTTPS:          c.EnableHTTPS,
		Recording:            c.Recording,
		MaxFlows:             c.MaxFlows,
		Upstream:             c.Upstream,
		UpstreamAddr:         stripUpstreamUserinfo(c.UpstreamAddr),
		UpstreamAuth:         c.UpstreamAuth,
		UpstreamUsername:     c.UpstreamUsername,
		UpstreamPasswordSet:  c.UpstreamPassword != "",
		ProxyAuth:            c.ProxyAuth,
		ProxyUsername:        c.ProxyUsername,
		ProxyPasswordSet:     c.ProxyPassword != "",
		SystemProxy:          c.SystemProxy,
		AutoProxy:            c.AutoProxy,
		Throttle:             c.Throttle,
		ThrottleKiBps:        c.ThrottleKiBps,
		LargeBodyPassthrough: c.LargeBodyPassthrough,
		LargeBodyKiB:         c.LargeBodyKiB,
		RunInBackground:      c.RunInBackground,
		DecryptScope:         c.DecryptScope,
		DecryptAllow:         append([]string(nil), c.DecryptAllow...),
		DecryptDeny:          append([]string(nil), c.DecryptDeny...),
	}
}

// splitUpstreamUserinfo 拆出代理地址中内嵌的账号密码,并尽量保留原地址格式。
// 解析失败时原样返回,避免规范化抹掉用户输入的非法地址。
func splitUpstreamUserinfo(addr string) (clean, username, password string, removed bool) {
	trimmed := strings.TrimSpace(addr)
	if trimmed == "" {
		return trimmed, "", "", false
	}
	hadScheme := strings.Contains(trimmed, "://")
	parseAddr := trimmed
	if !hadScheme {
		parseAddr = "http://" + parseAddr
	}
	u, err := url.Parse(parseAddr)
	if err != nil || u.User == nil {
		return addr, "", "", false
	}
	username = u.User.Username()
	password, _ = u.User.Password()
	u.User = nil
	clean = u.String()
	if !hadScheme {
		clean = strings.TrimPrefix(clean, "http://")
	}
	return clean, username, password, true
}

// removeUpstreamUserinfo 是只关心剥离结果时的 splitUpstreamUserinfo 简写。
func removeUpstreamUserinfo(addr string) (string, bool) {
	clean, _, _, removed := splitUpstreamUserinfo(addr)
	return clean, removed
}

// stripUpstreamUserinfo 删除代理地址中的内嵌账号密码,并尽量保留原地址格式。
// 旧配置中的 userinfo 不能通过对外配置视图泄露。
func stripUpstreamUserinfo(addr string) string {
	if clean, changed := removeUpstreamUserinfo(addr); changed {
		return clean
	}
	trimmed := strings.TrimSpace(addr)
	if trimmed == "" {
		return trimmed
	}
	parseAddr := trimmed
	if !strings.Contains(trimmed, "://") {
		parseAddr = "http://" + parseAddr
	}
	if _, err := url.Parse(parseAddr); err != nil {
		// 解析失败时只能按字符串抹掉。不整串丢弃:IPv6 zone、未转义的 % 这类
		// 不含凭据的地址也解析不了,返回空串会让前端以为地址被清空。
		return dropUserinfo(trimmed)
	}
	return trimmed
}

// normalizeUpstreamConfig 把旧格式地址中的 userinfo 迁移到独立认证字段,并在关闭
// 认证时删除独立保存的密码;返回是否需要把结果写回配置文件。
// 旧版本只能靠地址内嵌凭据认证,单纯剥离会让升级后的上游认证静默失效。
// 独立字段已有值时以其为准,与 EffectiveUpstream 的优先级一致。
func normalizeUpstreamConfig(c *AppConfig) bool {
	changed := false
	if clean, user, pass, removed := splitUpstreamUserinfo(c.UpstreamAddr); removed {
		c.UpstreamAddr = clean
		if c.UpstreamUsername == "" && c.UpstreamPassword == "" && (user != "" || pass != "") {
			c.UpstreamAuth = true
			c.UpstreamUsername = user
			c.UpstreamPassword = pass
		}
		changed = true
	}
	if !c.UpstreamAuth && c.UpstreamPassword != "" {
		c.UpstreamPassword = ""
		changed = true
	}
	if !c.ProxyAuth && c.ProxyPassword != "" {
		c.ProxyPassword = ""
		changed = true
	}
	return changed
}

// EffectiveUpstream 返回实际生效的上游代理地址:开关关闭时为空(直连)。
// 启用认证时把账号密码编码进 URL userinfo,供标准转发、保真转发与直通 CONNECT
// 三条链路统一生成 Proxy-Authorization。
func (c AppConfig) EffectiveUpstream() string {
	if !c.Upstream {
		return ""
	}
	addr := strings.TrimSpace(c.UpstreamAddr)
	if addr == "" {
		return addr
	}
	hadScheme := strings.Contains(addr, "://")
	if !hadScheme {
		addr = "http://" + addr
	}
	u, err := url.Parse(addr)
	if err != nil {
		// 保留原地址,让引擎沿用既有的地址校验与错误处理。
		return c.UpstreamAddr
	}
	// 账号密码都为空时不能编码 userinfo:url.UserPassword("", "") 生成 "http://:@host",
	// 三条链路会据此发出 Basic Og==,足以让原本免认证的上游代理开始回 407。
	if c.UpstreamAuth && (c.UpstreamUsername != "" || c.UpstreamPassword != "") {
		u.User = url.UserPassword(c.UpstreamUsername, c.UpstreamPassword)
		return u.String()
	}
	// 不发凭据时也必须清掉地址中可能遗留的 userinfo,否则 net/http 与
	// CONNECT 代码会根据它自动发送旧的 Proxy-Authorization。
	if u.User == nil {
		return strings.TrimSpace(c.UpstreamAddr)
	}
	return stripUpstreamUserinfo(c.UpstreamAddr)
}

// dropUserinfo 与 redactUserinfo 同一套切分,但整段删掉凭据而不是留 "xxxxx@"。
// 对外视图会被前端回灌再下发,留下的哨兵会被当成合法 userinfo 迁成一个不存在的账号。
func dropUserinfo(addr string) string {
	start := 0
	if i := strings.Index(addr, "://"); i >= 0 {
		start = i + 3
	}
	rest := addr[start:]
	at := strings.LastIndex(rest, "@")
	if at < 0 {
		return addr
	}
	return addr[:start] + rest[at+1:]
}

// redactUserinfo 把地址里的 userinfo 换成 xxxxx@,只用于日志。调用点都是 url.Parse
// 已经失败的场景,拿不到 *url.URL,只能按字符串切分。
// 取整段的最后一个 @ 而非 authority 段内的:密码里未转义的 /?# 会把 authority 提前
// 截断,而这正是解析失败的成因;合法地址走不到这里,宁可多剥也不留明文凭据。
func redactUserinfo(addr string) string {
	start := 0
	if i := strings.Index(addr, "://"); i >= 0 {
		start = i + 3
	}
	rest := addr[start:]
	at := strings.LastIndex(rest, "@")
	if at < 0 {
		return addr
	}
	return addr[:start] + "xxxxx@" + rest[at+1:]
}

// RedactUpstreamError 抹掉 url.Error 原样回显的地址中的凭据。地址不可解析时凭据不会
// 被规范化剥离,不能就这么写进按天落盘的日志。
func RedactUpstreamError(err error) error {
	var uerr *url.Error
	if !errors.As(err, &uerr) {
		return err
	}
	return fmt.Errorf("%s %q: %w", uerr.Op, redactUserinfo(uerr.URL), uerr.Err)
}

type configStore struct {
	mu   sync.RWMutex
	cfg  AppConfig
	path string
}

func newConfigStore(path string, defaults AppConfig) *configStore {
	cs := &configStore{cfg: defaults, path: path}
	cs.load()
	return cs
}

func (cs *configStore) load() {
	if cs.path == "" {
		return
	}
	// 修复旧版本创建的 0644 配置文件,避免其中的代理密码继续对同机其他用户可读。
	_ = os.Chmod(cs.path, 0o600)
	// 以当前默认值为底解码,文件中缺失的字段保持默认而不是被清零。
	c := cs.cfg
	if readConfigFile(cs.path, &c) {
		normalized := normalizeUpstreamConfig(&c)
		if !validThrottleKiBps(c.ThrottleKiBps) {
			c.ThrottleKiBps = defaultThrottleKiBps
		}
		if c.LargeBodyKiB <= 0 {
			c.LargeBodyKiB = defaultLargeBodyKiB
		}
		cs.cfg = c
		if normalized {
			cs.save()
		}
	}
}

// readConfigFile 把 path 处的 JSON 配置解码到 into,成功返回 true。
func readConfigFile(path string, into *AppConfig) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return json.Unmarshal(data, into) == nil
}

// LoadSaved 读取 configDir 下持久化的 config.json。
// 文件不存在或解析失败时 ok 为 false。供装配层在引擎创建前取回上次保存的配置。
func LoadSaved(configDir string) (AppConfig, bool) {
	if configDir == "" {
		return AppConfig{}, false
	}
	path := filepath.Join(configDir, configFileName)
	_ = os.Chmod(path, 0o600)
	// 下面的迁移会把整个结构体写回文件,必须以出厂默认值为底解码。
	c := defaultAppConfig()
	if !readConfigFile(path, &c) {
		return AppConfig{}, false
	}
	if normalizeUpstreamConfig(&c) {
		saveConfigFile(path, c)
	}
	return c, true
}

func (cs *configStore) save() {
	saveConfigFile(cs.path, cs.cfg)
}

func saveConfigFile(path string, c AppConfig) {
	if path == "" {
		return
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		return
	}
	// 先收紧权限再截断/写入,避免旧文件在本次保存过程中继续暴露新密码。
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return
	}
	if err := f.Truncate(0); err == nil {
		_, _ = f.Write(data)
	}
	_ = f.Close()
}

func (cs *configStore) get() AppConfig {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	return cs.cfg
}

// setSystemProxy 仅更新并持久化系统代理当前开关,不触发任何应用动作。
// 供桌面装配层在启动时把状态对齐为「自动启用」的结果。
func (cs *configStore) setSystemProxy(on bool) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.cfg.SystemProxy = on
	cs.save()
}

// update 合并部分字段并持久化。
//
// 监听端口(port)允许前端修改并持久化:它是启动期确定的部署设置(默认值 <
// config.json < headless 命令行参数),运行时改了不会即时重新绑定,需重启后才生效
// (见 app.ResolveListen)。监听地址(host)固定由默认值/命令行参数决定,不接受 IPC 修改。
func (cs *configStore) update(patch map[string]any) AppConfig {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if v, ok := patch["port"].(float64); ok && int(v) >= 1 && int(v) <= 65535 {
		cs.cfg.Port = int(v)
	}
	if v, ok := patch["enableHTTPS"].(bool); ok {
		cs.cfg.EnableHTTPS = v
	}
	if v, ok := patch["recording"].(bool); ok {
		cs.cfg.Recording = v
	}
	if v, ok := patch["maxFlows"].(float64); ok && int(v) > 0 {
		cs.cfg.MaxFlows = int(v)
	}
	if v, ok := patch["upstream"].(bool); ok {
		cs.cfg.Upstream = v
	}
	if v, ok := patch["upstreamAddr"].(string); ok {
		cs.cfg.UpstreamAddr = v
	}
	if v, ok := patch["upstreamAuth"].(bool); ok {
		cs.cfg.UpstreamAuth = v
	}
	if v, ok := patch["upstreamUsername"].(string); ok {
		cs.cfg.UpstreamUsername = v
	}
	if v, ok := patch["upstreamPassword"].(string); ok {
		cs.cfg.UpstreamPassword = v
	}
	if v, ok := patch["proxyAuth"].(bool); ok {
		cs.cfg.ProxyAuth = v
	}
	if v, ok := patch["proxyUsername"].(string); ok {
		cs.cfg.ProxyUsername = v
	}
	if v, ok := patch["proxyPassword"].(string); ok {
		cs.cfg.ProxyPassword = v
	}
	if v, ok := patch["systemProxy"].(bool); ok {
		cs.cfg.SystemProxy = v
	}
	if v, ok := patch["autoSystemProxy"].(bool); ok {
		cs.cfg.AutoProxy = v
	}
	if v, ok := patch["throttle"].(bool); ok {
		cs.cfg.Throttle = v
	}
	if v, ok := patchInt64(patch["throttleKiBps"]); ok && validThrottleKiBps(v) {
		cs.cfg.ThrottleKiBps = v
	}
	if v, ok := patch["largeBodyPassthrough"].(bool); ok {
		cs.cfg.LargeBodyPassthrough = v
	}
	if v, ok := patchInt64(patch["largeBodyKiB"]); ok && v > 0 {
		cs.cfg.LargeBodyKiB = v
	}
	if v, ok := patch["runInBackground"].(bool); ok {
		cs.cfg.RunInBackground = v
	}
	if v, ok := patch["decryptScope"].(string); ok {
		cs.cfg.DecryptScope = v
	}
	if v, ok := patch["decryptAllow"]; ok {
		cs.cfg.DecryptAllow = toStringSlice(v)
	}
	if v, ok := patch["decryptDeny"]; ok {
		cs.cfg.DecryptDeny = toStringSlice(v)
	}
	normalizeUpstreamConfig(&cs.cfg)
	cs.save()
	return cs.cfg
}

func validThrottleKiBps(v int64) bool {
	return v >= minThrottleKiBps && v <= maxThrottleKiBps
}

func patchInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int64:
		return n, true
	case float64:
		i := int64(n)
		return i, float64(i) == n
	default:
		return 0, false
	}
}

// toStringSlice 把 JSON 解码得到的任意值(desktop/headless 均为 []any)规整为 []string,
// 忽略非字符串元素;其它类型返回 nil。
func toStringSlice(v any) []string {
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}
