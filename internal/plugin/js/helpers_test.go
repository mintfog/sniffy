// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package js

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// hlpEval 在插件 VM 顶层执行助手脚本，并通过 store 传回 JSON 结果。
// hput/hputj 分别记录字符串和值的 JSON 表示。
func hlpEval(t *testing.T, id, body string) map[string]string {
	t.Helper()
	src := "var __hout = {};\n" +
		"function hput(k, v){ __hout[k] = String(v); }\n" +
		"function hputj(k, v){ __hout[k] = (v === undefined ? '@undefined' : JSON.stringify(v)); }\n" +
		body + "\n" +
		"store.set('__hout', JSON.stringify(__hout));\n"
	// 助手测试包含随机大载荷与循环，使用独立的宽松超时。
	p := mustPlugin(t, Config{ID: id, Timeout: 5 * time.Second, Source: src})
	raw, ok := p.Snapshot()["__hout"].(string)
	if !ok || raw == "" {
		t.Fatalf("脚本未产出结果: %#v", p.Snapshot()["__hout"])
	}
	var out map[string]string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("结果不可解析: %v (%q)", err, raw)
	}
	return out
}

// hlpGet 读取指定测试出口。
func hlpGet(t *testing.T, got map[string]string, key string) string {
	t.Helper()
	v, ok := got[key]
	if !ok {
		t.Fatalf("出口 %s 缺失,实际出口: %v", key, got)
	}
	return v
}

func hlpWant(t *testing.T, got map[string]string, key, want string) {
	t.Helper()
	if v := hlpGet(t, got, key); v != want {
		t.Errorf("%s = %q, 期望 %q", key, v, want)
	}
}

// 验证解码助手对合法与非法输入的返回值。
func TestJSHelperCodecErrorBranches(t *testing.T) {
	cases := []struct {
		name string
		expr string
		want string
	}{
		{"base64_非法字符", "base64.decode('!!!!')", ""},
		{"base64_缺少padding", "base64.decode('aGk')", ""},
		{"base64_容忍首尾空白", "base64.decode('  aGk=  ')", "hi"},
		{"atob_非法字符", "atob('!!!!')", ""},
		{"base64url_非法字符", "base64.urlDecode('!!!!')", ""},
		{"base64url_带padding", "base64.urlDecode('fn4=')", "~~"},
		{"base64url_不带padding", "base64.urlDecode('fn4')", "~~"},
		{"hex_非法字符", "hex.decode('zz')", ""},
		{"hex_奇数长度", "hex.decode('6')", ""},
		{"hex_容忍首尾空白", "hex.decode(' 6869 ')", "hi"},
	}
	var b strings.Builder
	for i, c := range cases {
		fmt.Fprintf(&b, "hput('c%d', %s);\n", i, c.expr)
	}
	b.WriteString("hput('decbytes_bad_len', base64.decodeBytes('!!!!').length);\n")
	b.WriteString("hput('decbytes_ok', base64.decodeBytes('aGk=').join(','));\n")
	got := hlpEval(t, "hlp-codec", b.String())

	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hlpWant(t, got, fmt.Sprintf("c%d", i), c.want)
		})
	}
	// decodeBytes 的失败结果为空数组。
	hlpWant(t, got, "decbytes_bad_len", "0")
	hlpWant(t, got, "decbytes_ok", "104,105")
}

// 验证标准 base64 与 base64url 使用各自的字母表。
func TestJSHelperBase64URLAlphabet(t *testing.T) {
	// 该输入同时覆盖两套字母表的差异字符。
	got := hlpEval(t, "hlp-b64url", `
hput('std', base64.encode('~~~???'));
hput('url', base64.urlEncode('~~~???'));
hput('roundtrip', base64.urlDecode(base64.urlEncode('~~~???')));
hput('same', base64.encode('~~~???') === base64.urlEncode('~~~???'));
`)
	hlpWant(t, got, "std", "fn5+Pz8/")
	hlpWant(t, got, "url", "fn5-Pz8_")
	hlpWant(t, got, "roundtrip", "~~~???")
	hlpWant(t, got, "same", "false")
}

