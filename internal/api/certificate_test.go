// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package api

import (
	"bytes"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeCertificateManager struct {
	called         bool
	err            error
	exportCalled   bool
	exportData     []byte
	exportMIME     string
	exportErr      error
	exportedFormat string
	exportPassword string
	importPEM      string
	importErr      error
	importCalled   bool
	importedData   []byte
	importPassword string
}

func (m *fakeCertificateManager) RegenerateCA() (string, error) {
	m.called = true
	return "certificate", m.err
}

func (m *fakeCertificateManager) ExportCAAs(format, password string) ([]byte, string, error) {
	m.exportCalled = true
	m.exportedFormat = format
	m.exportPassword = password
	return m.exportData, m.exportMIME, m.exportErr
}

func (m *fakeCertificateManager) ImportCA(data []byte, password string) (string, error) {
	m.importCalled = true
	m.importedData = append([]byte(nil), data...)
	m.importPassword = password
	return m.importPEM, m.importErr
}

type testInvalidInputError struct {
	message string
}

func (e *testInvalidInputError) Error() string      { return e.message }
func (e *testInvalidInputError) InvalidInput() bool { return true }

func TestHandleRegenerateCA(t *testing.T) {
	manager := &fakeCertificateManager{}
	server := &Server{certs: manager}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/certificate/regenerate", nil)
	server.handleRegenerateCA(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("重新生成返回状态码 %d,响应: %s", recorder.Code, recorder.Body.String())
	}
	if !manager.called {
		t.Fatal("未调用证书管理器")
	}
}

func TestHandleRegenerateCAFailure(t *testing.T) {
	server := &Server{certs: &fakeCertificateManager{err: errors.New("write failed")}}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/certificate/regenerate", nil)
	server.handleRegenerateCA(recorder, request)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("持久化失败返回状态码 %d,响应: %s", recorder.Code, recorder.Body.String())
	}
}

func TestHandleExportCA(t *testing.T) {
	manager := &fakeCertificateManager{
		exportData: []byte("p12-data"),
		exportMIME: "application/x-pkcs12",
	}
	server := &Server{certs: manager}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/certificate/export",
		strings.NewReader(`{"format":"p12","password":"secret"}`))
	server.handleExportCA(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("导出返回状态码 %d,响应: %s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Body.String(); got != "p12-data" {
		t.Fatalf("导出内容 = %q", got)
	}
	if manager.exportedFormat != "p12" || manager.exportPassword != "secret" {
		t.Fatalf("导出参数 = %q, %q", manager.exportedFormat, manager.exportPassword)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/x-pkcs12" {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := recorder.Header().Get("Content-Disposition"); got != `attachment; filename="sniffy-ca.p12"` {
		t.Fatalf("Content-Disposition = %q", got)
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
}

func TestHandleExportCAPublicCertificateDoesNotRequireAuthentication(t *testing.T) {
	manager := &fakeCertificateManager{
		exportData: []byte("certificate-pem"),
		exportMIME: "application/x-pem-file",
	}
	server := &Server{certs: manager}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/certificate/export", strings.NewReader(`{"format":"pem"}`))

	server.handleExportCA(recorder, request)

	if recorder.Code != http.StatusOK || recorder.Body.String() != "certificate-pem" {
		t.Fatalf("公开证书导出状态/内容 = %d/%q", recorder.Code, recorder.Body.String())
	}
}

func TestHandleExportCARejectsUnsupportedFormat(t *testing.T) {
	server := &Server{certs: &fakeCertificateManager{}}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/certificate/export",
		strings.NewReader(`{"format":"jks"}`))
	server.handleExportCA(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("不支持格式返回状态码 %d,响应: %s", recorder.Code, recorder.Body.String())
	}
}

