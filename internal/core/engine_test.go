// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package core

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mintfog/sniffy/ca"
	httpproc "github.com/mintfog/sniffy/capture/processors/http"
	"github.com/mintfog/sniffy/capture/types"
	"github.com/mintfog/sniffy/internal/bodycache"
	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/internal/pipeline"
	"github.com/mintfog/sniffy/plugins"
)

type coreTestConfig struct {
	address string
	port    int
	threads int
}

var _ types.Config = (*coreTestConfig)(nil)

func (c *coreTestConfig) GetAddress() string             { return c.address }
func (c *coreTestConfig) GetPort() int                   { return c.port }
func (c *coreTestConfig) GetBufferSize() int             { return 4096 }
func (c *coreTestConfig) GetReadTimeout() time.Duration  { return 5 * time.Second }
func (c *coreTestConfig) GetWriteTimeout() time.Duration { return 5 * time.Second }
func (c *coreTestConfig) IsLoggingEnabled() bool         { return true }
func (c *coreTestConfig) GetThreads() int                { return c.threads }

type coreTestLogger struct{}

var _ types.Logger = (*coreTestLogger)(nil)

func (*coreTestLogger) Info(string, ...interface{})  {}
func (*coreTestLogger) Error(string, ...interface{}) {}
func (*coreTestLogger) Debug(string, ...interface{}) {}
func (*coreTestLogger) Warn(string, ...interface{})  {}

// closeTrackingTransport 记录连接池被清理的次数,用于校验代理切换只在地址真正变化时清池。
type closeTrackingTransport struct {
	closes int
}

func (*closeTrackingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("测试 transport 不发送请求")
}

func (t *closeTrackingTransport) CloseIdleConnections() { t.closes++ }

// roundTripOnlyTransport 刻意只实现 RoundTrip,用于覆盖 SetUpstreamProxy 中
// 「Transport 不具备 CloseIdleConnections」的那条分支。
type roundTripOnlyTransport struct{}

func (roundTripOnlyTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("测试 transport 不发送请求")
}

func newCoreTestCA(t *testing.T) ca.CA {
	t.Helper()
	rootCA, err := ca.NewInMemorySelfSignedCA()
	if err != nil {
		t.Fatalf("创建内存 CA 失败: %v", err)
	}
	return rootCA
}

func TestNewEngineRequiresExplicitCA(t *testing.T) {
	if _, err := NewEngine(nil); err == nil {
		t.Fatal("未注入根 CA 时应该拒绝创建引擎")
	}

	rootCA := newCoreTestCA(t)
	engine, err := NewEngine(nil, WithCA(rootCA))
	if err != nil {
		t.Fatalf("注入根 CA 后创建引擎失败: %v", err)
	}
	if engine.CA() != rootCA {
		t.Fatal("引擎未持有注入的根 CA")
	}
}

func TestNewEngineOptionsAndAccessors(t *testing.T) {
	cfg := &coreTestConfig{address: "127.0.0.1", port: 0, threads: 1}
	rootCA := newCoreTestCA(t)
	logger := &coreTestLogger{}
	upstream := &http.Client{Transport: roundTripOnlyTransport{}}

	engine, err := NewEngine(cfg, WithCA(rootCA), WithUpstreamClient(upstream), WithLogger(logger))
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	if engine.Config() != cfg {
		t.Errorf("Config() = %#v，期望注入的配置", engine.Config())
	}
	if engine.CA() != rootCA {
		t.Error("CA() 未返回注入的根 CA")
	}
	if engine.UpstreamClient() != upstream {
		t.Error("UpstreamClient() 未返回注入的客户端")
	}
	if engine.logger != logger {
		t.Error("未保留注入的 logger")
	}
	if engine.Bus() == nil {
		t.Error("Bus() 为 nil，事件总线未创建")
	}
	if engine.Listener() == nil {
		t.Error("Listener() 为 nil，监听器未创建")
	}

	// nil 执行器应被静默忽略而非 panic;非 nil 时下发到监听器与数据包处理器。
	engine.SetHookExecutor(nil)
	exec := plugins.NewHookExecutor(nil, nil)
	engine.SetHookExecutor(exec)
}