// 验证原始字节以 number[] 穿过 VM 边界，并保持编码与哈希结果。
func TestJSHelperRawBytesCrossVMBoundary(t *testing.T) {
	got := hlpEval(t, "hlp-bytes", `
var raw = []; for (var i = 0; i < 256; i++) { raw.push(i); }
hput('enc256', base64.encodeBytes(raw));
hput('sha256_256', crypto.hashBytes('sha256', raw));
hputj('dec256', base64.decodeBytes(base64.encodeBytes(raw)));
var s = 'a\u0000b\u00ff\u4e2d';
hputj('u8bytes', utf8.toBytes(s));
hput('u8b64', base64.encodeBytes(utf8.toBytes(s)));
hput('u8roundtrip', utf8.fromBytes(utf8.toBytes(s)));
hput('u8replacement', utf8.fromBytes(utf8.toBytes(s)).indexOf('\ufffd'));
`)

	raw := make([]byte, 256)
	ints := make([]int, 256)
	for i := range raw {
		raw[i] = byte(i)
		ints[i] = i
	}
	sum := sha256.Sum256(raw)
	hlpWant(t, got, "enc256", base64.StdEncoding.EncodeToString(raw))
	hlpWant(t, got, "sha256_256", hex.EncodeToString(sum[:]))
	decWant, _ := json.Marshal(ints)
	hlpWant(t, got, "dec256", string(decWant))

	s := "a\x00bÿ中"
	sBytes := []byte(s)
	sInts := make([]int, len(sBytes))
	for i, v := range sBytes {
		sInts[i] = int(v)
	}
	u8Want, _ := json.Marshal(sInts)
	hlpWant(t, got, "u8bytes", string(u8Want))
	hlpWant(t, got, "u8b64", base64.StdEncoding.EncodeToString(sBytes))
	hlpWant(t, got, "u8roundtrip", s)
	hlpWant(t, got, "u8replacement", "-1")
}

var hlpHashes = map[string]func() hash.Hash{
	"md5":    md5.New,
	"sha1":   sha1.New,
	"sha256": sha256.New,
	"sha512": sha512.New,
}

// 验证四种哈希/HMAC 算法及其 hex、base64、base64url 输出。
func TestJSHelperHashHMACAlgorithms(t *testing.T) {
	algos := []string{"md5", "sha1", "sha256", "sha512"}
	var b strings.Builder
	for _, a := range algos {
		fmt.Fprintf(&b, "hput('%s_hex', crypto.hmac('%s','k','m'));\n", a, a)
		fmt.Fprintf(&b, "hput('%s_b64', crypto.hmacBase64('%s','k','m'));\n", a, a)
		fmt.Fprintf(&b, "hput('%s_b64url', crypto.hmacBase64Url('%s','k','m'));\n", a, a)
		fmt.Fprintf(&b, "hput('%s_bytes', crypto.hashBytes('%s',[0,255,128]));\n", a, a)
		fmt.Fprintf(&b, "hput('%s_str_hex', crypto.%s('m'));\n", a, a)
		fmt.Fprintf(&b, "hput('%s_str_b64', crypto.%sBase64('m'));\n", a, a)
	}
	// 算法名按大小写不敏感处理。
	b.WriteString("hput('upper_hmac', crypto.hmac('SHA256','k','m'));\n")
	b.WriteString("hput('mixed_hash', crypto.hashBytes('ShA1',[0,255,128]));\n")
	got := hlpEval(t, "hlp-hmac", b.String())

	for _, a := range algos {
		t.Run(a, func(t *testing.T) {
			mac := hmac.New(hlpHashes[a], []byte("k"))
			mac.Write([]byte("m"))
			sum := mac.Sum(nil)
			hlpWant(t, got, a+"_hex", hex.EncodeToString(sum))
			hlpWant(t, got, a+"_b64", base64.StdEncoding.EncodeToString(sum))
			hlpWant(t, got, a+"_b64url", base64.RawURLEncoding.EncodeToString(sum))

			h := hlpHashes[a]()
			h.Write([]byte{0, 255, 128})
			hlpWant(t, got, a+"_bytes", hex.EncodeToString(h.Sum(nil)))

			sh := hlpHashes[a]()
			sh.Write([]byte("m"))
			ssum := sh.Sum(nil)
			hlpWant(t, got, a+"_str_hex", hex.EncodeToString(ssum))
			hlpWant(t, got, a+"_str_b64", base64.StdEncoding.EncodeToString(ssum))
		})
	}
	hlpWant(t, got, "upper_hmac", hlpGet(t, got, "sha256_hex"))
	hlpWant(t, got, "mixed_hash", hlpGet(t, got, "sha1_bytes"))
}

