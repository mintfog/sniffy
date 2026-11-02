// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package service

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mintfog/sniffy/internal/flow"
)

const maxSessionImportBytes = 128 << 20

var ErrSessionImportTooLarge = errors.New("导入文件超过 128 MiB")

type SessionImportResult struct {
	Imported  int `json:"imported"`
	HTTP      int `json:"http"`
	WebSocket int `json:"webSocket"`
	// 导入产生的事件可能超出事件队列被丢弃，前端据此补齐列表。
	IDs []string `json:"ids"`
}

// ImportSessionArchive 全部解析和校验成功后追加会话；文件中的 ID 不参与入库。
// 导入只还原离线记录，不发起请求，也不改变实时流量统计。
func (s *Service) ImportSessionArchive(reader io.Reader) (SessionImportResult, error) {
	var result SessionImportResult
	data, err := io.ReadAll(io.LimitReader(reader, maxSessionImportBytes+1))
	if err != nil {
		return result, err
	}
	if len(data) > maxSessionImportBytes {
		return result, ErrSessionImportTooLarge
	}
	var archive HARArchive
	if err := json.Unmarshal(data, &archive); err != nil {
		return result, fmt.Errorf("HAR/JSON 格式错误: %w", err)
	}
	if archive.Log.Entries == nil || (archive.Log.Version != "" && archive.Log.Version != "1.2") {
		return result, errors.New("需要 HAR 1.2 格式的 log.entries 数组")
	}

	flows := make([]*flow.Flow, 0, len(archive.Log.Entries))
	sockets := make([]*flow.WSSession, 0)
	for i, entry := range archive.Log.Entries {
		f, ws, err := importArchiveEntry(entry)
		if err != nil {
			return result, fmt.Errorf("第 %d 条会话: %w", i+1, err)
		}
		if ws != nil {
			sockets = append(sockets, ws)
		} else {
			flows = append(flows, f)
		}
	}

	result.IDs = make([]string, 0, len(flows)+len(sockets))
	for _, f := range flows {
		s.ImportFlowUpdated(f)
		result.IDs = append(result.IDs, f.ID)
	}
	for _, ws := range sockets {
		s.ImportWSSession(ws, nil)
		result.IDs = append(result.IDs, ws.ID)
	}
	result.HTTP, result.WebSocket = len(flows), len(sockets)
	result.Imported = result.HTTP + result.WebSocket
	return result, nil
}

