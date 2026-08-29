// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

// Package js 用 goja 实现 JavaScript 插件层：请求、响应、WebSocket 与流消息钩子。
// 每个插件独占一个 Runtime 和邮箱 goroutine；Flow 通过 JSON 进入与离开 VM。
// 脚本抛错、超时或邮箱拥塞时按原值放行；回传值中形态非法的字段逐个丢弃，其余字段仍生效。
// 头部以首值扁平视图交互，回写时保留未修改字段的多值与顺序。
// 载荷在 VM 边界区分文本与 base64 两个通道，合法 UTF-8 走文本字段。
package js

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/dop251/goja"
	"github.com/mintfog/sniffy/internal/flow"
)

// Logger 是 js 插件需要的最小日志接口。
type Logger interface {
	Info(msg string, args ...any)
	Error(msg string, args ...any)
	Debug(msg string, args ...any)
}

// LogEntry 是一条结构化插件日志(供 UI 按级别过滤与展示)。
type LogEntry struct {
	Level string `json:"level"` // log|info|warn|error|debug|notify
	Msg   string `json:"msg"`
	Time  int64  `json:"time"` // Unix 毫秒
}

// Config 描述 JS 插件的元信息、脚本、持久化状态和日志回调。
type Config struct {
	ID        string
	Name      string
	Priority  int
	Enabled   bool
	Whitelist []string
	Blacklist []string
	Settings  map[string]any
	Source    string
	Timeout   time.Duration

	// StatePath 指定 store 落盘路径（<插件目录>/state.json）；省略时 store 驻留内存。
	StatePath string
	// InitialStore 是热重载时迁移到新实例的 store，优先于 StatePath。
	InitialStore map[string]any
	// OnLog 在每条插件日志产生时回调(管理器据此向 UI 实时推送)。可为 nil。
	OnLog func(LogEntry)
}

// Plugin 是一个 goja JS 插件,实现 pipeline 的钩子接口。
type Plugin struct {
	cfg     Config
	enabled atomic.Bool
	timeout time.Duration

	vm      *goja.Runtime
	driver  *goja.Program
	mailbox chan *job
	quit    chan struct{}
	once    sync.Once

	logger Logger

	store       map[string]any
	storeMu     sync.Mutex
	storeDirty  atomic.Bool
	flusherDone chan struct{} // storeFlusher 退出信号(StatePath 非空时启用)
	tmpNonce    string        // 落盘临时文件名后缀,避免新旧实例并发写撞同一 .tmp

	logsMu sync.Mutex
	logs   []LogEntry

	// runMu 保护运行代次与 VM 中断状态；runGen 标识当前运行，超时回调仅作用于对应代次。
	runMu  sync.Mutex
	runGen uint64
}

type job struct {
	phase string
	in    []byte
	reply chan []byte
}

// 进出 VM 的 flow 视图（请求字段扁平到顶层，响应位于 response 下）。
type jsFlow struct {
	ID       string            `json:"id"`
	Method   string            `json:"method,omitempty"`
	URL      string            `json:"url,omitempty"`
	Host     string            `json:"host,omitempty"`
	Path     string            `json:"path,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
	Body     string            `json:"body,omitempty"`
	BodyB64  string            `json:"bodyB64,omitempty"`
	Response *jsResponse       `json:"response,omitempty"`
	Process  *jsProcess        `json:"process,omitempty"`

	// WS / 流（SSE、gRPC、分块）专用字段。
	Direction string `json:"direction,omitempty"`
	Type      string `json:"type,omitempty"`
	Data      string `json:"data,omitempty"`
	DataB64   string `json:"dataB64,omitempty"`
	Kind      string `json:"kind,omitempty"`      // 流类型:sse|grpc|chunk
	EventType string `json:"eventType,omitempty"` // SSE 的 event 名
}

type jsResponse struct {
	Status     int               `json:"status"`
	StatusText string            `json:"statusText,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
	Body       string            `json:"body,omitempty"`
	BodyB64    string            `json:"bodyB64,omitempty"`
	Reason     string            `json:"reason,omitempty"`
}

type jsProcess struct {
	Name string `json:"name,omitempty"`
	PID  uint32 `json:"pid,omitempty"`
	Path string `json:"path,omitempty"`
}

type jsDecision struct {
	Kind   string `json:"kind"`
	Status int    `json:"status"`
	Reason string `json:"reason"`
}

type jsOut struct {
	Flow     jsFlow     `json:"flow"`
	Decision jsDecision `json:"decision"`
}