// 验证未知算法返回空字符串并保持字符串类型。
func TestJSHelperUnknownAlgorithmEmpty(t *testing.T) {
	got := hlpEval(t, "hlp-algo", `
hput('hashBytes', crypto.hashBytes('sha3', [1,2,3]));
hput('hashBytes_empty', crypto.hashBytes('', [1,2,3]));
hput('hmac', crypto.hmac('sha3','k','m'));
hput('hmacBase64', crypto.hmacBase64('sha3','k','m'));
hput('hmacBase64Url', crypto.hmacBase64Url('sha3','k','m'));
hput('type', typeof crypto.hmac('sha3','k','m'));
`)
	for _, k := range []string{"hashBytes", "hashBytes_empty", "hmac", "hmacBase64", "hmacBase64Url"} {
		hlpWant(t, got, k, "")
	}
	hlpWant(t, got, "type", "string")
}

// 验证随机助手的长度、范围、默认值与字符集约束。
func TestJSHelperRandomGuardrails(t *testing.T) {
	got := hlpEval(t, "hlp-rand", `
hput('bytes_0', crypto.randomBytes(0).length);
hput('bytes_neg', crypto.randomBytes(-1).length);
hput('bytes_over', crypto.randomBytes(4097).length);
hput('bytes_max', crypto.randomBytes(4096).length);
var bs = crypto.randomBytes(64), bad = 0;
for (var i = 0; i < bs.length; i++) {
  var v = bs[i];
  if (typeof v !== 'number' || v < 0 || v > 255 || v !== Math.floor(v)) { bad++; }
}
hput('bytes_out_of_range', bad);
hput('id_0', randomId(0).length);
hput('id_neg', randomId(-1).length);
hput('id_over', randomId(300).length);
hput('id_hex', /^[0-9a-f]{16}$/.test(randomId(0)));
hput('int_eq', crypto.randomInt(7, 7));
hput('int_reversed', crypto.randomInt(7, 3));
hput('int_truncates', crypto.randomInt(1.9, 2.9));
var outside = 0;
for (var j = 0; j < 200; j++) {
  var r = crypto.randomInt(10, 20);
  if (r < 10 || r >= 20 || r !== Math.floor(r)) { outside++; }
}
hput('int_outside', outside);
hput('str_0', crypto.randomString(0).length);
hput('str_over', crypto.randomString(4097).length);
hput('str_max', crypto.randomString(4096).length);
var alpha = 'ab', s = crypto.randomString(200, alpha), foreign = 0;
for (var k = 0; k < s.length; k++) { if (alpha.indexOf(s.charAt(k)) < 0) { foreign++; } }
hput('str_alpha_len', s.length);
hput('str_alpha_foreign', foreign);
hput('str_alpha_varies', s.indexOf('a') >= 0 && s.indexOf('b') >= 0);
`)
	want := map[string]string{
		"bytes_0":            "0",
		"bytes_neg":          "0",
		"bytes_over":         "0",
		"bytes_max":          "4096",
		"bytes_out_of_range": "0",
		// 越界长度使用 8 字节随机 ID。
		"id_0":              "16",
		"id_neg":            "16",
		"id_over":           "16",
		"id_hex":            "true",
		"int_eq":            "7",
		"int_reversed":      "7",
		"int_truncates":     "1",
		"int_outside":       "0",
		"str_0":             "0",
		"str_over":          "0",
		"str_max":           "4096",
		"str_alpha_len":     "200",
		"str_alpha_foreign": "0",
		// 结果覆盖指定字符集中的两个字符。
		"str_alpha_varies": "true",
	}
	for k, v := range want {
		hlpWant(t, got, k, v)
	}
}

