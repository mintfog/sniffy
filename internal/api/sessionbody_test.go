// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package api

import (
	"encoding/base64"
	"net/http"
	"strings"
	"testing"

	"github.com/mintfog/sniffy/internal/service"
)

// 本文件承担 /api/sessions/{id}/body 与 /body/raw 两条端点:前者回 base64 元信息(图片预览用),
// 后者流式写出原始字节并支持 Range(音视频播放器用)。两条同前缀,顺序错了音视频就永远拿不到
// 可播放的字节。

// newBodyServer 造一台带一条 10 字节音频响应会话的服务器。
func newBodyServer(t *testing.T, opts ...flowOpt) (*Server, *http.ServeMux) {
	t.Helper()
	s, mux := newTestServer(t)
	s.svc.RecordFlowCompleted(newFlowFixture("flow-body",
		append([]flowOpt{withResponse(http.StatusOK, "audio/mpeg", []byte("0123456789"))}, opts...)...))
	return s, mux
}

// TestSessionBodySourceSelectsRequestOrResponse request 与 response 是 service 里相邻的两条取数分支。
// 接反后用户在详情页点「请求体」看到的是响应内容、「另存为」存下的是错的那一半,而状态码仍是 200。
func TestSessionBodySourceSelectsRequestOrResponse(t *testing.T) {
	t.Parallel()
	_, mux := newBodyServer(t, withRequestBody("text/plain", []byte("req-bytes")))

	cases := []struct {
		name     string
		query    string
		wantBody string
		wantMime string
	}{
		{"显式取请求体", "?source=request", "req-bytes", "text/plain"},
		{"显式取响应体", "?source=response", "0123456789", "audio/mpeg"},
		{"缺省取响应体", "", "0123456789", "audio/mpeg"},
		// 无法识别的 source 按响应处理:这条兜底让拼错参数的调用方拿到默认视图而不是 400。
		{"无法识别的 source 按响应处理", "?source=bogus", "0123456789", "audio/mpeg"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			var dto service.BodyDTO
			rec := do(t, mux, http.MethodGet, "/api/sessions/flow-body/body"+c.query, "")
			if rec.Code != http.StatusOK {
				t.Fatalf("/body 状态码 = %d,响应 %s", rec.Code, rec.Body.String())
			}
			decodeEnvelope(t, rec).into(t, &dto)
			if dto.Mime != c.wantMime {
				t.Errorf("/body 的 mime = %q,期望 %q", dto.Mime, c.wantMime)
			}
			if want := base64.StdEncoding.EncodeToString([]byte(c.wantBody)); dto.Base64 != want {
				t.Errorf("/body 的 base64 = %q,期望 %q", dto.Base64, want)
			}
			if dto.Size != len(c.wantBody) || dto.TooLarge {
				t.Errorf("/body 的 size/tooLarge = %d/%v,期望 %d/false", dto.Size, dto.TooLarge, len(c.wantBody))
			}

			raw := do(t, mux, http.MethodGet, "/api/sessions/flow-body/body/raw"+c.query, "")
			if raw.Code != http.StatusOK {
				t.Fatalf("/body/raw 状态码 = %d", raw.Code)
			}
			if got := raw.Body.String(); got != c.wantBody {
				t.Errorf("/body/raw 的字节 = %q,期望 %q", got, c.wantBody)
			}
			if got := raw.Header().Get("Content-Type"); got != c.wantMime {
				t.Errorf("/body/raw 的 Content-Type = %q,期望 %q", got, c.wantMime)
			}
		})
	}
}

