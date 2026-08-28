// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package flow

import (
	"encoding/base64"
	"fmt"
	"net/textproto"
	"strings"
	"unicode/utf8"
)

// SanitizeJSONString 返回字符串经过 encoding/json 边界后的形态：每个非法 UTF-8 字节
// 对应一个 U+FFFD，合法输入保持原值。结果用于比较 JSON 往返前后的头值、路径和状态文本。
func SanitizeJSONString(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	var b strings.Builder
	// 为替换符预留额外空间，减少构建结果时的扩容。
	b.Grow(len(s) + len(s)/2 + 8)
	// 按连续合法片段写入，保持常见 ASCII 头值的低开销。
	last := 0
	for i := 0; i < len(s); {
		if s[i] < utf8.RuneSelf {
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			b.WriteString(s[last:i])
			b.WriteRune(utf8.RuneError)
			i++
			last = i
			continue
		}
		i += size
	}
	b.WriteString(s[last:])
	return b.String()
}

// RestoreHeaderPairBytes 根据导出时的有序头基准恢复回传值中的原始字节。
// 基于 JSON 可见形态匹配头名和值，每个基准值只使用一次；匹配顺序为同名优先、值相同次之。
// basis 的值全部为合法 UTF-8 时直接返回入参。
func RestoreHeaderPairBytes(edited, basis [][2]string) [][2]string {
	var pool []headerBasisValue
	for _, kv := range basis {
		if utf8.ValidString(kv[1]) {
			continue
		}
		pool = append(pool, headerBasisValue{
			name:      textproto.CanonicalMIMEHeaderKey(kv[0]),
			raw:       kv[1],
			sanitized: SanitizeJSONString(kv[1]),
		})
	}
	if pool == nil {
		return edited
	}

	out := make([][2]string, len(edited))
	copy(out, edited)
	var renamed []int
	for i := range out {
			// 头名按出站解析规则归一化。
		name := textproto.CanonicalMIMEHeaderKey(strings.TrimSpace(out[i][0]))
		if j := takeBasisValue(pool, name, true, out[i][1]); j >= 0 {
			out[i][1] = pool[j].raw
			pool[j].used = true
			continue
		}
		renamed = append(renamed, i)
	}
	for _, i := range renamed {
		if j := takeBasisValue(pool, "", false, out[i][1]); j >= 0 {
			out[i][1] = pool[j].raw
			pool[j].used = true
		}
	}
	return out
}

// HeaderPairValuesB64 返回与 pairs 下标对齐的值字节旁路。值含非法 UTF-8 字节时编码为标准
// base64，合法值对应空串；全部合法时返回 nil。
func HeaderPairValuesB64(pairs [][2]string) []string {
	var out []string
	for i, kv := range pairs {
		if utf8.ValidString(kv[1]) {
			continue
		}
		if out == nil {
			out = make([]string, len(pairs))
		}
		out[i] = base64.StdEncoding.EncodeToString([]byte(kv[1]))
	}
	return out
}

// ResolveEditedHeaders 将 UI 回传的有序头解析为出站字节。valuesB64 与 edited 下标对齐，
// 非空项使用解码后的值，空项使用 edited 中的文本；旁路为空时按 basis 恢复原始字节。
// 旁路长度或编码格式不符合协议时返回错误，并保留对应头名的错误上下文。
func ResolveEditedHeaders(edited [][2]string, valuesB64 []string, basis [][2]string) ([][2]string, error) {
	if len(valuesB64) == 0 {
		return RestoreHeaderPairBytes(edited, basis), nil
	}
	if len(valuesB64) != len(edited) {
		return nil, fmt.Errorf("头部字节旁路有 %d 项,与 %d 条头部对不上", len(valuesB64), len(edited))
	}
	out := make([][2]string, len(edited))
	copy(out, edited)
	for i, enc := range valuesB64 {
		if enc == "" {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(enc)
		if err != nil {
			return nil, fmt.Errorf("头部 %q 的字节编码无法解码: %w", out[i][0], err)
		}
		out[i][1] = string(raw)
	}
	return out, nil
}

// headerBasisValue 是值池里的一条原始头值。sanitized 预先算好:每条 edited 都要与整池
// 比对一遍,现算等于把同一个值 sanitize len(edited) 次。
type headerBasisValue struct {
	name      string
	raw       string
	sanitized string
	used      bool
}

// takeBasisValue 返回池中首个未被取用且 sanitize 形态等于 want 的下标，未找到时返回 -1。
// matchName 为假时不比头名。
func takeBasisValue(pool []headerBasisValue, name string, matchName bool, want string) int {
	for j := range pool {
		if pool[j].used || pool[j].sanitized != want {
			continue
		}
		if matchName && pool[j].name != name {
			continue
		}
		return j
	}
	return -1
}