// hostSetup 声明桥接全局与插件 API；mock/setBreakpoint 按钩子阶段处理。
const hostSetup = `
var flow; var __decision;
var __IN__; var __PHASE__; var __OUT__;
var __STOP = {__stop:true};
function __fmt(a){
  if (a === null) return 'null';
  if (a === undefined) return 'undefined';
  if (typeof a === 'object') { try { return JSON.stringify(a); } catch (e) { return String(a); } }
  return String(a);
}
function __join(args){ var p=[]; for (var i=0;i<args.length;i++){ p.push(__fmt(args[i])); } return p.join(' '); }
var console = {
  log:   function(){ __log('log',   __join(arguments)); },
  info:  function(){ __log('info',  __join(arguments)); },
  warn:  function(){ __log('warn',  __join(arguments)); },
  error: function(){ __log('error', __join(arguments)); },
  debug: function(){ __log('debug', __join(arguments)); }
};
var store = { get: function(k){ return __storeGet(k); }, set: function(k,v){ __storeSet(k,v); } };
function notify(t,m){ __log('notify', (t||'') + (m ? (' ' + m) : '')); }
function mock(r){
  if (__PHASE__ !== 'request') { __log('warn', 'mock() 仅在 onRequest 生效,当前阶段 ' + __PHASE__ + ' 已忽略'); return; }
  if (r) { flow.response = r; }
  __decision = {kind:'mock', status:0, reason:(r&&r.reason)||''}; throw __STOP;
}
function abort(o){ o=o||{}; __decision={kind:'abort', status:(o.status||0), reason:(o.reason||'')}; throw __STOP; }
function setBreakpoint(){
  if (__PHASE__ !== 'request' && __PHASE__ !== 'response') { __log('warn', 'setBreakpoint() 仅在 onRequest/onResponse 生效,当前阶段 ' + __PHASE__ + ' 已忽略'); return; }
  __decision = {kind:'breakpoint', status:0, reason:''}; throw __STOP;
}

// ---- 助手命名空间 ----
var base64 = { encode:__b64enc, decode:__b64dec, urlEncode:__b64urlenc, urlDecode:__b64urldec };
var hex = { encode:__hexenc, decode:__hexdec };
var url = { parse:__urlParse };
var query = { parse:__queryParse, stringify:__queryStringify };
function uuid(){ return __uuid(); }
function randomId(n){ return __randHex(n||8); }
// header:对扁平头对象(flow.headers / flow.response.headers)做大小写无关的读写。
var header = {
  get: function(h,name){ if(!h) return undefined; var ln=String(name).toLowerCase(); for(var k in h){ if(k.toLowerCase()===ln) return h[k]; } return undefined; },
  has: function(h,name){ return header.get(h,name) !== undefined; },
  set: function(h,name,val){ if(!h) return; var ln=String(name).toLowerCase(); for(var k in h){ if(k.toLowerCase()===ln){ h[k]=val; return; } } h[name]=val; },
  del: function(h,name){ if(!h) return; var ln=String(name).toLowerCase(); for(var k in h){ if(k.toLowerCase()===ln){ delete h[k]; } } }
};
// json:容错解析与点路径取值,纯 JS 实现,避免每次跨 VM 边界。
var json = {
  safeParse: function(s, fb){ try { return JSON.parse(s); } catch(e){ return (fb===undefined?null:fb); } },
  stringify: function(v, pretty){ try { return JSON.stringify(v, null, pretty?2:0); } catch(e){ return ''; } },
  get: function(o, path){
    if (typeof o === 'string') { o = json.safeParse(o); }
    if (o == null || !path) return undefined;
    var ps = String(path).split('.'); var cur = o;
    for (var i=0;i<ps.length;i++){ if (cur == null) return undefined; cur = cur[ps[i]]; }
    return cur;
  }
};
// crypto:哈希/HMAC 出口一律 hex/base64;随机数走 crypto/rand。
var crypto = {
  md5: __md5, sha1: __sha1, sha256: __sha256, sha512: __sha512,
  md5Base64: __md5b64, sha1Base64: __sha1b64, sha256Base64: __sha256b64, sha512Base64: __sha512b64,
  hashBytes: function(algo, bytes){ return __hashBytes(algo, bytes); },
  hmac: function(algo, key, msg){ return __hmac(algo, key, msg); },
  hmacBase64: function(algo, key, msg){ return __hmacB64(algo, key, msg); },
  hmacBase64Url: function(algo, key, msg){ return __hmacB64Url(algo, key, msg); },
  randomBytes: function(n){ return __randBytes(n|0); },
  randomInt: function(min, max){ return __randInt(min|0, max|0); },
  randomString: function(n, alphabet){ return __randStr(n|0, alphabet||''); }
};
// utf8 与字节版 base64:使用 number[](0-255)承载原始字节。
var utf8 = { toBytes: __utf8ToBytes, fromBytes: function(b){ return __utf8FromBytes(b); } };
base64.encodeBytes = function(b){ return __b64encBytes(b); };
base64.decodeBytes = function(s){ return __b64decBytes(s); };
function btoa(s){ return __b64enc(String(s)); }
function atob(s){ return __b64dec(String(s)); }
var time = { now: __nowMs, unix: __nowSec, iso: __nowISO,
  format: function(ms, layout){ return __fmtTime(ms, layout||''); } };
// jwt:decode 不验签,仅拆段;HS256 签发/验签复用 base64url 与 HMAC。
var jwt = {
  decode: function(token){
    if (!token) return null;
    var parts = String(token).split('.');
    if (parts.length < 2) return null;
    return { header: json.safeParse(base64.urlDecode(parts[0])),
             payload: json.safeParse(base64.urlDecode(parts[1])),
             signature: parts[2] || '' };
  },
  signHS256: function(payload, secret){
    var seg = base64.urlEncode(JSON.stringify({alg:'HS256', typ:'JWT'})) + '.' + base64.urlEncode(JSON.stringify(payload));
    return seg + '.' + __hmacB64Url('sha256', secret, seg);
  },
  verifyHS256: function(token, secret){
    var parts = String(token).split('.');
    if (parts.length !== 3) return false;
    return __hmacB64Url('sha256', secret, parts[0]+'.'+parts[1]) === parts[2];
  }
};
`

const driverSrc = `
(function(){
  flow = JSON.parse(__IN__);
  // 二进制载荷同时提供空文本字段，脚本可统一读取文本属性。
  if (flow.bodyB64 !== undefined && flow.body === undefined) { flow.body = ''; }
  if (flow.dataB64 !== undefined && flow.data === undefined) { flow.data = ''; }
  if (flow.response && flow.response.bodyB64 !== undefined && flow.response.body === undefined) { flow.response.body = ''; }
  __decision = {kind:'continue', status:0, reason:''};
  try {
    if (__PHASE__ === 'request') { if (typeof onRequest === 'function') onRequest(flow); }
    else if (__PHASE__ === 'response') { if (typeof onResponse === 'function') onResponse(flow); }
    else if (__PHASE__ === 'ws') { if (typeof onWebSocketMessage === 'function') onWebSocketMessage(flow); }
    else if (__PHASE__ === 'stream') { if (typeof onStreamMessage === 'function') onStreamMessage(flow); }
  } catch (e) { if (e !== __STOP) { __log('error', 'plugin error: ' + e); } }
  __OUT__ = JSON.stringify({flow: flow, decision: __decision});
})();
`

