// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package service

import (
	"encoding/base64"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mintfog/sniffy/internal/flow"
)

// 这些 DTO 与前端 HttpSession / HttpResponse 形状一致，供两种 transport 展示。
// 内部状态以 flow.Flow 为准。

// HTTPRequestDTO 对应前端 HttpRequest。
type HTTPRequestDTO struct {
	ID      string            `json:"id"`
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	// HeadersB64 是稀疏的值字节旁路，键对应 Headers 中值含非 UTF-8 字节的首值，
	// 使用标准 base64 编码，供界面按原始字节渲染。
	HeadersB64 map[string]string `json:"headersB64,omitempty"`
	Body       string            `json:"body,omitempty"`
	Timestamp  string            `json:"timestamp"`
	ClientIP   string            `json:"clientIP"`
	Host       string            `json:"host"`
	Path       string            `json:"path"`
	Protocol   string            `json:"protocol"`
	UserAgent  string            `json:"userAgent,omitempty"`
}

// HTTPResponseDTO 对应前端 HttpResponse。
type HTTPResponseDTO struct {
	ID         string            `json:"id"`
	RequestID  string            `json:"requestId"`
	Status     int               `json:"status"`
	StatusText string            `json:"statusText"`
	Headers    map[string]string `json:"headers"`
	// HeadersB64 与 HTTPRequestDTO.HeadersB64 使用相同的稀疏字节表示。
	HeadersB64   map[string]string `json:"headersB64,omitempty"`
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

// HTTPSessionMetadata 是不含头部、Body 和进程图标的轻量会话索引，供需要先筛选
// 再构造完整 DTO 的调用方使用。
type HTTPSessionMetadata struct {
	ID          string
	Method      string
	Host        string
	StatusCode  int
	HasResponse bool
	RequestAt   time.Time
}

// bodyPreviewLimit 是 DTO 中正文与消息载荷的预览截断尺度，与保留上限配套。
const bodyPreviewLimit = 1 << 20 // 1MB

// maxRawBodyBytes 限制按需拉取的原始体大小，超限时仅返回元信息。
const maxRawBodyBytes = 25 << 20 // 25MB

// BodyDTO 是按需拉取的原始消息体，Base64 为 identity 字节的标准编码。
type BodyDTO struct {
	Mime     string `json:"mime"`
	Size     int    `json:"size"`
	Base64   string `json:"base64,omitempty"`
	TooLarge bool   `json:"tooLarge,omitempty"`
}

// bodyDTO 把原始字节与头部组装成可预览的 BodyDTO(推断 MIME,按上限决定是否编码)。
func bodyDTO(body []byte, header map[string][]string) *BodyDTO {
	dto := &BodyDTO{Mime: detectMIME(header, body), Size: len(body)}
	if len(body) > maxRawBodyBytes {
		dto.TooLarge = true
		return dto
	}
	dto.Base64 = base64.StdEncoding.EncodeToString(body)
	return dto
}

// bodyDTOFromFile 组装透传旁路的 BodyDTO。超出上限或缓存不可用时仅返回元信息。
// MIME 取自响应头。
func bodyDTOFromFile(path string, size int64, header map[string][]string) *BodyDTO {
	dto := &BodyDTO{Mime: detectMIME(header, nil), Size: int(size)}
	if size > maxRawBodyBytes {
		dto.TooLarge = true
		return dto
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return dto
	}
	dto.Base64 = base64.StdEncoding.EncodeToString(data)
	return dto
}

// detectMIME 推断消息体 MIME:优先 Content-Type 头(去掉参数),缺省时按内容嗅探。
func detectMIME(header map[string][]string, body []byte) string {
	if ct := firstHeaderValue(header, "Content-Type"); ct != "" {
		if i := strings.IndexByte(ct, ';'); i >= 0 {
			ct = ct[:i]
		}
		if ct = strings.TrimSpace(ct); ct != "" {
			return ct
		}
	}
	if len(body) > 0 {
		return http.DetectContentType(body)
	}
	return "application/octet-stream"
}

// firstHeaderValue 大小写不敏感地取首个头值(header 键通常已规范化,这里兜底非规范情形)。
func firstHeaderValue(header map[string][]string, key string) string {
	if v, ok := header[key]; ok && len(v) > 0 {
		return v[0]
	}
	for k, v := range header {
		if len(v) > 0 && strings.EqualFold(k, key) {
			return v[0]
		}
	}
	return ""
}

// flattenHeaders 将每个头折叠为首值，并在同一遍遍历中生成对应的稀疏字节旁路。
// 两个返回值保持相同的键粒度；所有值均为合法 UTF-8 时旁路为 nil。
func flattenHeaders(h map[string][]string) (map[string]string, map[string]string) {
	out := make(map[string]string, len(h))
	var b64 map[string]string
	for k, v := range h {
		if len(v) == 0 {
			continue
		}
		out[k] = v[0]
		if utf8.ValidString(v[0]) {
			continue
		}
		if b64 == nil {
			b64 = make(map[string]string, 1)
		}
		b64[k] = base64.StdEncoding.EncodeToString([]byte(v[0]))
	}
	return out, b64
}

func rfc3339(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339Nano)
}

