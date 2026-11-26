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
	"cmp"
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mintfog/sniffy/ca"
	"github.com/mintfog/sniffy/capture"
	httpproc "github.com/mintfog/sniffy/capture/processors/http"
	"github.com/mintfog/sniffy/capture/types"
	"github.com/mintfog/sniffy/internal/bodycache"
	"github.com/mintfog/sniffy/internal/forward"
	"github.com/mintfog/sniffy/internal/netbind"
	"github.com/mintfog/sniffy/internal/outboundtls"
	"github.com/mintfog/sniffy/internal/pipeline"
	"github.com/mintfog/sniffy/internal/procinfo"
)

// Engine 抓包引擎。
type Engine struct {
	config    types.Config
	caMu      sync.RWMutex
	ca        ca.CA
	upstream  *http.Client
	tlsPolicy outboundtls.Policy
	// upstreamStream 与 upstream 共享 Transport 但不设总超时:SSE / WebSocket 这类长连接
	// 不能被 Client.Timeout 打断(该计时器在 Do 返回后仍覆盖 Body 读取)。
	upstreamStream *http.Client
	// upstreamProxy 持有当前上游代理(nil = 直连)。由 SetUpstreamProxy、BindOutbound 与后台解析在 upstreamMu
	// 下原子写入,被 upstream 客户端 Transport 的 Proxy 闭包并发读取,可运行时即时切换。
	upstreamProxy atomic.Pointer[httpproc.UpstreamProxy]
	listener      *capture.TCPListener
	bus           *EventBus
	logger        types.Logger

	// upstreamMu 串行化上游代理的写入:引擎与直通隧道各持一份,要一起换。
	upstreamMu sync.Mutex
	// outboundBound 表示出站绑定了物理网卡,即虚拟网卡开着;resolvePending 表示当前的判定还在等
	// 后台解析上游代理的域名,resolving 表示后台解析在跑(见 resolveLaterLocked)。都由 upstreamMu 保护。
	outboundBound  bool
	resolvePending bool
	resolving      bool
	// lookupHost 解析域名形式的上游代理地址,nil 时用系统默认的解析方式;resolveRetry 是后台解析
	// 的间隔,0 时取 upstreamResolveRetry。测试替换它们。
	lookupHost   func(ctx context.Context, host string) ([]netip.Addr, error)
	resolveRetry time.Duration
}

// NewEngine 构造引擎:注入 CA 与上游客户端,把它们交给 http 处理器,
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
		return nil, errors.New("未配置根 CA")
	}
	e.listener = capture.NewTCPListener(config)
	if e.logger != nil {
		e.listener.SetLogger(e.logger)
	}
	if e.upstream == nil {
		e.upstream = e.buildUpstreamClient()
	}

	// 把引擎拥有的 CA 与上游客户端注入处理器,确立所有权。
	httpproc.SetCA(e.ca)
	httpproc.SetOutboundTLSPolicy(&e.tlsPolicy)
	httpproc.SetDialContext(e.listener.DialContext)
	httpproc.SetUpstreamClient(e.upstream)
	e.upstreamStream = httpproc.StreamClientFrom(e.upstream)

	return e, nil
}