// NewPlugin 创建并启动一个 JS 插件。
func NewPlugin(cfg Config, logger Logger) (*Plugin, error) {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 100 * time.Millisecond
	}
	p := &Plugin{
		cfg:     cfg,
		timeout: cfg.Timeout,
		mailbox: make(chan *job),
		quit:    make(chan struct{}),
		logger:  logger,
		store:   make(map[string]any),
	}
	p.enabled.Store(cfg.Enabled)
	// store 初值:热重载迁移优先,其次磁盘,最后空。
	init := cfg.InitialStore
	if init == nil {
		init = loadStore(cfg.StatePath)
	}
	for k, v := range init {
		p.store[k] = v
	}
	if err := p.initVM(); err != nil {
		return nil, err
	}
	go p.loop()
	if cfg.StatePath != "" {
		p.tmpNonce = randHexBytes(4)
		p.flusherDone = make(chan struct{})
		go p.storeFlusher()
	}
	return p, nil
}

// initTimeout 返回顶层求值的中断上限，独立于单次钩子预算。
func (p *Plugin) initTimeout() time.Duration {
	if d := p.timeout * 10; d > time.Second {
		return d
	}
	return time.Second
}

func (p *Plugin) initVM() error {
	vm := goja.New()
	vm.SetMaxCallStackSize(2048)
	// 顶层求值使用独立中断上限，保护插件初始化路径。
	// 初始化期间由 initMu 同步完成标记与 VM 中断；结束时清理中断状态。
	var initMu sync.Mutex
	initDone := false
	timer := time.AfterFunc(p.initTimeout(), func() {
		initMu.Lock()
		if !initDone {
			vm.Interrupt("初始化超时")
		}
		initMu.Unlock()
	})
	defer func() {
		initMu.Lock()
		initDone = true
		vm.ClearInterrupt()
		initMu.Unlock()
		timer.Stop()
	}()

	_ = vm.Set("__log", func(level, msg string) {
		p.appendLog(level, msg)
		if p.logger != nil {
			p.logger.Debug("[插件:%s] %s", p.cfg.ID, msg)
		}
	})
	_ = vm.Set("__storeGet", func(k string) any {
		p.storeMu.Lock()
		defer p.storeMu.Unlock()
		return p.store[k]
	})
	_ = vm.Set("__storeSet", func(k string, v any) {
		p.storeMu.Lock()
		p.store[k] = v
		p.storeMu.Unlock()
		p.storeDirty.Store(true)
	})
	// settings 使用深拷贝后注入 VM，与 manifest 状态隔离。
	settings, _ := deepCopyJSON(p.cfg.Settings)
	_ = vm.Set("settings", settings)
	registerHelpers(vm)

	if _, err := vm.RunString(hostSetup); err != nil {
		return err
	}
	// 运行用户脚本，定义各阶段钩子。
	if _, err := vm.RunString(p.cfg.Source); err != nil {
		return err
	}
	// 用户脚本以非严格模式编译，兼容桥接全局变量赋值。
	driver, err := goja.Compile(p.cfg.ID+"-driver", driverSrc, false)
	if err != nil {
		return err
	}
	p.vm = vm
	p.driver = driver
	return nil
}