func importArchiveEntry(entry HAREntry) (*flow.Flow, *flow.WSSession, error) {
	u, err := url.Parse(entry.Request.URL)
	validURL := err == nil && u.Host != "" &&
		(u.Scheme == "http" || u.Scheme == "https" || u.Scheme == "ws" || u.Scheme == "wss")
	if !validURL {
		return nil, nil, errors.New("请求 URL 必须包含 HTTP(S) 或 WS(S) 协议和主机")
	}
	if entry.Request.Method == "" || strings.ContainsAny(entry.Request.Method, " \t\r\n") {
		return nil, nil, errors.New("请求方法无效")
	}
	if entry.Time < 0 || entry.Time > 1e12 || math.IsNaN(entry.Time) || math.IsInf(entry.Time, 0) {
		return nil, nil, errors.New("耗时无效")
	}
	if entry.Response.Status < 0 || entry.Response.Status > 999 {
		return nil, nil, errors.New("响应状态码无效")
	}
	if entry.Request.BodySize < -1 {
		return nil, nil, errors.New("请求正文大小无效")
	}
	if entry.StartedDateTime.IsZero() {
		return nil, nil, errors.New("缺少请求开始时间")
	}
	reqHeaders, reqRaw, err := importArchiveHeaders(entry.Request.Headers)
	if err != nil {
		return nil, nil, err
	}
	resHeaders, resRaw, err := importArchiveHeaders(entry.Response.Headers)
	if err != nil {
		return nil, nil, err
	}
	var body []byte
	if post := entry.Request.PostData; post != nil {
		if body, err = importArchivePostData(post); err != nil {
			return nil, nil, err
		}
	}
	responseBody, err := importArchiveText(entry.Response.Content.Text, entry.Response.Content.Encoding)
	if err != nil {
		return nil, nil, err
	}
	if u.Scheme == "ws" || u.Scheme == "wss" || entry.WebSocketMessages != nil {
		ws, err := importArchiveWebSocket(entry, flow.NewID())
		return nil, ws, err
	}

	if reqHeaders.Get("Content-Type") == "" && entry.Request.PostData != nil {
		reqHeaders.Set("Content-Type", entry.Request.PostData.MimeType)
	}
	if resHeaders.Get("Content-Type") == "" && entry.Response.Content.MimeType != "" {
		resHeaders.Set("Content-Type", entry.Response.Content.MimeType)
	}
	if resHeaders.Get("Location") == "" && entry.Response.RedirectURL != "" {
		resHeaders.Set("Location", entry.Response.RedirectURL)
	}
	proto := entry.Request.HTTPVersion
	if proto == "" {
		proto = "HTTP/1.1"
	}
	host := reqHeaders.Get("Host")
	if host == "" {
		host = u.Host
	}
	f := flow.New(u.Scheme)
	f.Request = &flow.Request{
		Method:     entry.Request.Method,
		URL:        entry.Request.URL,
		Host:       host,
		Path:       u.RequestURI(),
		Proto:      proto,
		Header:     reqHeaders,
		RawHeaders: reqRaw,
		Body:       body,
		ClientIP:   entry.ClientIP,
	}
	// HAR 的 bodySize 是线缆字节数，正文缺失或压缩时不能从 identity 正文推算。
	if entry.Request.BodySize != 0 {
		f.Metadata[harRequestBodySizeKey] = entry.Request.BodySize
	}
	f.State, f.Error = flow.StateCompleted, entry.Error
	if entry.Response.Status == 0 || entry.Error != "" {
		f.State = flow.StateErrored
	}
	ttfb := min(entry.Time, max(0, entry.Timings.Send)+max(0, entry.Timings.Wait))
	f.Timing = flow.Timing{
		RequestAt:   entry.StartedDateTime,
		ResponseAt:  entry.StartedDateTime.Add(time.Duration(ttfb * float64(time.Millisecond))),
		CompletedAt: entry.StartedDateTime.Add(time.Duration(entry.Time * float64(time.Millisecond))),
		TTFBMs:      int64(ttfb),
		DurationMs:  int64(entry.Time),
	}
	if entry.Response.Status > 0 {
		f.Response = &flow.Response{
			Status:     entry.Response.Status,
			StatusText: entry.Response.StatusText,
			Proto:      entry.Response.HTTPVersion,
			Header:     resHeaders,
			RawHeaders: resRaw,
			Body:       responseBody,
		}
		if entry.Response.Content.Size > int64(len(responseBody)) {
			// HAR 可只含正文元信息；保留已知大小，空路径表示没有正文副本。
			f.Response.SetPassthroughBody("", entry.Response.Content.Size)
		}
	}
	return f, nil, nil
}

func importArchivePostData(post *HARPostData) ([]byte, error) {
	if post.Text != nil || len(post.Params) == 0 {
		return importArchiveText(post.Text, post.Encoding)
	}
	mediaType, _, err := mime.ParseMediaType(post.MimeType)
	if err != nil || mediaType != "application/x-www-form-urlencoded" {
		return nil, fmt.Errorf("无法从 %q 的表单参数还原原始正文", post.MimeType)
	}
	var body strings.Builder
	for i, param := range post.Params {
		if param.FileName != "" || param.ContentType != "" {
			return nil, errors.New("表单参数包含文件信息，来源 HAR 未保留原始正文")
		}
		if i > 0 {
			body.WriteByte('&')
		}
		body.WriteString(url.QueryEscape(param.Name))
		body.WriteByte('=')
		body.WriteString(url.QueryEscape(param.Value))
	}
	return []byte(body.String()), nil
}

