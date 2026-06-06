// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package service

import (
	"time"

	"github.com/mintfog/sniffy/internal/flow"
)

// 这些 DTO 与前端 web2/src/types 中的 HttpSession / HttpResponse 形状一致,
// 是 service 暴露给两种 transport 的展示结构。内部仍以 flow.Flow 为真相。

// HTTPRequestDTO 对应前端 HttpRequest。
type HTTPRequestDTO struct {
	ID        string            `json:"id"`
	Method    string            `json:"method"`
	URL       string            `json:"url"`
	Headers   map[string]string `json:"headers"`
	Body      string            `json:"body,omitempty"`
	Timestamp string            `json:"timestamp"`
	ClientIP  string            `json:"clientIP"`
	Host      string            `json:"host"`
	Path      string            `json:"path"`
	Protocol  string            `json:"protocol"`
	UserAgent string            `json:"userAgent,omitempty"`
}

// HTTPResponseDTO 对应前端 HttpResponse。
type HTTPResponseDTO struct {
	ID           string            `json:"id"`
	RequestID    string            `json:"requestId"`
	Status       int               `json:"status"`
	StatusText   string            `json:"statusText"`
	Headers      map[string]string `json:"headers"`
	Body         string            `json:"body,omitempty"`
	Timestamp    string            `json:"timestamp"`
	Size         int64             `json:"size"`
	ResponseTime int64             `json:"responseTime"`
}

// HTTPSessionDTO 对应前端 HttpSession。
type HTTPSessionDTO struct {
	ID       string           `json:"id"`
	Request  HTTPRequestDTO   `json:"request"`
	Response *HTTPResponseDTO `json:"response,omitempty"`
	Duration int64            `json:"duration,omitempty"`
	Status   string           `json:"status"`
	Blocked  bool             `json:"blocked,omitempty"`
	Modified bool             `json:"modified,omitempty"`

	ProcessName  string `json:"processName,omitempty"`
	ProcessID    uint32 `json:"processId,omitempty"`
	ProcessPath  string `json:"processPath,omitempty"`
	ProcessUser  string `json:"processUser,omitempty"`
	IconData     string `json:"iconData,omitempty"`
	IconType     string `json:"iconType,omitempty"`
	IconSize     string `json:"iconSize,omitempty"`
	HasIcon      bool   `json:"hasIcon,omitempty"`
	IconCategory string `json:"iconCategory,omitempty"`
}

const bodyPreviewLimit = 1 << 20 // 1MB

func flattenHeaders(h map[string][]string) map[string]string {
	out := make(map[string]string, len(h))
	for k, v := range h {
		if len(v) > 0 {
			out[k] = v[0]
		}
	}
	return out
}