// registerHelpers 注入 Go 实现的 base64、hex、url、query、uuid 等助手函数。
func registerHelpers(vm *goja.Runtime) {
	_ = vm.Set("__b64enc", func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) })
	_ = vm.Set("__b64dec", func(s string) string {
		b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
		if err != nil {
			return ""
		}
		return string(b)
	})
	_ = vm.Set("__b64urlenc", func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) })
	_ = vm.Set("__b64urldec", func(s string) string {
		b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(strings.TrimSpace(s), "="))
		if err != nil {
			return ""
		}
		return string(b)
	})
	_ = vm.Set("__hexenc", func(s string) string { return hex.EncodeToString([]byte(s)) })
	_ = vm.Set("__hexdec", func(s string) string {
		b, err := hex.DecodeString(strings.TrimSpace(s))
		if err != nil {
			return ""
		}
		return string(b)
	})
	_ = vm.Set("__urlParse", func(s string) map[string]any {
		u, err := url.Parse(s)
		if err != nil {
			return nil
		}
		q := map[string]string{}
		for k, v := range u.Query() {
			if len(v) > 0 {
				q[k] = v[0]
			}
		}
		return map[string]any{
			"protocol": u.Scheme,
			"host":     u.Host,
			"hostname": u.Hostname(),
			"port":     u.Port(),
			"path":     u.Path,
			"query":    q,
			"hash":     u.Fragment,
		}
	})
	_ = vm.Set("__queryParse", func(s string) map[string]string {
		out := map[string]string{}
		vals, err := url.ParseQuery(strings.TrimPrefix(s, "?"))
		if err != nil {
			return out
		}
		for k, v := range vals {
			if len(v) > 0 {
				out[k] = v[0]
			}
		}
		return out
	})
	_ = vm.Set("__queryStringify", func(obj map[string]any) string {
		vals := url.Values{}
		for k, v := range obj {
			vals.Set(k, fmt.Sprint(v))
		}
		return vals.Encode()
	})
	_ = vm.Set("__uuid", func() string { return uuidV4() })
	_ = vm.Set("__randHex", func(n int) string {
		if n <= 0 || n > 256 {
			n = 8
		}
		b := make([]byte, n)
		_, _ = rand.Read(b)
		return hex.EncodeToString(b)
	})

	// ---- 哈希 / HMAC ----
	// 任意字节产物使用 hex 或 base64 字符串传回 VM。
	newHash := func(algo string) func() hash.Hash {
		switch strings.ToLower(algo) {
		case "md5":
			return md5.New
		case "sha1":
			return sha1.New
		case "sha256":
			return sha256.New
		case "sha512":
			return sha512.New
		}
		return nil
	}
	hashHex := func(s string, h hash.Hash) string { h.Write([]byte(s)); return hex.EncodeToString(h.Sum(nil)) }
	hashB64 := func(s string, h hash.Hash) string {
		h.Write([]byte(s))
		return base64.StdEncoding.EncodeToString(h.Sum(nil))
	}
	_ = vm.Set("__md5", func(s string) string { return hashHex(s, md5.New()) })
	_ = vm.Set("__md5b64", func(s string) string { return hashB64(s, md5.New()) })
	_ = vm.Set("__sha1", func(s string) string { return hashHex(s, sha1.New()) })
	_ = vm.Set("__sha1b64", func(s string) string { return hashB64(s, sha1.New()) })
	_ = vm.Set("__sha256", func(s string) string { return hashHex(s, sha256.New()) })
	_ = vm.Set("__sha256b64", func(s string) string { return hashB64(s, sha256.New()) })
	_ = vm.Set("__sha512", func(s string) string { return hashHex(s, sha512.New()) })
	_ = vm.Set("__sha512b64", func(s string) string { return hashB64(s, sha512.New()) })
	_ = vm.Set("__hashBytes", func(algo string, b []byte) string {
		nh := newHash(algo)
		if nh == nil {
			return ""
		}
		h := nh()
		h.Write(b)
		return hex.EncodeToString(h.Sum(nil))
	})
	hmacRaw := func(algo, key, msg string) []byte {
		nh := newHash(algo)
		if nh == nil {
			return nil
		}
		m := hmac.New(nh, []byte(key))
		m.Write([]byte(msg))
		return m.Sum(nil)
	}
	_ = vm.Set("__hmac", func(algo, key, msg string) string {
		sum := hmacRaw(algo, key, msg)
		if sum == nil {
			return ""
		}
		return hex.EncodeToString(sum)
	})
	_ = vm.Set("__hmacB64", func(algo, key, msg string) string {
		sum := hmacRaw(algo, key, msg)
		if sum == nil {
			return ""
		}
		return base64.StdEncoding.EncodeToString(sum)
	})
	_ = vm.Set("__hmacB64Url", func(algo, key, msg string) string {
		sum := hmacRaw(algo, key, msg)
		if sum == nil {
			return ""
		}
		return base64.RawURLEncoding.EncodeToString(sum)
	})

	// ---- 随机 ----
	// 随机数据使用 crypto/rand，并限制 number[] 长度。
	_ = vm.Set("__randBytes", func(n int) []int {
		if n <= 0 || n > 4096 {
			return nil
		}
		b := make([]byte, n)
		_, _ = rand.Read(b)
		out := make([]int, n)
		for i, v := range b {
			out[i] = int(v)
		}
		return out
	})
	_ = vm.Set("__randInt", func(min, max int) int {
		if max <= min {
			return min
		}
		var buf [8]byte
		_, _ = rand.Read(buf[:])
		return min + int(binary.BigEndian.Uint64(buf[:])%uint64(max-min))
	})
	_ = vm.Set("__randStr", func(n int, alphabet string) string {
		if n <= 0 || n > 4096 {
			return ""
		}
		if alphabet == "" {
			alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
		}
		al := []byte(alphabet)
		b := make([]byte, n)
		_, _ = rand.Read(b)
		for i := range b {
			b[i] = al[int(b[i])%len(al)]
		}
		return string(b)
	})

	// ---- utf8 / 字节版 base64 ----
	_ = vm.Set("__utf8ToBytes", func(s string) []int {
		bs := []byte(s)
		out := make([]int, len(bs))
		for i, v := range bs {
			out[i] = int(v)
		}
		return out
	})
	_ = vm.Set("__utf8FromBytes", func(b []byte) string { return string(b) })
	_ = vm.Set("__b64encBytes", func(b []byte) string { return base64.StdEncoding.EncodeToString(b) })
	_ = vm.Set("__b64decBytes", func(s string) []int {
		raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
		if err != nil {
			return nil
		}
		out := make([]int, len(raw))
		for i, v := range raw {
			out[i] = int(v)
		}
		return out
	})

	// ---- 时间 ----
	_ = vm.Set("__nowMs", func() int64 { return time.Now().UnixMilli() })
	_ = vm.Set("__nowSec", func() int64 { return time.Now().Unix() })
	_ = vm.Set("__nowISO", func() string { return time.Now().UTC().Format(time.RFC3339) })
	_ = vm.Set("__fmtTime", func(ms int64, layout string) string {
		switch layout {
		case "", "datetime":
			layout = "2006-01-02 15:04:05"
		case "date":
			layout = "2006-01-02"
		case "iso":
			layout = time.RFC3339
		}
		return time.UnixMilli(ms).UTC().Format(layout)
	})
}

func (p *Plugin) loop() {
	for {
		select {
		case <-p.quit:
			return
		case j := <-p.mailbox:
			j.reply <- p.run(j.phase, j.in)
		}
	}
}

// beginRun 返回本次运行的代次。
func (p *Plugin) beginRun() uint64 {
	p.runMu.Lock()
	defer p.runMu.Unlock()
	p.runGen++
	return p.runGen
}

// endRun 结束当前代次并清除 VM 中断状态。
func (p *Plugin) endRun() {
	p.runMu.Lock()
	p.runGen++
	p.vm.ClearInterrupt()
	p.runMu.Unlock()
}

// timeoutInterrupt 仅中断仍处于 gen 代次的运行。
func (p *Plugin) timeoutInterrupt(gen uint64) {
	p.runMu.Lock()
	if p.runGen == gen {
		p.vm.Interrupt("timeout")
	}
	p.runMu.Unlock()
}

// run 在 VM 中执行一次钩子(仅由 loop goroutine 调用)。
func (p *Plugin) run(phase string, in []byte) []byte {
	_ = p.vm.Set("__IN__", string(in))
	_ = p.vm.Set("__PHASE__", phase)

	gen := p.beginRun()
	timer := time.AfterFunc(p.timeout, func() { p.timeoutInterrupt(gen) })
	_, err := p.vm.RunProgram(p.driver)
	p.endRun()
	timer.Stop()
	if err != nil {
		p.appendLog("error", "runtime error: "+err.Error())
		return nil
	}
	out := p.vm.Get("__OUT__")
	if out == nil {
		return nil
	}
	return []byte(out.String())
}

