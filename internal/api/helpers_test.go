// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package api

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mintfog/sniffy/ca"
	"github.com/mintfog/sniffy/internal/core"
	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/internal/pipeline"
	"github.com/mintfog/sniffy/internal/service"
)

// 本文件集中放 api 包各测试共用的构造器、替身与响应解析:测试只声明自己关心的部分,
// 其余取稳定缺省值。绑真实端口、扫全进程栈的 WebSocket helper 刻意留在 ws_hub_test.go
// —— 它们的使用约束(禁止 t.Parallel、必须 t.Cleanup 收口)与这里的纯内存 helper 相反。

// testHost 是所有测试请求的默认 Host。管理 API 无 token 时靠回环 Host 兜底放行,
// 用别的值会让与鉴权无关的用例莫名 403。
const testHost = "http://127.0.0.1:8888"

// reqOpt 定制 do 构造的请求,按传入顺序生效。
type reqOpt func(*http.Request)

func withHeader(key, value string) reqOpt {
	return func(r *http.Request) { r.Header.Set(key, value) }
}

// withHost 改写 Host 头。httptest.NewRequest 从 URL 取 Host,单独覆盖它才能构造出
// 「URL 是回环、Host 头是攻击者域名」这类 DNS rebinding 形态。
func withHost(host string) reqOpt {
	return func(r *http.Request) { r.Host = host }
}

func withQuery(raw string) reqOpt {
	return func(r *http.Request) { r.URL.RawQuery = raw }
}

func withCtx(ctx context.Context) reqOpt {
	return func(r *http.Request) { *r = *r.WithContext(ctx) }
}

// do 向 h 发一条请求并返回记录器。path 自动补上回环前缀;body 为空串时不带请求体
// —— 解码分支对「没有体」与「有体但内容非法」给的结论不同,两者要能分别构造出来。
func do(t *testing.T, h http.Handler, method, path, body string, opts ...reqOpt) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, testHost+path, reader)
	for _, opt := range opts {
		opt(req)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// serverConfig 是 newTestServer 的可调装配项。
type serverConfig struct {
	svc        *service.Service
	pipe       *pipeline.Pipeline
	plugins    PluginProvider
	certs      CertificateManager
	sender     RequestSender
	wsComposer WebSocketComposer
	token      string
	rootCA     ca.CA
	configDir  string
	certDir    string
}

type serverOpt func(*serverConfig)

// withoutPipeline / withoutPlugins / withoutSender / withoutComposer 摘掉一个子系统,
// 用于覆盖「本次构建没有它」的回退分支(501 / 503)。
func withoutPipeline() serverOpt { return func(c *serverConfig) { c.pipe = nil } }
func withoutPlugins() serverOpt  { return func(c *serverConfig) { c.plugins = nil } }
func withoutSender() serverOpt   { return func(c *serverConfig) { c.sender = nil } }
func withoutComposer() serverOpt { return func(c *serverConfig) { c.wsComposer = nil } }

func withCerts(m CertificateManager) serverOpt {
	return func(c *serverConfig) { c.certs = m }
}

func withToken(token string) serverOpt {
	return func(c *serverConfig) { c.token = token }
}

// withCA 让 service 持有一个真实的自签根 CA,证书下载端点据此才有内容可发。
func withCA(root ca.CA) serverOpt {
	return func(c *serverConfig) { c.rootCA = root }
}

// withService 换掉整个 service,用于需要自己控制 configDir/certDir 的用例。
func withService(svc *service.Service) serverOpt {
	return func(c *serverConfig) { c.svc = svc }
}

// newTestServer 装配一台依赖齐全的服务器并返回它与已注册路由的 mux。
// 返回 *Server 本体是刻意的:「被拒的请求没有产生副作用」这类断言要靠它拿到 svc 与替身。
func newTestServer(t *testing.T, opts ...serverOpt) (*Server, *http.ServeMux) {
	t.Helper()
	composer := &recordingComposer{newFlowID: "flow-1", newWSID: "ws-1", stopOK: true}
	cfg := &serverConfig{
		pipe:       pipeline.New(nil, nil),
		plugins:    &recordingPlugins{list: []map[string]any{{"id": "demo"}}, source: "function onRequest(f){}"},
		sender:     composer,
		wsComposer: composer,
		configDir:  t.TempDir(),
		certDir:    t.TempDir(),
	}
	for _, opt := range opts {
		opt(cfg)
	}
	if cfg.svc == nil {
		cfg.svc = service.New(cfg.rootCA, core.NewEventBus(), cfg.configDir, cfg.certDir)
	}
	s := New(cfg.svc, cfg.pipe, cfg.plugins, cfg.certs, "127.0.0.1:0", cfg.token)
	// 走真实的装配入口而不是直接写字段:app 就是这样接线的,绕过去会让这两个 Set 方法
	// 连同它们将来可能长出的校验一起脱离测试。
	if cfg.sender != nil {
		s.SetRequestSender(cfg.sender)
	}
	if cfg.wsComposer != nil {
		s.SetWebSocketComposer(cfg.wsComposer)
	}
	mux := http.NewServeMux()
	s.routes(mux)
	return s, mux
}

// testComposer 取出 newTestServer 默认装配的构造器替身。
func testComposer(t *testing.T, s *Server) *recordingComposer {
	t.Helper()
	c, ok := s.sender.(*recordingComposer)
	if !ok {
		t.Fatalf("sender 不是 recordingComposer,而是 %T", s.sender)
	}
	return c
}

// call 是替身记下的一次调用:方法名与全部实参。断言「被拒的请求零副作用」靠的是
// 调用记录为空,而不是各替身自己维护的一组 bool —— 后者漏置一个字段,断言就恒真。
type call struct {
	Method string
	Args   []any
}

func (c call) String() string { return fmt.Sprintf("%s%v", c.Method, c.Args) }

// assertNoCalls 断言替身一次都没被调到。
func assertNoCalls(t *testing.T, got []call) {
	t.Helper()
	if len(got) > 0 {
		t.Errorf("期望零副作用,实际发生了 %v", got)
	}
}

// assertCalls 逐条比对调用序列(方法名与实参)。
func assertCalls(t *testing.T, got []call, want ...call) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("调用序列 = %v,期望 %v", got, want)
		return
	}
	for i := range want {
		if got[i].Method != want[i].Method || fmt.Sprint(got[i].Args) != fmt.Sprint(want[i].Args) {
			t.Errorf("第 %d 次调用 = %v,期望 %v", i, got[i], want[i])
		}
	}
}

