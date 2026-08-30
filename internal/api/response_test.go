// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package api

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mintfog/sniffy/internal/service"
)

// 本文件覆盖跨端点共享的响应契约：信封形状、分页字段、参数回退、请求体上限与解码严格度。

// TestResponseEnvelopeShapes ok(nil) 省略 data，ok(空切片) 保留空数组；失败信封包含 message 和状态码。
func TestResponseEnvelopeShapes(t *testing.T) {
	t.Parallel()

	t.Run("ok(nil) 没有 data 键", func(t *testing.T) {
		t.Parallel()
		rec := httptest.NewRecorder()
		ok(rec, nil)
		if rec.Code != http.StatusOK {
			t.Errorf("状态码 = %d", rec.Code)
		}
		if got := rec.Header().Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q", got)
		}
		if got := bodyKeys(t, rec); !slices.Equal(got, []string{"success", "timestamp"}) {
			t.Errorf("响应键 = %v,期望恰为 {success,timestamp}", got)
		}
		if !decodeEnvelope(t, rec).Success {
			t.Error("success 应为 true")
		}
	})

	t.Run("ok(空切片) 的 data 是空数组", func(t *testing.T) {
		t.Parallel()
		rec := httptest.NewRecorder()
		ok(rec, []any{})
		if got := bodyKeys(t, rec); !slices.Equal(got, []string{"data", "success", "timestamp"}) {
			t.Errorf("响应键 = %v,期望恰为 {data,success,timestamp}", got)
		}
		if got := string(decodeEnvelope(t, rec).Data); got != "[]" {
			t.Errorf("data = %s,期望 [](不是 null)", got)
		}
	})

	t.Run("fail 带文案且无 data 键", func(t *testing.T) {
		t.Parallel()
		rec := httptest.NewRecorder()
		fail(rec, http.StatusBadRequest, "invalid json")
		if rec.Code != http.StatusBadRequest {
			t.Errorf("状态码 = %d", rec.Code)
		}
		if got := bodyKeys(t, rec); !slices.Equal(got, []string{"message", "success", "timestamp"}) {
			t.Errorf("响应键 = %v,期望恰为 {message,success,timestamp}", got)
		}
		if e := decodeEnvelope(t, rec); e.Success || e.Message != "invalid json" {
			t.Errorf("响应 = success:%v message:%q", e.Success, e.Message)
		}
	})

	t.Run("405 声明 Allow", func(t *testing.T) {
		t.Parallel()
		rec := httptest.NewRecorder()
		failMethodNotAllowed(rec, http.MethodGet, http.MethodPost)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("状态码 = %d", rec.Code)
		}
		if got := rec.Header().Get("Allow"); got != "GET, POST" {
			t.Errorf("Allow = %q,期望 \"GET, POST\"", got)
		}
		if e := decodeEnvelope(t, rec); e.Message != "method not allowed" {
			t.Errorf("message = %q", e.Message)
		}
	})

	// timestamp 使用 RFC3339，供客户端排序和时钟诊断。
	t.Run("timestamp 是 RFC3339", func(t *testing.T) {
		t.Parallel()
		for name, write := range map[string]func(*httptest.ResponseRecorder){
			"ok":   func(rec *httptest.ResponseRecorder) { ok(rec, nil) },
			"fail": func(rec *httptest.ResponseRecorder) { fail(rec, http.StatusBadRequest, "x") },
		} {
			rec := httptest.NewRecorder()
			write(rec)
			ts := decodeEnvelope(t, rec).Timestamp
			parsed, err := time.Parse(time.RFC3339, ts)
			if err != nil {
				t.Errorf("%s 的 timestamp %q 不是 RFC3339: %v", name, ts, err)
				continue
			}
			if d := time.Since(parsed); d < -time.Minute || d > time.Minute {
				t.Errorf("%s 的 timestamp %q 与当前时间相差 %v", name, ts, d)
			}
		}
	})
}

// TestPaginatedEnvelopeBoundaries 校验 hasNext/hasPrev 在空集、首页、末页、越界页和溢出页的边界值。
func TestPaginatedEnvelopeBoundaries(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name                     string
		total, page, pageSize    int
		wantHasNext, wantHasPrev bool
	}{
		{"空集", 0, 1, 50, false, false},
		{"首页还有后续", 3, 1, 2, true, false},
		{"末页", 3, 2, 2, false, true},
		{"越界页", 3, 5, 2, false, true},
		{"整页整除时首页仍有后续", 4, 1, 2, true, false},
		// 用 math.MaxInt 构造乘法溢出边界，并保持跨架构可编译。
		{"页码乘法溢出", 1, math.MaxInt/2 + 1, 2, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			rec := httptest.NewRecorder()
			paginated(rec, []string{}, c.total, c.page, c.pageSize)
			got := decodePage(t, rec)
			if got.HasNext != c.wantHasNext || got.HasPrev != c.wantHasPrev {
				t.Errorf("hasNext/hasPrev = %v/%v,期望 %v/%v", got.HasNext, got.HasPrev, c.wantHasNext, c.wantHasPrev)
			}
			if got.Total != c.total || got.Page != c.page || got.PageSize != c.pageSize {
				t.Errorf("回显 total/page/pageSize = %d/%d/%d,期望 %d/%d/%d",
					got.Total, got.Page, got.PageSize, c.total, c.page, c.pageSize)
			}
			if string(got.Data) != "[]" {
				t.Errorf("data = %s,期望 [](不是 null)", got.Data)
			}
		})
	}

	// 端点级用例确保 helper 与真实端点采用同一套分页结论。
	t.Run("会话端点末页", func(t *testing.T) {
		t.Parallel()
		s, mux := newTestServer(t)
		for _, id := range []string{"Flow-A", "Flow-B", "Flow-C"} {
			s.svc.RecordFlowCompleted(newFlowFixture(id))
		}
		got := decodePage(t, do(t, mux, http.MethodGet, "/api/sessions?page=2&pageSize=2", ""))
		if got.Total != 3 || got.Page != 2 || got.PageSize != 2 || got.HasNext || !got.HasPrev {
			t.Errorf("末页信封 = %+v", got)
		}
	})
}