// buildUpstreamClient 为严格与例外目标创建独立连接池,共享可动态切换的上游代理。
func (e *Engine) buildUpstreamClient() *http.Client {
	proxy := func(req *http.Request) (*url.URL, error) {
		return e.upstreamProxy.Load().For(types.IsTransparent(req.Context())), nil
	}
	buildTransport := func(insecure bool) http.RoundTripper {
		tlsCfg := &tls.Config{}
		if insecure {
			tlsCfg = e.tlsPolicy.InsecureTLSConfig()
		}
		// 标准 Transport:作为「无法保真转发」时的回退(h2、Upgrade、超大头、握手失败等)。
		fallback := &http.Transport{
			// 每次请求读取当前上游代理(nil 表示直连),写入方见 Engine.upstreamProxy。
			Proxy:           proxy,
			TLSClientConfig: tlsCfg.Clone(),
			// 自定义 TLSClientConfig 会让 net/http 默认禁用 HTTP/2;显式开启,使代理可对
			// h2(乃至 h2-only 的 gRPC)源站协商 HTTP/2 并捕获其响应/尾部。
			ForceAttemptHTTP2: true,
			// MITM 代理必须忠实转发:Go 默认会给没带 Accept-Encoding 的请求注入 gzip,
			// 这会让上游看到客户端从未发过的头,破坏 App 的签名/防篡改校验(表现为"参数错误")。
			// 关掉自动压缩后,客户端的 Accept-Encoding 原样透传;响应体由 flow 层按实际编码解码。
			DisableCompression:    true,
			MaxIdleConns:          httpproc.MaxIdleConns,
			MaxIdleConnsPerHost:   httpproc.MaxIdleConnsPerHost,
			MaxConnsPerHost:       httpproc.MaxConnsPerHost,
			IdleConnTimeout:       httpproc.IdleConnTimeout,
			DisableKeepAlives:     false,
			TLSHandshakeTimeout:   httpproc.TLSHandshakeTimeout,
			ResponseHeaderTimeout: httpproc.ResponseHeaderTimeout,
			ExpectContinueTimeout: httpproc.ExpectContinueTimeout,
		}
		if e.listener != nil {
			fallback.DialContext = e.listener.DialContext
		}
		var tlsConfigForHost func(string) *tls.Config
		if insecure {
			e.tlsPolicy.ConfigureInsecureHTTPTransport(fallback)
			tlsConfigForHost = e.tlsPolicy.ConfigForHost
		}

		// 无侵入保真转发:HTTP/1.x 请求按客户端原始头顺序/大小写写线,绕开 http.Transport 的
		// 排序/规范化/注入;无法保真的情形自动回退到上面的 fallback。
		return forward.New(forward.Config{
			Fallback:          forward.IsolateByOriginalDst(fallback),
			Proxy:             proxy,
			DialContext:       fallback.DialContext,
			TLSClientConfig:   tlsCfg,
			TLSConfigForHost:  tlsConfigForHost,
			DialTimeout:       httpproc.TLSHandshakeTimeout,
			TLSTimeout:        httpproc.TLSHandshakeTimeout,
			RespHeaderTimeout: httpproc.ResponseHeaderTimeout,
			IdleConnTimeout:   httpproc.IdleConnTimeout,
			MaxIdlePerHost:    httpproc.MaxIdleConnsPerHost,
			Disabled:          faithfulDisabled(),
		})
	}
	strictTransport := buildTransport(false)
	exceptionTransport := buildTransport(true)
	return &http.Client{
		Transport: outboundtls.NewTransport(&e.tlsPolicy, strictTransport, exceptionTransport),
		// 30x 一律原样交回客户端,不代替它跟随:代跟随既让那一跳在抓包里彻底消失
		// (客户端只看到最终响应),又会因为保真路径逐字重放 ctx 里属于**上一跳**的有序头,
		// 把 Authorization / Cookie 连同旧 Host 送给新主机 —— 而 net/http 自己跟随时
		// 是会剥离跨站敏感头的。让客户端自己跟随,每一跳还能各记一条 flow。
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Timeout:       httpproc.ClientTimeout,
	}
}

// SetTLSInsecureHosts 仅为精确主机允许调试证书例外;撤销后新请求不能复用例外连接池。
func (e *Engine) SetTLSInsecureHosts(hosts []string) error {
	changed, err := e.tlsPolicy.SetInsecureHosts(hosts)
	if err != nil {
		return err
	}
	httpproc.SetOutboundTLSPolicy(&e.tlsPolicy)
	if changed {
		e.closeIdleUpstream()
	}
	return nil
}

