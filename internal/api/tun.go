// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/mintfog/sniffy/internal/service"
)

// handleTun 读写虚拟网卡的开关。POST {"enabled":true} 开始开启并立即返回 starting 状态，结果经
// WebSocket 的 tun_state 推送；POST {"enabled":false} 等网卡消失后才返回。
func (s *Server) handleTun(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		ok(w, s.svc.TunStatus())
	case http.MethodPost:
		enabled, valid := decodeEnabled(w, r)
		if !valid {
			return
		}
		var st service.TunStatus
		var err error
		if enabled {
			st, err = s.svc.StartTun()
		} else {
			st, err = s.svc.StopTun()
		}
		switch {
		case errors.Is(err, service.ErrTunUnavailable):
			fail(w, http.StatusNotImplemented, err.Error())
		case errors.Is(err, service.ErrTunStopping):
			fail(w, http.StatusConflict, err.Error())
		case err != nil:
			fail(w, http.StatusInternalServerError, err.Error())
		default:
			ok(w, st)
		}
	default:
		failMethodNotAllowed(w, http.MethodGet, http.MethodHead, http.MethodPost)
	}
}

// handleDecryptBypass 列出或清空直通名单，清空后所有主机的透明连接重新尝试解密。
func (s *Server) handleDecryptBypass(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		ok(w, s.svc.DecryptBypass())
	case http.MethodDelete:
		s.svc.ClearDecryptBypass()
		ok(w, s.svc.DecryptBypass())
	default:
		failMethodNotAllowed(w, http.MethodGet, http.MethodHead, http.MethodDelete)
	}
}

// handleDecryptBypassHost 处理 DELETE /api/tun/decrypt-bypass/{host:port}：删掉一条记录，此后到它的
// 透明连接重新尝试解密。不在名单里的同样成功，返回删除后的名单。
func (s *Server) handleDecryptBypassHost(w http.ResponseWriter, r *http.Request) {
	host := strings.TrimPrefix(r.URL.Path, "/api/tun/decrypt-bypass/")
	if strings.Contains(host, "/") {
		fail(w, http.StatusNotFound, "unknown action")
		return
	}
	if !allowMethods(w, r, http.MethodDelete) {
		return
	}
	if err := s.svc.ForgetDecryptBypass(host); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ok(w, s.svc.DecryptBypass())
}