// lastCall 取最后一次指定方法的调用;没有则让用例失败。
func lastCall(t *testing.T, got []call, method string) call {
	t.Helper()
	for i := len(got) - 1; i >= 0; i-- {
		if got[i].Method == method {
			return got[i]
		}
	}
	t.Fatalf("没有 %s 的调用记录,实际为 %v", method, got)
	return call{}
}

// recordingPlugins 是 PluginProvider 的可编程替身:每次调用连同实参记进 calls,
// 返回的错误由 errs 按方法名给定(缺省回落到 "*")。
type recordingPlugins struct {
	calls  []call
	list   []map[string]any
	source string
	errs   map[string]error
}

func (p *recordingPlugins) record(method string, args ...any) error {
	p.calls = append(p.calls, call{Method: method, Args: args})
	if err, ok := p.errs[method]; ok {
		return err
	}
	return p.errs["*"]
}

func (p *recordingPlugins) ListPlugins() []map[string]any {
	_ = p.record("ListPlugins")
	return p.list
}

func (p *recordingPlugins) EnablePlugin(id string, enabled bool) error {
	return p.record("EnablePlugin", id, enabled)
}

func (p *recordingPlugins) GetPluginSource(id string) (string, error) {
	if err := p.record("GetPluginSource", id); err != nil {
		return "", err
	}
	return p.source, nil
}

func (p *recordingPlugins) SavePluginSource(id, source string) error {
	return p.record("SavePluginSource", id, source)
}

func (p *recordingPlugins) CreatePlugin(meta map[string]any, source string) (map[string]any, error) {
	if err := p.record("CreatePlugin", meta, source); err != nil {
		return nil, err
	}
	return meta, nil
}

func (p *recordingPlugins) DeletePlugin(id string) error {
	return p.record("DeletePlugin", id)
}

func (p *recordingPlugins) UpdateManifest(id string, patch map[string]any) error {
	return p.record("UpdateManifest", id, patch)
}

func (p *recordingPlugins) ClearPluginLogs(id string) error {
	return p.record("ClearPluginLogs", id)
}

// recordingComposer 同时实现 RequestSender 与 WebSocketComposer:两者操作的是同一台
// 构造器,分成两份替身就写不出「发到了哪条连接」这类跨接口断言。
type recordingComposer struct {
	calls []call
	// specs 逐次记下 SendRequest 收到的完整 spec:calls 只存 Method/URL/Body 概要,
	// headersB64 这类按序号对位的字节旁路塞不进去。
	specs     []flow.RequestSpec
	newFlowID string
	newWSID   string
	stopOK    bool
	sendErr   error
	openErr   error
	sendWSErr error
	closeErr  error
}

func (c *recordingComposer) record(method string, args ...any) {
	c.calls = append(c.calls, call{Method: method, Args: args})
}

