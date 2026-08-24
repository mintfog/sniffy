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
	if s.plugins == nil {
		if r.Method == http.MethodPost {
			fail(w, http.StatusNotImplemented, "plugins unavailable")
			return
		}
		ok(w, []any{})
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
	rest := strings.TrimPrefix(r.URL.Path, "/api/plugins/")
	parts := strings.Split(rest, "/")
	id := parts[0]
	if id == "" {
		fail(w, http.StatusBadRequest, "invalid plugin id")
		return
	}
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}
	// DELETE /api/plugins/{id} 删除插件。
	if action == "" && r.Method == http.MethodDelete {
		if err := s.plugins.DeletePlugin(id); err != nil {
			fail(w, pluginErrStatus(err), err.Error())
			return
		}
		ok(w, nil)
		return
	}
	if isSafeMethod(r.Method) && action != "source" {
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	switch action {
	case "enable", "disable":
		if err := s.plugins.EnablePlugin(id, action == "enable"); err != nil {
			fail(w, pluginErrStatus(err), err.Error())
			return
		}
		ok(w, nil)
	case "manifest":
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
		if err := s.plugins.ClearPluginLogs(id); err != nil {
			fail(w, pluginErrStatus(err), err.Error())
			return
		}
		ok(w, nil)
	case "source":
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
		fail(w, http.StatusNotImplemented, "not implemented")
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