// 验证 url.parse 对绝对 URL、相对 URL 和非法 URL 的字段解析。
func TestJSHelperURLParse(t *testing.T) {
	got := hlpEval(t, "hlp-url", `
hput('bad', String(url.parse('http://x/%zz')));
var u = url.parse('https://u:p@example.com:8443/a/b?x=1&x=2&y=z#frag');
hput('protocol', u.protocol);
hput('host', u.host);
hput('hostname', u.hostname);
hput('port', u.port);
hput('path', u.path);
hput('hash', u.hash);
hput('query_x', u.query.x);
hput('query_y', u.query.y);
hput('query_n', Object.keys(u.query).length);
var rel = url.parse('/only/path?a=1');
hput('rel_protocol', JSON.stringify(rel.protocol));
hput('rel_host', JSON.stringify(rel.host));
hput('rel_path', rel.path);
`)
	want := map[string]string{
		"bad":      "null",
		"protocol": "https",
		// host/hostname 不包含 userinfo。
		"host":     "example.com:8443",
		"hostname": "example.com",
		"port":     "8443",
		"path":     "/a/b",
		"hash":     "frag",
		// 重复 query key 保留首值。
		"query_x":      "1",
		"query_y":      "z",
		"query_n":      "2",
		"rel_protocol": `""`,
		"rel_host":     `""`,
		"rel_path":     "/only/path",
	}
	for k, v := range want {
		hlpWant(t, got, k, v)
	}
}

// 验证 query.parse 的输入形式、重复键与 query.stringify 编码。
func TestJSHelperQueryParseStringify(t *testing.T) {
	got := hlpEval(t, "hlp-query", `
var withQ = query.parse('?a=1&b=2');
hput('withQ_a', withQ.a);
hput('withQ_b', withQ.b);
hput('withQ_n', Object.keys(withQ).length);
var noQ = query.parse('a=1&b=2');
hput('noQ_a', noQ.a);
hput('noQ_n', Object.keys(noQ).length);
var dup = query.parse('a=1&a=2');
hput('dup_a', dup.a);
hput('dup_n', Object.keys(dup).length);
hput('bad_n', Object.keys(query.parse('%zz=1')).length);
hput('empty_n', Object.keys(query.parse('')).length);
hput('stringify', query.stringify({b: 2, a: 'x y', c: true, d: 1.5}));
hput('stringify_empty', query.stringify({}));
`)
	want := map[string]string{
		"withQ_a": "1",
		"withQ_b": "2",
		"withQ_n": "2",
		"noQ_a":   "1",
		"noQ_n":   "2",
		"dup_a":   "1",
		"dup_n":   "1",
		"bad_n":   "0",
		"empty_n": "0",
		// stringify 按键排序并使用表单 URL 编码。
		"stringify":       "a=x+y&b=2&c=true&d=1.5",
		"stringify_empty": "",
	}
	for k, v := range want {
		hlpWant(t, got, k, v)
	}
}

// 验证 time 助手的 UTC 格式及 now、unix、iso 的单位与格式。
func TestJSHelperTimeNamespace(t *testing.T) {
	before := time.Now()
	got := hlpEval(t, "hlp-time", `
hput('empty', time.format(0, ''));
hput('datetime', time.format(0, 'datetime'));
hput('date', time.format(0, 'date'));
hput('iso', time.format(0, 'iso'));
hput('default', time.format(0));
hput('custom', time.format(1700000000123, '2006/01/02 15:04:05'));
hput('now', time.now());
hput('unix', time.unix());
hput('isoNow', time.iso());
`)
	after := time.Now()

	layouts := map[string]string{
		"empty":    "1970-01-01 00:00:00",
		"datetime": "1970-01-01 00:00:00",
		"default":  "1970-01-01 00:00:00",
		"date":     "1970-01-01",
		// ISO 输出以 Z 表示 UTC。
		"iso":    "1970-01-01T00:00:00Z",
		"custom": "2023/11/14 22:13:20",
	}
	for k, v := range layouts {
		hlpWant(t, got, k, v)
	}

	nowMs, err := strconv.ParseInt(hlpGet(t, got, "now"), 10, 64)
	if err != nil {
		t.Fatalf("time.now() 非整数: %q (%v)", got["now"], err)
	}
	unixSec, err := strconv.ParseInt(hlpGet(t, got, "unix"), 10, 64)
	if err != nil {
		t.Fatalf("time.unix() 非整数: %q (%v)", got["unix"], err)
	}
	if nowMs < before.UnixMilli() || nowMs > after.UnixMilli() {
		t.Errorf("time.now() = %d 不在 [%d,%d] 内,量纲可能不是毫秒", nowMs, before.UnixMilli(), after.UnixMilli())
	}
	if unixSec < before.Unix() || unixSec > after.Unix() {
		t.Errorf("time.unix() = %d 不在 [%d,%d] 内,量纲可能不是秒", unixSec, before.Unix(), after.Unix())
	}
	isoNow := hlpGet(t, got, "isoNow")
	if !strings.HasSuffix(isoNow, "Z") {
		t.Errorf("time.iso() = %q 未按 UTC 渲染", isoNow)
	}
	ts, err := time.Parse(time.RFC3339, isoNow)
	if err != nil {
		t.Fatalf("time.iso() 不是 RFC3339: %q (%v)", isoNow, err)
	}
	if ts.Unix() < before.Unix()-1 || ts.Unix() > after.Unix()+1 {
		t.Errorf("time.iso() = %q 与 now 不自洽 (窗口 %d..%d)", isoNow, before.Unix(), after.Unix())
	}
}