func (c *recordingComposer) SendRequest(spec flow.RequestSpec) (string, error) {
	c.record("SendRequest", spec.Method, spec.URL, spec.Body)
	c.specs = append(c.specs, spec)
	if c.sendErr != nil {
		return "", c.sendErr
	}
	return c.newFlowID, nil
}

func (c *recordingComposer) StopStream(id string) bool {
	c.record("StopStream", id)
	return c.stopOK
}

func (c *recordingComposer) OpenWebSocket(spec flow.RequestSpec) (string, error) {
	c.record("OpenWebSocket", spec.URL)
	if c.openErr != nil {
		return "", c.openErr
	}
	return c.newWSID, nil
}

func (c *recordingComposer) SendWSMessage(flowID, msgType, data string) error {
	c.record("SendWSMessage", flowID, msgType, data)
	return c.sendWSErr
}

func (c *recordingComposer) CloseWebSocket(flowID string) error {
	c.record("CloseWebSocket", flowID)
	return c.closeErr
}

// fakeCertificateManager 是 CertificateManager 的可编程替身。
type fakeCertificateManager struct {
	calls []call

	regenErr error
	regenPEM string

	exportData []byte
	exportMIME string
	exportErr  error

	importPEM string
	importErr error
	// importedData 是最后一次导入收到的字节;用它断言上传内容被逐字送达。
	importedData []byte
}

func (m *fakeCertificateManager) RegenerateCA() (string, error) {
	m.calls = append(m.calls, call{Method: "RegenerateCA"})
	return m.regenPEM, m.regenErr
}

func (m *fakeCertificateManager) ExportCAAs(format, password string) ([]byte, string, error) {
	m.calls = append(m.calls, call{Method: "ExportCAAs", Args: []any{format, password}})
	return m.exportData, m.exportMIME, m.exportErr
}

func (m *fakeCertificateManager) ImportCA(data []byte, password string) (string, error) {
	m.calls = append(m.calls, call{Method: "ImportCA", Args: []any{len(data), password}})
	m.importedData = append([]byte(nil), data...)
	return m.importPEM, m.importErr
}

// testInvalidInputError 冒充 plugin / ca 包里「调用方输入非法」那一类错误。两侧靠方法名
// 构成鸭子契约,任一侧改名编译期都不报错,只会在运行时静默降级成 500。
type testInvalidInputError struct {
	message string
}

func (e *testInvalidInputError) Error() string      { return e.message }
func (e *testInvalidInputError) InvalidInput() bool { return true }

// envelope 是 apiResponse 的解析视图。Data 保留成 RawMessage,好让用例分别断言
// 「键存在与否」与「解出的内容」—— 前者是 omitempty 决定的线上形状,后者是业务数据。
type envelope struct {
	Data      json.RawMessage `json:"data"`
	Success   bool            `json:"success"`
	Message   string          `json:"message"`
	Timestamp string          `json:"timestamp"`
}

func decodeEnvelope(t *testing.T, rec *httptest.ResponseRecorder) envelope {
	t.Helper()
	var e envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("响应不是 apiResponse 信封: %v (%s)", err, rec.Body.String())
	}
	return e
}

// into 把 data 解进 v。data 缺席时直接失败:调用方要的是数据,不是零值。
func (e envelope) into(t *testing.T, v any) {
	t.Helper()
	if len(e.Data) == 0 {
		t.Fatalf("响应没有 data 字段")
	}
	if err := json.Unmarshal(e.Data, v); err != nil {
		t.Fatalf("解析 data 失败: %v (%s)", err, e.Data)
	}
}

// pageEnvelope 是 paginatedResponse 的解析视图。它与 envelope 是两种并存的线上形状,
// 前端按端点分别解析,混用哪一种都会让调用方拿到 200 却解不出内容。
type pageEnvelope struct {
	Data     json.RawMessage `json:"data"`
	Total    int             `json:"total"`
	Page     int             `json:"page"`
	PageSize int             `json:"pageSize"`
	HasNext  bool            `json:"hasNext"`
	HasPrev  bool            `json:"hasPrev"`
}

func decodePage(t *testing.T, rec *httptest.ResponseRecorder) pageEnvelope {
	t.Helper()
	var p pageEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("响应不是分页信封: %v (%s)", err, rec.Body.String())
	}
	return p
}