func importArchiveHeaders(pairs []HARPair) (http.Header, [][2]string, error) {
	h := make(http.Header)
	raw := make([][2]string, 0, len(pairs))
	for _, p := range pairs {
		value := p.Value
		if p.ValueBase64 != "" {
			decoded, err := base64.StdEncoding.DecodeString(p.ValueBase64)
			if err != nil {
				return nil, nil, errors.New("头部 base64 编码无效")
			}
			value = string(decoded)
		}
		h.Add(p.Name, value)
		raw = append(raw, [2]string{p.Name, value})
	}
	if err := flow.ValidateHeaderPairs(raw); err != nil {
		return nil, nil, err
	}
	return h, raw, nil
}

func importArchiveText(text *string, encoding string) ([]byte, error) {
	if text == nil {
		return nil, nil
	}
	switch encoding {
	case "":
		return []byte(*text), nil
	case "base64":
		data, err := base64.StdEncoding.DecodeString(*text)
		if err != nil {
			return nil, errors.New("正文 base64 编码无效")
		}
		return data, nil
	default:
		return nil, fmt.Errorf("不支持的正文编码 %q", encoding)
	}
}

func importArchiveWebSocket(entry HAREntry, id string) (*flow.WSSession, error) {
	end := entry.StartedDateTime.Add(time.Duration(entry.Time * float64(time.Millisecond)))
	rawURL := entry.Request.URL
	if strings.HasPrefix(rawURL, "https://") {
		rawURL = "wss://" + strings.TrimPrefix(rawURL, "https://")
	}
	if strings.HasPrefix(rawURL, "http://") {
		rawURL = "ws://" + strings.TrimPrefix(rawURL, "http://")
	}
	ws := &flow.WSSession{
		ID:        id,
		URL:       rawURL,
		Status:    "closed",
		StartTime: entry.StartedDateTime,
		EndTime:   &end,
		Messages:  []flow.WSMessage{},
	}
	for _, m := range entry.WebSocketMessages {
		kind := ""
		switch m.Opcode {
		case 1:
			kind = flow.WSText
		case 2:
			kind = flow.WSBinary
		case 8:
			kind = flow.WSClose
		case 9:
			kind = flow.WSPing
		case 10:
			kind = flow.WSPong
		default:
			return nil, errors.New("WebSocket opcode 无效")
		}
		if m.Type != "send" && m.Type != "receive" {
			return nil, errors.New("WebSocket 消息方向无效")
		}
		if m.Time < 0 || m.Time > 1e11 || math.IsNaN(m.Time) || math.IsInf(m.Time, 0) {
			return nil, errors.New("WebSocket 消息时间无效")
		}
		encoding := m.Encoding
		// Chromium 的二进制消息按 base64 保存，但不附带编码字段。
		if m.Opcode == 2 && encoding == "" {
			encoding = "base64"
		}
		body, err := importArchiveText(&m.Data, encoding)
		if err != nil {
			return nil, err
		}
		payload, size := flow.RetainPayload(body)
		direction := flow.WSServerToClient
		if m.Type == "send" {
			direction = flow.WSClientToServer
		}
		message := flow.WSMessage{
			ID:        flow.NewID(),
			FlowID:    id,
			URL:       ws.URL,
			Type:      kind,
			Direction: direction,
			Data:      payload,
			Timestamp: time.UnixMicro(int64(m.Time * 1e6)),
			Size:      max(m.Size, size),
		}
		ws.Messages = flow.TrimWSMessages(append(ws.Messages, message))
		ws.MessageCount++
		ws.TotalSize += message.PayloadSize()
	}
	return ws, nil
}