// 验证 header 命名空间的大小写不敏感读写和别名合并。
func TestJSHelperHeaderNamespace(t *testing.T) {
	got := hlpEval(t, "hlp-header", `
var h = {'X-Foo': '1', 'Other': 'z'};
hput('get_lower', header.get(h, 'x-foo'));
hput('get_upper', header.get(h, 'X-FOO'));
hput('has_mixed', header.has(h, 'x-FoO'));
hput('has_missing', header.has(h, 'nope'));
hput('get_missing', String(header.get(h, 'nope')));
header.set(h, 'x-foo', '2');
hputj('keys_after_set', Object.keys(h).sort());
hput('value_after_set', h['X-Foo']);
header.set(h, 'X-New', 'n');
hput('value_new', h['X-New']);
var h2 = {'X-Foo': '1', 'x-foo': '2', 'Keep': 'k'};
header.del(h2, 'X-FOO');
hputj('keys_after_del', Object.keys(h2).sort());
hput('nil_get', String(header.get(undefined, 'a')));
hput('nil_has', header.has(null, 'a'));
header.set(undefined, 'a', 'b');
header.del(null, 'a');
hput('nil_survived', '1');
`)
	want := map[string]string{
		"get_lower":   "1",
		"get_upper":   "1",
		"has_mixed":   "true",
		"has_missing": "false",
		"get_missing": "undefined",
		// 已有异名同键时保留原键名并更新值。
		"keys_after_set":  `["Other","X-Foo"]`,
		"value_after_set": "2",
		"value_new":       "n",
		// del 移除同名的全部大小写变体。
		"keys_after_del": `["Keep"]`,
		"nil_get":        "undefined",
		"nil_has":        "false",
		// 空容器上的助手调用保持安全返回。
		"nil_survived": "1",
	}
	for k, v := range want {
		hlpWant(t, got, k, v)
	}
}

// 验证 json 助手的安全解析、路径读取与序列化结果。
func TestJSHelperJSONNamespace(t *testing.T) {
	got := hlpEval(t, "hlp-json", `
hput('parse_ok', JSON.stringify(json.safeParse('{"a":1}')));
hput('parse_no_fb', String(json.safeParse('bad')));
hput('parse_fb_zero', JSON.stringify(json.safeParse('bad', 0)));
hput('parse_fb_false', JSON.stringify(json.safeParse('bad', false)));
hput('parse_fb_empty', JSON.stringify(json.safeParse('bad', '')));
hput('parse_fb_null', String(json.safeParse('bad', null)));
hputj('get_from_string', json.get('{"a":{"b":[10,20]}}', 'a.b.1'));
hputj('get_array_index', json.get({a: [{b: 'deep'}]}, 'a.0.b'));
hputj('get_mid_null', json.get({a: null}, 'a.b'));
hputj('get_empty_path', json.get({a: 1}, ''));
hputj('get_null_root', json.get(null, 'a'));
hputj('get_bad_string', json.get('nope', 'a'));
hput('stringify_pretty', json.stringify({a: 1}, true));
hput('stringify_plain', json.stringify({a: 1}, false));
hput('stringify_default', json.stringify({a: 1}));
var cyc = {}; cyc.self = cyc;
hput('stringify_cyclic', json.stringify(cyc));
hput('stringify_cyclic_type', typeof json.stringify(cyc));
`)
	want := map[string]string{
		"parse_ok":    `{"a":1}`,
		"parse_no_fb": "null",
		// 0、false 和空字符串均作为显式 fallback 保留。
		"parse_fb_zero":  "0",
		"parse_fb_false": "false",
		"parse_fb_empty": `""`,
		"parse_fb_null":  "null",
		// 点路径支持数组下标。
		"get_from_string": "20",
		"get_array_index": `"deep"`,
		// 路径中遇到 null 返回 undefined。
		"get_mid_null":          "@undefined",
		"get_empty_path":        "@undefined",
		"get_null_root":         "@undefined",
		"get_bad_string":        "@undefined",
		"stringify_pretty":      "{\n  \"a\": 1\n}",
		"stringify_plain":       `{"a":1}`,
		"stringify_default":     `{"a":1}`,
		"stringify_cyclic":      "",
		"stringify_cyclic_type": "string",
	}
	for k, v := range want {
		hlpWant(t, got, k, v)
	}
}

