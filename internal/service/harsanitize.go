// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/url"
	"regexp"
	"strings"
)

const (
	redactedValue = "[REDACTED]"
	// 正文脱敏要完整解析一遍 JSON，超过此大小按无法解析处理，避免导出卡在单个巨型正文上。
	maxSanitizeBodyBytes = 32 << 20
)

var redactedJSONValue = []byte(`"` + redactedValue + `"`)

// SanitizeOptions 是 HAR 导出的脱敏选项，零值表示原样导出。
type SanitizeOptions struct {
	// Redact 替换认证头、Cookie 值、URL 内嵌凭据，以及查询参数、表单与 JSON 正文中敏感字段的值。
	Redact bool `json:"redact"`
	// Fields 是额外按字段名匹配的敏感字段，忽略大小写与 - _ . 分隔符；仅在 Redact 时生效。
	Fields []string `json:"fields,omitempty"`
	// OmitBodies 省略请求正文、响应正文与 WebSocket 消息内容，保留大小。
	OmitBodies bool `json:"omitBodies"`
	// OmitClientInfo 省略客户端 IP 与发起进程。
	OmitClientInfo bool `json:"omitClientInfo"`
}

// SanitizeStats 汇总一次导出的脱敏结果。
type SanitizeStats struct {
	// Redacted 是被替换的值的个数；同一个值在 HAR 中的派生副本（cookies、queryString）不重复计数。
	Redacted int `json:"redacted"`
	// Unparsed 是无法按字段脱敏而整段省略的正文个数：结构化格式解析失败、超过大小上限，或是 multipart。
	Unparsed int `json:"unparsed"`
}

// 归一化后包含这些片段的字段名视为敏感。token 单独判断，见 classify。
var sensitiveKeyParts = []string{
	"secret", "password", "passwd", "pwd", "apikey", "accesskey", "privatekey",
	"credential", "sessionid", "sessid", "csrf", "xsrf", "signature", "authorization", "cookie",
}

// sensitivity 决定 JSON 中哪些类型的值会被替换；查询参数、表单与头部的值都是文本，两级同样替换。
type sensitivity int

const (
	notSensitive sensitivity = iota
	// tokenCounter 是名称含 token 但不以它结尾的字段（max_tokens、prompt_tokens、token_count），
	// 多为计数：只替换文本、对象与数组，数字和布尔值保留。
	tokenCounter
	// sensitiveField 替换除 null 以外的任何值：显式指定的字段与明确的凭据字段可能是数字（otp、pin、手机号）。
	sensitiveField
)

// 这些名字太短，按片段匹配会误伤大量普通字段，只做整词匹配。
var sensitiveKeys = map[string]struct{}{
	"auth": {}, "sid": {}, "session": {}, "otp": {}, "jwt": {}, "sig": {}, "key": {},
}

// 错误信息里内嵌的 URL，例如 net/http 的 `Get "https://…?access_token=…": dial tcp …`。
// 只以空白断开：引号、括号都可能合法地出现在查询串里（O'Reilly，或 %q 转义出的 \"），
// 拿它们当结束符会让后面的令牌漏出去。
// 协议名可选，协议相对地址（//user:pass@host）同样匹配。
var embeddedURLPattern = regexp.MustCompile(`(?i)(?:[a-z][a-z0-9+.-]*:)?//\S+`)

// 包裹 URL 的尾随标点：至多一个闭合符号加至多一个句读，如 `":`、`).`。
// 匹配以空白断开，包裹标点之后必然是空白或文本结尾，所以剩余部分须整体符合此形式。
var embeddedURLTrailer = regexp.MustCompile(`^["'>)\]}]?[,.;:]?$`)

type sanitizer struct {
	opts   SanitizeOptions
	fields map[string]struct{}
	stats  SanitizeStats
}

