// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

// Package js 用 goja 实现可由用户编写的 JavaScript 插件层。
//
// 设计要点(v1):
//   - 每个插件独占一个 goja.Runtime,由一个邮箱 goroutine 串行执行(goja 非协程安全)。
//   - flow 以 JSON 进出 VM(JSON.parse/stringify),避免宿主对象绑定的边界陷阱,简单且正确。
//   - 每次调用设超时,到点 Interrupt;脚本报错/超时一律失败开放(Continue),绝不影响代理。
//   - 暴露给脚本的 API:onRequest/onResponse/onWebSocketMessage/onStreamMessage、
//     mock()/abort()/setBreakpoint()、console.*、store.get/set(可落盘持久化)、settings、
//     以及助手命名空间 base64/hex/url/query/header/crypto/jwt/json/time/utf8
//     与 uuid()/randomId()/btoa()/atob()。
//
// 无侵入转发约束:头部以「首值扁平视图」进出 VM(作者侧仍用 flow.headers['X-Foo']='bar'
// 的简单写法),但写回时只覆盖脚本真正改过的键,未改动的键保留原始多值/顺序;响应结构亦
// 就地增量改写而非整体替换,从而不破坏 RawHeaders / Trailer / 保真回放(见 applyHTTP)。
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
	"strings"
	"sync"
	"sync/atomic"
	"time"

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

// Config 创建一个 JS 插件所需的元信息。
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

	// StatePath 为 store 的落盘路径(<插件目录>/state.json);为空则 store 仅驻内存。
	StatePath string
	// InitialStore 在热重载时由上一个实例迁移而来,优先于 StatePath 落盘内容,
	// 使 store 不因「保存即重载」而丢失。
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
}

type job struct {
	phase string
	in    []byte
	reply chan []byte
}