func TestHandleExportCARejectsOversizedBody(t *testing.T) {
	manager := &fakeCertificateManager{}
	server := &Server{certs: manager}
	recorder := httptest.NewRecorder()
	body := `{"format":"pem","padding":"` + strings.Repeat("x", 64<<10) + `"}`
	request := httptest.NewRequest(http.MethodPost, "/api/certificate/export", strings.NewReader(body))

	server.handleExportCA(recorder, request)

	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("超限导出请求返回状态码 %d,期望 413,响应: %s", recorder.Code, recorder.Body.String())
	}
	if manager.exportCalled {
		t.Fatal("超限导出请求不应调用证书管理器")
	}
}

func TestCAExportFile(t *testing.T) {
	tests := []struct {
		requested string
		format    string
		filename  string
		valid     bool
	}{
		{"", "pem", "sniffy-ca.pem", true},
		{" PEM ", "pem", "sniffy-ca.pem", true},
		{"crt", "crt", "sniffy-ca.crt", true},
		{"der", "der", "sniffy-ca.der", true},
		{"p12", "p12", "sniffy-ca.p12", true},
		{"bundle", "", "", false},
		{"jks", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.requested, func(t *testing.T) {
			format, filename, valid := caExportFile(tt.requested)
			if format != tt.format || filename != tt.filename || valid != tt.valid {
				t.Fatalf("caExportFile(%q) = %q, %q, %v", tt.requested, format, filename, valid)
			}
		})
	}
}

func TestHandleImportCA(t *testing.T) {
	manager := &fakeCertificateManager{importPEM: "new-root-pem"}
	server := &Server{certs: manager}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "root.p12")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("p12-data")); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteField("password", "secret"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/certificate/import", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	server.handleImportCA(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("导入返回状态码 %d,响应: %s", recorder.Code, recorder.Body.String())
	}
	if string(manager.importedData) != "p12-data" || manager.importPassword != "secret" {
		t.Fatalf("导入参数 = %q, %q", manager.importedData, manager.importPassword)
	}
	if !strings.Contains(recorder.Body.String(), `"certificatePEM":"new-root-pem"`) {
		t.Fatalf("导入响应: %s", recorder.Body.String())
	}
}

func TestHandleImportCARejectsMissingFile(t *testing.T) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	manager := &fakeCertificateManager{importErr: errors.New("should not be called")}
	server := &Server{certs: manager}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/certificate/import", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	server.handleImportCA(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("缺少文件返回状态码 %d,响应: %s", recorder.Code, recorder.Body.String())
	}
	if manager.importCalled {
		t.Fatal("缺少文件时不应调用证书管理器")
	}
}

func TestHandleExportCARejectsUnsafePrivateKeyExports(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"unencrypted bundle", `{"format":"bundle"}`},
		{"empty PKCS12 password", `{"format":"p12","password":""}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := &fakeCertificateManager{}
			server := &Server{certs: manager}
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/certificate/export", strings.NewReader(tt.body))
			server.handleExportCA(recorder, request)

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("不安全导出返回状态码 %d,响应: %s", recorder.Code, recorder.Body.String())
			}
			if manager.exportCalled {
				t.Fatal("不安全导出不应调用证书管理器")
			}
		})
	}
}

func TestHandleImportCAClassifiesManagerErrors(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
	}{
		{"invalid certificate", &testInvalidInputError{message: "invalid certificate"}, http.StatusBadRequest},
		{"persistence failure", errors.New("disk write failed"), http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := &fakeCertificateManager{importErr: tt.err}
			server := &Server{certs: manager}

			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			part, err := writer.CreateFormFile("file", "root.pem")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := part.Write([]byte("certificate-data")); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}

			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/certificate/import", &body)
			request.Header.Set("Content-Type", writer.FormDataContentType())
			server.handleImportCA(recorder, request)

			if recorder.Code != tt.status {
				t.Fatalf("导入错误返回状态码 %d,期望 %d,响应: %s", recorder.Code, tt.status, recorder.Body.String())
			}
			if !manager.importCalled {
				t.Fatal("有效上传应调用证书管理器")
			}
		})
	}
}
