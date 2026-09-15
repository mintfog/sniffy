// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package http

import (
	"crypto/x509"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDefaultUpstreamClientRejectsUntrustedHTTPS(t *testing.T) {
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("默认客户端不能向未验证的源站发送 HTTP 请求")
	}))
	t.Cleanup(origin.Close)
	client := newDefaultUpstreamClient()
	t.Cleanup(client.CloseIdleConnections)
	resp, err := client.Get(origin.URL)
	if resp != nil {
		_ = resp.Body.Close()
	}
	var unknownAuthority x509.UnknownAuthorityError
	if !errors.As(err, &unknownAuthority) {
		t.Fatalf("应拒绝不受信任的源站证书, 实际错误: %v", err)
	}
}
