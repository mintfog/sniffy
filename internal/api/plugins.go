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
			fail(w, http.StatusBadRequest, err.Error())
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
			fail(w, http.StatusNotFound, err.Error())
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
	case "enable":
		_ = s.plugins.EnablePlugin(id, true)
		ok(w, nil)
	case "disable":
		_ = s.plugins.EnablePlugin(id, false)
		ok(w, nil)
	case "manifest":
		var patch map[string]any
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
			fail(w, http.StatusBadRequest, "invalid json")
			return
		}
		if err := s.plugins.UpdateManifest(id, patch); err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, os.ErrNotExist) {
				status = http.StatusNotFound
			}
			fail(w, status, err.Error())
			return
		}
		ok(w, nil)
	case "logs":
		if err := s.plugins.ClearPluginLogs(id); err != nil {
			fail(w, http.StatusNotFound, err.Error())
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
				fail(w, http.StatusInternalServerError, err.Error())
				return
			}
			ok(w, nil)
			return
		}
		src, found := s.plugins.GetPluginSource(id)
		if !found {
			fail(w, http.StatusNotFound, "plugin not found")
			return
		}
		ok(w, map[string]any{"source": src})
	default:
		fail(w, http.StatusNotImplemented, "not implemented")
	}
}
