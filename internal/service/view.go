// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package service

import (
	"encoding/base64"
	"time"
	"unicode/utf8"

	"github.com/mintfog/sniffy/internal/flow"
)

// 这些 DTO 与前端 web/src/types 中的 HttpSession / HttpResponse 形状一致,
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
	Error    string           `json:"error,omitempty"` // 处理出错时的原因(如 TLS 握手失败),供 UI 展示

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
		Error:    f.Error,
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
	if p := f.Process(); p != nil {
		dto.ProcessName = p.Name
		dto.ProcessID = p.PID
		dto.ProcessPath = p.Path
		dto.ProcessUser = p.User
		dto.IconData = p.IconData
		dto.IconType = p.IconType
		dto.HasIcon = p.HasIcon
		dto.IconCategory = p.IconCategory
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
	Direction string `json:"direction"`        // inbound|outbound
	Type      string `json:"type"`             // text|binary
	Data      string `json:"data"`             // 文本帧为原文;二进制帧为 base64(见 Binary)
	Binary    bool   `json:"binary,omitempty"` // true 时 Data 为 base64,前端按需 hex 展示
	Timestamp string `json:"timestamp"`
	Size      int64  `json:"size"`
}

// wsMessageData 把一帧 WebSocket 消息编码为前端可展示的字符串。
// 文本帧(UTF-8)按原文返回(超长截断);二进制或非 UTF-8 帧 base64 编码并标记 binary,
// 以便前端 hex 展示——修复历史上二进制帧被 BodyPreview 丢成空串(详情面板"白板")的问题。
func wsMessageData(m flow.WSMessage) (data string, binary bool) {
	if m.Type == flow.WSText && utf8.Valid(m.Data) {
		s := string(m.Data)
		if len(s) > bodyPreviewLimit {
			s = s[:bodyPreviewLimit]
		}
		return s, false
	}
	raw := m.Data
	if len(raw) > bodyPreviewLimit {
		raw = raw[:bodyPreviewLimit]
	}
	return base64.StdEncoding.EncodeToString(raw), true
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

// StreamMessageDTO 对应前端 StreamMessage(SSE 事件 / gRPC 消息 / 分块)。
type StreamMessageDTO struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
	Direction string `json:"direction"` // inbound|outbound
	Kind      string `json:"kind"`      // sse|grpc|chunk
	EventType string `json:"eventType,omitempty"`
	Data      string `json:"data"`             // 文本按原文,二进制 base64
	Binary    bool   `json:"binary,omitempty"` // true 时 Data 为 base64
	Timestamp string `json:"timestamp"`
	Seq       int    `json:"seq"`
	Size      int64  `json:"size"`
}

// StreamSessionDTOType 对应前端 StreamSession。
type StreamSessionDTOType struct {
	ID           string             `json:"id"`
	URL          string             `json:"url"`
	Kind         string             `json:"kind"` // sse|grpc|chunk
	Method       string             `json:"method,omitempty"`
	StatusCode   int                `json:"statusCode,omitempty"`
	Status       string             `json:"status"` // open|closed
	StartTime    string             `json:"startTime"`
	EndTime      string             `json:"endTime,omitempty"`
	MessageCount int                `json:"messageCount"`
	TotalSize    int64              `json:"totalSize"`
	Messages     []StreamMessageDTO `json:"messages"`

	ProcessName  string `json:"processName,omitempty"`
	ProcessID    uint32 `json:"processId,omitempty"`
	IconData     string `json:"iconData,omitempty"`
	IconType     string `json:"iconType,omitempty"`
	HasIcon      bool   `json:"hasIcon,omitempty"`
	IconCategory string `json:"iconCategory,omitempty"`
}

// streamMessageData 把一条流消息编码为前端可展示字符串(UTF-8 原文,否则 base64+binary)。
func streamMessageData(m flow.StreamMessage) (data string, binary bool) {
	if utf8.Valid(m.Data) {
		s := string(m.Data)
		if len(s) > bodyPreviewLimit {
			s = s[:bodyPreviewLimit]
		}
		return s, false
	}
	raw := m.Data
	if len(raw) > bodyPreviewLimit {
		raw = raw[:bodyPreviewLimit]
	}
	return base64.StdEncoding.EncodeToString(raw), true
}

// StreamSessionDTO 把 flow.StreamSession 转换为前端 StreamSession 形状。
func StreamSessionDTO(ss *flow.StreamSession) StreamSessionDTOType {
	msgs := make([]StreamMessageDTO, 0, len(ss.Messages))
	for _, m := range ss.Messages {
		data, binary := streamMessageData(m)
		msgs = append(msgs, StreamMessageDTO{
			ID:        m.ID,
			SessionID: ss.ID,
			Direction: wsDirectionToFrontend(m.Direction),
			Kind:      m.Kind,
			EventType: m.EventType,
			Data:      data,
			Binary:    binary,
			Timestamp: rfc3339(m.Timestamp),
			Seq:       m.Seq,
			Size:      int64(len(m.Data)),
		})
	}
	dto := StreamSessionDTOType{
		ID:           ss.ID,
		URL:          ss.URL,
		Kind:         ss.Kind,
		Method:       ss.Method,
		StatusCode:   ss.StatusCode,
		Status:       ss.Status,
		StartTime:    rfc3339(ss.StartTime),
		MessageCount: ss.MessageCount,
		TotalSize:    ss.TotalSize,
		Messages:     msgs,
	}
	if ss.EndTime != nil {
		dto.EndTime = rfc3339(*ss.EndTime)
	}
	if ss.Process != nil {
		dto.ProcessName = ss.Process.Name
		dto.ProcessID = ss.Process.PID
		dto.IconData = ss.Process.IconData
		dto.IconType = ss.Process.IconType
		dto.HasIcon = ss.Process.HasIcon
		dto.IconCategory = ss.Process.IconCategory
	}
	return dto
}

// WSSessionDTO 把 flow.WSSession 转换为前端 WebSocketSession 形状。
func WSSessionDTO(ws *flow.WSSession) WSSessionDTOType {
	msgs := make([]WSMessageDTO, 0, len(ws.Messages))
	for _, m := range ws.Messages {
		data, binary := wsMessageData(m)
		typ := flow.WSText
		if binary {
			typ = "binary"
		}
		msgs = append(msgs, WSMessageDTO{
			ID:        m.ID,
			SessionID: ws.ID,
			Direction: wsDirectionToFrontend(m.Direction),
			Type:      typ,
			Data:      data,
			Binary:    binary,
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