// TestEngineStartStopLifecycle 覆盖完整生命周期,重点是 Stop 之后重启:
// 重启曾经会绑上端口、IsRunning 报 true,但 accept 循环因 ctx 已取消而立即退出,
// 表现为静默假死,故这里用一次真实代理请求确认重启后确实在服务。
func TestEngineStartStopLifecycle(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("origin-ok"))
	}))
	defer origin.Close()

	cfg := &coreTestConfig{address: "127.0.0.1", port: 0, threads: 1}
	engine, err := NewEngine(cfg, WithCA(newCoreTestCA(t)), WithLogger(&coreTestLogger{}))
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	if err := engine.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = engine.Stop() })
	if !engine.Listener().IsRunning() {
		t.Fatal("Start 后监听器未运行")
	}
	if err := engine.Start(); err == nil {
		t.Fatal("重复 Start 应返回错误")
	}
	assertProxies(t, engine, origin.URL, "首次启动")

	if err := engine.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if engine.Listener().IsRunning() {
		t.Fatal("Stop 后监听器仍在运行")
	}
	if err := engine.Stop(); err != nil {
		t.Fatalf("重复 Stop 应幂等: %v", err)
	}

	if err := engine.Start(); err != nil {
		t.Fatalf("Stop 后重启: %v", err)
	}
	assertProxies(t, engine, origin.URL, "重启后")
}

// assertProxies 经引擎的监听端口发一次代理请求,确认它确实在转发而不只是端口开着。
func assertProxies(t *testing.T, engine *Engine, targetURL, stage string) {
	t.Helper()

	proxyURL, err := url.Parse("http://" + engine.Listener().GetAddress())
	if err != nil {
		t.Fatalf("%s: 解析监听地址失败: %v", stage, err)
	}
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
		Timeout:   10 * time.Second,
	}
	defer client.CloseIdleConnections()

	resp, err := client.Get(targetURL)
	if err != nil {
		t.Fatalf("%s: 经引擎代理请求失败(引擎未在服务?): %v", stage, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("%s: 读取响应体失败: %v", stage, err)
	}
	if resp.StatusCode != http.StatusOK || string(body) != "origin-ok" {
		t.Fatalf("%s: 响应 = %d/%q，期望 200/\"origin-ok\"", stage, resp.StatusCode, body)
	}
}

func TestEngineSetCA(t *testing.T) {
	initial := newCoreTestCA(t)
	replacement := newCoreTestCA(t)
	engine := &Engine{ca: initial}

	if err := engine.SetCA(nil); err == nil {
		t.Fatal("SetCA(nil) 应返回错误")
	}
	if engine.CA() != initial {
		t.Fatal("SetCA(nil) 不应修改现有 CA")
	}
	if err := engine.SetCA(replacement); err != nil {
		t.Fatalf("SetCA: %v", err)
	}
	if engine.CA() != replacement {
		t.Fatal("SetCA 后未返回新 CA")
	}
}

// TestEngineRuntimeSetters 只覆盖这组 setter 的参数边界:nil / 空参数既不应 panic
// 也不应报错(装配层在配置尚未就绪时就会这么调)。「配置确实下发到了下游包」是另一回事,
// 由本文件后面的到达性用例用真实代理请求反推——下游包的全局是私有的,跨包读不回来。
// 只有 SetProcessResolver 例外:procinfo 的解析器无从伪造,真解析又依赖运行环境的
// /proc 与 lsof 权限,放进到达性用例只会换来一条时灵时不灵的断言,故仅冒烟到这里。
func TestEngineRuntimeSetters(t *testing.T) {
	// 恢复必须先于变更注册:否则中途 t.Fatal 会把全局状态泄漏给同一二进制里的后续用例。
	t.Cleanup(restoreRuntimeDefaults)

	engine := &Engine{}
	if err := engine.SetDecryptScope(true, "allow", []string{"*.example.com"}, nil); err != nil {
		t.Fatalf("SetDecryptScope: %v", err)
	}
	if err := engine.SetThrottle(true, 64); err != nil {
		t.Fatalf("SetThrottle: %v", err)
	}
	if err := engine.SetPassthrough(false, 1024); err != nil {
		t.Fatalf("SetPassthrough: %v", err)
	}
	if err := engine.SetImportedServerCerts(nil); err != nil {
		t.Fatalf("SetImportedServerCerts: %v", err)
	}
	engine.SetBodyCache(nil)
	engine.SetPipeline(nil)
	engine.SetFlowSink(nil)
	engine.SetStreamSink(nil)
	engine.SetProcessResolver(nil)
}