// OutboundTLSConfig 返回实际拨号主机的 TLS 配置;nil 引擎使用系统信任库。
func (e *Engine) OutboundTLSConfig(host string) *tls.Config {
	if e == nil {
		return (*outboundtls.Policy)(nil).ConfigForHost(host)
	}
	return e.tlsPolicy.ConfigForHost(host)
}

// faithfulDisabled 读取运维兜底开关:SNIFFY_FAITHFUL=0/false/off 时禁用保真转发,全部走标准 Transport。
func faithfulDisabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("SNIFFY_FAITHFUL"))) {
	case "0", "false", "off", "no":
		return true
	}
	return false
}

// SetUpstreamProxy 设置(或清除)上游代理,运行时即时生效、并发安全。
// addr 为空表示直连;不含 scheme 时默认按 http:// 解析。地址实际变更时才换上新的上游(见
// setUpstreamLocked)并清理旧连接池。
// 注:仅对引擎自建的上游客户端有效;经 WithUpstreamClient 注入的自定义客户端不受影响。
func (e *Engine) SetUpstreamProxy(addr string) error {
	addr = strings.TrimSpace(addr)
	var u *url.URL
	if addr != "" {
		if !strings.Contains(addr, "://") {
			addr = "http://" + addr
		}
		parsed, err := url.Parse(addr)
		if err != nil {
			return err
		}
		u = parsed
	}
	e.upstreamMu.Lock()
	defer e.upstreamMu.Unlock()
	prev := e.upstreamProxy.Load()
	// 地址没变时沿用原来的判定:每次保存配置都会走到这里,开着虚拟网卡时不必每次都解析一遍。
	if sameURL(prev.URL(), u) {
		return nil
	}
	e.setUpstreamLocked(u)
	if e.outboundBound {
		e.logJudgmentChange(prev, e.upstreamProxy.Load())
	}
	// 切换代理后丢弃指向旧上游的空闲连接。
	e.closeIdleUpstream()
	return nil
}

const (
	// upstreamResolveTimeout 是判定上游代理是否在本机时解析其域名的时限。
	upstreamResolveTimeout = 2 * time.Second
	// upstreamResolveRetry 是后台解析上游代理域名的间隔。
	upstreamResolveRetry = time.Second
)

// setUpstreamLocked 换上上游代理 u 并判定它是否在本机,此前等着的后台解析随之作废。是否在本机只对
// 经虚拟网卡的连接有影响:虚拟网卡没开时只看地址字面,开着时还解析域名。解析失败时先按在本机处理,
// 由后台重试(见 resolveLaterLocked)。调用方持有 upstreamMu。
func (e *Engine) setUpstreamLocked(u *url.URL) {
	e.resolvePending = false
	if !e.outboundBound {
		e.storeUpstreamLocked(httpproc.NewUpstreamProxy(u))
		return
	}
	p, err := e.resolveUpstream(u)
	e.storeUpstreamLocked(p)
	if err == nil {
		return
	}
	if e.logger != nil {
		e.logger.Warn("解析上游代理 %s 失败，按在本机处理、每秒重试，其间经虚拟网卡的连接直接连出、不交给它: %v", u.Host, err)
	}
	e.resolveLaterLocked()
}

// storeUpstreamLocked 换上判定好的上游代理 p。调用方持有 upstreamMu。
func (e *Engine) storeUpstreamLocked(p *httpproc.UpstreamProxy) {
	e.upstreamProxy.Store(p)
	// 直通隧道(不解密的 CONNECT)不经上游客户端,单独同步一份供其建 CONNECT。
	httpproc.SetUpstreamProxy(p)
}

// resolveUpstream 解析上游代理 u 的域名(最多等 upstreamResolveTimeout)并判定它是否在本机,见
// httpproc.ResolveUpstreamProxy。
func (e *Engine) resolveUpstream(u *url.URL) (*httpproc.UpstreamProxy, error) {
	lookup := e.lookupHost
	if lookup == nil {
		lookup = func(ctx context.Context, host string) ([]netip.Addr, error) {
			return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), upstreamResolveTimeout)
	defer cancel()
	return httpproc.ResolveUpstreamProxy(ctx, u, lookup)
}