// TestPageParamsFallbacks 非法 page/pageSize 回退到默认值，合法参数回显并驱动对应切片。
func TestPageParamsFallbacks(t *testing.T) {
	t.Parallel()
	cases := []struct {
		query              string
		wantPage, wantSize int
	}{
		{"", 1, 50},
		{"page=abc", 1, 50},
		{"page=0", 1, 50},
		{"page=-1", 1, 50},
		{"page=1e3", 1, 50}, // 科学计数法不是十进制整数
		{"pageSize=xyz", 1, 50},
		{"pageSize=0", 1, 50},
		{"pageSize=-5", 1, 50},
		{"page=2&page=9", 2, 50}, // 同名参数取第一个
		{"page=3&pageSize=7", 3, 7},
	}
	for _, c := range cases {
		t.Run(c.query, func(t *testing.T) {
			t.Parallel()
			s, mux := newTestServer(t)
			s.svc.RecordFlowCompleted(newFlowFixture("Flow-A"))
			got := decodePage(t, do(t, mux, http.MethodGet, "/api/sessions?"+c.query, ""))
			if got.Page != c.wantPage || got.PageSize != c.wantSize {
				t.Errorf("回显 page/pageSize = %d/%d,期望 %d/%d", got.Page, got.PageSize, c.wantPage, c.wantSize)
			}
		})
	}

	// 回显的分页参数与实际切片保持自洽。
	t.Run("回显与切片自洽", func(t *testing.T) {
		t.Parallel()
		s, mux := newTestServer(t)
		for _, name := range []string{"r1", "r2", "r3"} {
			s.svc.CreateRule(&service.InterceptRule{Name: name})
		}
		got := decodePage(t, do(t, mux, http.MethodGet, "/api/intercept/rules?page=2&pageSize=2", ""))
		var rules []*service.InterceptRule
		if err := json.Unmarshal(got.Data, &rules); err != nil {
			t.Fatalf("解析规则页失败: %v", err)
		}
		if got.Page != 2 || got.PageSize != 2 || got.Total != 3 || len(rules) != 1 {
			t.Errorf("page/pageSize/total/len = %d/%d/%d/%d,期望 2/2/3/1", got.Page, got.PageSize, got.Total, len(rules))
		}
	})
}

// TestDecodeLimitedJSONBoundary 验证请求体恰好等于上限时放行，超出一个字节时返回 413。
func TestDecodeLimitedJSONBoundary(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name           string
		path           string
		limit          int64
		prefix, suffix string
		wantCalls      int
	}{
		{"compose", "/api/compose", maxComposeRequestBytes,
			`{"method":"POST","url":"https://example.com/","body":"`, `"}`, 1},
		{"compose-ws-send", "/api/compose/ws/ws-1/send", maxComposeWSSendBytes,
			`{"type":"text","data":"`, `"}`, 1},
		{"export", "/api/export", maxSessionExportRequestBytes,
			`{"format":"json","sessionIds":["`, `"]}`, 0}, // export 不经 composer
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			pad := int(c.limit) - len(c.prefix) - len(c.suffix)

			s, mux := newTestServer(t)
			atLimit := c.prefix + strings.Repeat("y", pad) + c.suffix
			if got := int64(len(atLimit)); got != c.limit {
				t.Fatalf("构造的请求体 %d 字节,应恰为上限 %d", got, c.limit)
			}
			rec := do(t, mux, http.MethodPost, c.path, atLimit)
			if rec.Code != http.StatusOK {
				t.Errorf("恰好等于上限 = %d,期望 200;响应 %s", rec.Code, rec.Body.String())
			}
			if got := len(testComposer(t, s).calls); got != c.wantCalls {
				t.Errorf("上限之内应照常放行,composer 调用 %d 次,期望 %d", got, c.wantCalls)
			}

			s, mux = newTestServer(t)
			overLimit := c.prefix + strings.Repeat("y", pad+1) + c.suffix
			rec = do(t, mux, http.MethodPost, c.path, overLimit)
			if rec.Code != http.StatusRequestEntityTooLarge {
				t.Errorf("超限 1 字节 = %d,期望 413;响应 %s", rec.Code, rec.Body.String())
			}
			assertNoCalls(t, testComposer(t, s).calls)
		})
	}
}