// restoreRuntimeDefaults 把 TestEngineRuntimeSetters 与到达性用例改动过的全局恢复到
// 默认语义(解密范围默认为 nil 指针,行为上等价于 enabled+"all")。
func restoreRuntimeDefaults() {
	e := &Engine{}
	_ = e.SetDecryptScope(true, "all", nil, nil)
	_ = e.SetThrottle(false, 0)
	_ = e.SetPassthrough(true, httpproc.DefaultPassthroughThreshold)
	_ = e.SetImportedServerCerts(nil)
	e.SetBodyCache(nil)
	e.SetPipeline(nil)
	e.SetFlowSink(nil)
	e.SetStreamSink(nil)
	e.SetProcessResolver(nil)
}

// ==================== 下发型 setter 的到达性 ====================
//
// 这些 setter 都写向 httpproc / capture 的包级私有全局,跨包读不回来,所以下面一律用
// 一台真跑的引擎 + 一次真实代理请求反推:改成空实现时这些断言会失败。各项配置本身的
// 语义(通配匹配、旁路阈值判定、限速分块……)由对应包自己的测试覆盖,这里只问一件事:
// 引擎有没有把它交出去。

// probeFlowSink 记录 HTTP 处理器回调的 flow。
type probeFlowSink struct {
	completed chan *flow.Flow
}

func newProbeFlowSink() *probeFlowSink { return &probeFlowSink{completed: make(chan *flow.Flow, 8)} }

func (*probeFlowSink) RecordFlowStarted(*flow.Flow) {}
func (*probeFlowSink) RecordFlowUpdated(*flow.Flow) {}
func (s *probeFlowSink) RecordFlowCompleted(f *flow.Flow) {
	select {
	case s.completed <- f:
	default:
	}
}

// take 取一条已完成的 flow。sink 没被下发时这里永远等不到,故带超时。
func (s *probeFlowSink) take(t *testing.T, stage string) *flow.Flow {
	t.Helper()
	select {
	case f := <-s.completed:
		return f
	case <-time.After(5 * time.Second):
		t.Fatalf("%s: 5s 内没有收到完成的 flow，SetFlowSink 未下发到处理器", stage)
		return nil
	}
}

// probeStreamSink 记录流式(SSE / gRPC)会话快照。
type probeStreamSink struct {
	sessions chan *flow.StreamSession
}

func newProbeStreamSink() *probeStreamSink {
	return &probeStreamSink{sessions: make(chan *flow.StreamSession, 8)}
}

func (s *probeStreamSink) RecordStreamSession(ss *flow.StreamSession) {
	select {
	case s.sessions <- ss:
	default:
	}
}

func (s *probeStreamSink) take(t *testing.T, stage string) *flow.StreamSession {
	t.Helper()
	select {
	case ss := <-s.sessions:
		return ss
	case <-time.After(5 * time.Second):
		t.Fatalf("%s: 5s 内没有收到流式会话，SetStreamSink 未下发到处理器", stage)
		return nil
	}
}

// probeRequestHook 是注册进管道的核心钩子,记录它看到的请求 URL。
type probeRequestHook struct {
	mu   sync.Mutex
	urls []string
}

func (*probeRequestHook) Name() string      { return "engine-setter-probe" }
func (*probeRequestHook) Priority() int     { return 0 }
func (*probeRequestHook) Enabled() bool     { return true }
func (*probeRequestHook) Match(string) bool { return true }

func (h *probeRequestHook) OnRequest(_ context.Context, f *flow.Flow) flow.Decision {
	h.mu.Lock()
	h.urls = append(h.urls, f.Request.URL)
	h.mu.Unlock()
	return flow.ContinueDecision()
}

func (h *probeRequestHook) seen() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.urls...)
}

// connProbePlugin 是一个最小的原生插件,只数连接开始钩子被调用了几次。
type connProbePlugin struct{ starts atomic.Int64 }

