// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

func (s *Server) handleGetCA(w http.ResponseWriter, r *http.Request) {
	pem := s.svc.CertificatePEM()
	if len(pem) == 0 {
		fail(w, http.StatusInternalServerError, "certificate unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/x-pem-file")
	w.Header().Set("Content-Disposition", "attachment; filename=sniffy-ca.crt")
	_, _ = w.Write(pem)
}

// handleIOSProfile 返回内嵌根证书的 iOS 配置描述文件,供 Safari 下载安装。
// MIME application/x-apple-aspen-config 触发 iOS 识别为描述文件。
func (s *Server) handleIOSProfile(w http.ResponseWriter, r *http.Request) {
	profile := s.svc.IOSMobileconfig()
	if len(profile) == 0 {
		fail(w, http.StatusInternalServerError, "certificate unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/x-apple-aspen-config")
	w.Header().Set("Content-Disposition", "attachment; filename=sniffy.mobileconfig")
	_, _ = w.Write(profile)
}

func (s *Server) handleRegenerateCA(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.certs == nil {
		fail(w, http.StatusNotImplemented, "certificate management unavailable")
		return
	}
	if _, err := s.certs.RegenerateCA(); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	ok(w, map[string]any{"message": "根证书已重新生成"})
}

const maxCAImportBytes int64 = 10 << 20

// handleExportCA 按请求的格式返回根 CA 文件。导出口令放在 JSON 请求体中,
// 避免 PKCS12 口令出现在 URL 与访问日志里。
func (s *Server) handleExportCA(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.certs == nil {
		fail(w, http.StatusNotImplemented, "certificate management unavailable")
		return
	}
	var body struct {
		Format   string `json:"format"`
		Password string `json:"password"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&body); err != nil {
		failExportJSONDecode(w, err)
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		failExportJSONDecode(w, err)
		return
	}
	format, filename, valid := caExportFile(body.Format)
	if !valid {
		fail(w, http.StatusBadRequest, "unsupported certificate format")
		return
	}
	if format == "p12" {
		if body.Password == "" {
			fail(w, http.StatusBadRequest, "password is required for PKCS12 export")
			return
		}
	}
	data, contentType, err := s.certs.ExportCAAs(format, body.Password)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if len(data) == 0 {
		fail(w, http.StatusInternalServerError, "certificate export is empty")
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func failExportJSONDecode(w http.ResponseWriter, err error) {
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		fail(w, http.StatusRequestEntityTooLarge, "request body is too large")
		return
	}
	fail(w, http.StatusBadRequest, "invalid json")
}

func caExportFile(requested string) (format, filename string, valid bool) {
	switch strings.ToLower(strings.TrimSpace(requested)) {
	case "", "pem":
		return "pem", "sniffy-ca.pem", true
	case "crt":
		return "crt", "sniffy-ca.crt", true
	case "der":
		return "der", "sniffy-ca.der", true
	case "p12":
		return "p12", "sniffy-ca.p12", true
	default:
		return "", "", false
	}
}

// handleImportCA 接收 multipart/form-data 中的 file 与可选 password,导入并热切换根 CA。
func (s *Server) handleImportCA(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.certs == nil {
		fail(w, http.StatusNotImplemented, "certificate management unavailable")
		return
	}
	// 额外 1 MiB 留给 multipart 边界和表单字段;文件本身仍严格限制为 10 MiB。
	r.Body = http.MaxBytesReader(w, r.Body, maxCAImportBytes+(1<<20))
	err := r.ParseMultipartForm(1 << 20)
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			fail(w, http.StatusRequestEntityTooLarge, "certificate file is too large")
		} else {
			fail(w, http.StatusBadRequest, "invalid multipart form")
		}
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		fail(w, http.StatusBadRequest, "missing certificate file")
		return
	}
	defer file.Close()
	if header.Size > maxCAImportBytes {
		fail(w, http.StatusRequestEntityTooLarge, "certificate file is too large")
		return
	}
	data, err := io.ReadAll(io.LimitReader(file, maxCAImportBytes+1))
	if err != nil {
		fail(w, http.StatusBadRequest, "failed to read certificate file")
		return
	}
	if int64(len(data)) > maxCAImportBytes {
		fail(w, http.StatusRequestEntityTooLarge, "certificate file is too large")
		return
	}
	pem, err := s.certs.ImportCA(data, r.FormValue("password"))
	if err != nil {
		var invalid InvalidInputError
		if errors.As(err, &invalid) && invalid.InvalidInput() {
			fail(w, http.StatusBadRequest, err.Error())
		} else {
			fail(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	ok(w, map[string]any{"certificatePEM": pem})
}

// handleServerCerts 管理按主机导入的服务端证书:GET 列表(不含私钥)、POST 导入、DELETE 删除。
func (s *Server) handleServerCerts(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		ok(w, s.svc.ServerCerts())
	case http.MethodPost, http.MethodPut:
		var body struct {
			CertPEM string `json:"certPEM"`
			KeyPEM  string `json:"keyPEM"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			fail(w, http.StatusBadRequest, "invalid json")
			return
		}
		dto, err := s.svc.ImportServerCert(body.CertPEM, body.KeyPEM)
		if err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		ok(w, dto)
	case http.MethodDelete:
		id := r.URL.Query().Get("id")
		if id == "" {
			fail(w, http.StatusBadRequest, "missing id")
			return
		}
		s.svc.DeleteServerCert(id)
		ok(w, map[string]any{"deleted": id})
	default:
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}
