// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build desktop

package desktop

import (
	"net/http"
	"strings"

	"github.com/mintfog/sniffy/internal/service"
)

// bodyRoute 是消息体字节流挂在前端资源服务器上的前缀。Wails 把命中该前缀的请求交给
// bodyRouteHandler(见 application.ServiceOptions.Route),并已从 URL 路径中去掉前缀。
const bodyRoute = "/body"

// bodyRouteHandler 按 /body/{id}?source=request|response 流式写出消息体,支持 Range。
// 详情页的音视频预览用它做 <video>/<audio> 的 src:媒体体走透传旁路只在磁盘上,
// 经 bridge 以 base64 搬运既撑内存也拖不动进度条。
type bodyRouteHandler struct{ svc *service.Service }

func (h *bodyRouteHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	h.svc.ServeMessageBody(w, r, id, r.URL.Query().Get("source"))
}