func (*connProbePlugin) GetInfo() plugins.PluginInfo {
	return plugins.PluginInfo{Name: "conn-probe", Version: "1.0.0", Category: "test"}
}
func (*connProbePlugin) Initialize(context.Context, plugins.PluginConfig) error { return nil }
func (*connProbePlugin) Start(context.Context) error                            { return nil }
func (*connProbePlugin) Stop(context.Context) error                             { return nil }
func (*connProbePlugin) IsEnabled() bool                                        { return true }
func (*connProbePlugin) GetPriority() int                                       { return 0 }
func (p *connProbePlugin) OnConnectionStart(context.Context, types.Connection) error {
	p.starts.Add(1)
	return nil
}
func (*connProbePlugin) OnConnectionEnd(context.Context, types.Connection, time.Duration) error {
	return nil
}

// newProbeEngine 构造一台可代理真实请求的引擎,并登记「用例结束后复位包级全局」。
// 引擎尚未启动:下发型 setter 必须在 Start 之前调用,监听器的钩子执行器不是原子字段,
// 启动后再改会与 accept 协程竞态。
func newProbeEngine(t *testing.T) *Engine {
	t.Helper()
	t.Cleanup(restoreRuntimeDefaults)
	engine, err := NewEngine(&coreTestConfig{address: "127.0.0.1", port: 0, threads: 1},
		WithCA(newCoreTestCA(t)), WithLogger(&coreTestLogger{}))
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return engine
}

// startProbeEngine 启动引擎并返回一个经它代理的客户端。
func startProbeEngine(t *testing.T, engine *Engine) *http.Client {
	t.Helper()
	if err := engine.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = engine.Stop() })
	return newProxyClient(t, engine)
}

// newProxyClient 返回一个把请求都发给引擎监听端口的客户端。每次调用都是全新的连接池:
// 需要重新握手(如切换解密范围后)时不能复用上一个客户端缓存的隧道。
func newProxyClient(t *testing.T, engine *Engine) *http.Client {
	t.Helper()
	proxyURL, err := url.Parse("http://" + engine.Listener().GetAddress())
	if err != nil {
		t.Fatalf("解析监听地址失败: %v", err)
	}
	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
			// MITM 证书由测试用内存 CA 现签,客户端不校验。
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
		Timeout: 10 * time.Second,
	}
	t.Cleanup(client.CloseIdleConnections)
	return client
}

