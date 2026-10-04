// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/internal/version"
)

// WriteSessionArchive 按 ids 顺序逐条写出 HAR 1.2，跳过已移出存储的会话。
// 正文读取失败会返回错误，调用方不得把部分写出的文件当作成功结果。
func (s *Service) WriteSessionArchive(ctx context.Context, w io.Writer, ids []string, includeRequestBody, includeResponseBody bool) error {
	creator, _ := json.Marshal(HARCreator{Name: "Sniffy", Version: version.Get()})
	if _, err := fmt.Fprintf(w, `{"log":{"version":"1.2","creator":%s,"entries":[`, creator); err != nil {
		return err
	}
	first := true
	encoder := json.NewEncoder(w)
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return err
		}
		entry, err := s.archiveEntry(id, includeRequestBody, includeResponseBody)
		if err != nil {
			return err
		}
		if entry == nil {
			continue
		}
		if !first {
			if _, err := io.WriteString(w, ","); err != nil {
				return err
			}
		}
		if err := encoder.Encode(entry); err != nil {
			return err
		}
		first = false
	}
	_, err := io.WriteString(w, "]}}\n")
	return err
}

func (s *Service) archiveEntry(id string, includeRequestBody, includeResponseBody bool) (*HAREntry, error) {
	f, ok := s.sessions.get(id)
	if !ok {
		ws, found := s.ws.get(id)
		if !found {
			return nil, nil
		}
		return archiveWebSocket(ws), nil
	}
	if f.Request == nil {
		return nil, nil
	}
	r := f.Request
	proto := r.Proto
	if proto == "" {
		proto = "HTTP/1.1"
	}
	requestCookies := (&http.Request{Header: http.Header(r.Header)}).Cookies()
	entry := &HAREntry{
		ID:              f.ID,
		StartedDateTime: f.Timing.RequestAt,
		Time:            float64(max(0, f.Timing.DurationMs)),
		ClientIP:        r.ClientIP,
		Error:           f.Error,
		Cache:           map[string]any{},
		Request: HARRequest{
			Method:      r.Method,
			URL:         r.URL,
			HTTPVersion: proto,
			Headers:     archiveHeaders(flow.OrderedRequestHeaders(r)),
			Cookies:     archiveCookies(requestCookies),
			QueryString: archiveQueryURL(r.URL),
			HeadersSize: -1,
			BodySize:    int64(len(r.Body)),
		},
		Response: HARResponse{
			HTTPVersion: proto,
			Headers:     []HARPair{},
			Cookies:     []HARCookie{},
			HeadersSize: -1,
			BodySize:    -1,
			Content:     HARContent{MimeType: "application/octet-stream"},
		},
	}
	if entry.StartedDateTime.IsZero() {
		entry.StartedDateTime = time.Unix(0, 0).UTC()
	}
	if encoded := r.OriginalEncodedBody(r.Body); encoded != nil {
		entry.Request.BodySize = int64(len(encoded))
	} else if encoding := http.Header(r.Header).Get("Content-Encoding"); encoding != "" && encoding != "identity" {
		entry.Request.BodySize = -1
	}
	if size, ok := f.Metadata[harRequestBodySizeKey].(int64); ok {
		entry.Request.BodySize = size
	}

	if includeRequestBody && len(r.Body) > 0 {
		text, encoding := archiveText(r.Body)
		entry.Request.PostData = &HARPostData{MimeType: detectMIME(r.Header, r.Body), Text: &text, Encoding: encoding}
	}
	wait := min(entry.Time, float64(max(0, f.Timing.TTFBMs)))
	entry.Timings = HARTimings{Wait: wait, Receive: entry.Time - wait}
	if f.Response == nil {
		return entry, nil
	}
	res := f.Response
	bodySize := res.BodyLen()
	responseCookies := (&http.Response{Header: http.Header(res.Header)}).Cookies()
	entry.Response.Status, entry.Response.StatusText = res.Status, res.StatusText
	if entry.Response.StatusText == "" {
		entry.Response.StatusText = http.StatusText(res.Status)
	}
	entry.Response.Headers = archiveHeaders(flow.OrderedResponseHeaders(res))
	entry.Response.Cookies = archiveCookies(responseCookies)
	entry.Response.RedirectURL = http.Header(res.Header).Get("Location")
	entry.Response.Content = HARContent{Size: bodySize, MimeType: detectMIME(res.Header, res.Body)}
	entry.Response.BodySize = bodySize
	if encoded := res.OriginalEncodedBody(res.Body); encoded != nil {
		entry.Response.BodySize = int64(len(encoded))
	} else if encoding := http.Header(res.Header).Get("Content-Encoding"); encoding != "" && encoding != "identity" {
		entry.Response.BodySize = -1
	}
	bodyPath, _ := res.BodyFile()
	// 导入的 HAR 可只带正文大小：Body 为空且无缓存路径时没有可导出的正文。
	responseBodyAvailable := len(res.Body) > 0 || bodySize == 0 || bodyPath != ""
	if !includeResponseBody || !responseBodyAvailable {
		return entry, nil
	}
	body := res.Body
	if bodyPath != "" {
		var err error
		body, err = os.ReadFile(bodyPath)
		if err != nil {
			return nil, fmt.Errorf("读取会话 %s 正文: %w", id, err)
		}
	}
	text, encoding := archiveText(body)
	entry.Response.Content.Text, entry.Response.Content.Encoding = &text, encoding
	return entry, nil
}

