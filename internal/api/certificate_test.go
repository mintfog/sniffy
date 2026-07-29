// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakeCertificateManager struct {
	called bool
	err    error
}

func (m *fakeCertificateManager) RegenerateCA() (string, error) {
	m.called = true
	return "certificate", m.err
}

func TestHandleRegenerateCA(t *testing.T) {
	manager := &fakeCertificateManager{}
	server := &Server{certs: manager}
	recorder := httptest.NewRecorder()
	server.handleRegenerateCA(recorder, httptest.NewRequest(http.MethodPost, "/api/certificate/regenerate", nil))
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
	server.handleRegenerateCA(recorder, httptest.NewRequest(http.MethodPost, "/api/certificate/regenerate", nil))
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("持久化失败返回状态码 %d,响应: %s", recorder.Code, recorder.Body.String())
	}
}
