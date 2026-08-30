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

// 插件端点的方法与路径关口由 method_test.go 的矩阵守;本文件只管 manager 错误到状态码的分流。

// pluginBranches 覆盖插件接口里把 manager 错误映射成状态码的全部分支。
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

// TestPluginErrorStatusMapping 每个分支都走同一套分类:id 未知 404、输入非法 400、磁盘故障 500。
// 三档缺一不可 —— 磁盘故障若被并进 400/404,调用方会以为是自己传错了,而实际是操作没能落盘。
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

// TestPluginToggleSuccess enable/disable 由路径段决定开关方向,接反后用户点「启用」实际是禁用。
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

// TestPluginSourceRoundTrip /source 读写共用一条路径,方法是唯一的意图信号。
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

// TestPluginErrorStatusWithRealManager 用真实 Manager 钉住跨包分类:plugin 侧的 inputError 与本包的
// InvalidInputError 是鸭子契约,任一侧改了方法名编译期都不会报错,只会在运行时静默降级成 500。
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
			// 每格一个独立的 Manager 与插件目录:共用一个会让前一格的落盘结果影响后一格。
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