func archiveHeaders(pairs [][2]string) []HARPair {
	out := make([]HARPair, 0, len(pairs))
	for _, pair := range pairs {
		p := HARPair{Name: pair[0], Value: pair[1]}
		if !utf8.ValidString(p.Value) {
			p.ValueBase64 = base64.StdEncoding.EncodeToString([]byte(p.Value))
		}
		out = append(out, p)
	}
	return out
}

func archiveCookies(cookies []*http.Cookie) []HARCookie {
	out := make([]HARCookie, 0, len(cookies))
	for _, c := range cookies {
		cookie := HARCookie{
			Name:     c.Name,
			Value:    c.Value,
			Path:     c.Path,
			Domain:   c.Domain,
			HTTPOnly: c.HttpOnly,
			Secure:   c.Secure,
		}
		if !c.Expires.IsZero() {
			expires := c.Expires
			cookie.Expires = &expires
		}
		out = append(out, cookie)
	}
	return out
}

func archiveText(body []byte) (string, string) {
	if utf8.Valid(body) && bytes.IndexByte(body, 0) < 0 {
		return string(body), ""
	}
	return base64.StdEncoding.EncodeToString(body), "base64"
}

func archiveWebSocket(ws *flow.WSSession) *HAREntry {
	// WS store 未保存升级握手头，因此仅记录已知的协议升级状态。
	entry := &HAREntry{
		ID:              ws.ID,
		StartedDateTime: ws.StartTime,
		Cache:           map[string]any{},
		Request: HARRequest{
			Method:      "GET",
			URL:         ws.URL,
			HTTPVersion: "HTTP/1.1",
			Headers:     []HARPair{},
			Cookies:     []HARCookie{},
			QueryString: archiveQueryURL(ws.URL),
			HeadersSize: -1,
		},
		Response: HARResponse{
			Status:      http.StatusSwitchingProtocols,
			StatusText:  http.StatusText(http.StatusSwitchingProtocols),
			HTTPVersion: "HTTP/1.1",
			Headers:     []HARPair{},
			Cookies:     []HARCookie{},
			HeadersSize: -1,
			Content:     HARContent{MimeType: "application/octet-stream"},
		},
	}
	if ws.EndTime != nil {
		entry.Time = float64(max(0, ws.EndTime.Sub(ws.StartTime).Milliseconds()))
		entry.Timings.Wait = entry.Time
	}
	for _, m := range ws.Messages {
		opcode := wsOpcode(m.Type)
		var data, encoding string
		if opcode == 2 {
			data, encoding = base64.StdEncoding.EncodeToString(m.Data), "base64"
		} else {
			data, encoding = archiveText(m.Data)
		}
		direction := "receive"
		if m.Direction == flow.WSClientToServer {
			direction = "send"
		}
		entry.WebSocketMessages = append(entry.WebSocketMessages, HARWSMessage{
			Type:     direction,
			Time:     float64(m.Timestamp.UnixMicro()) / 1e6,
			Opcode:   opcode,
			Data:     data,
			Encoding: encoding,
			Size:     m.PayloadSize(),
		})
	}
	return entry
}

func wsOpcode(kind string) int {
	switch kind {
	case flow.WSBinary:
		return 2
	case flow.WSClose:
		return 8
	case flow.WSPing:
		return 9
	case flow.WSPong:
		return 10
	default:
		return 1
	}
}

func archiveQueryURL(rawURL string) []HARPair {
	out := make([]HARPair, 0)
	u, err := url.Parse(rawURL)
	if err != nil {
		return out
	}
	for _, pair := range strings.Split(u.RawQuery, "&") {
		if pair == "" {
			continue
		}
		name, value, _ := strings.Cut(pair, "=")
		if decoded, err := url.QueryUnescape(name); err == nil {
			name = decoded
		}
		if decoded, err := url.QueryUnescape(value); err == nil {
			value = decoded
		}
		out = append(out, HARPair{Name: name, Value: value})
	}
	return out
}
