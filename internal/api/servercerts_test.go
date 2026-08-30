// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package api

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/mintfog/sniffy/internal/service"
)

// /api/server-certs 管的是「给某个域名换上真证书替代 MITM 伪证书」的固定证书,
// 与根 CA 是两套 store 与两组 DTO,故独立成文件。

// importServerCert 生成一对自签证书并经端点导入,返回导入回执。
func importServerCert(t *testing.T, mux http.Handler, cn string, dnsNames ...string) service.ServerCertDTO {
	t.Helper()
	certPEM, keyPEM := newSelfSignedPEM(t, cn, dnsNames, nil)
	body, err := json.Marshal(map[string]string{"certPEM": string(certPEM), "keyPEM": string(keyPEM)})
	if err != nil {
		t.Fatalf("构造导入请求体: %v", err)
	}
	rec := do(t, mux, http.MethodPost, "/api/server-certs", string(body))
	if rec.Code != http.StatusOK {
		t.Fatalf("导入状态码 = %d,响应 %s", rec.Code, rec.Body.String())
	}
	var dto service.ServerCertDTO
	decodeEnvelope(t, rec).into(t, &dto)
	return dto
}

// TestServerCertsListNeverLeaksPrivateKey 这是唯一把导入证书回显给客户端的端点。若改成直接返回
// store 里的 ServerCert(含 KeyPEM)而不是 DTO,任何能访问管理 API 的进程/脚本一次 GET 就拿走
// 用户导入的服务端私钥,可冒充该域名。
func TestServerCertsListNeverLeaksPrivateKey(t *testing.T) {
	t.Parallel()
	_, mux := newTestServer(t)

	// 空库先回空数组:形状回归成 null 时外部脚本的 res.data.length 直接崩。
	rec := do(t, mux, http.MethodGet, "/api/server-certs", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("空库状态码 = %d", rec.Code)
	}
	if got := string(decodeEnvelope(t, rec).Data); got != "[]" {
		t.Errorf("空库 data = %s,期望 []", got)
	}

	imported := importServerCert(t, mux, "example.com", "example.com")

	rec = do(t, mux, http.MethodGet, "/api/server-certs", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("列表状态码 = %d", rec.Code)
	}
	var list []service.ServerCertDTO
	decodeEnvelope(t, rec).into(t, &list)
	if len(list) != 1 {
		t.Fatalf("列表长度 = %d,期望 1", len(list))
	}
	got := list[0]
	if got.ID != imported.ID || got.ID == "" {
		t.Errorf("列表里的 id = %q,期望与导入回执一致的 %q", got.ID, imported.ID)
	}
	if !slices.Contains(got.Hosts, "example.com") {
		t.Errorf("hosts = %v,期望含 example.com", got.Hosts)
	}
	if got.Subject == "" || got.Issuer == "" || got.NotAfter == "" {
		t.Errorf("证书元信息不完整: %+v", got)
	}

	// 私钥一个字节都不能出现在响应里。
	raw := rec.Body.String()
	if strings.Contains(raw, "PRIVATE KEY") {
		t.Errorf("响应体含私钥 PEM 块: %s", raw)
	}
	if strings.Contains(raw, "keyPEM") {
		t.Errorf("响应体含 keyPEM 字段: %s", raw)
	}
}

// TestServerCertImportRoundTrip 这是「给某域名换上真证书」的唯一入口。回归成静默丢弃(解析了 body
// 却没落到 store)时接口仍回 200,用户以为固定证书已生效,实际客户端继续拿到自签伪证书、
// pinning 继续失败,且没有任何可定位的错误。
func TestServerCertImportRoundTrip(t *testing.T) {
	t.Parallel()
	certPEM, keyPEM := newSelfSignedPEM(t, "example.com", []string{"example.com"}, nil)
	body, err := json.Marshal(map[string]string{"certPEM": string(certPEM), "keyPEM": string(keyPEM)})
	if err != nil {
		t.Fatalf("构造请求体: %v", err)
	}

	// POST 与 PUT 走同一条分支,前端只用其中一个 —— 两个都得能用。
	for _, method := range []string{http.MethodPost, http.MethodPut} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()
			s, mux := newTestServer(t)
			rec := do(t, mux, method, "/api/server-certs", string(body))
			if rec.Code != http.StatusOK {
				t.Fatalf("状态码 = %d,响应 %s", rec.Code, rec.Body.String())
			}
			var dto service.ServerCertDTO
			decodeEnvelope(t, rec).into(t, &dto)
			if dto.ID == "" || !slices.Contains(dto.Hosts, "example.com") {
				t.Errorf("回执 = %+v", dto)
			}

			stored := s.svc.ServerCerts()
			if len(stored) != 1 || stored[0].ID != dto.ID {
				t.Errorf("store 里的证书 = %+v,期望恰有一条 id 为 %q", stored, dto.ID)
			}
		})
	}
}