// bodyKeys 返回响应体顶层的键集合(已排序)。omitempty 决定的「字段整个消失」只能这样断言:
// 解进结构体会把缺席与零值抹平成同一个结果。
func bodyKeys(t *testing.T, rec *httptest.ResponseRecorder) []string {
	t.Helper()
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("响应不是 JSON 对象: %v (%s)", err, rec.Body.String())
	}
	keys := make([]string, 0, len(raw))
	for k := range raw {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// newSelfSignedPEM 生成一对自签名证书与 EC 私钥的 PEM,SAN 由 dnsNames/ips 指定。
// 服务端证书导入与管理 API 的 TLS 监听都要用它 —— 两处都得是真证书,伪造的字节走不到被测逻辑。
func newSelfSignedPEM(t *testing.T, cn string, dnsNames []string, ips []net.IP) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("生成私钥失败: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     dnsNames,
		IPAddresses:  ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("签发证书失败: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("编码私钥失败: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
}

// fixtureTime 是会话 fixture 的基准时刻,固定值让时间过滤的边界断言可复现。
var fixtureTime = time.Date(2026, time.August, 18, 10, 0, 0, 0, time.UTC)

// flowOpt 定制 newFlowFixture 造出的 Flow,按传入顺序生效。
type flowOpt func(*flow.Flow)

// newFlowFixture 造一条已完成的测试会话:默认 GET https://api.example.com/resource,
// 200 text/plain 响应,时刻为 fixtureTime。
func newFlowFixture(id string, opts ...flowOpt) *flow.Flow {
	f := flow.New(flow.ProtoHTTPS)
	f.ID = id
	f.State = flow.StateCompleted
	f.Timing.RequestAt = fixtureTime
	f.Timing.ResponseAt = fixtureTime.Add(25 * time.Millisecond)
	f.Timing.CompletedAt = fixtureTime.Add(50 * time.Millisecond)
	f.Timing.DurationMs = 50
	f.Request = &flow.Request{
		Method: http.MethodGet,
		URL:    "https://api.example.com/resource",
		Host:   "api.example.com",
		Path:   "/resource",
		Header: map[string][]string{"Content-Type": {"text/plain"}},
	}
	f.Response = &flow.Response{
		Status: http.StatusOK,
		Header: map[string][]string{"Content-Type": {"text/plain"}},
		Body:   []byte("response-body"),
	}
	for _, opt := range opts {
		opt(f)
	}
	return f
}

// withRequest 改写请求行。
func withRequest(method, rawURL, host string) flowOpt {
	return func(f *flow.Flow) {
		f.Request.Method = method
		f.Request.URL = rawURL
		f.Request.Host = host
	}
}

func withRequestHeader(key string, values ...string) flowOpt {
	return func(f *flow.Flow) { f.Request.Header[key] = values }
}

func withRequestBody(mime string, data []byte) flowOpt {
	return func(f *flow.Flow) {
		if mime != "" {
			f.Request.Header["Content-Type"] = []string{mime}
		}
		f.Request.Body = data
	}
}

func withResponse(status int, mime string, data []byte) flowOpt {
	return func(f *flow.Flow) {
		f.Response = &flow.Response{
			Status: status,
			Header: map[string][]string{"Content-Type": {mime}},
			Body:   data,
		}
	}
}

// withoutResponse 造一条仍在进行中的会话(导出的状态码过滤要靠它才能走到 HasResponse 分支)。
func withoutResponse() flowOpt {
	return func(f *flow.Flow) {
		f.Response = nil
		f.State = flow.StatePending
	}
}

func withRequestAt(at time.Time) flowOpt {
	return func(f *flow.Flow) { f.Timing.RequestAt = at }
}

// pausedFlow 把一条 flow 按到断点上,返回它的 id、flow 指针与「等 Pause 收尾并取回处置结果」
// 的函数(true = 被阻断,false = 被放行)。
//
// 必须等自己这一条进列表:等「列表非空」在多条并发时会立刻被前一条满足,后面的 flow 还没挂上
// 就去放行,少放的那条会把用例卡满整个断点超时。
func pausedFlow(t *testing.T, bp *pipeline.BreakpointManager) (string, *flow.Flow, func() bool) {
	t.Helper()
	f := flow.New(flow.ProtoHTTP)
	f.Request = &flow.Request{
		Method: http.MethodGet,
		URL:    "https://x.com/",
		Host:   "x.com",
		Path:   "/",
		Header: map[string][]string{},
	}
	done := make(chan bool, 1)
	go func() { done <- bp.Pause(f, flow.PhaseRequest) }()

	wait := func() bool {
		select {
		case abort := <-done:
			return abort
		case <-time.After(10 * time.Second):
			t.Error("Pause 未在超时前返回")
			return false
		}
	}
	// 兜底:用例中途失败时也要把 flow 放走,否则它会一直占着 goroutine 与名额。
	t.Cleanup(func() { _ = bp.Abort(f.ID) })

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, item := range bp.List() {
			if item.ID == f.ID {
				return f.ID, f, wait
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("flow 未进入断点暂停")
	return "", nil, wait
}