// fetch 经代理取一次 URL,返回响应(body 已读尽)与响应体。
func fetch(t *testing.T, client *http.Client, target, stage string) (*http.Response, []byte) {
	t.Helper()
	resp, err := client.Get(target)
	if err != nil {
		t.Fatalf("%s: 经引擎代理请求失败: %v", stage, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("%s: 读取响应体失败: %v", stage, err)
	}
	return resp, body
}

// TestEngineHTTPSettersReachProcessor 校验 SetFlowSink / SetPipeline / SetPassthrough /
// SetBodyCache / SetStreamSink 确实到达了 HTTP 处理器:一条 flow 要能回到注入的 sink,
// 一次请求要能经过注入的核心钩子,阈值下调要能把同一个响应从缓冲路径切到透传旁路,
// 旁路的 body 要落进注入的缓存目录,SSE 要能回到注入的流会话 sink。
func TestEngineHTTPSettersReachProcessor(t *testing.T) {
	const bodySize = 8 << 10
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sse" {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: probe\n\n")
			w.(http.Flusher).Flush()
			return
		}
		// 显式给长度:chunked(长度未知)的响应不参与按大小的旁路判定。
		w.Header().Set("Content-Length", strconv.Itoa(bodySize))
		_, _ = w.Write(bytes.Repeat([]byte("x"), bodySize))
	}))
	defer origin.Close()

	cacheDir := t.TempDir()
	cache, err := bodycache.New(cacheDir, 1<<20)
	if err != nil {
		t.Fatalf("创建 body 缓存: %v", err)
	}
	sink, streams, hook := newProbeFlowSink(), newProbeStreamSink(), &probeRequestHook{}
	p := pipeline.New(nil, nil)
	p.RegisterCore(hook)

	engine := newProbeEngine(t)
	engine.SetFlowSink(sink)
	engine.SetStreamSink(streams)
	engine.SetPipeline(p)
	engine.SetBodyCache(cache)
	if err := engine.SetPassthrough(true, 64<<10); err != nil { // 阈值高于响应体:走缓冲路径
		t.Fatalf("SetPassthrough: %v", err)
	}
	client := startProbeEngine(t, engine)

	_, body := fetch(t, client, origin.URL+"/buffered", "缓冲路径")
	if len(body) != bodySize {
		t.Fatalf("缓冲路径: 客户端收到 %d 字节，期望 %d", len(body), bodySize)
	}
	// 先问管道:没有管道的处理器会退化成不记 flow 的简单转发,不先分开断言的话
	// SetPipeline 丢失会伪装成 SetFlowSink 丢失。
	if len(hook.seen()) == 0 {
		t.Fatal("请求已完成却没经过注入的核心钩子，SetPipeline 未下发到处理器")
	}
	buffered := sink.take(t, "缓冲路径")
	if buffered.Response == nil {
		t.Fatal("缓冲路径: 完成的 flow 里没有响应")
	}
	if len(buffered.Response.Body) != bodySize {
		// 这里必须印 len(Body) 而非 BodyLen():阈值泄漏导致响应误走旁路时 Body 为空、
		// BodyLen() 却返回旁路记录的大小,印后者会得到「8192 字节，期望 8192」的自相矛盾信息。
		t.Fatalf("缓冲路径: flow 内响应体 %d 字节，期望 %d(阈值未下发?)", len(buffered.Response.Body), bodySize)
	}
	if path, _ := buffered.Response.BodyFile(); path != "" {
		t.Fatalf("缓冲路径不该落盘，实际 path=%q", path)
	}

	// 阈值下调到响应体以下:同一个响应改走透传旁路,body 不进内存而是落到缓存目录。
	if err := engine.SetPassthrough(true, 1<<10); err != nil {
		t.Fatalf("SetPassthrough: %v", err)
	}
	if _, body = fetch(t, client, origin.URL+"/passthrough", "透传旁路"); len(body) != bodySize {
		t.Fatalf("透传旁路: 客户端收到 %d 字节，期望 %d", len(body), bodySize)
	}
	passed := sink.take(t, "透传旁路")
	if passed.Metadata["passthrough"] != true {
		t.Fatal("阈值已下调到响应体以下,响应却仍走缓冲路径，SetPassthrough 未下发到处理器")
	}
	path, size := passed.Response.BodyFile()
	if path == "" {
		t.Fatal("透传旁路的 body 没有落盘，SetBodyCache 未下发到处理器")
	}
	if !strings.HasPrefix(path, cacheDir) {
		t.Fatalf("落盘路径 %q 不在注入的缓存目录 %q 下", path, cacheDir)
	}
	if size != bodySize {
		t.Fatalf("旁路记录的大小 = %d，期望 %d", size, bodySize)
	}
	if st, err := os.Stat(path); err != nil {
		t.Fatalf("落盘副本不可读: %v", err)
	} else if st.Size() != bodySize {
		t.Fatalf("落盘副本 %d 字节，期望 %d", st.Size(), bodySize)
	}

	fetch(t, client, origin.URL+"/sse", "流式")
	if ss := streams.take(t, "流式"); ss.Kind != flow.StreamSSE {
		t.Fatalf("流式会话类型 = %q，期望 %q", ss.Kind, flow.StreamSSE)
	}

	if seen := hook.seen(); len(seen) != 3 {
		t.Fatalf("核心钩子看到 %d 个请求(%v)，期望 3 个，SetPipeline 未下发到处理器", len(seen), seen)
	}
}

// TestEngineSetHookExecutorReachesListener 校验钩子执行器确实到达了监听器:
// 一次代理请求必须触发插件的连接开始钩子。
func TestEngineSetHookExecutorReachesListener(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("origin-ok"))
	}))
	defer origin.Close()

	logger := &coreTestLogger{}
	// 只用工厂插件,故插件目录给空目录即可(LoadPlugins 会扫描它)。
	manager := plugins.NewPluginManager(nil, logger, plugins.ManagerConfig{
		PluginsDir:  t.TempDir(),
		ConfigDir:   t.TempDir(),
		LoadTimeout: 5 * time.Second,
	})
	probe := &connProbePlugin{}
	manager.RegisterFactory("conn-probe", func(plugins.PluginAPI) plugins.Plugin { return probe })
	if err := manager.LoadPlugins(); err != nil {
		t.Fatalf("加载探针插件: %v", err)
	}

	engine := newProbeEngine(t)
	engine.SetHookExecutor(nil) // nil 应被静默忽略,不得清掉随后注入的执行器
	engine.SetHookExecutor(plugins.NewHookExecutor(manager, logger))
	client := startProbeEngine(t, engine)

	fetch(t, client, origin.URL, "连接钩子")

	if got := probe.starts.Load(); got == 0 {
		t.Fatal("代理请求已完成,连接开始钩子却一次都没被调用，SetHookExecutor 未下发到监听器")
	}
}