func newSanitizer(opts SanitizeOptions) *sanitizer {
	fields := make(map[string]struct{}, len(opts.Fields))
	for _, field := range opts.Fields {
		fields[normalizeKey(field)] = struct{}{}
	}
	return &sanitizer{opts: opts, fields: fields}
}

func normalizeKey(name string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '-', '_', '.', ' ':
			return -1
		}
		return r
	}, strings.ToLower(strings.TrimSpace(name)))
}

func (z *sanitizer) sensitive(name string) bool {
	return z.classify(name) != notSensitive
}

func (z *sanitizer) classify(name string) sensitivity {
	key := normalizeKey(name)
	if key == "" {
		return notSensitive
	}
	if _, ok := z.fields[key]; ok {
		return sensitiveField
	}
	if _, ok := sensitiveKeys[key]; ok {
		return sensitiveField
	}
	for _, part := range sensitiveKeyParts {
		if strings.Contains(key, part) {
			return sensitiveField
		}
	}
	switch {
	case strings.HasSuffix(key, "token"):
		return sensitiveField
	case strings.Contains(key, "token"):
		return tokenCounter
	}
	return notSensitive
}

func (z *sanitizer) entry(e *HAREntry) {
	if z.opts.OmitClientInfo {
		e.ClientIP, e.Process = "", nil
	}
	if z.opts.OmitBodies {
		e.Request.PostData = nil
		e.Response.Content.Text, e.Response.Content.Encoding = nil, ""
		for i := range e.WebSocketMessages {
			e.WebSocketMessages[i].Data, e.WebSocketMessages[i].Encoding = "", ""
		}
	}
	if !z.opts.Redact {
		return
	}

	requestURL := e.Request.URL
	var n int
	e.Request.URL, n = z.url(e.Request.URL)
	z.stats.Redacted += n
	e.Request.QueryString = archiveQueryURL(e.Request.URL)
	z.headers(e.Request.Headers)
	z.headers(e.Response.Headers)
	redactCookies(e.Request.Cookies)
	redactCookies(e.Response.Cookies)
	// redirectURL 是 Location 头的副本，计数已在头部完成。
	e.Response.RedirectURL, _ = z.url(e.Response.RedirectURL)
	e.Error = z.errorText(e.Error, requestURL, e.Request.URL)

	if post := e.Request.PostData; post != nil && post.Text != nil {
		if text, ok := z.body(post.MimeType, *post.Text, post.Encoding); ok {
			post.Text = &text
		} else {
			e.Request.PostData = nil
		}
	}
	if content := &e.Response.Content; content.Text != nil {
		if text, ok := z.body(content.MimeType, *content.Text, content.Encoding); ok {
			if text != *content.Text {
				// 导入时 size 大于正文长度会被当作「只有大小、没有正文」，必须与改写后的正文一致。
				content.Size = int64(len(text))
			}
			content.Text = &text
		} else {
			content.Text, content.Encoding = nil, ""
		}
	}
	for i := range e.WebSocketMessages {
		z.wsMessage(&e.WebSocketMessages[i])
	}
}

// errorText 处理错误信息中内嵌的 URL，不计数：它们通常是请求 URL 的副本。
//
// 末尾的标点无法区分是包裹 URL 的引号括号，还是敏感值自身的一部分（password=... 整个值都是标点）。
// 所以只在边界确定时保留它：匹配以原始请求 URL 开头且剩余部分只是包裹标点，
// 就用脱敏后的请求 URL 替换这一段，标点原样保留。其他情况整段交给 url 处理，
// 最后一个参数敏感时尾随内容随值一起被替换。
func (z *sanitizer) errorText(text, requestURL, sanitizedRequestURL string) string {
	return embeddedURLPattern.ReplaceAllStringFunc(text, func(match string) string {
		if requestURL != "" && strings.HasPrefix(match, requestURL) {
			rest := match[len(requestURL):]
			if embeddedURLTrailer.MatchString(rest) {
				return sanitizedRequestURL + rest
			}
		}
		out, _ := z.url(match)
		return out
	})
}

