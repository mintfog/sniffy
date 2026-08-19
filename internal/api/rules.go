// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/mintfog/sniffy/internal/service"
)

func (s *Server) handleRules(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		page, pageSize := pageParams(r)
		all := s.svc.Rules()
		total := len(all)
		start := (page - 1) * pageSize
		if start < 0 || start > total {
			start = total
		}
		end := start + pageSize
		if end < start || end > total {
			end = total
		}
		paginated(w, all[start:end], total, page, pageSize)
	case http.MethodPost:
		var rule service.InterceptRule
		if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
			fail(w, http.StatusBadRequest, "invalid json")
			return
		}
		ok(w, s.svc.CreateRule(&rule))
	default:
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleRule(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/intercept/rules/")
	parts := strings.Split(rest, "/")
	id := parts[0]
	if id == "" {
		fail(w, http.StatusBadRequest, "invalid rule id")
		return
	}
	if len(parts) > 1 && parts[1] == "toggle" {
		if isSafeMethod(r.Method) {
			fail(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var body struct {
			Enabled bool `json:"enabled"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		rule, found := s.svc.ToggleRule(id, body.Enabled)
		if !found {
			fail(w, http.StatusNotFound, "rule not found")
			return
		}
		ok(w, rule)
		return
	}
	switch r.Method {
	case http.MethodGet:
		rule, found := s.svc.Rule(id)
		if !found {
			fail(w, http.StatusNotFound, "rule not found")
			return
		}
		ok(w, rule)
	case http.MethodPut:
		var rule service.InterceptRule
		if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
			fail(w, http.StatusBadRequest, "invalid json")
			return
		}
		updated, found := s.svc.UpdateRule(id, &rule)
		if !found {
			fail(w, http.StatusNotFound, "rule not found")
			return
		}
		ok(w, updated)
	case http.MethodDelete:
		s.svc.DeleteRule(id)
		ok(w, nil)
	default:
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}