// dispatch 把一个调用投递到邮箱并等待结果(带保护)。
func (p *Plugin) dispatch(phase string, in []byte) []byte {
	reply := make(chan []byte, 1)
	select {
	case p.mailbox <- &job{phase: phase, in: in, reply: reply}:
	case <-time.After(p.timeout * 3):
		return nil // 插件繁忙,失败开放
	case <-p.quit:
		return nil
	}
	select {
	case out := <-reply:
		return out
	case <-time.After(p.timeout * 3):
		return nil
	}
}

// ---- pipeline.Hook 接口 ----

func (p *Plugin) Name() string  { return p.cfg.ID }
func (p *Plugin) Priority() int { return p.cfg.Priority }
func (p *Plugin) Enabled() bool { return p.enabled.Load() }

// SetEnabled 启用/禁用插件。
func (p *Plugin) SetEnabled(v bool) { p.enabled.Store(v) }

// Match 按白/黑名单判断是否作用于该 URL。
func (p *Plugin) Match(url string) bool {
	for _, b := range p.cfg.Blacklist {
		if matchPattern(b, url) {
			return false
		}
	}
	if len(p.cfg.Whitelist) == 0 {
		return true
	}
	for _, wcard := range p.cfg.Whitelist {
		if matchPattern(wcard, url) {
			return true
		}
	}
	return false
}

// OnRequest 执行请求钩子。
func (p *Plugin) OnRequest(ctx context.Context, f *flow.Flow) flow.Decision {
	return p.runHTTP("request", f, flow.PhaseRequest)
}

// OnResponse 执行响应钩子。
func (p *Plugin) OnResponse(ctx context.Context, f *flow.Flow) flow.Decision {
	return p.runHTTP("response", f, flow.PhaseResponse)
}

// runHTTP 执行请求/响应钩子的公共逻辑。
func (p *Plugin) runHTTP(phase string, f *flow.Flow, ph flow.Phase) flow.Decision {
	// 保存送入 VM 的视图，回程值以此作为字段改动比较基准。
	sent := requestToJS(f)
	in, _ := json.Marshal(&sent)
	out := p.dispatch(phase, in)
	if out == nil {
		return flow.ContinueDecision()
	}
	return applyHTTP(f, &sent, out, ph, p.appendLog)
}

// OnWebSocketMessage 执行 WS 钩子。
func (p *Plugin) OnWebSocketMessage(ctx context.Context, m *flow.WSMessage) flow.Decision {
	text, b64 := payloadToJS(m.Data)
	in, _ := json.Marshal(jsFlow{
		Direction: m.Direction,
		Type:      m.Type,
		Data:      text,
		DataB64:   b64,
		URL:       m.URL,
	})
	out := p.dispatch("ws", in)
	if out == nil {
		return flow.ContinueDecision()
	}
	res, dropped, ok := parseOut(out, p.appendLog)
	if !ok {
		return flow.ContinueDecision()
	}
	p.applyMessagePayload(&m.Data, res.Flow, dropped)
	return decisionFromJS(res.Decision, flow.PhaseRequest)
}

// OnStreamMessage 执行流消息钩子(SSE / gRPC / 分块)。插件可就地改写 flow.data。
func (p *Plugin) OnStreamMessage(ctx context.Context, m *flow.StreamMessage) flow.Decision {
	text, b64 := payloadToJS(m.Data)
	in, _ := json.Marshal(jsFlow{
		Direction: m.Direction,
		Kind:      m.Kind,
		EventType: m.EventType,
		Data:      text,
		DataB64:   b64,
		URL:       m.URL,
	})
	out := p.dispatch("stream", in)
	if out == nil {
		return flow.ContinueDecision()
	}
	res, dropped, ok := parseOut(out, p.appendLog)
	if !ok {
		return flow.ContinueDecision()
	}
	p.applyMessagePayload(&m.Data, res.Flow, dropped)
	return decisionFromJS(res.Decision, flow.PhaseResponse)
}

// applyMessagePayload 将 WS / 流钩子的载荷写回消息，仅在字节变化时更新原始切片。
func (p *Plugin) applyMessagePayload(data *[]byte, jf jsFlow, dropped dropSet) {
	if dropped.has("flow.data", "flow.dataB64") {
		return
	}
	b, ok := resolvePayload(jf.Data, jf.DataB64, "flow.data",
		"flow.dataB64 不是合法的标准 base64,消息载荷保持原值", p.appendLog)
	if !ok || bytes.Equal(b, *data) {
		return
	}
	*data = b
}

// Logs 返回最近的插件日志(结构化,供 UI 按级别过滤)。
func (p *Plugin) Logs() []LogEntry {
	p.logsMu.Lock()
	defer p.logsMu.Unlock()
	out := make([]LogEntry, len(p.logs))
	copy(out, p.logs)
	return out
}

// ClearLogs 清空插件日志环形缓冲。
func (p *Plugin) ClearLogs() {
	p.logsMu.Lock()
	p.logs = nil
	p.logsMu.Unlock()
}

// Snapshot 返回 store 的深拷贝（store 内容均为 JSON 形态），供热重载迁移到新实例。
// 迁移结果记录被跳过的不可序列化路径。
func (p *Plugin) Snapshot() map[string]any {
	p.storeMu.Lock()
	out, dropped := deepCopyJSON(p.store)
	p.storeMu.Unlock()
	if len(dropped) > 0 {
		p.appendLog("error", "store 快照跳过了不可序列化的键: "+strings.Join(dropped, ", "))
	}
	return out
}

// Close 停止插件 goroutine，等待 store 刷写 goroutine 退出后完成最终落盘。
func (p *Plugin) Close() {
	p.once.Do(func() {
		close(p.quit)
		if p.flusherDone != nil {
			<-p.flusherDone
		}
		p.flushStore()
	})
}