// resolveLaterLocked 让后台每隔 resolveRetry 解析一次当前上游代理的域名,解析成功后按结果判定;
// 换了上游、重新判定过或关闭虚拟网卡后停止。同一时刻只有一个在跑。调用方持有 upstreamMu。
func (e *Engine) resolveLaterLocked() {
	e.resolvePending = true
	if e.resolving {
		return
	}
	e.resolving = true
	go e.resolveInBackground()
}

// resolveInBackground 是 resolveLaterLocked 起的后台解析,resolvePending 清掉后退出。解析在
// upstreamMu 之外进行,不耽误出口跟随与保存配置。
func (e *Engine) resolveInBackground() {
	interval := cmp.Or(e.resolveRetry, upstreamResolveRetry)
	for {
		time.Sleep(interval)
		e.upstreamMu.Lock()
		if !e.resolvePending {
			e.resolving = false
			e.upstreamMu.Unlock()
			return
		}
		p := e.upstreamProxy.Load()
		e.upstreamMu.Unlock()
		if next, err := e.resolveUpstream(p.URL()); err == nil {
			e.applyResolved(p, next)
		}
	}
}

// applyResolved 用后台解析 p 得到的 next 换下 p。解析期间换过上游或重新判定过的,结果作废。
func (e *Engine) applyResolved(p, next *httpproc.UpstreamProxy) {
	e.upstreamMu.Lock()
	defer e.upstreamMu.Unlock()
	if !e.resolvePending || e.upstreamProxy.Load() != p {
		return
	}
	e.resolvePending = false
	e.storeUpstreamLocked(next)
	e.logJudgmentChange(p, next)
}

// logJudgmentChange 记下开着虚拟网卡期间上游代理判定的变化:换上的上游在本机,或同一个上游的判定
// 翻转。开启时的判定由 TUN 的状态日志记下,解析失败时的推定由 setUpstreamLocked 记下,这里都不记。
func (e *Engine) logJudgmentChange(prev, next *httpproc.UpstreamProxy) {
	if e.logger == nil || next == nil || next.Unresolved() {
		return
	}
	sameUpstream := sameURL(prev.URL(), next.URL())
	switch {
	case next.Local() && (!sameUpstream || !prev.Local()):
		e.logger.Info("上游代理 %s 在本机：经虚拟网卡的连接直接连出、不交给它，以免它自己的出站再进虚拟网卡形成回环", next.URL().Host)
	case !next.Local() && sameUpstream && prev.Local():
		e.logger.Info("上游代理 %s 不在本机，经虚拟网卡的连接交给它", next.URL().Host)
	}
}

// BindOutbound 改变 sniffy 自己出站的网卡绑定，见 capture.TCPListener.BindOutbound，并丢弃上游
// 客户端的空闲连接：它们经原来的网卡建立，原网卡断开后复用会挂到 TCP 超时。
//
// 绑定到物理网卡时（开启虚拟网卡、出口变化）重新判定上游代理是否在本机：本机的网卡地址此时可能
// 已经变了，如开机时 Wi-Fi 还没连上、上游填的是本机的局域网地址。开启时这一步先于添加路由，解析
// 域名的查询不会进虚拟网卡。出口变化时路由已在，查询可能经虚拟网卡发出，而网卡一侧的 UDP 中继要等
// 随后的 tunhost.Host.SetEgress 才换到新出口（经 helper 时它不等换完就返回），当场解析多半要等到
// 超时；故地址字面的当场重新判定，域名沿用原来的判定、交给后台解析。
func (e *Engine) BindOutbound(egress netbind.Egress) {
	e.listener.BindOutbound(egress)
	e.upstreamMu.Lock()
	rebind := e.outboundBound
	e.outboundBound = egress.IPv4 != nil || egress.IPv6 != nil
	prev := e.upstreamProxy.Load()
	switch u := prev.URL(); {
	case !e.outboundBound:
		e.resolvePending = false
	case rebind && httpproc.UpstreamNeedsLookup(u):
		e.resolveLaterLocked()
	default:
		e.setUpstreamLocked(u)
		if rebind {
			e.logJudgmentChange(prev, e.upstreamProxy.Load())
		}
	}
	e.upstreamMu.Unlock()
	e.closeIdleUpstream()
}

