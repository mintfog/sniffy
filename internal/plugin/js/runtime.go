// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package js

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"
)

type job struct {
	phase string
	in    []byte
	reply chan []byte
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

// uuidV4 生成符合 RFC 4122 的随机 UUID(crypto/rand)。
func uuidV4() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