// TestSessionBodyRawRangeContract 详情页把 <video>/<audio> 的 src 指向这条端点,播放器先探总长
// 再按拖动发 Range。丢了 Content-Range 进度条不可拖动;Accept-Ranges 丢失退化成一次性下载;
// 越界 Range 回 200 全量则让拖到结尾变成重下整个文件。
func TestSessionBodyRawRangeContract(t *testing.T) {
	t.Parallel()
	_, mux := newBodyServer(t)
	const path = "/api/sessions/flow-body/body/raw?source=response"

	t.Run("HEAD 只回元信息", func(t *testing.T) {
		t.Parallel()
		rec := do(t, mux, http.MethodHead, path, "")
		if rec.Code != http.StatusOK {
			t.Errorf("状态码 = %d,期望 200", rec.Code)
		}
		if got := rec.Header().Get("Content-Length"); got != "10" {
			t.Errorf("Content-Length = %q,期望 \"10\"", got)
		}
		if got := rec.Header().Get("Accept-Ranges"); got != "bytes" {
			t.Errorf("Accept-Ranges = %q,期望 \"bytes\"", got)
		}
		if got := rec.Header().Get("Content-Type"); got != "audio/mpeg" {
			t.Errorf("Content-Type = %q", got)
		}
		if n := rec.Body.Len(); n != 0 {
			t.Errorf("HEAD 不应写出响应体,实际 %d 字节", n)
		}
	})

	t.Run("区间请求回 206", func(t *testing.T) {
		t.Parallel()
		rec := do(t, mux, http.MethodGet, path, "", withHeader("Range", "bytes=2-5"))
		if rec.Code != http.StatusPartialContent {
			t.Fatalf("状态码 = %d,期望 206,响应 %s", rec.Code, rec.Body.String())
		}
		if got := rec.Body.String(); got != "2345" {
			t.Errorf("分片内容 = %q,期望 \"2345\"", got)
		}
		if got := rec.Header().Get("Content-Range"); got != "bytes 2-5/10" {
			t.Errorf("Content-Range = %q,期望 \"bytes 2-5/10\"", got)
		}
	})

	t.Run("越界区间回 416", func(t *testing.T) {
		t.Parallel()
		rec := do(t, mux, http.MethodGet, path, "", withHeader("Range", "bytes=100-200"))
		if rec.Code != http.StatusRequestedRangeNotSatisfiable {
			t.Errorf("状态码 = %d,期望 416", rec.Code)
		}
		if got := rec.Header().Get("Content-Range"); got != "bytes */10" {
			t.Errorf("Content-Range = %q,期望 \"bytes */10\"", got)
		}
	})
}

// TestSessionBodyMissingResponseShapes 两条挨着的兄弟端点对同一种「找不到」给出两种响应形状:
// /body 是 JSON 信封,/body/raw 是 net/http 的纯文本 404。按 JSON 统一解错误体的客户端
// 会在 /body/raw 上解析崩溃。这四格是当前分叉的记录,任何统一都必须显式改这条。
func TestSessionBodyMissingResponseShapes(t *testing.T) {
	t.Parallel()
	_, mux := newBodyServer(t) // 请求体为空,响应体 10 字节

	t.Run("raw 未命中回纯文本 404", func(t *testing.T) {
		t.Parallel()
		rec := do(t, mux, http.MethodGet, "/api/sessions/nope/body/raw", "")
		if rec.Code != http.StatusNotFound {
			t.Fatalf("状态码 = %d,期望 404", rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
			t.Errorf("Content-Type = %q,期望 text/plain 前缀", ct)
		}
		if got := rec.Body.String(); got != "404 page not found\n" {
			t.Errorf("响应体 = %q", got)
		}
	})

	t.Run("base64 未命中回 JSON 信封", func(t *testing.T) {
		t.Parallel()
		rec := do(t, mux, http.MethodGet, "/api/sessions/nope/body", "")
		if rec.Code != http.StatusNotFound {
			t.Fatalf("状态码 = %d,期望 404", rec.Code)
		}
		if e := decodeEnvelope(t, rec); e.Success || e.Message != "session not found" {
			t.Errorf("响应 = success:%v message:%q", e.Success, e.Message)
		}
	})

	// 空请求体在两条端点上同样分叉:/body 认为「有这条会话,只是体是空的」,
	// /body/raw 直接当成没有这份内容。
	t.Run("空请求体在 base64 端点回 200", func(t *testing.T) {
		t.Parallel()
		rec := do(t, mux, http.MethodGet, "/api/sessions/flow-body/body?source=request", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("状态码 = %d,期望 200", rec.Code)
		}
		var dto service.BodyDTO
		decodeEnvelope(t, rec).into(t, &dto)
		if dto.Base64 != "" || dto.Size != 0 {
			t.Errorf("空请求体的 base64/size = %q/%d,期望 \"\"/0", dto.Base64, dto.Size)
		}
	})

	t.Run("空请求体在 raw 端点回 404", func(t *testing.T) {
		t.Parallel()
		rec := do(t, mux, http.MethodGet, "/api/sessions/flow-body/body/raw?source=request", "")
		if rec.Code != http.StatusNotFound {
			t.Errorf("状态码 = %d,期望 404", rec.Code)
		}
	})
}
