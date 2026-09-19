// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package api

import "net/http"

const maxUpdateBodyBytes = 1 << 10

func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	if !allowMethods(w, r, http.MethodGet, http.MethodHead) {
		return
	}
	ok(w, s.svc.UpdateState())
}

func (s *Server) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	if !allowMethods(w, r, http.MethodPost) {
		return
	}
	// 检查失败通过状态快照的 Error 字段返回,HTTP 状态仍为 200。
	state, _ := s.svc.CheckForUpdate(r.Context())
	ok(w, state)
}

func (s *Server) handleUpdateSkip(w http.ResponseWriter, r *http.Request) {
	if !allowMethods(w, r, http.MethodPost) {
		return
	}
	var body struct {
		Version string `json:"version"`
	}
	// 允许空体:不带版本号即跳过当前查到的最新版。
	if r.ContentLength != 0 && !decodeLimitedJSON(w, r, maxUpdateBodyBytes, &body, "invalid json") {
		return
	}
	ok(w, s.svc.SkipUpdateVersion(body.Version))
}

func (s *Server) handleUpdateAuto(w http.ResponseWriter, r *http.Request) {
	if !allowMethods(w, r, http.MethodPost, http.MethodPut) {
		return
	}
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if !decodeLimitedJSON(w, r, maxUpdateBodyBytes, &body, "invalid json") {
		return
	}
	if body.Enabled == nil {
		fail(w, http.StatusBadRequest, "enabled is required")
		return
	}
	ok(w, s.svc.SetUpdateAutoCheck(*body.Enabled))
}

func (s *Server) handleUpdateUnskip(w http.ResponseWriter, r *http.Request) {
	if !allowMethods(w, r, http.MethodPost) {
		return
	}
	ok(w, s.svc.ClearSkippedUpdateVersion())
}

func (s *Server) handleUpdateDownload(w http.ResponseWriter, r *http.Request) {
	if !allowMethods(w, r, http.MethodPost) {
		return
	}
	state, err := s.svc.StartUpdateDownload()
	if err != nil {
		fail(w, http.StatusConflict, err.Error())
		return
	}
	ok(w, state)
}

func (s *Server) handleUpdateDownloadCancel(w http.ResponseWriter, r *http.Request) {
	if !allowMethods(w, r, http.MethodPost) {
		return
	}
	ok(w, s.svc.CancelUpdateDownload())
}
