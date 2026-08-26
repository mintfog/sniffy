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
	"time"

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

// maxBreakpointBody 是断点端点的请求体上限。放行会带回改过的消息体,不设限就等于把
// 一个无上限的内存放大面挂在管理 API 上;上限比编辑体上限宽一档,留给头部与 JSON 转义。
const maxBreakpointBody = flow.MaxComposeBodyBytes + (1 << 20)

// decodeBreakpointJSON 解出恰好一个 JSON 值。超限自行回 413 并返回 errBodyTooLarge,
// 调用方据此跳过自己的 400 —— 上限的意义之一就是让调用方分得清"发错了"和"发太大了"。
// strictFields 让解码器拒绝未知字段(见 handleBreakpoint 的 resume 分支)。
const strictFields = true

func decodeBreakpointJSON(w http.ResponseWriter, r *http.Request, dst any, allowEmpty bool, strict ...bool) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBreakpointBody)
	decoder := json.NewDecoder(r.Body)
	if len(strict) > 0 && strict[0] {
		decoder.DisallowUnknownFields()
	}
	if err := decoder.Decode(dst); err != nil {
		if allowEmpty && errors.Is(err, io.EOF) {
			return nil
		}
		return breakpointDecodeError(w, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return breakpointDecodeError(w, errors.New("request body must contain exactly one JSON value"))
	}
	return nil
}

// errBodyTooLarge 标记"响应已由 decodeBreakpointJSON 写完",调用方直接 return。
var errBodyTooLarge = errors.New("request body is too large")

func breakpointDecodeError(w http.ResponseWriter, err error) error {
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		fail(w, http.StatusRequestEntityTooLarge, errBodyTooLarge.Error())
		return errBodyTooLarge
	}
	return err
}

// failBreakpointDecode 把解码失败翻成 400;超限的那条响应已经写过,不再重复写。
func failBreakpointDecode(w http.ResponseWriter, err error) {
	if !errors.Is(err, errBodyTooLarge) {
		fail(w, http.StatusBadRequest, "invalid json")
	}
}

func (s *Server) handleBreakpoints(w http.ResponseWriter, r *http.Request) {
	if s.pipe == nil {
		if isReadMethod(r.Method) {
			ok(w, []any{})
		} else {
			fail(w, http.StatusNotImplemented, "breakpoints unavailable")
		}
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		ok(w, s.pipe.Breakpoints().List())
	case http.MethodPost:
		// 设置全局"断在请求/响应"开关。
		// 保留该入口以兼容已有客户端；新客户端应使用 /api/breakpoints/global。
		var body breakpointGlobalState
		if err := decodeBreakpointJSON(w, r, &body, false); err != nil {
			failBreakpointDecode(w, err)
			return
		}
		s.pipe.Breakpoints().SetGlobalBreak(body.OnRequest, body.OnResponse)
		ok(w, body)
	default:
		failMethodNotAllowed(w, http.MethodGet, http.MethodHead, http.MethodPost)
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
	case http.MethodGet, http.MethodHead:
		onRequest, onResponse := bp.GlobalBreak()
		ok(w, breakpointGlobalState{OnRequest: onRequest, OnResponse: onResponse})
	case http.MethodPut, http.MethodPost:
		var body breakpointGlobalState
		if err := decodeBreakpointJSON(w, r, &body, false); err != nil {
			failBreakpointDecode(w, err)
			return
		}
		bp.SetGlobalBreak(body.OnRequest, body.OnResponse)
		ok(w, body)
	default:
		failMethodNotAllowed(w, http.MethodGet, http.MethodHead, http.MethodPut, http.MethodPost)
	}
}