// 进出 VM 的 flow 视图(请求字段扁平到顶层,响应在 response 下)。
type jsFlow struct {
	ID       string            `json:"id"`
	Method   string            `json:"method,omitempty"`
	URL      string            `json:"url,omitempty"`
	Host     string            `json:"host,omitempty"`
	Path     string            `json:"path,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
	Body     string            `json:"body,omitempty"`
	Response *jsResponse       `json:"response,omitempty"`
	Process  *jsProcess        `json:"process,omitempty"`

	// WS / 流(SSE / gRPC / 分块)专用字段。
	Direction string `json:"direction,omitempty"`
	Type      string `json:"type,omitempty"`
	Data      string `json:"data,omitempty"`
	Kind      string `json:"kind,omitempty"`      // 流类型:sse|grpc|chunk
	EventType string `json:"eventType,omitempty"` // SSE 的 event 名
}

type jsResponse struct {
	Status     int               `json:"status"`
	StatusText string            `json:"statusText,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
	Body       string            `json:"body,omitempty"`
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

// hostSetup 声明桥接全局与作者可用的 API。注意:mock/setBreakpoint 仅在能被消费的
// 阶段生效(请求 / 请求·响应),在其它阶段调用会给出告警而非静默失效。
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
// utf8 与字节版 base64:用 number[](0-255)承载原始字节,避免 latin1 string 歧义。
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

func (p *Plugin) initVM() error {
	vm := goja.New()
	vm.SetMaxCallStackSize(2048)

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
	// settings 深拷贝后再交给 VM:绝不把 manifest 持有的活 map 暴露给脚本,
	// 否则脚本写 settings.x 会与 saveManifest 的 json.Marshal 形成并发读写(可崩溃)。
	_ = vm.Set("settings", deepCopyJSON(p.cfg.Settings))
	registerHelpers(vm)

	if _, err := vm.RunString(hostSetup); err != nil {
		return err
	}
	// 运行用户脚本,定义 onRequest/onResponse/onWebSocketMessage/onStreamMessage。
	if _, err := vm.RunString(p.cfg.Source); err != nil {
		return err
	}
	// 非严格模式编译,避免对桥接全局变量赋值时的严格模式限制。
	driver, err := goja.Compile(p.cfg.ID+"-driver", driverSrc, false)
	if err != nil {
		return err
	}
	p.vm = vm
	p.driver = driver
	return nil
}

// registerHelpers 注入 Go 实现的助手函数(base64/hex/url/query/uuid),
// 全部为纯 CPU、立即返回,不触碰网络/磁盘,符合 goja 单 VM、无事件循环约束。
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
	// 任意字节产物一律以 hex/base64 出口,绝不把裸字节当作 string 回传 VM
	// (Go string([]byte) 按 UTF-8 解释,会把哈希字节损坏成 U+FFFD)。
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
	// 一律走 crypto/rand;number[] 上限防作者误传巨值拖垮热路径。
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

// run 在 VM 中执行一次钩子(仅由 loop goroutine 调用)。
func (p *Plugin) run(phase string, in []byte) []byte {
	_ = p.vm.Set("__IN__", string(in))
	_ = p.vm.Set("__PHASE__", phase)

	timer := time.AfterFunc(p.timeout, func() { p.vm.Interrupt("timeout") })
	_, err := p.vm.RunProgram(p.driver)
	timer.Stop()
	p.vm.ClearInterrupt()
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
	in, _ := json.Marshal(requestToJS(f))
	out := p.dispatch(phase, in)
	if out == nil {
		return flow.ContinueDecision()
	}
	return applyHTTP(f, out, ph)
}

// OnWebSocketMessage 执行 WS 钩子。
func (p *Plugin) OnWebSocketMessage(ctx context.Context, m *flow.WSMessage) flow.Decision {
	in, _ := json.Marshal(jsFlow{
		Direction: m.Direction,
		Type:      m.Type,
		Data:      string(m.Data),
		URL:       m.URL,
	})
	out := p.dispatch("ws", in)
	if out == nil {
		return flow.ContinueDecision()
	}
	var res jsOut
	if json.Unmarshal(out, &res) == nil {
		m.Data = []byte(res.Flow.Data)
	}
	return decisionFromJS(res.Decision, flow.PhaseRequest)
}

// OnStreamMessage 执行流消息钩子(SSE / gRPC / 分块)。插件可就地改写 flow.data。
func (p *Plugin) OnStreamMessage(ctx context.Context, m *flow.StreamMessage) flow.Decision {
	in, _ := json.Marshal(jsFlow{
		Direction: m.Direction,
		Kind:      m.Kind,
		EventType: m.EventType,
		Data:      string(m.Data),
		URL:       m.URL,
	})
	out := p.dispatch("stream", in)
	if out == nil {
		return flow.ContinueDecision()
	}
	var res jsOut
	if json.Unmarshal(out, &res) == nil {
		m.Data = []byte(res.Flow.Data)
	}
	return decisionFromJS(res.Decision, flow.PhaseResponse)
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

// Snapshot 返回 store 的深拷贝(store 内容均为 JSON 形态),供热重载时迁移到新实例。
func (p *Plugin) Snapshot() map[string]any {
	p.storeMu.Lock()
	defer p.storeMu.Unlock()
	return deepCopyJSON(p.store)
}

// Close 停止插件 goroutine,等待 store 刷写 goroutine 退出后做最终落盘。
// 等待保证 Close 返回后不再有该插件的磁盘 I/O,使调用方(如 DeletePlugin 的 RemoveAll)有序。
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
	p.storeMu.Unlock()
	if err != nil {
		return
	}
	// 每个实例独占临时文件名,避免热重载期间新旧实例并发写撞同一 .tmp;最终 rename 原子。
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

func requestToJS(f *flow.Flow) jsFlow {
	v := jsFlow{ID: f.ID}
	if f.Request != nil {
		v.Method = f.Request.Method
		v.URL = f.Request.URL
		v.Host = f.Request.Host
		v.Path = f.Request.Path
		v.Headers = flatten(f.Request.Header)
		v.Body = string(f.Request.Body)
	}
	if f.Response != nil {
		v.Response = &jsResponse{
			Status:     f.Response.Status,
			StatusText: f.Response.StatusText,
			Headers:    flatten(f.Response.Header),
			Body:       string(f.Response.Body),
		}
	}
	if p := f.Process(); p != nil {
		v.Process = &jsProcess{Name: p.Name, PID: p.PID, Path: p.Path}
	}
	return v
}

// applyHTTP 把 VM 返回的 flow 增量应用回 Go flow.Flow,并返回处置。
// 关键:只覆盖脚本真正改过的字段。头部经 mergeHeaders 保留未改键的原始多值/顺序;
// 响应就地增量改写而非整体替换,从而不破坏 RawHeaders / Trailer / 保真回放。
func applyHTTP(f *flow.Flow, out []byte, phase flow.Phase) flow.Decision {
	var res jsOut
	if err := json.Unmarshal(out, &res); err != nil {
		return flow.ContinueDecision()
	}
	jf := res.Flow
	changed := false

	if f.Request != nil {
		r := f.Request
		if jf.Method != r.Method {
			r.Method = jf.Method
			changed = true
		}
		if jf.URL != r.URL {
			r.URL = jf.URL
			changed = true
		}
		if jf.Host != "" && jf.Host != r.Host {
			r.Host = jf.Host
			changed = true
		}
		if jf.Path != "" && jf.Path != r.Path {
			r.Path = jf.Path
			changed = true
		}
		if jf.Headers != nil {
			if nh, hChanged := mergeHeaders(r.Header, jf.Headers); hChanged {
				r.Header = nh
				changed = true
			}
		}
		if !bytes.Equal([]byte(jf.Body), r.Body) {
			r.Body = []byte(jf.Body)
			changed = true
		}
	}

	if jf.Response != nil {
		if f.Response != nil {
			r := f.Response
			if jf.Response.Status != 0 && jf.Response.Status != r.Status {
				r.Status = jf.Response.Status
				changed = true
			}
			if jf.Response.StatusText != "" && jf.Response.StatusText != r.StatusText {
				r.StatusText = jf.Response.StatusText
				changed = true
			}
			if jf.Response.Headers != nil {
				if nh, hChanged := mergeHeaders(r.Header, jf.Response.Headers); hChanged {
					r.Header = nh
					changed = true
				}
			}
			if !bytes.Equal([]byte(jf.Response.Body), r.Body) {
				r.Body = []byte(jf.Response.Body)
				changed = true
			}
		} else {
			// 请求阶段脚本设置了 response(mock):此时无原始响应可保留,整体新建。
			f.Response = &flow.Response{
				Status:     jf.Response.Status,
				StatusText: jf.Response.StatusText,
				Header:     unflatten(jf.Response.Headers),
				Body:       []byte(jf.Response.Body),
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

func flatten(h map[string][]string) map[string]string {
	out := make(map[string]string, len(h))
	for k, v := range h {
		if len(v) > 0 {
			out[k] = v[0]
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

// mergeHeaders 把脚本看到的扁平头视图(edited,首值)合并回原始多值头(orig):
// 未改动的键保留原始多值/顺序(维持无侵入转发);首值改变或新增的键以单值写入;
// 原有但脚本删除的键被移除。返回合并结果与是否发生改动。
func mergeHeaders(orig map[string][]string, edited map[string]string) (map[string][]string, bool) {
	if sameStringMap(flatten(orig), edited) {
		return orig, false
	}
	out := make(map[string][]string, len(edited))
	for k, v := range edited {
		if ov, ok := orig[k]; ok && len(ov) > 0 && ov[0] == v {
			out[k] = ov // 首值未变:保留原始多值切片
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

// deepCopyJSON 通过 JSON 往返深拷贝一个 settings map(settings 本就是 JSON 来源)。
func deepCopyJSON(m map[string]any) map[string]any {
	if len(m) == 0 {
		return map[string]any{}
	}
	b, err := json.Marshal(m)
	if err != nil {
		return map[string]any{}
	}
	var out map[string]any
	if json.Unmarshal(b, &out) != nil {
		return map[string]any{}
	}
	return out
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