// TestJSONDecodeStrictnessMatrix 记录各端点对多个 JSON 值的解码严格度，覆盖证书、断点和构造器路径。
func TestJSONDecodeStrictnessMatrix(t *testing.T) {
	t.Parallel()

	t.Run("证书导出拒绝多值", func(t *testing.T) {
		t.Parallel()
		certs := &fakeCertificateManager{exportData: []byte("pem"), exportMIME: "application/x-pem-file"}
		_, mux := newTestServer(t, withCerts(certs))
		rec := do(t, mux, http.MethodPost, "/api/certificate/export", `{} {}`)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("状态码 = %d,期望 400", rec.Code)
		}
		if e := decodeEnvelope(t, rec); e.Message != "invalid json" {
			t.Errorf("message = %q", e.Message)
		}
		assertNoCalls(t, certs.calls)
	})

	t.Run("断点放行拒绝多值", func(t *testing.T) {
		t.Parallel()
		s, mux := newTestServer(t)
		bp := s.pipe.Breakpoints()
		id, _, wait := pausedFlow(t, bp)
		rec := do(t, mux, http.MethodPost, "/api/breakpoints/"+id+"/resume", `{} {}`)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("状态码 = %d,期望 400", rec.Code)
		}
		if n := len(bp.List()); n != 1 {
			t.Errorf("被拒的放行不应把 flow 从断点上放走,剩余 %d 条", n)
		}
		_ = bp.Abort(id)
		wait()
	})

	// 构造器端点当前只解第一个 JSON 值，作为线上兼容契约记录。
	t.Run("构造器只解第一个值", func(t *testing.T) {
		t.Parallel()
		s, mux := newTestServer(t)
		rec := do(t, mux, http.MethodPost, "/api/compose",
			`{"method":"POST","url":"https://a.example/"} {"method":"PUT","url":"https://b.example/"}`)
		if rec.Code != http.StatusOK {
			t.Errorf("状态码 = %d,期望 200(当前分叉)", rec.Code)
		}
		c := testComposer(t, s)
		assertCalls(t, c.calls, call{Method: "SendRequest", Args: []any{"POST", "https://a.example/", ""}})
	})

	t.Run("出站帧只解第一个值", func(t *testing.T) {
		t.Parallel()
		s, mux := newTestServer(t)
		rec := do(t, mux, http.MethodPost, "/api/compose/ws/ws-1/send",
			`{"type":"text","data":"first"} {"type":"text","data":"second"}`)
		if rec.Code != http.StatusOK {
			t.Errorf("状态码 = %d,期望 200(当前分叉)", rec.Code)
		}
		c := testComposer(t, s)
		assertCalls(t, c.calls, call{Method: "SendWSMessage", Args: []any{"ws-1", "text", "first"}})
	})
}

// TestNullJSONBodyReachesHandlers 记录 null JSON 在构造器端点的当前语义，并区分空体校验结果。
func TestNullJSONBodyReachesHandlers(t *testing.T) {
	t.Parallel()

	t.Run("compose 的 null 解成零值 spec", func(t *testing.T) {
		t.Parallel()
		s, mux := newTestServer(t)
		if rec := do(t, mux, http.MethodPost, "/api/compose", `null`); rec.Code != http.StatusOK {
			t.Errorf("状态码 = %d,期望 200", rec.Code)
		}
		assertCalls(t, testComposer(t, s).calls, call{Method: "SendRequest", Args: []any{"", "", ""}})
	})

	t.Run("compose 的空体仍是 400", func(t *testing.T) {
		t.Parallel()
		s, mux := newTestServer(t)
		rec := do(t, mux, http.MethodPost, "/api/compose", "")
		if rec.Code != http.StatusBadRequest {
			t.Errorf("状态码 = %d,期望 400", rec.Code)
		}
		if e := decodeEnvelope(t, rec); e.Message != "invalid request spec" {
			t.Errorf("message = %q", e.Message)
		}
		assertNoCalls(t, testComposer(t, s).calls)
	})

	t.Run("出站帧的 null 会真的发出一帧空消息", func(t *testing.T) {
		t.Parallel()
		s, mux := newTestServer(t)
		rec := do(t, mux, http.MethodPost, "/api/compose/ws/ws-1/send", `null`)
		if rec.Code != http.StatusOK {
			t.Errorf("状态码 = %d,期望 200", rec.Code)
		}
		var body struct {
			Sent bool `json:"sent"`
		}
		decodeEnvelope(t, rec).into(t, &body)
		if !body.Sent {
			t.Error("data.sent 应为 true")
		}
		assertCalls(t, testComposer(t, s).calls, call{Method: "SendWSMessage", Args: []any{"ws-1", "", ""}})
	})
}