func stateToStatus(s flow.FlowState) string {
	switch s {
	// 断点暂停映射为 pending 状态，表示流程仍在处理中。
	case flow.StatePending, flow.StateAwaitingResponse, flow.StatePausedAtBreakpoint:
		return "pending"
	case flow.StateCompleted, flow.StateMocked:
		return "completed"
	default:
		return "error"
	}
}

// SessionDTO 把一个 flow.Flow 转换为前端 HttpSession 形状。
func SessionDTO(f *flow.Flow) HTTPSessionDTO {
	return sessionDTO(f, true, true)
}

func sessionDTO(f *flow.Flow, includeRequestBody, includeResponseBody bool) HTTPSessionDTO {
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
		body := ""
		if includeRequestBody {
			body = flow.BodyPreview(f.Request.Body, bodyPreviewLimit)
		}
		headers, headersB64 := flattenHeaders(f.Request.Header)
		dto.Request = HTTPRequestDTO{
			ID:         f.ID,
			Method:     f.Request.Method,
			URL:        f.Request.URL,
			Headers:    headers,
			HeadersB64: headersB64,
			Body:       body,
			Timestamp:  rfc3339(f.Timing.RequestAt),
			ClientIP:   f.Request.ClientIP,
			Host:       f.Request.Host,
			Path:       f.Request.Path,
			Protocol:   f.Protocol,
			UserAgent:  ua,
		}
	}
	if f.Response != nil {
		dto.Response = responseDTOPtr(f, includeResponseBody)
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

func responseDTOPtr(f *flow.Flow, includeBody bool) *HTTPResponseDTO {
	r := f.Response
	body := ""
	if includeBody {
		body = flow.BodyPreview(r.Body, bodyPreviewLimit)
	}
	headers, headersB64 := flattenHeaders(r.Header)
	dto := &HTTPResponseDTO{
		ID:         f.ID + "-resp",
		RequestID:  f.ID,
		Status:     r.Status,
		StatusText: r.StatusText,
		Headers:    headers,
		HeadersB64: headersB64,
		Body:       body,
		Timestamp:  rfc3339(f.Timing.ResponseAt),
		// 走过透传旁路时 Body 为空,大小只能取旁路记录的值(见 flow.Response.BodyLen)。
		Size:         r.BodyLen(),
		ResponseTime: f.Timing.DurationMs,
	}
	return dto
}

// ResponseDTO 单独导出响应 DTO,用于 http_response 实时事件。
func ResponseDTO(f *flow.Flow) *HTTPResponseDTO {
	if f.Response == nil {
		return nil
	}
	return responseDTOPtr(f, true)
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
	Size      int64  `json:"size"` // 载荷真实字节数,可能大于 Data 还原出来的长度
	// Truncated 为真时 Data 只是载荷的开头一段(会话保留策略或预览上限所致)。
	Truncated bool `json:"truncated,omitempty"`
}

// wsMessageData 将 WebSocket 消息编码为前端可展示的字符串。
// 文本帧按 UTF-8 返回，二进制或非 UTF-8 帧使用 base64 并标记 binary。
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

// wsMessageDTO 把一条 WebSocket 消息转成前端形状。
func wsMessageDTO(sessionID string, m flow.WSMessage) WSMessageDTO {
	data, binary := wsMessageData(m)
	typ := flow.WSText
	if binary {
		typ = "binary"
	}
	size := m.PayloadSize()
	// 两处截断都算:保留策略在入口砍过一刀,预览上限在这里可能再砍一刀。
	shown := int64(len(m.Data))
	if shown > bodyPreviewLimit {
		shown = bodyPreviewLimit
	}
	return WSMessageDTO{
		ID:        m.ID,
		SessionID: sessionID,
		Direction: wsDirectionToFrontend(m.Direction),
		Type:      typ,
		Data:      data,
		Binary:    binary,
		Timestamp: rfc3339(m.Timestamp),
		Size:      size,
		Truncated: shown < size,
	}
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
	Size      int64  `json:"size"` // 载荷真实字节数,可能大于 Data 还原出来的长度
	// Truncated 语义同 WSMessageDTO.Truncated。
	Truncated bool `json:"truncated,omitempty"`
}

// streamMessageDTO 把一条流消息转成前端形状。
func streamMessageDTO(sessionID string, m flow.StreamMessage) StreamMessageDTO {
	data, binary := streamMessageData(m)
	size := m.PayloadSize()
	shown := int64(len(m.Data))
	if shown > bodyPreviewLimit {
		shown = bodyPreviewLimit
	}
	return StreamMessageDTO{
		ID:        m.ID,
		SessionID: sessionID,
		Direction: wsDirectionToFrontend(m.Direction),
		Kind:      m.Kind,
		EventType: m.EventType,
		Data:      data,
		Binary:    binary,
		Timestamp: rfc3339(m.Timestamp),
		Seq:       m.Seq,
		Size:      size,
		Truncated: shown < size,
	}
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

// streamMessageData 将流消息编码为前端可展示字符串（UTF-8 原文或 base64+binary）。
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

// streamSessionMeta 构造不含 messages 的会话 DTO，Messages 保持空数组。
func streamSessionMeta(ss *flow.StreamSession) StreamSessionDTOType {
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
		Messages:     []StreamMessageDTO{},
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

// StreamSessionDTO 将 flow.StreamSession 转换为前端形状（含全量消息），用于按需拉取。
func StreamSessionDTO(ss *flow.StreamSession) StreamSessionDTOType {
	dto := streamSessionMeta(ss)
	msgs := make([]StreamMessageDTO, 0, len(ss.Messages))
	for _, m := range ss.Messages {
		msgs = append(msgs, streamMessageDTO(ss.ID, m))
	}
	dto.Messages = msgs
	return dto
}

// wsSessionMeta 构造不含 messages 的会话 DTO。
func wsSessionMeta(ws *flow.WSSession) WSSessionDTOType {
	dto := WSSessionDTOType{
		ID:           ws.ID,
		URL:          ws.URL,
		Status:       ws.Status,
		StartTime:    rfc3339(ws.StartTime),
		MessageCount: ws.MessageCount,
		TotalSize:    ws.TotalSize,
		Messages:     []WSMessageDTO{},
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

// WSSessionDTO 将 flow.WSSession 转换为前端 WebSocketSession 形状，用于按需拉取。
func WSSessionDTO(ws *flow.WSSession) WSSessionDTOType {
	dto := wsSessionMeta(ws)
	msgs := make([]WSMessageDTO, 0, len(ws.Messages))
	for _, m := range ws.Messages {
		msgs = append(msgs, wsMessageDTO(ws.ID, m))
	}
	dto.Messages = msgs
	return dto
}

// 长连接会话的实时推送载荷，每帧只携带新增消息。
// Session.MessageCount 作为序号，前端可据此检测缺帧并重新拉取完整会话。
type WSDeltaDTO struct {
	Session WSSessionDTOType `json:"session"`           // 会话元数据,messages 恒为空
	Message *WSMessageDTO    `json:"message,omitempty"` // 本次新增的那条;为空表示只更新了元数据
	// Retained 是后端裁剪后当前保留的条数，前端据此同步本地时间线。
	Retained int `json:"retained"`
}

// StreamDeltaDTO 是流式会话的实时推送载荷,语义同 WSDeltaDTO。
type StreamDeltaDTO struct {
	Session  StreamSessionDTOType `json:"session"`
	Message  *StreamMessageDTO    `json:"message,omitempty"`
	Retained int                  `json:"retained"`
}

// WSDelta 组装一条 WebSocket 增量推送。added 为 nil 表示本次只有元数据变化。
func WSDelta(ws *flow.WSSession, added *flow.WSMessage) WSDeltaDTO {
	d := WSDeltaDTO{Session: wsSessionMeta(ws), Retained: len(ws.Messages)}
	if added != nil {
		m := wsMessageDTO(ws.ID, *added)
		d.Message = &m
	}
	return d
}

// StreamDelta 组装一条流式增量推送。added 为 nil 表示本次只有元数据变化。
func StreamDelta(ss *flow.StreamSession, added *flow.StreamMessage) StreamDeltaDTO {
	d := StreamDeltaDTO{Session: streamSessionMeta(ss), Retained: len(ss.Messages)}
	if added != nil {
		m := streamMessageDTO(ss.ID, *added)
		d.Message = &m
	}
	return d
}