// TestEngineSetThrottleReachesConnLayer 校验限速确实到达了连接层。引擎按 KiB/s 收参、
// 按字节下发,故这里同时锁住换算:丢了 ×1024 会让传输慢上三个数量级,直接把客户端拖到超时。
func TestEngineSetThrottleReachesConnLayer(t *testing.T) {
	const bodySize, rateKiB = 16 << 10, 16
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("x"), bodySize))
	}))
	defer origin.Close()

	engine := newProbeEngine(t)
	if err := engine.SetThrottle(true, rateKiB); err != nil {
		t.Fatalf("SetThrottle: %v", err)
	}
	client := startProbeEngine(t, engine)

	start := time.Now()
	_, body := fetch(t, client, origin.URL, "限速")
	elapsed := time.Since(start)
	if len(body) != bodySize {
		t.Fatalf("限速下客户端收到 %d 字节，期望 %d", len(body), bodySize)
	}
	// 理论耗时 bodySize/rate = 1s;取 1/4 作下界容忍调度抖动,仍远高于不限速时的毫秒级。
	if floor := time.Second / 4; elapsed < floor {
		t.Fatalf("限速 %d KiB/s 传 %d 字节只用了 %s(<%s)，SetThrottle 未下发到连接层",
			rateKiB, bodySize, elapsed, floor)
	}
}

// TestEngineSetDecryptScopeReachesTunnel 校验解密范围确实到达了 CONNECT 分流:
// 范围外客户端应看到源站自己的证书(盲转发),范围内应看到引擎 CA 现签的证书(MITM)。
func TestEngineSetDecryptScopeReachesTunnel(t *testing.T) {
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("origin-ok"))
	}))
	defer origin.Close()

	engine := newProbeEngine(t)
	if err := engine.SetDecryptScope(true, "deny", nil, []string{"127.0.0.1"}); err != nil {
		t.Fatalf("SetDecryptScope: %v", err)
	}
	client := startProbeEngine(t, engine)

	resp, body := fetch(t, client, origin.URL, "范围外")
	if string(body) != "origin-ok" {
		t.Fatalf("范围外: 响应体 = %q，期望 origin-ok", body)
	}
	if !servedCert(t, resp, "范围外").Equal(origin.Certificate()) {
		t.Fatal("范围外主机应被盲转发,客户端却拿到了引擎签发的证书")
	}

	// 切到 all:同一台源站改为 MITM 解密。必须换新客户端,老客户端会复用已建好的隧道。
	if err := engine.SetDecryptScope(true, "all", nil, nil); err != nil {
		t.Fatalf("SetDecryptScope: %v", err)
	}
	resp, body = fetch(t, newProxyClient(t, engine), origin.URL, "范围内")
	if string(body) != "origin-ok" {
		t.Fatalf("范围内: 响应体 = %q，期望 origin-ok", body)
	}
	leaf := servedCert(t, resp, "范围内")
	if leaf.Equal(origin.Certificate()) {
		t.Fatal("范围内主机应被 MITM 解密,客户端却拿到了源站证书，SetDecryptScope 未下发到处理器")
	}
	if got, want := leaf.Issuer.CommonName, engine.CA().GetCA().Subject.CommonName; got != want {
		t.Fatalf("MITM 证书 Issuer = %q，期望引擎 CA 的 %q", got, want)
	}
}

