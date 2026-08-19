// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package api

import (
	"encoding/json"
	"net/http"

	"github.com/mintfog/sniffy/internal/service"
)

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	ok(w, map[string]any{
		"status":  "running",
		"version": "2.0.0",
		"uptime":  s.svc.UptimeSeconds(),
	})
}

func (s *Server) handleStatistics(w http.ResponseWriter, r *http.Request) {
	ok(w, s.svc.Statistics())
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		ok(w, service.PublicConfig(s.svc.Config()))
	case http.MethodPut, http.MethodPost:
		var patch map[string]any
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
			fail(w, http.StatusBadRequest, "invalid json")
			return
		}
		ok(w, service.PublicConfig(s.svc.UpdateConfig(patch)))
	default:
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleRecordingStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	s.svc.StartRecording()
	ok(w, map[string]any{"recording": true})
}

func (s *Server) handleRecordingStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	s.svc.StopRecording()
	ok(w, map[string]any{"recording": false})
}

func (s *Server) handleRecordingStatus(w http.ResponseWriter, r *http.Request) {
	ok(w, map[string]any{"recording": s.svc.IsRecording()})
}
