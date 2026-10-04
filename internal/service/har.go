// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package service

import "time"

const harRequestBodySizeKey = "har.request.bodySize"

type HARArchive struct {
	Log HARLog `json:"log"`
}

type HARLog struct {
	Version string     `json:"version"`
	Creator HARCreator `json:"creator"`
	Entries []HAREntry `json:"entries"`
}

type HARCreator struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type HARPair struct {
	Name        string `json:"name"`
	Value       string `json:"value"`
	ValueBase64 string `json:"_valueBase64,omitempty"`
}

type HARCookie struct {
	Name     string     `json:"name"`
	Value    string     `json:"value"`
	Path     string     `json:"path,omitempty"`
	Domain   string     `json:"domain,omitempty"`
	Expires  *time.Time `json:"expires,omitempty"`
	HTTPOnly bool       `json:"httpOnly"`
	Secure   bool       `json:"secure"`
}

type HARPostParam struct {
	Name        string `json:"name"`
	Value       string `json:"value"`
	FileName    string `json:"fileName,omitempty"`
	ContentType string `json:"contentType,omitempty"`
}

type HARPostData struct {
	MimeType string         `json:"mimeType"`
	Text     *string        `json:"text,omitempty"`
	Params   []HARPostParam `json:"params,omitempty"`
	// HAR 1.2 的 postData 未定义二进制编码，扩展字段保留原始请求体。
	Encoding string `json:"_encoding,omitempty"`
}

type HARRequest struct {
	Method      string       `json:"method"`
	URL         string       `json:"url"`
	HTTPVersion string       `json:"httpVersion"`
	Cookies     []HARCookie  `json:"cookies"`
	Headers     []HARPair    `json:"headers"`
	QueryString []HARPair    `json:"queryString"`
	PostData    *HARPostData `json:"postData,omitempty"`
	HeadersSize int64        `json:"headersSize"`
	BodySize    int64        `json:"bodySize"`
}

type HARContent struct {
	Size     int64  `json:"size"`
	MimeType string `json:"mimeType"`
	// nil 表示来源未保留正文，非 nil 的空串表示已保留空正文。
	Text     *string `json:"text,omitempty"`
	Encoding string  `json:"encoding,omitempty"`
}

type HARResponse struct {
	Status      int         `json:"status"`
	StatusText  string      `json:"statusText"`
	HTTPVersion string      `json:"httpVersion"`
	Cookies     []HARCookie `json:"cookies"`
	Headers     []HARPair   `json:"headers"`
	Content     HARContent  `json:"content"`
	RedirectURL string      `json:"redirectURL"`
	HeadersSize int64       `json:"headersSize"`
	BodySize    int64       `json:"bodySize"`
}

type HARTimings struct {
	Send    float64 `json:"send"`
	Wait    float64 `json:"wait"`
	Receive float64 `json:"receive"`
}

type HARWSMessage struct {
	Type     string  `json:"type"`
	Time     float64 `json:"time"`
	Opcode   int     `json:"opcode"`
	Data     string  `json:"data"`
	Encoding string  `json:"_encoding,omitempty"`
	Size     int64   `json:"_size,omitempty"`
}

type HAREntry struct {
	StartedDateTime   time.Time      `json:"startedDateTime"`
	Time              float64        `json:"time"`
	Request           HARRequest     `json:"request"`
	Response          HARResponse    `json:"response"`
	Cache             map[string]any `json:"cache"`
	Timings           HARTimings     `json:"timings"`
	ID                string         `json:"_id,omitempty"`
	ClientIP          string         `json:"_clientIPAddress,omitempty"`
	Error             string         `json:"_error,omitempty"`
	WebSocketMessages []HARWSMessage `json:"_webSocketMessages,omitempty"`
}