// closeIdleUpstream 关闭上游客户端的空闲连接（forward.Transport.CloseIdleConnections 会一并清理
// 其内部回退 *http.Transport 的空闲连接）。
func (e *Engine) closeIdleUpstream() {
	if e.upstream == nil {
		return
	}
	if tr, ok := e.upstream.Transport.(interface{ CloseIdleConnections() }); ok {
		tr.CloseIdleConnections()
	}
}

// SetDecryptScope 下发 HTTPS 解密范围到 HTTP 处理器,运行时即时生效。
// enabled 为「启用 HTTPS MITM」总开关;mode 取 "all"/"allow"/"deny";allow/deny 为主机通配模式。
func (e *Engine) SetDecryptScope(enabled bool, mode string, allow, deny []string) error {
	httpproc.SetDecryptScope(enabled, mode, allow, deny)
	return nil
}

// SetProxyAuth 设置连到 Sniffy 的客户端所需的 Basic 认证(与上游代理凭据无关),对新连接
// 与新请求即时生效。已建立的 CONNECT 隧道不受影响:隧道内是不透明字节流,无从复检,
// 要撤销正在进行的访问只能断开对应连接。
func (e *Engine) SetProxyAuth(enabled bool, username, password string) error {
	httpproc.SetProxyAuth(enabled, username, password)
	return nil
}

// SetThrottle 下发全局网络限速开关与单连接速率到连接层。新旧连接都会在下一次读写时读取最新值。
func (e *Engine) SetThrottle(enabled bool, kibPerSecond int64) error {
	capture.SetThrottle(enabled, kibPerSecond*1024)
	return nil
}

// SetPassthrough 下发大体积 / 媒体响应透传旁路的开关与大小阈值,运行时即时生效
// (只影响此后到来的响应,进行中的转发不受影响)。
func (e *Engine) SetPassthrough(enabled bool, thresholdBytes int64) error {
	httpproc.SetPassthrough(enabled, thresholdBytes)
	return nil
}

// SetBodyCache 下发大体积响应体的落盘缓存到 HTTP 处理器。
func (e *Engine) SetBodyCache(c *bodycache.Cache) {
	httpproc.SetBodyCache(c)
}

// SetImportedServerCerts 下发用户导入的服务端证书到 HTTP 处理器,运行时即时生效。
// MITM 握手命中(按证书自身 SAN)的连接将呈给客户端这张真实证书,而非现签的伪造证书。
func (e *Engine) SetImportedServerCerts(certs []*tls.Certificate) error {
	httpproc.SetImportedServerCerts(certs)
	return nil
}

// sameURL 比较两个代理 URL 是否等价(含双 nil)。
func sameURL(a, b *url.URL) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.String() == b.String()
}

// SetPipeline 注入插件管道到 HTTP 处理器。
func (e *Engine) SetPipeline(p *pipeline.Pipeline) { httpproc.SetPipeline(p) }

// SetFlowSink 注入 flow 接收器(由 service 实现)到 HTTP 处理器。
func (e *Engine) SetFlowSink(s httpproc.FlowSink) { httpproc.SetFlowSink(s) }

// SetStreamSink 注入流式会话接收器(由 service 实现)到 HTTP 处理器。
func (e *Engine) SetStreamSink(s httpproc.StreamSink) { httpproc.SetStreamSink(s) }

// SetProcessResolver 注入进程解析器到 HTTP / WebSocket 处理器。
func (e *Engine) SetProcessResolver(r *procinfo.Resolver) { httpproc.SetProcessResolver(r) }