func (p *Plugin) appendLog(level, msg string) {
	e := LogEntry{Level: level, Msg: msg, Time: time.Now().UnixMilli()}
	p.logsMu.Lock()
	p.logs = append(p.logs, e)
	if len(p.logs) > 200 {
		p.logs = p.logs[len(p.logs)-200:]
	}
	p.logsMu.Unlock()
	if p.cfg.OnLog != nil {
		p.cfg.OnLog(e)
	}
}

// ---- store 持久化 ----

func (p *Plugin) storeFlusher() {
	defer close(p.flusherDone)
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-p.quit:
			return
		case <-t.C:
			if p.storeDirty.Swap(false) {
				p.flushStore()
			}
		}
	}
}

func (p *Plugin) flushStore() {
	if p.cfg.StatePath == "" {
		return
	}
	p.storeMu.Lock()
	data, err := json.Marshal(p.store)
	var dropped []string
	if err != nil {
		// 按键递归复制可序列化数据，并更新 store。
		var clean map[string]any
		clean, dropped = deepCopyJSONPerKey(p.store)
		if data, err = json.Marshal(clean); err == nil {
			p.store = clean
		}
	}
	p.storeMu.Unlock()
	if len(dropped) > 0 {
		p.appendLog("error", "store 落盘跳过了不可序列化的键: "+strings.Join(dropped, ", "))
	}
	if err != nil {
		return
	}
	// 每个实例使用独立临时文件名，最终通过 rename 原子替换目标文件。
	tmp := p.cfg.StatePath + ".tmp." + p.tmpNonce
	if os.WriteFile(tmp, data, 0o644) == nil {
		_ = os.Rename(tmp, p.cfg.StatePath)
	}
}

