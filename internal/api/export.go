// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package api

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/mintfog/sniffy/internal/service"
)

const maxSessionExportRequestBytes int64 = 64 << 10

type sessionExportRequest struct {
	Format              string                  `json:"format"`
	SessionIDs          []string                `json:"sessionIds"`
	Methods             []string                `json:"methods"`
	Hosts               []string                `json:"hosts"`
	StatusCodes         []int                   `json:"statusCodes"`
	TimeRange           *sessionExportTimeRange `json:"timeRange"`
	IncludeRequestBody  *bool                   `json:"includeRequestBody"`
	IncludeResponseBody *bool                   `json:"includeResponseBody"`
}

type sessionExportTimeRange struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

type sessionExportFilter struct {
	sessionIDs          map[string]struct{}
	methods             map[string]struct{}
	hosts               map[string]struct{}
	statusCodes         map[int]struct{}
	start               time.Time
	end                 time.Time
	includeRequestBody  bool
	includeResponseBody bool
}

// handleExport 按过滤条件流式导出 JSON 会话数组。Body 是会话 DTO 中最多 1 MiB
// 的文本预览；二进制及透传旁路内容仍须经 /body/raw 单独获取。
func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	req, err := decodeSessionExportRequest(w, r)
	if err != nil {
		failSessionExportDecode(w, err)
		return
	}
	filter, err := newSessionExportFilter(req)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="sessions.json"`)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")

	if _, err := io.WriteString(w, "["); err != nil {
		return
	}
	first := true
	for _, id := range s.svc.SessionIDs() {
		if err := r.Context().Err(); err != nil {
			return
		}
		meta, found := s.svc.SessionMetadata(id)
		if !found || !filter.match(meta) {
			continue
		}
		session, found := s.svc.SessionWithBodyPreviews(id, filter.includeRequestBody, filter.includeResponseBody)
		if !found {
			continue
		}
		data, err := json.Marshal(session)
		if err != nil {
			return
		}
		if !first {
			if _, err := io.WriteString(w, ","); err != nil {
				return
			}
		}
		if _, err := w.Write(data); err != nil {
			return
		}
		first = false
	}
	_, _ = io.WriteString(w, "]\n")
}

func decodeSessionExportRequest(w http.ResponseWriter, r *http.Request) (sessionExportRequest, error) {
	var req sessionExportRequest
	r.Body = http.MaxBytesReader(w, r.Body, maxSessionExportRequestBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		if errors.Is(err, io.EOF) {
			return req, nil
		}
		return req, err
	}
	err := decoder.Decode(&struct{}{})
	if errors.Is(err, io.EOF) {
		return req, nil
	}
	if err != nil {
		return req, err
	}
	return req, errors.New("request body must contain exactly one JSON value")
}

func failSessionExportDecode(w http.ResponseWriter, err error) {
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		fail(w, http.StatusRequestEntityTooLarge, "request body is too large")
		return
	}
	fail(w, http.StatusBadRequest, "invalid json")
}

func newSessionExportFilter(req sessionExportRequest) (sessionExportFilter, error) {
	format := strings.ToLower(strings.TrimSpace(req.Format))
	if format != "" && format != "json" {
		return sessionExportFilter{}, errors.New("unsupported export format")
	}

	f := sessionExportFilter{
		sessionIDs:          stringSet(req.SessionIDs, nil),
		methods:             stringSet(req.Methods, strings.ToUpper),
		hosts:               stringSet(req.Hosts, strings.ToLower),
		statusCodes:         intSet(req.StatusCodes),
		includeRequestBody:  true,
		includeResponseBody: true,
	}
	if req.IncludeRequestBody != nil {
		f.includeRequestBody = *req.IncludeRequestBody
	}
	if req.IncludeResponseBody != nil {
		f.includeResponseBody = *req.IncludeResponseBody
	}
	for _, status := range req.StatusCodes {
		if status < 100 || status > 999 {
			return sessionExportFilter{}, errors.New("statusCodes must contain valid HTTP status codes")
		}
	}

	if req.TimeRange == nil {
		return f, nil
	}
	var err error
	if start := strings.TrimSpace(req.TimeRange.Start); start != "" {
		f.start, err = time.Parse(time.RFC3339, start)
		if err != nil {
			return sessionExportFilter{}, errors.New("invalid timeRange.start")
		}
	}
	if end := strings.TrimSpace(req.TimeRange.End); end != "" {
		f.end, err = time.Parse(time.RFC3339, end)
		if err != nil {
			return sessionExportFilter{}, errors.New("invalid timeRange.end")
		}
	}
	if !f.start.IsZero() && !f.end.IsZero() && f.start.After(f.end) {
		return sessionExportFilter{}, errors.New("timeRange.start must not be after timeRange.end")
	}
	return f, nil
}

func stringSet(values []string, normalize func(string) string) map[string]struct{} {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if normalize != nil {
			value = normalize(value)
		}
		out[value] = struct{}{}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func intSet(values []int) map[int]struct{} {
	if len(values) == 0 {
		return nil
	}
	out := make(map[int]struct{}, len(values))
	for _, value := range values {
		out[value] = struct{}{}
	}
	return out
}

func (f sessionExportFilter) match(session service.HTTPSessionMetadata) bool {
	if len(f.sessionIDs) > 0 {
		if _, ok := f.sessionIDs[session.ID]; !ok {
			return false
		}
	}
	if len(f.methods) > 0 {
		if _, ok := f.methods[strings.ToUpper(session.Method)]; !ok {
			return false
		}
	}
	if len(f.hosts) > 0 && !matchSessionExportHost(session.Host, f.hosts) {
		return false
	}
	if len(f.statusCodes) > 0 {
		if !session.HasResponse {
			return false
		}
		if _, ok := f.statusCodes[session.StatusCode]; !ok {
			return false
		}
	}
	if f.start.IsZero() && f.end.IsZero() {
		return true
	}
	if session.RequestAt.IsZero() {
		return false
	}
	return (f.start.IsZero() || !session.RequestAt.Before(f.start)) && (f.end.IsZero() || !session.RequestAt.After(f.end))
}

func matchSessionExportHost(host string, filters map[string]struct{}) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if _, ok := filters[host]; ok {
		return true
	}
	hostname := host
	if parsed, _, err := net.SplitHostPort(host); err == nil {
		hostname = strings.ToLower(parsed)
	}
	_, ok := filters[hostname]
	return ok
}
