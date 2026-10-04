// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package api

import (
	"errors"
	"net/http"

	"github.com/mintfog/sniffy/internal/service"
)

func (s *Server) handleImportSessions(w http.ResponseWriter, r *http.Request) {
	if !allowMethods(w, r, http.MethodPost) {
		return
	}
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
