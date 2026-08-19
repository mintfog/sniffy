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
	"strings"

	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/internal/pipeline"
)

type breakpointGlobalState struct {
	OnRequest  bool `json:"onRequest"`
	OnResponse bool `json:"onResponse"`
}

type breakpointRuleInput struct {
	URL        string `json:"url"`
	OnRequest  bool   `json:"onRequest"`
	OnResponse bool   `json:"onResponse"`
	Enabled    *bool  `json:"enabled"`
}

func decodeBreakpointJSON(r *http.Request, dst any, allowEmpty bool) error {
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(dst); err != nil {
		if allowEmpty && errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain exactly one JSON value")
	}
	return nil
}

func (s *Server) handleBreakpoints(w http.ResponseWriter, r *http.Request) {
	if s.pipe == nil {
		if r.Method == http.MethodGet {
			ok(w, []any{})
		} else {
			fail(w, http.StatusNotImplemented, "breakpoints unavailable")
		}
		return
	}
	switch r.Method {
	case http.MethodGet:
		ok(w, s.pipe.Breakpoints().List())
	case http.MethodPost:
		// 设置全局"断在请求/响应"开关。
		// 保留该入口以兼容已有客户端；新客户端应使用 /api/breakpoints/global。
		var body breakpointGlobalState
		if err := decodeBreakpointJSON(r, &body, false); err != nil {
			fail(w, http.StatusBadRequest, "invalid json")
			return
		}
		s.pipe.Breakpoints().SetGlobalBreak(body.OnRequest, body.OnResponse)
		ok(w, body)
	default:
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleBreakpointGlobal 读取或设置全局请求/响应断点开关。
func (s *Server) handleBreakpointGlobal(w http.ResponseWriter, r *http.Request) {
	if s.pipe == nil {
		fail(w, http.StatusNotImplemented, "breakpoints unavailable")
		return
	}
	bp := s.pipe.Breakpoints()
	switch r.Method {
	case http.MethodGet:
		onRequest, onResponse := bp.GlobalBreak()
		ok(w, breakpointGlobalState{OnRequest: onRequest, OnResponse: onResponse})
	case http.MethodPut, http.MethodPost:
		var body breakpointGlobalState
		if err := decodeBreakpointJSON(r, &body, false); err != nil {
			fail(w, http.StatusBadRequest, "invalid json")
			return
		}
		bp.SetGlobalBreak(body.OnRequest, body.OnResponse)
		ok(w, body)
	default:
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleBreakpointRules 列出或新增 URL 断点规则。
func (s *Server) handleBreakpointRules(w http.ResponseWriter, r *http.Request) {
	if s.pipe == nil {
		if r.Method == http.MethodGet {
			ok(w, []any{})
		} else {
			fail(w, http.StatusNotImplemented, "breakpoints unavailable")
		}
		return
	}
	bp := s.pipe.Breakpoints()
	switch r.Method {
	case http.MethodGet:
		ok(w, bp.ListRules())
	case http.MethodPost:
		var body breakpointRuleInput
		if err := decodeBreakpointJSON(r, &body, false); err != nil {
			fail(w, http.StatusBadRequest, "invalid json")
			return
		}
		if strings.TrimSpace(body.URL) == "" {
			fail(w, http.StatusBadRequest, "url is required")
			return
		}
		enabled := true
		if body.Enabled != nil {
			enabled = *body.Enabled
		}
		created := bp.AddRuleWithEnabled(body.URL, body.OnRequest, body.OnResponse, enabled)
		ok(w, created)
	default:
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleBreakpointRule 读取、更新、启停或删除单条 URL 断点规则。
func (s *Server) handleBreakpointRule(w http.ResponseWriter, r *http.Request) {
	if s.pipe == nil {
		fail(w, http.StatusNotImplemented, "breakpoints unavailable")
		return
	}
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/breakpoints/rules/"), "/")
	parts := strings.Split(rest, "/")
	if rest == "" || len(parts) > 2 {
		fail(w, http.StatusBadRequest, "invalid breakpoint rule id")
		return
	}
	id := parts[0]
	bp := s.pipe.Breakpoints()

	if len(parts) == 2 {
		if parts[1] != "toggle" {
			fail(w, http.StatusNotFound, "unknown action")
			return
		}
		if r.Method != http.MethodPost && r.Method != http.MethodPut {
			fail(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var body struct {
			Enabled *bool `json:"enabled"`
		}
		if err := decodeBreakpointJSON(r, &body, false); err != nil {
			fail(w, http.StatusBadRequest, "invalid json")
			return
		}
		if body.Enabled == nil {
			fail(w, http.StatusBadRequest, "enabled is required")
			return
		}
		rule, found := bp.ToggleRule(id, *body.Enabled)
		if !found {
			fail(w, http.StatusNotFound, "breakpoint rule not found")
			return
		}
		ok(w, rule)
		return
	}

	switch r.Method {
	case http.MethodGet:
		rule, found := breakpointRuleByID(bp, id)
		if !found {
			fail(w, http.StatusNotFound, "breakpoint rule not found")
			return
		}
		ok(w, rule)
	case http.MethodPut:
		var body breakpointRuleInput
		if err := decodeBreakpointJSON(r, &body, false); err != nil {
			fail(w, http.StatusBadRequest, "invalid json")
			return
		}
		if strings.TrimSpace(body.URL) == "" {
			fail(w, http.StatusBadRequest, "url is required")
			return
		}
		rule, found := bp.UpdateRuleFields(id, body.URL, body.OnRequest, body.OnResponse, body.Enabled)
		if !found {
			fail(w, http.StatusNotFound, "breakpoint rule not found")
			return
		}
		ok(w, rule)
	case http.MethodDelete:
		if !bp.DeleteRule(id) {
			fail(w, http.StatusNotFound, "breakpoint rule not found")
			return
		}
		ok(w, nil)
	default:
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func breakpointRuleByID(bp *pipeline.BreakpointManager, id string) (*pipeline.BreakRule, bool) {
	for _, rule := range bp.ListRules() {
		if rule.ID == id {
			return rule, true
		}
	}
	return nil, false
}

func (s *Server) handleBreakpoint(w http.ResponseWriter, r *http.Request) {
	if s.pipe == nil {
		fail(w, http.StatusNotImplemented, "breakpoints unavailable")
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/breakpoints/")
	parts := strings.Split(rest, "/")
	id := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}
	if id == "" || len(parts) != 2 {
		fail(w, http.StatusBadRequest, "invalid breakpoint id")
		return
	}
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	switch action {
	case "resume":
		var edited *flow.Flow
		if err := decodeBreakpointJSON(r, &edited, true); err != nil {
			fail(w, http.StatusBadRequest, "invalid json")
			return
		}
		if s.pipe.Breakpoints().Resume(id, edited) {
			ok(w, nil)
		} else {
			fail(w, http.StatusNotFound, "breakpoint not found")
		}
	case "abort":
		if s.pipe.Breakpoints().Abort(id) {
			ok(w, nil)
		} else {
			fail(w, http.StatusNotFound, "breakpoint not found")
		}
	default:
		fail(w, http.StatusNotFound, "unknown action")
	}
}