// Start 启动抓包监听。
func (e *Engine) Start() error { return e.listener.Start() }

// SetPortalInstallHandler 为连接指南接入桌面端的证书安装确认入口。
func (e *Engine) SetPortalInstallHandler(handler func()) {
	httpproc.SetPortalInstallHandler(handler)
}

// Stop 停止抓包监听。
func (e *Engine) Stop() error { return e.listener.Stop() }

// Bus 返回事件总线,供 service 层订阅。
func (e *Engine) Bus() *EventBus { return e.bus }

// CA 返回引擎持有的 CA,供 service 层导出证书等。
func (e *Engine) CA() ca.CA {
	e.caMu.RLock()
	defer e.caMu.RUnlock()
	return e.ca
}

// SetCA 热切换根 CA。持久化由 app 层负责，Engine 只管理运行时依赖。
func (e *Engine) SetCA(c ca.CA) error {
	if c == nil {
		return errors.New("根 CA 不能为空")
	}
	e.caMu.Lock()
	e.ca = c
	e.caMu.Unlock()
	httpproc.SetCA(c)
	return nil
}

// ForgetCertRejections 清除透明连接上应用拒绝证书的直通记录，根证书装入信任库后调用。
func (e *Engine) ForgetCertRejections() { httpproc.ForgetCertRejections() }

// SetTransparentCATrusted 按根证书是否被系统信任设置透明连接上的 TLS 是否解密，见
// httpproc.SetTransparentCATrusted。
func (e *Engine) SetTransparentCATrusted(trusted bool) { httpproc.SetTransparentCATrusted(trusted) }

// SetTransparentFilter 设置透明连接按所属应用的过滤器，见 capture.SetTransparentFilter。
func (e *Engine) SetTransparentFilter(f capture.TransparentFilter) { capture.SetTransparentFilter(f) }

// BypassHosts 返回透明连接直通名单里已生效的记录。
func (e *Engine) BypassHosts() []httpproc.BypassHost { return httpproc.BypassHosts() }

// ForgetBypass 删掉直通名单里 host 的记录。
func (e *Engine) ForgetBypass(host string) { httpproc.ForgetBypass(host) }

// ClearBypass 清空直通名单。
func (e *Engine) ClearBypass() { httpproc.ClearBypass() }

// UpstreamClient 返回引擎持有的上游 HTTP 客户端。
func (e *Engine) UpstreamClient() *http.Client { return e.upstream }

// StreamUpstreamClient 返回不设总超时的上游客户端,供构造器发起的 SSE 等长连接使用。
// 与 UpstreamClient 共享 Transport,故上游代理切换对它同样即时生效。
func (e *Engine) StreamUpstreamClient() *http.Client { return e.upstreamStream }

// UpstreamProxyURL 返回当前上游代理地址的副本(nil = 直连),供不经 UpstreamClient 的出站
// 连接自行建隧道(构造器的 WebSocket 客户端用 gorilla Dialer,不吃 Transport 的 Proxy 闭包)。
// 返回副本而非原指针:该指针被 Transport 的 Proxy 闭包并发读,交出去等于让调用方能改写它。
func (e *Engine) UpstreamProxyURL() *url.URL { return e.upstreamProxy.Load().URL() }

// LocalUpstreamProxy 返回确定在本机的上游代理地址副本,上游不在本机、直连,或只是解析域名失败后按在
// 本机处理时为 nil。经虚拟网卡的连接不交给在本机或按在本机处理的上游,见 httpproc.UpstreamProxy.For。
func (e *Engine) LocalUpstreamProxy() *url.URL {
	if p := e.upstreamProxy.Load(); p.Local() && !p.Unresolved() {
		return p.URL()
	}
	return nil
}

// Listener 返回底层 TCP 监听器(过渡期暴露,后续逐步收敛)。
func (e *Engine) Listener() *capture.TCPListener { return e.listener }

// Config 返回引擎配置。
func (e *Engine) Config() types.Config { return e.config }
