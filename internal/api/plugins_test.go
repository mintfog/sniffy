// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/mintfog/sniffy/internal/pipeline"
	"github.com/mintfog/sniffy/internal/plugin"
)

// togglePlugins 记录最后一次开关调用。
type togglePlugins struct {
	spyPlugins
	lastID  string
	lastVal bool
}

func (p *togglePlugins) EnablePlugin(id string, enabled bool) error {
	p.lastID, p.lastVal = id, enabled
	return nil
}

// errPlugins 让每个插件操作都返回同一个错误,用来验证状态码映射。
type errPlugins struct {
	spyPlugins
	err error
}

func (p *errPlugins) EnablePlugin(string, bool) error             { return p.err }
func (p *errPlugins) GetPluginSource(string) (string, error)      { return "", p.err }
func (p *errPlugins) UpdateManifest(string, map[string]any) error { return p.err }
func (p *errPlugins) SavePluginSource(string, string) error       { return p.err }
func (p *errPlugins) DeletePlugin(string) error                   { return p.err }
func (p *errPlugins) ClearPluginLogs(string) error                { return p.err }
func (p *errPlugins) CreatePlugin(map[string]any, string) (map[string]any, error) {
	return nil, p.err
}

func pluginRequest(h http.HandlerFunc, method, path, body string) *httptest.ResponseRecorder {
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, "http://127.0.0.1:8888"+path, r)
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

// pluginBranches 覆盖插件接口里把 manager 错误映射成状态码的全部分支。
var pluginBranches = []struct {
	name   string
	invoke func(*Server) *httptest.ResponseRecorder
}{
	{"enable", func(s *Server) *httptest.ResponseRecorder {
		return pluginRequest(s.handlePlugin, http.MethodPost, "/api/plugins/demo/enable", "")
	}},
	{"disable", func(s *Server) *httptest.ResponseRecorder {
		return pluginRequest(s.handlePlugin, http.MethodPost, "/api/plugins/demo/disable", "")
	}},
	{"manifest", func(s *Server) *httptest.ResponseRecorder {
		return pluginRequest(s.handlePlugin, http.MethodPost, "/api/plugins/demo/manifest", `{"name":"demo"}`)
	}},
	{"logs", func(s *Server) *httptest.ResponseRecorder {
		return pluginRequest(s.handlePlugin, http.MethodPost, "/api/plugins/demo/logs", "")
	}},
	{"source", func(s *Server) *httptest.ResponseRecorder {
		return pluginRequest(s.handlePlugin, http.MethodPut, "/api/plugins/demo/source", `{"source":"function onRequest(f){}"}`)
	}},
	{"delete", func(s *Server) *httptest.ResponseRecorder {
		return pluginRequest(s.handlePlugin, http.MethodDelete, "/api/plugins/demo", "")
	}},
	{"create", func(s *Server) *httptest.ResponseRecorder {
		return pluginRequest(s.handlePlugins, http.MethodPost, "/api/plugins", `{"manifest":{"id":"demo"},"source":"function onRequest(f){}"}`)
	}},
	{"source-get", func(s *Server) *httptest.ResponseRecorder {
		return pluginRequest(s.handlePlugin, http.MethodGet, "/api/plugins/demo/source", "")
	}},
}

// 每个分支都走同一套分类:id 未知 404、输入非法 400、磁盘故障 500。三档缺一不可——
// 磁盘故障若被并进 400/404,调用方会以为是自己传错了,而实际是操作没能落盘。
func TestPluginErrorStatusMapping(t *testing.T) {
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
				rec := b.invoke(&Server{plugins: &errPlugins{err: c.err}})
				if rec.Code != c.want {
					t.Fatalf("期望 %d,got %d", c.want, rec.Code)
				}
				var resp apiResponse
				if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
					t.Fatalf("解析响应: %v", err)
				}
				if resp.Success {
					t.Fatal("失败响应的 success 应为 false")
				}
			})
		}
	}
}

func TestPluginToggleSuccess(t *testing.T) {
	for action, want := range map[string]bool{"enable": true, "disable": false} {
		spy := &togglePlugins{}
		s := &Server{plugins: spy}
		rec := pluginRequest(s.handlePlugin, http.MethodPost, "/api/plugins/demo/"+action, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("%s 应返回 200,got %d", action, rec.Code)
		}
		if spy.lastID != "demo" || spy.lastVal != want {
			t.Fatalf("%s 传参错误: id=%q enabled=%v", action, spy.lastID, spy.lastVal)
		}
	}
}

// 用真实 Manager 钉住跨包分类:plugin 侧的 inputError 与本包的 InvalidInputError 是鸭子契约,
// 任一侧改了方法名编译期都不会报错,只会在运行时静默降级成 500。
func TestPluginErrorStatusWithRealManager(t *testing.T) {
	s := &Server{plugins: plugin.NewManager(pipeline.New(nil, nil), t.TempDir(), nil, nil)}
	cases := []struct {
		name string
		rec  *httptest.ResponseRecorder
		want int
	}{
		{"非法 id", pluginRequest(s.handlePlugins, http.MethodPost, "/api/plugins", `{"manifest":{"id":"BAD ID"},"source":""}`), http.StatusBadRequest},
		{"未知 id", pluginRequest(s.handlePlugin, http.MethodPost, "/api/plugins/ghost/enable", ""), http.StatusNotFound},
	}
	for _, c := range cases {
		if c.rec.Code != c.want {
			t.Errorf("%s: 期望 %d,got %d (%s)", c.name, c.want, c.rec.Code, c.rec.Body.String())
		}
	}
}