func rfc3339(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

func stateToStatus(s flow.FlowState) string {
	switch s {
	case flow.StatePending, flow.StateAwaitingResponse:
		return "pending"
	case flow.StateCompleted, flow.StateMocked:
		return "completed"
	default:
		return "error"
	}
}

// SessionDTO 把一个 flow.Flow 转换为前端 HttpSession 形状。
func SessionDTO(f *flow.Flow) HTTPSessionDTO {
	dto := HTTPSessionDTO{
		ID:       f.ID,
		Status:   stateToStatus(f.State),
		Duration: f.Timing.DurationMs,
		Blocked:  f.State == flow.StateBlocked,
		Modified: f.Modified,
	}
	if f.Request != nil {
		ua := ""
		if v := f.Request.Header["User-Agent"]; len(v) > 0 {
			ua = v[0]
		}
		dto.Request = HTTPRequestDTO{
			ID:        f.ID,
			Method:    f.Request.Method,
			URL:       f.Request.URL,
			Headers:   flattenHeaders(f.Request.Header),
			Body:      flow.BodyPreview(f.Request.Body, bodyPreviewLimit),
			Timestamp: rfc3339(f.Timing.RequestAt),
			ClientIP:  f.Request.ClientIP,
			Host:      f.Request.Host,
			Path:      f.Request.Path,
			Protocol:  f.Protocol,
			UserAgent: ua,
		}
	}
	if f.Response != nil {
		dto.Response = responseDTOPtr(f)
	}
	if f.Process != nil {
		dto.ProcessName = f.Process.Name
		dto.ProcessID = f.Process.PID
		dto.ProcessPath = f.Process.Path
		dto.ProcessUser = f.Process.User
		dto.IconData = f.Process.IconData
		dto.IconType = f.Process.IconType
		dto.HasIcon = f.Process.HasIcon
		dto.IconCategory = f.Process.IconCategory
	}
	return dto
}

func responseDTOPtr(f *flow.Flow) *HTTPResponseDTO {
	r := f.Response
	dto := &HTTPResponseDTO{
		ID:           f.ID + "-resp",
		RequestID:    f.ID,
		Status:       r.Status,
		StatusText:   r.StatusText,
		Headers:      flattenHeaders(r.Header),
		Body:         flow.BodyPreview(r.Body, bodyPreviewLimit),
		Timestamp:    rfc3339(f.Timing.ResponseAt),
		Size:         int64(len(r.Body)),
		ResponseTime: f.Timing.DurationMs,
	}
	return dto
}

// ResponseDTO 单独导出响应 DTO,用于 http_response 实时事件。
func ResponseDTO(f *flow.Flow) *HTTPResponseDTO {
	if f.Response == nil {
		return nil
	}
	return responseDTOPtr(f)
}

// WSMessageDTO 对应前端 WebSocketMessage。
type WSMessageDTO struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
	Direction string `json:"direction"` // inbound|outbound
	Type      string `json:"type"`      // text|binary
	Data      string `json:"data"`
	Timestamp string `json:"timestamp"`
	Size      int64  `json:"size"`
}

// WSSessionDTOType 对应前端 WebSocketSession。
type WSSessionDTOType struct {
	ID           string         `json:"id"`
	URL          string         `json:"url"`
	Status       string         `json:"status"`
	StartTime    string         `json:"startTime"`
	EndTime      string         `json:"endTime,omitempty"`
	MessageCount int            `json:"messageCount"`
	TotalSize    int64          `json:"totalSize"`
	Messages     []WSMessageDTO `json:"messages"`

	ProcessName  string `json:"processName,omitempty"`
	ProcessID    uint32 `json:"processId,omitempty"`
	IconData     string `json:"iconData,omitempty"`
	IconType     string `json:"iconType,omitempty"`
	HasIcon      bool   `json:"hasIcon,omitempty"`
	IconCategory string `json:"iconCategory,omitempty"`
}

func wsDirectionToFrontend(d string) string {
	if d == flow.WSClientToServer {
		return "outbound"
	}
	return "inbound"
}

// WSSessionDTO 把 flow.WSSession 转换为前端 WebSocketSession 形状。
func WSSessionDTO(ws *flow.WSSession) WSSessionDTOType {
	msgs := make([]WSMessageDTO, 0, len(ws.Messages))
	for _, m := range ws.Messages {
		typ := m.Type
		if typ != flow.WSText {
			typ = "binary"
		}
		msgs = append(msgs, WSMessageDTO{
			ID:        m.ID,
			SessionID: ws.ID,
			Direction: wsDirectionToFrontend(m.Direction),
			Type:      typ,
			Data:      flow.BodyPreview(m.Data, bodyPreviewLimit),
			Timestamp: rfc3339(m.Timestamp),
			Size:      int64(len(m.Data)),
		})
	}
	dto := WSSessionDTOType{
		ID:           ws.ID,
		URL:          ws.URL,
		Status:       ws.Status,
		StartTime:    rfc3339(ws.StartTime),
		MessageCount: ws.MessageCount,
		TotalSize:    ws.TotalSize,
		Messages:     msgs,
	}
	if ws.EndTime != nil {
		dto.EndTime = rfc3339(*ws.EndTime)
	}
	if ws.Process != nil {
		dto.ProcessName = ws.Process.Name
		dto.ProcessID = ws.Process.PID
		dto.IconData = ws.Process.IconData
		dto.IconType = ws.Process.IconType
		dto.HasIcon = ws.Process.HasIcon
		dto.IconCategory = ws.Process.IconCategory
	}
	return dto
}
