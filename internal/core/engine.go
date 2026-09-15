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
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/mintfog/sniffy/ca"
	"github.com/mintfog/sniffy/capture"
	httpproc "github.com/mintfog/sniffy/capture/processors/http"
	"github.com/mintfog/sniffy/capture/types"
	"github.com/mintfog/sniffy/internal/bodycache"
	"github.com/mintfog/sniffy/internal/forward"
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
	// upstreamProxy 持有当前上游代理地址(nil = 直连)。由 SetUpstreamProxy 原子写入,
	// 被 upstream 客户端 Transport 的 Proxy 闭包并发读取,故全程无锁竞态,可运行时即时切换。
	upstreamProxy atomic.Pointer[url.URL]
	listener      *capture.TCPListener
	bus           *EventBus
	logger        types.Logger
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
	if e.upstream == nil {
		e.upstream = e.buildUpstreamClient()
	}

	// 把引擎拥有的 CA 与上游客户端注入处理器,确立所有权。
	httpproc.SetCA(e.ca)
	httpproc.SetOutboundTLSPolicy(&e.tlsPolicy)
	httpproc.SetUpstreamClient(e.upstream)
	e.upstreamStream = httpproc.StreamClientFrom(e.upstream)

	e.listener = capture.NewTCPListener(config)
	if e.logger != nil {
		e.listener.SetLogger(e.logger)
	}
	return e, nil
}

// buildUpstreamClient 为严格与例外目标创建独立连接池,共享可动态切换的上游代理。
func (e *Engine) buildUpstreamClient() *http.Client {
	proxy := func(*http.Request) (*url.URL, error) { return e.upstreamProxy.Load(), nil }
	buildTransport := func(insecure bool) http.RoundTripper {
		tlsCfg := &tls.Config{}
		if insecure {
			tlsCfg = e.tlsPolicy.InsecureTLSConfig()
		}
		// 标准 Transport:作为「无法保真转发」时的回退(h2、Upgrade、超大头、握手失败等)。
		fallback := &http.Transport{
			// 每次请求读取当前上游代理(nil 表示直连);写入由 SetUpstreamProxy 原子完成。
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
		var tlsConfigForHost func(string) *tls.Config
		if insecure {
			e.tlsPolicy.ConfigureInsecureHTTPTransport(fallback)
			tlsConfigForHost = e.tlsPolicy.ConfigForHost
		}

		// 无侵入保真转发:HTTP/1.x 请求按客户端原始头顺序/大小写写线,绕开 http.Transport 的
		// 排序/规范化/注入;无法保真的情形自动回退到上面的 fallback。
		return forward.New(forward.Config{
			Fallback:          fallback,
			Proxy:             proxy,
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
	if changed && e.upstream != nil {
		if tr, ok := e.upstream.Transport.(interface{ CloseIdleConnections() }); ok {
			tr.CloseIdleConnections()
		}
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
// addr 为空表示直连;不含 scheme 时默认按 http:// 解析。仅在地址实际变更时清理旧连接池。
// 注:仅对引擎自建的上游客户端有效;经 WithUpstreamClient 注入的自定义客户端不受影响。
func (e *Engine) SetUpstreamProxy(addr string) error {
	addr = strings.TrimSpace(addr)
	var next *url.URL
	if addr != "" {
		if !strings.Contains(addr, "://") {
			addr = "http://" + addr
		}
		u, err := url.Parse(addr)
		if err != nil {
			return err
		}
		next = u
	}
	prev := e.upstreamProxy.Swap(next)
	// 直通隧道(不解密的 CONNECT)不经上游客户端,单独同步裸 URL 供其建 CONNECT。
	httpproc.SetUpstreamProxyURL(next)
	if !sameURL(prev, next) {
		// 切换代理后丢弃指向旧上游的空闲连接(forward.Transport.CloseIdleConnections
		// 会一并清理其内部回退 *http.Transport 的空闲连接)。
		if tr, ok := e.upstream.Transport.(interface{ CloseIdleConnections() }); ok {
			tr.CloseIdleConnections()
		}
	}
	return nil
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

// UpstreamClient 返回引擎持有的上游 HTTP 客户端。
func (e *Engine) UpstreamClient() *http.Client { return e.upstream }

// StreamUpstreamClient 返回不设总超时的上游客户端,供构造器发起的 SSE 等长连接使用。
// 与 UpstreamClient 共享 Transport,故上游代理切换对它同样即时生效。
func (e *Engine) StreamUpstreamClient() *http.Client { return e.upstreamStream }

// UpstreamProxyURL 返回当前上游代理地址的副本(nil = 直连),供不经 UpstreamClient 的出站
// 连接自行建隧道(构造器的 WebSocket 客户端用 gorilla Dialer,不吃 Transport 的 Proxy 闭包)。
// 返回副本而非原指针:该指针被 Transport 的 Proxy 闭包并发读,交出去等于让调用方能改写它。
func (e *Engine) UpstreamProxyURL() *url.URL {
	u := e.upstreamProxy.Load()
	if u == nil {
		return nil
	}
	cp := *u
	return &cp
}

// Listener 返回底层 TCP 监听器(过渡期暴露,后续逐步收敛)。
func (e *Engine) Listener() *capture.TCPListener { return e.listener }

// Config 返回引擎配置。
func (e *Engine) Config() types.Config { return e.config }