func (z *sanitizer) headers(pairs []HARPair) {
	for i := range pairs {
		p := &pairs[i]
		key := normalizeKey(p.Name)
		value := p.Value
		switch {
		case key == "cookie":
			value = z.cookieHeader(value)
		case key == "setcookie":
			value = z.setCookieHeader(value)
		case key == "authorization" || key == "proxyauthorization":
			value = z.authorization(value)
		case key == "location" || key == "referer" || key == "contentlocation":
			// 值是 URL，查询串与片段里可能带凭据。
			var n int
			value, n = z.url(value)
			z.stats.Redacted += n
		case z.sensitive(key) && value != "":
			value = redactedValue
			z.stats.Redacted++
		}
		if value != p.Value {
			// 原始字节副本与改写前的值一致，必须一起丢弃。
			p.Value, p.ValueBase64 = value, ""
		}
	}
}

// authorization 保留认证方案（Bearer、Basic 等），便于排查用的是哪种认证。
func (z *sanitizer) authorization(value string) string {
	if strings.TrimSpace(value) == "" {
		return value
	}
	z.stats.Redacted++
	if scheme, _, ok := strings.Cut(strings.TrimSpace(value), " "); ok {
		return scheme + " " + redactedValue
	}
	return redactedValue
}

func (z *sanitizer) cookieHeader(value string) string {
	parts := strings.Split(value, ";")
	for i, part := range parts {
		if name, v, ok := strings.Cut(part, "="); ok && v != "" {
			parts[i] = name + "=" + redactedValue
			z.stats.Redacted++
		}
	}
	return strings.Join(parts, ";")
}

// setCookieHeader 只替换首段的 Cookie 值，Path、Expires 等属性保留。
func (z *sanitizer) setCookieHeader(value string) string {
	pair, attrs, hasAttrs := strings.Cut(value, ";")
	name, v, ok := strings.Cut(pair, "=")
	if !ok || strings.TrimSpace(v) == "" {
		return value
	}
	z.stats.Redacted++
	out := name + "=" + redactedValue
	if hasAttrs {
		out += ";" + attrs
	}
	return out
}

// redactCookies 处理头部 Cookie 的解析副本，计数已在头部完成。
func redactCookies(cookies []HARCookie) {
	for i := range cookies {
		if cookies[i].Value != "" {
			cookies[i].Value = redactedValue
		}
	}
}

// url 去掉内嵌账号密码，并替换查询串与片段中敏感参数的值。只做字符串切分而不经 url.Parse
// 往返，其余部分的编码保持原样。片段会被一并处理，OAuth 隐式授权把令牌放在那里。
// 查询串里字段名的转义不合法时无法整段省略，按宽松解码的结果匹配。
func (z *sanitizer) url(raw string) (string, int) {
	if raw == "" {
		return raw, 0
	}
	count := 0
	rest, fragment, hasFragment := strings.Cut(raw, "#")
	rest, query, hasQuery := strings.Cut(rest, "?")
	if prefix, after, ok := splitAuthority(rest); ok {
		authority, path, hasPath := strings.Cut(after, "/")
		if at := strings.LastIndexByte(authority, '@'); at >= 0 {
			authority = authority[at+1:]
			count++
		}
		rest = prefix + authority
		if hasPath {
			rest += "/" + path
		}
	}
	if hasQuery {
		query, n, _ := z.formPairs(query)
		rest += "?" + query
		count += n
	}
	if hasFragment {
		fragment, n, _ := z.formPairs(fragment)
		rest += "#" + fragment
		count += n
	}
	return rest, count
}

// splitAuthority 拆出 authority 之前的部分（`scheme://` 或协议相对地址的 `//`）。
// `://` 前必须是合法的协议名，路径里出现的 `://` 不算。
func splitAuthority(s string) (prefix, rest string, ok bool) {
	if strings.HasPrefix(s, "//") {
		return "//", s[2:], true
	}
	scheme, rest, ok := strings.Cut(s, "://")
	if !ok || !isURLScheme(scheme) {
		return "", s, false
	}
	return scheme + "://", rest, true
}