// TestEngineSetImportedServerCertsReachHandshake 校验导入的服务端证书确实到达了 MITM 握手:
// 命中主机时客户端应看到导入的那张真实证书,而不是引擎 CA 现签的伪造证书。
func TestEngineSetImportedServerCertsReachHandshake(t *testing.T) {
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("origin-ok"))
	}))
	defer origin.Close()

	imported, leaf := newImportedCert(t, "sniffy-imported-probe")
	engine := newProbeEngine(t)
	if err := engine.SetDecryptScope(true, "all", nil, nil); err != nil { // 导入证书只在 MITM 时用得上
		t.Fatalf("SetDecryptScope: %v", err)
	}
	if err := engine.SetImportedServerCerts([]*tls.Certificate{imported}); err != nil {
		t.Fatalf("SetImportedServerCerts: %v", err)
	}
	client := startProbeEngine(t, engine)

	resp, body := fetch(t, client, origin.URL, "导入证书")
	if string(body) != "origin-ok" {
		t.Fatalf("导入证书: 响应体 = %q，期望 origin-ok", body)
	}
	if got := servedCert(t, resp, "导入证书"); !got.Equal(leaf) {
		t.Fatalf("握手呈上的证书 CN = %q，期望导入证书的 %q，SetImportedServerCerts 未下发到处理器",
			got.Subject.CommonName, leaf.Subject.CommonName)
	}
}

// newImportedCert 生成一张覆盖 127.0.0.1 的自签服务端证书,冒充用户导入的真实证书。
func newImportedCert(t *testing.T, cn string) (*tls.Certificate, *x509.Certificate) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("生成密钥: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("签发证书: %v", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("解析证书: %v", err)
	}
	return &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, leaf
}

// servedCert 返回服务端在本次响应的 TLS 连接上呈给客户端的叶子证书。
func servedCert(t *testing.T, resp *http.Response, stage string) *x509.Certificate {
	t.Helper()
	if resp.TLS == nil || len(resp.TLS.PeerCertificates) == 0 {
		t.Fatalf("%s: 响应不带 TLS 证书信息", stage)
	}
	return resp.TLS.PeerCertificates[0]
}

func TestFaithfulDisabled(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "unset-like", value: "", want: false},
		{name: "enabled", value: "1", want: false},
		{name: "unrecognized", value: "yes-please", want: false},
		{name: "zero", value: "0", want: true},
		{name: "false", value: "false", want: true},
		{name: "off trimmed and case insensitive", value: "  OFF  ", want: true},
		{name: "no", value: "no", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("SNIFFY_FAITHFUL", tt.value)
			if got := faithfulDisabled(); got != tt.want {
				t.Fatalf("faithfulDisabled() = %v，期望 %v", got, tt.want)
			}
		})
	}
}

// proxyURLFor 返回上游客户端 Transport 对给定目标会选用的代理 URL(nil=直连)。
// 上游 Transport 现为 forward.Transport(保真转发器),经其 ResolveProxy 自检代理选择。
func proxyURLFor(t *testing.T, c *http.Client, target string) string {
	t.Helper()
	tr, ok := c.Transport.(interface {
		ResolveProxy(*http.Request) (*url.URL, error)
	})
	if !ok {
		t.Fatalf("upstream client 缺少可自检代理选择的 Transport")
	}
	u, err := tr.ResolveProxy(httptest.NewRequest(http.MethodGet, target, nil))
	if err != nil {
		t.Fatalf("Proxy 闭包返回错误: %v", err)
	}
	if u == nil {
		return ""
	}
	return u.String()
}

// tunnelProxyURL 返回直通隧道(不解密的 CONNECT)当前使用的上游代理地址。
// 它与上游客户端的 Proxy 闭包是两条独立路径,SetUpstreamProxy 必须同时更新。
func tunnelProxyURL() string {
	if u := httpproc.UpstreamProxyURL(); u != nil {
		return u.String()
	}
	return ""
}

// TestSetUpstreamProxy 校验上游代理在运行时即时切换:默认直连、设置后生效、
// 无 scheme 时补 http://、清空后恢复直连;两条路径(转发客户端与直通隧道)都要跟上。
func TestSetUpstreamProxy(t *testing.T) {
	e := &Engine{}
	e.upstream = e.buildUpstreamClient()
	t.Cleanup(func() { _ = e.SetUpstreamProxy("") })
	const target = "http://example.com/x"

	check := func(stage, want string) {
		t.Helper()
		if got := proxyURLFor(t, e.upstream, target); got != want {
			t.Errorf("%s: 转发客户端代理 = %q，期望 %q", stage, got, want)
		}
		if got := tunnelProxyURL(); got != want {
			t.Errorf("%s: 直通隧道代理 = %q，期望 %q", stage, got, want)
		}
	}

	if err := e.SetUpstreamProxy(""); err != nil {
		t.Fatalf("SetUpstreamProxy(初始清空): %v", err)
	}
	check("默认", "")

	if err := e.SetUpstreamProxy("http://127.0.0.1:7777"); err != nil {
		t.Fatalf("SetUpstreamProxy: %v", err)
	}
	check("设置后", "http://127.0.0.1:7777")

	// 不含 scheme 时按 http:// 解析。
	if err := e.SetUpstreamProxy("127.0.0.1:8888"); err != nil {
		t.Fatalf("SetUpstreamProxy(无 scheme): %v", err)
	}
	check("无 scheme", "http://127.0.0.1:8888")

	// 空地址恢复直连。
	if err := e.SetUpstreamProxy("   "); err != nil {
		t.Fatalf("SetUpstreamProxy(空): %v", err)
	}
	check("清空后", "")
}