// handleBreakpointRules 列出或新增 URL 断点规则。
func (s *Server) handleBreakpointRules(w http.ResponseWriter, r *http.Request) {
	if s.pipe == nil {
		if isReadMethod(r.Method) {
			ok(w, []any{})
		} else {
			fail(w, http.StatusNotImplemented, "breakpoints unavailable")
		}
		return
	}
	bp := s.pipe.Breakpoints()
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		ok(w, bp.ListRules())
	case http.MethodPost:
		var body breakpointRuleInput
		if err := decodeBreakpointJSON(w, r, &body, false); err != nil {
			failBreakpointDecode(w, err)
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
		failMethodNotAllowed(w, http.MethodGet, http.MethodHead, http.MethodPost)
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
		if !allowMethods(w, r, http.MethodPost, http.MethodPut) {
			return
		}
		var body struct {
			Enabled *bool `json:"enabled"`
		}
		if err := decodeBreakpointJSON(w, r, &body, false); err != nil {
			failBreakpointDecode(w, err)
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
	case http.MethodGet, http.MethodHead:
		rule, found := breakpointRuleByID(bp, id)
		if !found {
			fail(w, http.StatusNotFound, "breakpoint rule not found")
			return
		}
		ok(w, rule)
	case http.MethodPut:
		var body breakpointRuleInput
		if err := decodeBreakpointJSON(w, r, &body, false); err != nil {
			failBreakpointDecode(w, err)
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
		failMethodNotAllowed(w, http.MethodGet, http.MethodHead, http.MethodPut, http.MethodDelete)
	}
}

// failBreakpointResolve 把放行/阻断的结果翻成 HTTP 语义:该 flow 已不在暂停中是 404,
// 编辑内容不合法是 400(此时 flow 仍被按住,改回来还能重来)。
func failBreakpointResolve(w http.ResponseWriter, err error) {
	switch {
	case err == nil:
		ok(w, nil)
	case errors.Is(err, pipeline.ErrBreakpointNotFound):
		fail(w, http.StatusNotFound, "breakpoint not found")
	default:
		fail(w, http.StatusBadRequest, err.Error())
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

// breakpointDeadline 是续期端点的返回体:新的自动放行时刻。
type breakpointDeadline struct {
	PausedUntil time.Time `json:"pausedUntil"`
}

// handleBreakpointResumeAll / handleBreakpointAbortAll 批量处置全部暂停项。
// 全局断点一开,一个页面几十个并发请求会同时断住,逐条处置不是可用的操作。
func (s *Server) handleBreakpointResumeAll(w http.ResponseWriter, r *http.Request) {
	s.handleBreakpointBulk(w, r, func(bp *pipeline.BreakpointManager) int { return bp.ResumeAll() })
}

func (s *Server) handleBreakpointAbortAll(w http.ResponseWriter, r *http.Request) {
	s.handleBreakpointBulk(w, r, func(bp *pipeline.BreakpointManager) int { return bp.AbortAll() })
}

func (s *Server) handleBreakpointBulk(w http.ResponseWriter, r *http.Request, apply func(*pipeline.BreakpointManager) int) {
	if s.pipe == nil {
		fail(w, http.StatusNotImplemented, "breakpoints unavailable")
		return
	}
	if !allowMethods(w, r, http.MethodPost) {
		return
	}
	ok(w, map[string]int{"resolved": apply(s.pipe.Breakpoints())})
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
	if !allowMethods(w, r, http.MethodPost) {
		return
	}
	switch action {
	case "extend":
		deadline, found := s.pipe.Breakpoints().Extend(id)
		if !found {
			fail(w, http.StatusNotFound, "breakpoint not found")
			return
		}
		ok(w, breakpointDeadline{PausedUntil: deadline})
	case "resume":
		// 严格解码:放行的请求体是一份 patch(见 pipeline.BreakpointEdit),不认识的键必须报错。
		// 把整个 flow 送回来的调用方在 request.body 上与 patch 同名不同义(那边是 base64 字节,
		// 这里是明文),宽松解码会让它拿到 200,而上游收到的是一串 base64 字面量。
		var edit *pipeline.BreakpointEdit
		if err := decodeBreakpointJSON(w, r, &edit, true, strictFields); err != nil {
			failBreakpointDecode(w, err)
			return
		}
		failBreakpointResolve(w, s.pipe.Breakpoints().Resume(id, edit))
	case "abort":
		failBreakpointResolve(w, s.pipe.Breakpoints().Abort(id))
	default:
		fail(w, http.StatusNotFound, "unknown action")
	}
}