func isURLScheme(s string) bool {
	if s == "" || !isASCIILetter(s[0]) {
		return false
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		if !isASCIILetter(c) && (c < '0' || c > '9') && c != '+' && c != '-' && c != '.' {
			return false
		}
	}
	return true
}

func isASCIILetter(c byte) bool {
	return (c|0x20) >= 'a' && (c|0x20) <= 'z'
}

// formPairs 替换敏感字段的值。字段名存在非法转义时仍按宽松解码的结果完成替换，
// 并报告 valid=false，由要求严格解析的调用方决定是否整段省略。
func (z *sanitizer) formPairs(s string) (out string, count int, valid bool) {
	if !strings.Contains(s, "=") {
		return s, 0, true
	}
	valid = true
	parts := strings.Split(s, "&")
	for i, part := range parts {
		name, value, ok := strings.Cut(part, "=")
		if !ok || value == "" {
			continue
		}
		decoded, nameValid := lenientQueryUnescape(name)
		valid = valid && nameValid
		if z.sensitive(decoded) {
			parts[i] = name + "=" + url.QueryEscape(redactedValue)
			count++
		}
	}
	return strings.Join(parts, "&"), count, valid
}

// lenientQueryUnescape 解码合法的 %XX 与 +，非法转义按字面保留；valid 报告是否全部合法。
// 宽松解码让 p%61ssword%ZZ 仍能被识别为 password 字段。
func lenientQueryUnescape(s string) (decoded string, valid bool) {
	if !strings.ContainsAny(s, "%+") {
		return s, true
	}
	valid = true
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '+':
			b.WriteByte(' ')
		case c == '%' && i+2 < len(s) && isHex(s[i+1]) && isHex(s[i+2]):
			b.WriteByte(unhex(s[i+1])<<4 | unhex(s[i+2]))
			i += 2
		case c == '%':
			valid = false
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	return b.String(), valid
}

func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || ((c|0x20) >= 'a' && (c|0x20) <= 'f')
}

func unhex(c byte) byte {
	if c <= '9' {
		return c - '0'
	}
	return (c | 0x20) - 'a' + 10
}

// body 返回改写后的正文；ok=false 表示正文声明了结构化格式却无法解析，调用方应整段省略，
// 不能把可能含敏感字段的原文写出去。非结构化正文原样返回。
func (z *sanitizer) body(mimeType, text, encoding string) (string, bool) {
	kind := structuredBodyKind(mimeType)
	if kind == "" {
		// fetch 以字符串为请求体时 Content-Type 默认是 text/plain，JSON 常以这种形式出现。
		// 未声明格式的正文按内容识别，解析失败视为普通文本原样保留。
		if encoding == "" && looksLikeJSON(text) {
			if out, ok := z.redactText("json", text); ok {
				return out, true
			}
		}
		return text, true
	}
	if encoding == "" {
		if out, ok := z.redactText(kind, text); ok {
			return out, true
		}
	}
	z.stats.Unparsed++
	return "", false
}

// redactText 按 kind 改写正文并累计替换数；超过大小上限、无法解析或格式不支持时返回 false。
func (z *sanitizer) redactText(kind, text string) (string, bool) {
	if len(text) > maxSanitizeBodyBytes {
		return "", false
	}
	var out string
	var n int
	switch kind {
	case "form":
		var valid bool
		if out, n, valid = z.formPairs(text); !valid {
			return "", false
		}
	case "json":
		redacted, count, err := redactJSON([]byte(text), z.classify)
		if err != nil {
			return "", false
		}
		out, n = string(redacted), count
	default:
		return "", false
	}
	z.stats.Redacted += n
	return out, true
}

