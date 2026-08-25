// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
)

func (s *Server) handlePlugins(w http.ResponseWriter, r *http.Request) {
	if !allowMethods(w, r, http.MethodGet, http.MethodHead, http.MethodPost) {
		return
	}
	if s.plugins == nil {
		if isReadMethod(r.Method) {
			ok(w, []any{})
			return
		}
		fail(w, http.StatusNotImplemented, "plugins unavailable")
		return
	}
	if r.Method == http.MethodPost {
		var body struct {
			Manifest map[string]any `json:"manifest"`
			Source   string         `json:"source"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			fail(w, http.StatusBadRequest, "invalid json")
			return
		}
		created, err := s.plugins.CreatePlugin(body.Manifest, body.Source)
		if err != nil {
			fail(w, pluginErrStatus(err), err.Error())
			return
		}
		ok(w, created)
		return
	}
	ok(w, s.plugins.ListPlugins())
}

func (s *Server) handlePlugin(w http.ResponseWriter, r *http.Request) {
	if s.plugins == nil {
		fail(w, http.StatusNotImplemented, "plugins unavailable")
		return
	}
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/plugins/"), "/")
	parts := strings.Split(rest, "/")
	id := parts[0]
	if id == "" {
		fail(w, http.StatusBadRequest, "invalid plugin id")
		return
	}
	// 多余路径段一律拒绝:被忽略的话 /api/plugins/{id}/enable/typo 会当成 .../enable 执行,
	// 调用方拼错路径却拿到 200 和一次真实的状态变更。
	if len(parts) > 2 {
		fail(w, http.StatusNotFound, "unknown action")
		return
	}
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}
	switch action {
	case "":
		if !allowMethods(w, r, http.MethodDelete) {
			return
		}
		if err := s.plugins.DeletePlugin(id); err != nil {
			fail(w, pluginErrStatus(err), err.Error())
			return
		}
		ok(w, nil)
	case "enable", "disable":
		if !allowMethods(w, r, http.MethodPost) {
			return
		}
		if err := s.plugins.EnablePlugin(id, action == "enable"); err != nil {
			fail(w, pluginErrStatus(err), err.Error())
			return
		}
		ok(w, nil)
	case "manifest":
		if !allowMethods(w, r, http.MethodPost, http.MethodPut) {
			return
		}
		var patch map[string]any
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
			fail(w, http.StatusBadRequest, "invalid json")
			return
		}
		if err := s.plugins.UpdateManifest(id, patch); err != nil {
			fail(w, pluginErrStatus(err), err.Error())
			return
		}
		ok(w, nil)
	case "logs":
		if !allowMethods(w, r, http.MethodPost) {
			return
		}
		if err := s.plugins.ClearPluginLogs(id); err != nil {
			fail(w, pluginErrStatus(err), err.Error())
			return
		}
		ok(w, nil)
	case "source":
		// 读写共用一条路径,方法就是唯一的意图信号:白名单放宽一档就会让本想保存源码的请求
		// 落到读分支,拿到 200 和旧源码,改动被静默丢弃。
		if !allowMethods(w, r, http.MethodGet, http.MethodHead, http.MethodPut) {
			return
		}
		if r.Method == http.MethodPut {
			var body struct {
				Source string `json:"source"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				fail(w, http.StatusBadRequest, "invalid json")
				return
			}
			if err := s.plugins.SavePluginSource(id, body.Source); err != nil {
				fail(w, pluginErrStatus(err), err.Error())
				return
			}
			ok(w, nil)
			return
		}
		src, err := s.plugins.GetPluginSource(id)
		if err != nil {
			fail(w, pluginErrStatus(err), err.Error())
			return
		}
		ok(w, map[string]any{"source": src})
	default:
		fail(w, http.StatusNotFound, "unknown action")
	}
}

// pluginErrStatus 按 plugin 包的错误分类选状态码:id 未知 404、调用方输入非法 400、其余 500。
// 除 DeletePlugin 外,500 都等于「什么都没发生」;DeletePlugin 的 500 表示实例已摘除但目录还在,
// 重启后插件会复活。
func pluginErrStatus(err error) int {
	if errors.Is(err, os.ErrNotExist) {
		return http.StatusNotFound
	}
	if isInvalidInput(err) {
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}
