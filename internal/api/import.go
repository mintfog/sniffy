// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/mintfog/sniffy/internal/service"
)

func (s *Server) handleImportSessions(w http.ResponseWriter, r *http.Request) {
	if !allowMethods(w, r, http.MethodPost) {
		return
	}
	// 解除服务器的整请求读取期限：档案边上传边解析，耗时随体积增长。
	_ = http.NewResponseController(w).SetReadDeadline(time.Time{})
	result, err := s.svc.ImportSessionArchive(r.Body)
	if errors.Is(err, service.ErrSessionImportTooLarge) {
		fail(w, http.StatusRequestEntityTooLarge, err.Error())
		return
	}
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ok(w, result)
}