// randHexBytes 返回 n 字节随机数据的 hex 串。
func randHexBytes(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func loadStore(path string) map[string]any {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(data, &m) != nil {
		return nil
	}
	return m
}

// ---- 转换 ----

// payloadToJS 将合法 UTF-8 载荷放入文本字段，其余载荷放入标准 base64 字段。
func payloadToJS(b []byte) (text, b64 string) {
	if len(b) == 0 {
		return "", ""
	}
	if utf8.Valid(b) {
		return string(b), ""
	}
	return "", base64.StdEncoding.EncodeToString(b)
}

// payloadFromJS 按文本优先级还原脚本回传的载荷字节；文本为空时解码 b64。
// ok 表示载荷是否成功解析。
func payloadFromJS(text, b64 string) ([]byte, bool) {
	if text == "" && b64 != "" {
		raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
		if err != nil {
			return nil, false
		}
		return raw, true
	}
	return []byte(text), true
}

// resolvePayload 按通道优先级解析载荷，并记录字段冲突与解码错误。
func resolvePayload(text, b64, field, failMsg string, logf func(level, msg string)) ([]byte, bool) {
	if text != "" && b64 != "" {
		emitLog(logf, "error", field+" 与 "+field+"B64 同时有值,已按 "+field+" 为准;"+
			"要走二进制通道请先把 "+field+" 置为空串")
	}
	b, ok := payloadFromJS(text, b64)
	if !ok {
		emitLog(logf, "error", failMsg)
	}
	return b, ok
}

func emitLog(logf func(level, msg string), level, msg string) {
	if logf != nil {
		logf(level, msg)
	}
}

// 严格解码下各字段要求的 JSON 值形态。
// 's' 字符串、'n' 数字、'h' 字符串值对象（扁平头视图）、'o' 对象。
var (
	jsFlowFieldKinds = map[string]byte{
		"id": 's', "method": 's', "url": 's', "host": 's', "path": 's',
		"headers": 'h', "body": 's', "bodyB64": 's', "response": 'o', "process": 'o',
		"direction": 's', "type": 's', "data": 's', "dataB64": 's', "kind": 's', "eventType": 's',
	}
	jsResponseFieldKinds = map[string]byte{
		"status": 'n', "statusText": 's', "headers": 'h', "body": 's', "bodyB64": 's', "reason": 's',
	}
	jsProcessFieldKinds  = map[string]byte{"name": 's', "pid": 'n', "path": 's'}
	jsDecisionFieldKinds = map[string]byte{"kind": 's', "status": 'n', "reason": 's'}
)

// parseOut 先按字段类型严格解码；类型不匹配时清理对应字段并记录，其余字段继续生效。
func parseOut(out []byte, logf func(level, msg string)) (jsOut, dropSet, bool) {
	var res jsOut
	if json.Unmarshal(out, &res) == nil {
		return res, nil, true
	}
	var doc map[string]any
	if json.Unmarshal(out, &doc) != nil {
		emitLog(logf, "error", "插件返回的 flow 无法解析,本次改动与处置已忽略")
		return jsOut{}, nil, false
	}
	dropped := dropSet{}
	if fl, ok := doc["flow"].(map[string]any); ok {
		pruneJSObject(fl, jsFlowFieldKinds, "flow", dropped, logf)
		if r, ok := fl["response"].(map[string]any); ok {
			pruneJSObject(r, jsResponseFieldKinds, "flow.response", dropped, logf)
		}
		if pr, ok := fl["process"].(map[string]any); ok {
			pruneJSObject(pr, jsProcessFieldKinds, "flow.process", dropped, logf)
		}
	} else {
		// flow 需要对象形态；其他形态的可编辑字段沿用原值。
		if v, present := doc["flow"]; present && v != nil {
			emitLog(logf, "error", "插件把 flow 写成了非法类型,本次 flow 改动已忽略")
		}
		delete(doc, "flow")
		markFlowDropped(dropped)
	}
	if d, present := doc["decision"]; present && d != nil {
		if dm, ok := d.(map[string]any); ok {
			pruneJSObject(dm, jsDecisionFieldKinds, "decision", dropped, logf)
		} else {
			// 非对象 decision 采用 Continue，并记录字段错误。
			delete(doc, "decision")
			emitLog(logf, "error", "插件把 decision 写成了非法类型,本次处置按放行处理")
		}
	}
	b, err := json.Marshal(doc)
	if err != nil {
		return jsOut{}, nil, false
	}
	// 使用新对象承接清理后的 JSON。
	var clean jsOut
	if json.Unmarshal(b, &clean) != nil {
		return jsOut{}, nil, false
	}
	return clean, dropped, true
}

// dropSet 记录解析时跳过的字段路径，写回逻辑据此保留对应原值。
type dropSet map[string]bool

func (d dropSet) has(paths ...string) bool {
	for _, p := range paths {
		if d[p] {
			return true
		}
	}
	return false
}

// markFlowDropped 标记 flow 中的可编辑字段，供非法 flow 形态保持原值。
func markFlowDropped(dropped dropSet) {
	for _, k := range []string{
		"flow.method", "flow.url", "flow.body", "flow.bodyB64",
		"flow.data", "flow.dataB64", "flow.response.body", "flow.response.bodyB64",
	} {
		dropped[k] = true
	}
}

// pruneJSObject 删除类型与 kinds 声明不符的字段；头对象包含非字符串值时整体跳过。
func pruneJSObject(obj map[string]any, kinds map[string]byte, path string, dropped dropSet, logf func(level, msg string)) {
	for k, v := range obj {
		kind, known := kinds[k]
		if !known {
			delete(obj, k) // 未知字段不属于脚本输出契约
			continue
		}
		if v == nil {
			continue
		}
		ok := false
		msg := "插件把 " + path + "." + k + " 写成了非法类型,该字段已忽略"
		switch kind {
		case 's':
			_, ok = v.(string)
		case 'n':
			_, ok = v.(float64)
		case 'o':
			_, ok = v.(map[string]any)
		case 'h':
			var hm map[string]any
			if hm, ok = v.(map[string]any); ok {
				for hk, hv := range hm {
					if _, isStr := hv.(string); !isStr && hv != nil {
						ok = false
						msg = "插件把 " + path + "." + k + "['" + hk + "'] 写成了非字符串,本次头改动已忽略"
						break
					}
				}
			}
		}
		if !ok {
			delete(obj, k)
			dropped[path+"."+k] = true
			emitLog(logf, "error", msg)
		}
	}
}

func requestToJS(f *flow.Flow) jsFlow {
	v := jsFlow{ID: f.ID}
	if f.Request != nil {
		// 标量字段按 JSON 出境后的字符串形态构造，作为回程比较基准。
		v.Method = flow.SanitizeJSONString(f.Request.Method)
		v.URL = flow.SanitizeJSONString(f.Request.URL)
		v.Host = flow.SanitizeJSONString(f.Request.Host)
		v.Path = flow.SanitizeJSONString(f.Request.Path)
		v.Headers = flatten(f.Request.Header)
		v.Body, v.BodyB64 = payloadToJS(f.Request.Body)
	}
	if f.Response != nil {
		body, b64 := payloadToJS(f.Response.Body)
		v.Response = &jsResponse{
			Status:     f.Response.Status,
			StatusText: flow.SanitizeJSONString(f.Response.StatusText),
			Headers:    flatten(f.Response.Header),
			Body:       body,
			BodyB64:    b64,
		}
	}
	if p := f.Process(); p != nil {
		v.Process = &jsProcess{Name: p.Name, PID: p.PID, Path: p.Path}
	}
	return v
}

// applyHTTP 将 VM 返回的 flow 增量应用回 Go flow.Flow，并以 sent 作为改动比较基准。
// 头部保留未改键的原始多值与顺序，响应结构按字段增量更新。
func applyHTTP(f *flow.Flow, sent *jsFlow, out []byte, phase flow.Phase, logf func(level, msg string)) flow.Decision {
	res, dropped, ok := parseOut(out, logf)
	if !ok {
		return flow.ContinueDecision()
	}
	jf := res.Flow
	changed := false

	if f.Request != nil {
		r := f.Request
		// method/url 仅接受非空且相对 sent 有变化的值；host/path 使用同一比较规则。
		if !dropped.has("flow.method") && jf.Method != "" && jf.Method != sent.Method {
			r.Method = jf.Method
			changed = true
		}
		if !dropped.has("flow.url") && jf.URL != "" && jf.URL != sent.URL {
			r.URL = jf.URL
			changed = true
		}
		if jf.Host != "" && jf.Host != sent.Host {
			r.Host = jf.Host
			changed = true
		}
		if jf.Path != "" && jf.Path != sent.Path {
			r.Path = jf.Path
			changed = true
		}
		// headers 为 nil 表示未提供，空 map 表示清空全部头。
		if jf.Headers != nil {
			if nh, hChanged := mergeHeaders(r.Header, sent.Headers, jf.Headers); hChanged {
				r.Header = nh
				changed = true
			}
		}
		if !dropped.has("flow.body", "flow.bodyB64") {
			if b, ok := resolvePayload(jf.Body, jf.BodyB64, "flow.body",
				"flow.bodyB64 不是合法的标准 base64,请求体保持原值", logf); ok {
				if !bytes.Equal(b, r.Body) {
					r.Body = b
					changed = true
				}
			}
		}
	}

	if jf.Response != nil {
		if f.Response != nil {
			r := f.Response
			sr := sent.Response
			if sr == nil {
				sr = &jsResponse{} // sent 与 f 同源，此处为异常形态提供空视图
			}
			if jf.Response.Status != 0 && jf.Response.Status != r.Status {
				r.Status = jf.Response.Status
				changed = true
			}
			if jf.Response.StatusText != "" && jf.Response.StatusText != sr.StatusText {
				r.StatusText = jf.Response.StatusText
				changed = true
			}
			if jf.Response.Headers != nil {
				if nh, hChanged := mergeHeaders(r.Header, sr.Headers, jf.Response.Headers); hChanged {
					r.Header = nh
					changed = true
				}
			}
			if !dropped.has("flow.response.body", "flow.response.bodyB64") {
				if b, ok := resolvePayload(jf.Response.Body, jf.Response.BodyB64, "flow.response.body",
					"flow.response.bodyB64 不是合法的标准 base64,响应体保持原值", logf); ok {
					if !bytes.Equal(b, r.Body) {
						r.Body = b
						changed = true
					}
				}
			}
		} else {
			// 请求阶段脚本设置了 response(mock):此时无原始响应可保留,整体新建。
			// mock 载荷按文本优先级解析，bodyB64 提供二进制响应体。
			body, ok := resolvePayload(jf.Response.Body, jf.Response.BodyB64, "mock 的 body",
				"mock 的 bodyB64 不是合法的标准 base64,已按空响应体处理", logf)
			if !ok {
				body = nil
			}
			f.Response = &flow.Response{
				Status:     jf.Response.Status,
				StatusText: jf.Response.StatusText,
				Header:     unflatten(jf.Response.Headers),
				Body:       body,
			}
			changed = true
		}
	}

	if changed {
		f.Modified = true
	}
	return decisionFromJS(res.Decision, phase)
}

func decisionFromJS(d jsDecision, phase flow.Phase) flow.Decision {
	switch d.Kind {
	case "mock":
		return flow.MockDecision(d.Reason)
	case "abort":
		return flow.AbortDecision(d.Status, d.Reason)
	case "breakpoint":
		return flow.BreakpointDecision(phase, d.Reason)
	default:
		return flow.ContinueDecision()
	}
}

// flatten 生成脚本可见的首值扁平头视图，并将值归一为 JSON 出境后的字符串形态。
// 头名含非法 UTF-8 字节时跳过该键，mergeHeaders 会保留原始头。
func flatten(h map[string][]string) map[string]string {
	out := make(map[string]string, len(h))
	for k, v := range h {
		if len(v) > 0 && utf8.ValidString(k) {
			out[k] = flow.SanitizeJSONString(v[0])
		}
	}
	return out
}

func unflatten(m map[string]string) map[string][]string {
	out := make(map[string][]string, len(m))
	for k, v := range m {
		out[k] = []string{v}
	}
	return out
}

// mergeHeaders 将脚本回传的扁平头视图合并回原始多值头。
// edited 与 sent 相同的键保留 orig，多值变化或新增键写入单值，缺失键移除。
func mergeHeaders(orig map[string][]string, sent, edited map[string]string) (map[string][]string, bool) {
	if sameStringMap(sent, edited) {
		return orig, false
	}
	out := make(map[string][]string, len(edited)+2)
	// 脚本视图未覆盖的头保留原值。
	for k, v := range orig {
		if len(v) == 0 || !utf8.ValidString(k) {
			out[k] = v
		}
	}
	for k, v := range edited {
		if sv, ok := sent[k]; ok && sv == v {
			out[k] = orig[k] // 脚本没碰:保留原始多值切片
		} else {
			out[k] = []string{v}
		}
	}
	return out, true
}

func sameStringMap(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}

// deepCopyJSON 通过 JSON 深拷贝 map，并返回跳过的键路径。
func deepCopyJSON(m map[string]any) (map[string]any, []string) {
	if len(m) == 0 {
		return map[string]any{}, nil
	}
	if b, err := json.Marshal(m); err == nil {
		var out map[string]any
		if json.Unmarshal(b, &out) == nil {
			return out, nil
		}
	}
	return deepCopyJSONPerKey(m)
}

// deepCopyJSONPerKey 逐键递归拷贝 map，并记录不可序列化的路径。
func deepCopyJSONPerKey(m map[string]any) (map[string]any, []string) {
	var dropped []string
	out := make(map[string]any, len(m))
	seen := make(map[uintptr]bool)
	for k, v := range m {
		if cv, ok := copyValueJSON(v, k, seen, 0, &dropped); ok {
			out[k] = cv
		}
	}
	return out, dropped
}

// copyValueJSON 递归拷贝值；seen 记录当前路径上的 map/slice，循环引用按路径记录并跳过。
func copyValueJSON(v any, path string, seen map[uintptr]bool, depth int, dropped *[]string) (any, bool) {
	if b, err := json.Marshal(v); err == nil {
		var out any
		if json.Unmarshal(b, &out) == nil {
			return out, true
		}
	}
	if depth < 64 {
		switch t := v.(type) {
		case map[string]any:
			ptr := reflect.ValueOf(t).Pointer()
			if seen[ptr] {
				break
			}
			seen[ptr] = true
			out := make(map[string]any, len(t))
			for k, sv := range t {
				if cv, ok := copyValueJSON(sv, path+"."+k, seen, depth+1, dropped); ok {
					out[k] = cv
				}
			}
			delete(seen, ptr)
			return out, true
		case []any:
			ptr := reflect.ValueOf(t).Pointer()
			if seen[ptr] {
				break
			}
			seen[ptr] = true
			out := make([]any, 0, len(t))
			for i, sv := range t {
				if cv, ok := copyValueJSON(sv, path+"["+strconv.Itoa(i)+"]", seen, depth+1, dropped); ok {
					out = append(out, cv)
				}
			}
			delete(seen, ptr)
			return out, true
		}
	}
	*dropped = append(*dropped, path)
	return nil, false
}

// uuidV4 生成符合 RFC 4122 的随机 UUID(crypto/rand)。
func uuidV4() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// matchPattern 支持 *、prefix*、*suffix、精确匹配。
func matchPattern(pattern, s string) bool {
	if pattern == "*" || pattern == "" {
		return true
	}
	if strings.HasPrefix(pattern, "*") && strings.HasSuffix(pattern, "*") && len(pattern) > 1 {
		return strings.Contains(s, strings.Trim(pattern, "*"))
	}
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(s, strings.TrimSuffix(pattern, "*"))
	}
	if strings.HasPrefix(pattern, "*") {
		return strings.HasSuffix(s, strings.TrimPrefix(pattern, "*"))
	}
	return pattern == s
}