// 验证 JWT 助手对无效 token、签名和 URL 安全编码的处理。
func TestJSHelperJWTEdgeCases(t *testing.T) {
	got := hlpEval(t, "hlp-jwt", `
hput('decode_null', String(jwt.decode(null)));
hput('decode_undefined', String(jwt.decode(undefined)));
hput('decode_empty', String(jwt.decode('')));
hput('decode_one_seg', String(jwt.decode('abc')));
var two = base64.urlEncode('{"alg":"none"}') + '.' + base64.urlEncode('{"sub":"7"}');
hput('two_alg', jwt.decode(two).header.alg);
hput('two_sub', jwt.decode(two).payload.sub);
hput('two_sig', JSON.stringify(jwt.decode(two).signature));
hput('verify_one_seg', jwt.verifyHS256('abc', 'k'));
hput('verify_two_seg', jwt.verifyHS256(two, 'k'));
hput('verify_four_seg', jwt.verifyHS256('a.b.c.d', 'k'));
var tok = jwt.signHS256({sub: '42'}, 'sec');
hput('sign_segments', tok.split('.').length);
hput('sign_header', base64.urlDecode(tok.split('.')[0]));
hput('sign_url_safe', /[=+\/]/.test(tok));
hput('sign_verifies', jwt.verifyHS256(tok, 'sec'));
hput('sign_wrong_key', jwt.verifyHS256(tok, 'other'));
hput('sign_trailing_seg', jwt.verifyHS256(tok + '.extra', 'sec'));
`)
	want := map[string]string{
		"decode_null":      "null",
		"decode_undefined": "null",
		"decode_empty":     "null",
		"decode_one_seg":   "null",
		// 两段 token 的 signature 表示为空字符串。
		"two_alg":         "none",
		"two_sub":         "7",
		"two_sig":         `""`,
		"verify_one_seg":  "false",
		"verify_two_seg":  "false",
		"verify_four_seg": "false",
		"sign_segments":   "3",
		"sign_header":     `{"alg":"HS256","typ":"JWT"}`,
		// 签名 token 使用无填充的 base64url 字符集。
		"sign_url_safe":  "false",
		"sign_verifies":  "true",
		"sign_wrong_key": "false",
		// 额外段使 token 校验结果为 false。
		"sign_trailing_seg": "false",
	}
	for k, v := range want {
		hlpWant(t, got, k, v)
	}
}

// 验证 uuid() 生成 RFC 4122 v4 格式且每次结果唯一。
func TestJSHelperUUIDv4Shape(t *testing.T) {
	got := hlpEval(t, "hlp-uuid", `
for (var i = 0; i < 8; i++) { hput('u' + i, uuid()); }
`)
	re := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	seen := make(map[string]bool, 8)
	for i := 0; i < 8; i++ {
		u := hlpGet(t, got, fmt.Sprintf("u%d", i))
		if !re.MatchString(u) {
			t.Errorf("uuid() = %q 不符合 RFC 4122 v4", u)
		}
		if seen[u] {
			t.Errorf("uuid() 重复: %q", u)
		}
		seen[u] = true
	}
}
