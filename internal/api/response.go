// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
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

// failMethodNotAllowed 按 RFC 9110 §15.5.6 返回 405，并通过 Allow 声明资源支持的方法。
func failMethodNotAllowed(w http.ResponseWriter, methods ...string) {
	w.Header().Set("Allow", strings.Join(methods, ", "))
	fail(w, http.StatusMethodNotAllowed, "method not allowed")
}

// allowMethods 是端点的方法白名单关口；命中返回 true，其他方法写入 405 并返回 false。
func allowMethods(w http.ResponseWriter, r *http.Request, methods ...string) bool {
	for _, m := range methods {
		if r.Method == m {
			return true
		}
	}
	failMethodNotAllowed(w, methods...)
	return false
}

// isReadMethod 让 HEAD 与 GET 共用同一条读分支,响应体由 net/http 自行丢弃。
func isReadMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead
}

// decodeLimitedJSON 按字节上限读取并解码请求体；超限返回 413，畸形内容返回 400。
// 两种结果都会写完响应，返回 false 表示处理结束。
//
// 构造器端点会将内容整体读入内存并随会话保存，因此请求体必须设有上限。
func decodeLimitedJSON(w http.ResponseWriter, r *http.Request, limit int64, dst any, invalidMsg string) bool {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			fail(w, http.StatusRequestEntityTooLarge, "request body is too large")
			return false
		}
		fail(w, http.StatusBadRequest, invalidMsg)
		return false
	}
	return true
}

func paginated(w http.ResponseWriter, data any, total, page, pageSize int) {
	writeJSON(w, http.StatusOK, paginatedResponse{
		Data:     data,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
		HasNext:  hasNextPage(page, pageSize, total),
		HasPrev:  page > 1,
	})
}

// hasNextPage 判断当前页之后是否还有数据，并在 page*pageSize 溢出时按越界页处理。
func hasNextPage(page, pageSize, total int) bool {
	if page < 1 || pageSize < 1 {
		return false
	}
	end := page * pageSize
	// 除法还原不回原值表示乘法溢出，页尾已越过 total。
	if end/pageSize != page {
		return false
	}
	return end < total
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
