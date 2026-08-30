// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/mintfog/sniffy/internal/pipeline"
	"github.com/mintfog/sniffy/internal/plugin"
)

// 插件端点的方法与路径由 method_test.go 覆盖；本文件验证 manager 错误到状态码的分流。

// pluginBranches 覆盖插件接口将 manager 错误映射为状态码的全部分支。
var pluginBranches = []struct {
	name   string
	invoke func(*testing.T, *Server) *httptest.ResponseRecorder
}{
	{"enable", func(t *testing.T, s *Server) *httptest.ResponseRecorder {
		return do(t, http.HandlerFunc(s.handlePlugin), http.MethodPost, "/api/plugins/demo/enable", "")
	}},
	{"disable", func(t *testing.T, s *Server) *httptest.ResponseRecorder {
		return do(t, http.HandlerFunc(s.handlePlugin), http.MethodPost, "/api/plugins/demo/disable", "")
	}},
	{"manifest", func(t *testing.T, s *Server) *httptest.ResponseRecorder {
		return do(t, http.HandlerFunc(s.handlePlugin), http.MethodPost, "/api/plugins/demo/manifest", `{"name":"demo"}`)
	}},
	{"logs", func(t *testing.T, s *Server) *httptest.ResponseRecorder {
		return do(t, http.HandlerFunc(s.handlePlugin), http.MethodPost, "/api/plugins/demo/logs", "")
	}},
	{"source-put", func(t *testing.T, s *Server) *httptest.ResponseRecorder {
		return do(t, http.HandlerFunc(s.handlePlugin), http.MethodPut, "/api/plugins/demo/source", `{"source":"function onRequest(f){}"}`)
	}},
	{"source-get", func(t *testing.T, s *Server) *httptest.ResponseRecorder {
		return do(t, http.HandlerFunc(s.handlePlugin), http.MethodGet, "/api/plugins/demo/source", "")
	}},
	{"delete", func(t *testing.T, s *Server) *httptest.ResponseRecorder {
		return do(t, http.HandlerFunc(s.handlePlugin), http.MethodDelete, "/api/plugins/demo", "")
	}},
	{"create", func(t *testing.T, s *Server) *httptest.ResponseRecorder {
		return do(t, http.HandlerFunc(s.handlePlugins), http.MethodPost, "/api/plugins",
			`{"manifest":{"id":"demo"},"source":"function onRequest(f){}"}`)
	}},
}

// TestPluginErrorStatusMapping 统一验证未知 ID=404、输入非法=400、持久化故障=500。
func TestPluginErrorStatusMapping(t *testing.T) {
	t.Parallel()
	classes := []struct {
		name string
		err  error
		want int
	}{
		{"未知 id", os.ErrNotExist, http.StatusNotFound},
		{"输入非法", &testInvalidInputError{message: "非法插件 ID"}, http.StatusBadRequest},
		{"磁盘故障", errors.New("写入 plugin.json: no space left on device"), http.StatusInternalServerError},
	}
	for _, b := range pluginBranches {
		for _, c := range classes {
			t.Run(b.name+"/"+c.name, func(t *testing.T) {
				t.Parallel()
				s := &Server{plugins: &recordingPlugins{errs: map[string]error{"*": c.err}}}
				rec := b.invoke(t, s)
				if rec.Code != c.want {
					t.Errorf("状态码 = %d,期望 %d", rec.Code, c.want)
				}
				if e := decodeEnvelope(t, rec); e.Success {
					t.Error("失败响应的 success 应为 false")
				}
			})
		}
	}
}

// TestPluginToggleSuccess enable/disable 按路径段设置对应的开关方向。
func TestPluginToggleSuccess(t *testing.T) {
	t.Parallel()
	for action, want := range map[string]bool{"enable": true, "disable": false} {
		t.Run(action, func(t *testing.T) {
			t.Parallel()
			spy := &recordingPlugins{}
			s := &Server{plugins: spy}
			rec := do(t, http.HandlerFunc(s.handlePlugin), http.MethodPost, "/api/plugins/demo/"+action, "")
			if rec.Code != http.StatusOK {
				t.Fatalf("状态码 = %d", rec.Code)
			}
			assertCalls(t, spy.calls, call{Method: "EnablePlugin", Args: []any{"demo", want}})
		})
	}
}

// TestPluginSourceRoundTrip /source 通过 HTTP 方法区分读取和保存。
func TestPluginSourceRoundTrip(t *testing.T) {
	t.Parallel()

	t.Run("PUT 保存源码", func(t *testing.T) {
		t.Parallel()
		spy := &recordingPlugins{}
		s := &Server{plugins: spy}
		rec := do(t, http.HandlerFunc(s.handlePlugin), http.MethodPut, "/api/plugins/demo/source", `{"source":"saved"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("状态码 = %d", rec.Code)
		}
		assertCalls(t, spy.calls, call{Method: "SavePluginSource", Args: []any{"demo", "saved"}})
	})

	t.Run("GET 返回源码", func(t *testing.T) {
		t.Parallel()
		spy := &recordingPlugins{source: "function onRequest(f){}"}
		s := &Server{plugins: spy}
		rec := do(t, http.HandlerFunc(s.handlePlugin), http.MethodGet, "/api/plugins/demo/source", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("状态码 = %d", rec.Code)
		}
		var data struct {
			Source string `json:"source"`
		}
		decodeEnvelope(t, rec).into(t, &data)
		if data.Source != spy.source {
			t.Errorf("data.source = %q,期望 %q", data.Source, spy.source)
		}
	})
}

// TestPluginErrorStatusWithRealManager 用真实 Manager 验证 plugin 与 API 的 InvalidInputError 契约。
func TestPluginErrorStatusWithRealManager(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		invoke func(*testing.T, *Server) *httptest.ResponseRecorder
		want   int
	}{
		{"非法 id 归 400", func(t *testing.T, s *Server) *httptest.ResponseRecorder {
			return do(t, http.HandlerFunc(s.handlePlugins), http.MethodPost, "/api/plugins", `{"manifest":{"id":"BAD ID"},"source":""}`)
		}, http.StatusBadRequest},
		{"未知 id 归 404", func(t *testing.T, s *Server) *httptest.ResponseRecorder {
			return do(t, http.HandlerFunc(s.handlePlugin), http.MethodPost, "/api/plugins/ghost/enable", "")
		}, http.StatusNotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			// 每格使用独立 Manager 与插件目录，避免落盘状态串扰。
			s := &Server{plugins: plugin.NewManager(pipeline.New(nil, nil), t.TempDir(), nil, nil)}
			rec := c.invoke(t, s)
			if rec.Code != c.want {
				t.Errorf("状态码 = %d,期望 %d (%s)", rec.Code, c.want, rec.Body.String())
			}
			if e := decodeEnvelope(t, rec); e.Success {
				t.Error("失败响应的 success 应为 false")
			}
		})
	}
}