// TestSetUpstreamProxyConnectionCleanup 校验只在代理地址真正变化时清理旧连接池,
// 以及校验失败时不得污染已有状态。
func TestSetUpstreamProxyConnectionCleanup(t *testing.T) {
	transport := &closeTrackingTransport{}
	engine := &Engine{upstream: &http.Client{Transport: transport}}
	t.Cleanup(func() { _ = engine.SetUpstreamProxy("") })

	if err := engine.SetUpstreamProxy("proxy.example:8080"); err != nil {
		t.Fatalf("设置代理: %v", err)
	}
	if transport.closes != 1 {
		t.Fatalf("首次切换后清理次数 = %d，期望 1", transport.closes)
	}

	// 校验失败必须发生在写入之前:既不清池,也不能把已生效的代理冲掉。
	if err := engine.SetUpstreamProxy("%"); err == nil {
		t.Fatal("非法 URL 应返回解析错误")
	}
	if transport.closes != 1 {
		t.Fatalf("非法 URL 不应清理连接池，实际次数 %d", transport.closes)
	}
	if got := tunnelProxyURL(); got != "http://proxy.example:8080" {
		t.Fatalf("非法 URL 后代理 = %q，期望保持 http://proxy.example:8080", got)
	}

	if err := engine.SetUpstreamProxy("http://proxy.example:8080"); err != nil {
		t.Fatalf("重复设置等价代理: %v", err)
	}
	if transport.closes != 1 {
		t.Fatalf("等价代理不应再次清理连接池，实际次数 %d", transport.closes)
	}

	if err := engine.SetUpstreamProxy(""); err != nil {
		t.Fatalf("清空代理: %v", err)
	}
	if transport.closes != 2 {
		t.Fatalf("清空代理后清理次数 = %d，期望 2", transport.closes)
	}

	// 连续清空(配置页重复保存)属于 nil→nil,不应产生无谓的连接池抖动。
	if err := engine.SetUpstreamProxy(""); err != nil {
		t.Fatalf("重复清空代理: %v", err)
	}
	if transport.closes != 2 {
		t.Fatalf("重复清空不应再次清理连接池，实际次数 %d", transport.closes)
	}

	// Transport 未实现 CloseIdleConnections 时也应正常切换而非 panic。
	engine.upstream = &http.Client{Transport: roundTripOnlyTransport{}}
	if err := engine.SetUpstreamProxy("http://other.example:8080"); err != nil {
		t.Fatalf("使用不支持清池的 Transport 设置代理: %v", err)
	}
}

// TestUpstreamProxyEndToEnd 用一个假上游代理验证请求确实经代理转发:
// 目标域名不可解析,若没走代理必然失败,从而证明代理真实生效。
func TestUpstreamProxyEndToEnd(t *testing.T) {
	var hits int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		// 转发代理收到的是绝对 URI 请求行;这里直接应答以证明流量到达。
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("via-proxy"))
	}))
	defer proxy.Close()

	e := &Engine{}
	e.upstream = e.buildUpstreamClient()
	if err := e.SetUpstreamProxy(proxy.URL); err != nil {
		t.Fatalf("SetUpstreamProxy: %v", err)
	}
	t.Cleanup(func() { _ = e.SetUpstreamProxy("") })

	resp, err := e.upstream.Get("http://nonexistent.invalid/")
	if err != nil {
		t.Fatalf("经代理请求失败(代理未生效?): %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d, 期望 200", resp.StatusCode)
	}
	if atomic.LoadInt32(&hits) == 0 {
		t.Fatal("请求未经过上游代理")
	}
}