// TestServerCertDeleteEffect 删除同时触发向引擎热下发新的证书列表。回归成「回 200 但没删」时,
// 用户以为已经停用某张固定证书,实际抓包仍在用它 —— 这类失败完全静默,只能靠抓包结果反推。
func TestServerCertDeleteEffect(t *testing.T) {
	t.Parallel()
	s, mux := newTestServer(t)
	imported := importServerCert(t, mux, "example.com", "example.com")

	t.Run("缺 id 回 400 且不删任何东西", func(t *testing.T) {
		rec := do(t, mux, http.MethodDelete, "/api/server-certs", "")
		if rec.Code != http.StatusBadRequest {
			t.Errorf("状态码 = %d,期望 400", rec.Code)
		}
		if e := decodeEnvelope(t, rec); e.Message != "missing id" {
			t.Errorf("message = %q", e.Message)
		}
		if n := len(s.svc.ServerCerts()); n != 1 {
			t.Errorf("证书数 = %d,期望仍为 1", n)
		}
	})

	t.Run("按指纹删除", func(t *testing.T) {
		rec := do(t, mux, http.MethodDelete, "/api/server-certs?id="+imported.ID, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("状态码 = %d", rec.Code)
		}
		var data struct {
			Deleted string `json:"deleted"`
		}
		decodeEnvelope(t, rec).into(t, &data)
		if data.Deleted != imported.ID {
			t.Errorf("data.deleted = %q,期望 %q", data.Deleted, imported.ID)
		}
		if n := len(s.svc.ServerCerts()); n != 0 {
			t.Errorf("证书数 = %d,期望 0", n)
		}
	})

	// 幂等:另一个窗口先删掉时重复删除不该报错。
	t.Run("未知 id 仍回 200", func(t *testing.T) {
		if rec := do(t, mux, http.MethodDelete, "/api/server-certs?id=ghost", ""); rec.Code != http.StatusOK {
			t.Errorf("状态码 = %d,期望 200(幂等)", rec.Code)
		}
		if n := len(s.svc.ServerCerts()); n != 0 {
			t.Errorf("证书数 = %d,期望 0", n)
		}
	})
}

// TestServerCertErrorClassification 用户贴错 PEM 却拿到 500,会去翻服务端日志、以为程序坏了;
// 反过来磁盘写失败被报成 400,用户会反复重贴同一份完全正确的证书,永远好不了。
func TestServerCertErrorClassification(t *testing.T) {
	t.Parallel()

	t.Run("畸形 JSON 回 400", func(t *testing.T) {
		t.Parallel()
		s, mux := newTestServer(t)
		rec := do(t, mux, http.MethodPost, "/api/server-certs", `{`)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("状态码 = %d,期望 400", rec.Code)
		}
		if e := decodeEnvelope(t, rec); e.Message != "invalid json" {
			t.Errorf("message = %q", e.Message)
		}
		if n := len(s.svc.ServerCerts()); n != 0 {
			t.Errorf("证书数 = %d,期望 0", n)
		}
	})

	t.Run("非法 PEM 回 400 且带证书层文案", func(t *testing.T) {
		t.Parallel()
		s, mux := newTestServer(t)
		rec := do(t, mux, http.MethodPost, "/api/server-certs", `{"certPEM":"not-a-pem","keyPEM":"not-a-key"}`)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("状态码 = %d,期望 400,响应 %s", rec.Code, rec.Body.String())
		}
		if e := decodeEnvelope(t, rec); e.Success || e.Message == "" {
			t.Errorf("响应 = success:%v message:%q,期望带上证书层的原因", e.Success, e.Message)
		}
		if n := len(s.svc.ServerCerts()); n != 0 {
			t.Errorf("证书数 = %d,期望 0", n)
		}
	})
}
