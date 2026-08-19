// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

type apiResponse struct {
	Data      any    `json:"data,omitempty"`
	Success   bool   `json:"success"`
	Message   string `json:"message,omitempty"`
	Timestamp string `json:"timestamp"`
}

type paginatedResponse struct {
	Data     any  `json:"data"`
	Total    int  `json:"total"`
	Page     int  `json:"page"`
	PageSize int  `json:"pageSize"`
	HasNext  bool `json:"hasNext"`
	HasPrev  bool `json:"hasPrev"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func ok(w http.ResponseWriter, data any) {
	writeJSON(w, http.StatusOK, apiResponse{Data: data, Success: true, Timestamp: time.Now().Format(time.RFC3339)})
}

func fail(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, apiResponse{Success: false, Message: msg, Timestamp: time.Now().Format(time.RFC3339)})
}

func paginated(w http.ResponseWriter, data any, total, page, pageSize int) {
	writeJSON(w, http.StatusOK, paginatedResponse{
		Data:     data,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
		HasNext:  page*pageSize < total,
		HasPrev:  page > 1,
	})
}

func pageParams(r *http.Request) (page, pageSize int) {
	page, pageSize = 1, 50
	if v := r.URL.Query().Get("page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			page = n
		}
	}
	if v := r.URL.Query().Get("pageSize"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			pageSize = n
		}
	}
	return
}