func structuredBodyKind(mimeType string) string {
	mediaType, _, err := mime.ParseMediaType(mimeType)
	if err != nil {
		mediaType = strings.ToLower(strings.TrimSpace(mimeType))
	}
	switch {
	case strings.Contains(mediaType, "json"):
		return "json"
	case mediaType == "application/x-www-form-urlencoded":
		return "form"
	case mediaType == "multipart/form-data":
		return "multipart"
	}
	return ""
}

// wsMessage 只改写看起来是 JSON 的文本消息；其余文本与二进制消息无法按字段识别，原样保留。
func (z *sanitizer) wsMessage(m *HARWSMessage) {
	if m.Encoding != "" || m.Opcode != 1 || !looksLikeJSON(m.Data) {
		return
	}
	if out, ok := z.redactText("json", m.Data); ok {
		m.Data = out
		return
	}
	m.Data = ""
	z.stats.Unparsed++
}

func looksLikeJSON(s string) bool {
	s = strings.TrimSpace(s)
	return s != "" && (s[0] == '{' || s[0] == '[')
}

type jsonFrame struct {
	object  bool
	wantKey bool
}

// redactJSON 按 classify 的分级替换敏感键对应的值，返回改写后的字节与替换个数。
// 按 token 定位值的字节区间后原地拼接，未改动部分的格式、键序与数字写法都保持原样。
// 支持连续多个顶层值（NDJSON）。
func redactJSON(data []byte, classify func(string) sensitivity) ([]byte, int, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	var stack []jsonFrame
	var spans [][2]int64
	valueDone := func() {
		if n := len(stack); n > 0 && stack[n-1].object {
			stack[n-1].wantKey = true
		}
	}
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			if len(stack) > 0 {
				return nil, 0, io.ErrUnexpectedEOF
			}
			break
		}
		if err != nil {
			return nil, 0, err
		}
		if n := len(stack); n > 0 && stack[n-1].object && stack[n-1].wantKey {
			if tok == json.Delim('}') {
				stack = stack[:n-1]
				valueDone()
				continue
			}
			key, _ := tok.(string)
			stack[n-1].wantKey = false
			level := classify(key)
			if level == notSensitive {
				continue
			}
			start := jsonValueStart(data, dec.InputOffset())
			if start >= int64(len(data)) || !redactableJSONValue(data[start], level) {
				continue
			}
			if err := skipJSONValue(dec); err != nil {
				return nil, 0, err
			}
			spans = append(spans, [2]int64{start, dec.InputOffset()})
			stack[n-1].wantKey = true
			continue
		}
		switch tok {
		case json.Delim('{'):
			stack = append(stack, jsonFrame{object: true, wantKey: true})
		case json.Delim('['):
			stack = append(stack, jsonFrame{})
		case json.Delim('}'), json.Delim(']'):
			stack = stack[:len(stack)-1]
			valueDone()
		default:
			valueDone()
		}
	}
	if len(spans) == 0 {
		return data, 0, nil
	}
	var out bytes.Buffer
	out.Grow(len(data))
	var last int64
	for _, span := range spans {
		out.Write(data[last:span[0]])
		out.Write(redactedJSONValue)
		last = span[1]
	}
	out.Write(data[last:])
	return out.Bytes(), len(spans), nil
}

// jsonValueStart 从键之后跳过空白与冒号，定位值的第一个字节。
func jsonValueStart(data []byte, offset int64) int64 {
	for offset < int64(len(data)) {
		switch data[offset] {
		case ' ', '\t', '\r', '\n', ':':
			offset++
		default:
			return offset
		}
	}
	return offset
}

func redactableJSONValue(first byte, level sensitivity) bool {
	switch first {
	case '"', '{', '[':
		return true
	case 'n':
		return false
	default:
		return level == sensitiveField
	}
}

func skipJSONValue(dec *json.Decoder) error {
	depth := 0
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch tok {
		case json.Delim('{'), json.Delim('['):
			depth++
		case json.Delim('}'), json.Delim(']'):
			depth--
		}
		if depth == 0 {
			return nil
		}
	}
}
