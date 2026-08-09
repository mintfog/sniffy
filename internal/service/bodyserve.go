// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package service

import (
	"bytes"
	"net/http"
	"os"
	"time"
)

// ServeMessageBody 流式写出会话请求/响应体,并支持 Range(由 http.ServeContent 解析并回 206)。
// 详情页的音视频预览把 <video>/<audio> 的 src 指向这里:媒体体走透传旁路只在磁盘上,
// 整块读进内存再 base64 过 transport 既撑内存也无法拖进度条。
//
// 会话/消息体不存在,或落盘副本已被缓存淘汰时回 404。
func (s *Service) ServeMessageBody(w http.ResponseWriter, r *http.Request, id, source string) {
	src, ok := s.MessageBodySource(id, source)
	if !ok {
		http.NotFound(w, r)
		return
	}
	// 显式给出 MIME:副本文件名是 flow id,没有扩展名可供 ServeContent 推断。
	w.Header().Set("Content-Type", src.Mime)
	if src.Path == "" {
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(src.Data))
		return
	}
	f, err := os.Open(src.Path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	// modtime 传零值:副本随会话存活、内容不会变,不需要协商缓存。
	http.ServeContent(w, r, "", time.Time{}, f)
}
