// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package api

import (
	"bytes"
	"encoding/pem"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/mintfog/sniffy/ca"
)

// 本文件覆盖 certificate.go 的根 CA 下载、重新生成、导出和导入；服务端证书见 servercerts_test.go。

// multipartUpload 构造 multipart/form-data 请求体及其 Content-Type；filename 为空表示缺少 file 部分。
func multipartUpload(t *testing.T, filename string, content []byte, fields map[string]string) (io.Reader, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if filename != "" {
		part, err := writer.CreateFormFile("file", filename)
		if err != nil {
			t.Fatalf("构造 file 部分: %v", err)
		}
		if _, err := part.Write(content); err != nil {
			t.Fatalf("写 file 部分: %v", err)
		}
	}
	for k, v := range fields {
		if err := writer.WriteField(k, v); err != nil {
			t.Fatalf("写字段 %s: %v", k, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("收尾 multipart: %v", err)
	}
	return &body, writer.FormDataContentType()
}

// postMultipart 向导入端点发送 multipart 请求。
func postMultipart(t *testing.T, h http.Handler, body io.Reader, contentType string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, testHost+"/api/certificate/import", body)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestCACertificateDownloadContract CA 下载和 iOS 描述文件使用各自的 MIME、文件名与内容；CA 未就绪返回 500 信封。
func TestCACertificateDownloadContract(t *testing.T) {
	t.Parallel()

	t.Run("CA 就绪时按下载文件发出", func(t *testing.T) {
		t.Parallel()
		root, err := ca.NewInMemorySelfSignedCA()
		if err != nil {
			t.Fatalf("创建 CA 失败: %v", err)
		}
		_, mux := newTestServer(t, withCA(root))

		rec := do(t, mux, http.MethodGet, "/api/certificate/ca", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("状态码 = %d,响应 %s", rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Content-Type"); got != "application/x-pem-file" {
			t.Errorf("Content-Type = %q", got)
		}
		if got := rec.Header().Get("Content-Disposition"); got != "attachment; filename=sniffy-ca.crt" {
			t.Errorf("Content-Disposition = %q", got)
		}
		block, _ := pem.Decode(rec.Body.Bytes())
		if block == nil || block.Type != "CERTIFICATE" {
			t.Errorf("响应体不是一份 CERTIFICATE PEM: %q", rec.Body.String())
		}

		profile := do(t, mux, http.MethodGet, "/api/certificate/ios-profile", "")
		if profile.Code != http.StatusOK {
			t.Fatalf("描述文件状态码 = %d", profile.Code)
		}
		if got := profile.Header().Get("Content-Type"); got != "application/x-apple-aspen-config" {
			t.Errorf("描述文件 Content-Type = %q(iOS 靠它识别描述文件)", got)
		}
		if got := profile.Header().Get("Content-Disposition"); got != "attachment; filename=sniffy.mobileconfig" {
			t.Errorf("描述文件 Content-Disposition = %q", got)
		}
		if profile.Body.Len() == 0 {
			t.Error("描述文件为空")
		}
	})

	t.Run("CA 未就绪时回 500 而不是空文件", func(t *testing.T) {
		t.Parallel()
		_, mux := newTestServer(t) // 不带 CA
		for _, path := range []string{"/api/certificate/ca", "/api/certificate/ios-profile"} {
			rec := do(t, mux, http.MethodGet, path, "")
			if rec.Code != http.StatusInternalServerError {
				t.Errorf("%s 状态码 = %d,期望 500", path, rec.Code)
			}
			if e := decodeEnvelope(t, rec); e.Success || e.Message != "certificate unavailable" {
				t.Errorf("%s 响应 = success:%v message:%q", path, e.Success, e.Message)
			}
		}
	})
}

// TestHandleRegenerateCA 重新生成返回 200，并向管理器发起一次 RegenerateCA 调用；运行时热切换由 app 层负责。
func TestHandleRegenerateCA(t *testing.T) {
	t.Parallel()
	manager := &fakeCertificateManager{}
	_, mux := newTestServer(t, withCerts(manager))

	rec := do(t, mux, http.MethodPost, "/api/certificate/regenerate", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d,响应 %s", rec.Code, rec.Body.String())
	}
	assertCalls(t, manager.calls, call{Method: "RegenerateCA"})
}

// TestHandleRegenerateCAFailure 持久化失败返回 500，调用方可据此区分服务端故障与输入问题。
func TestHandleRegenerateCAFailure(t *testing.T) {
	t.Parallel()
	_, mux := newTestServer(t, withCerts(&fakeCertificateManager{regenErr: errors.New("write failed")}))
	rec := do(t, mux, http.MethodPost, "/api/certificate/regenerate", "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("状态码 = %d,期望 500,响应 %s", rec.Code, rec.Body.String())
	}
}

// TestHandleExportCA 导出成功原样写出管理器数据，并设置附件、缓存、嗅探和 MIME 响应头。
func TestHandleExportCA(t *testing.T) {
	t.Parallel()
	manager := &fakeCertificateManager{exportData: []byte("p12-data"), exportMIME: "application/x-pkcs12"}
	_, mux := newTestServer(t, withCerts(manager))

	rec := do(t, mux, http.MethodPost, "/api/certificate/export", `{"format":"p12","password":"secret"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d,响应 %s", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); got != "p12-data" {
		t.Errorf("导出内容 = %q", got)
	}
	assertCalls(t, manager.calls, call{Method: "ExportCAAs", Args: []any{"p12", "secret"}})
	for _, c := range []struct{ key, want string }{
		{"Content-Type", "application/x-pkcs12"},
		{"Content-Disposition", `attachment; filename="sniffy-ca.p12"`},
		{"Cache-Control", "no-store"},
		{"X-Content-Type-Options", "nosniff"},
	} {
		if got := rec.Header().Get(c.key); got != c.want {
			t.Errorf("%s = %q,期望 %q", c.key, got, c.want)
		}
	}
}

// TestHandleExportCAPEMNeedsNoPassword PEM 导出使用公开证书内容，PKCS12 导出才需要口令。
func TestHandleExportCAPEMNeedsNoPassword(t *testing.T) {
	t.Parallel()
	manager := &fakeCertificateManager{exportData: []byte("certificate-pem"), exportMIME: "application/x-pem-file"}
	_, mux := newTestServer(t, withCerts(manager))

	rec := do(t, mux, http.MethodPost, "/api/certificate/export", `{"format":"pem"}`)
	if rec.Code != http.StatusOK || rec.Body.String() != "certificate-pem" {
		t.Fatalf("状态/内容 = %d/%q", rec.Code, rec.Body.String())
	}
	assertCalls(t, manager.calls, call{Method: "ExportCAAs", Args: []any{"pem", ""}})
}

// TestHandleExportCARejectsUnsafeRequests 不支持的格式和缺少 PKCS12 口令在调用管理器前返回 400。
func TestHandleExportCARejectsUnsafeRequests(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		body    string
		wantMsg string
	}{
		{"格式不受支持", `{"format":"jks"}`, "unsupported certificate format"},
		{"PKCS12 缺口令", `{"format":"p12","password":""}`, "password is required for PKCS12 export"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			manager := &fakeCertificateManager{}
			_, mux := newTestServer(t, withCerts(manager))
			rec := do(t, mux, http.MethodPost, "/api/certificate/export", c.body)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("状态码 = %d,期望 400,响应 %s", rec.Code, rec.Body.String())
			}
			if e := decodeEnvelope(t, rec); e.Message != c.wantMsg {
				t.Errorf("message = %q,期望 %q", e.Message, c.wantMsg)
			}
			assertNoCalls(t, manager.calls)
		})
	}
}

// TestCAExportFormatWhitelistRejectsAliases API 层仅允许公开的导出格式；别名与包含私钥的联合 PEM 均在入口拒绝。
func TestCAExportFormatWhitelistRejectsAliases(t *testing.T) {
	t.Parallel()
	aliases := []string{"bundle", "pem-bundle", "PEM-BUNDLE", "pfx", "cer"}

	t.Run("caExportFile 不认别名", func(t *testing.T) {
		t.Parallel()
		for _, alias := range aliases {
			format, filename, valid := caExportFile(alias)
			if valid || format != "" || filename != "" {
				t.Errorf("caExportFile(%q) = %q, %q, %v,期望全部为零值", alias, format, filename, valid)
			}
		}
	})

	t.Run("端点在调到管理器前就拒掉", func(t *testing.T) {
		t.Parallel()
		for _, alias := range aliases {
			manager := &fakeCertificateManager{exportData: []byte("root-key-and-cert")}
			_, mux := newTestServer(t, withCerts(manager))
			rec := do(t, mux, http.MethodPost, "/api/certificate/export", `{"format":"`+alias+`"}`)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("format=%q 状态码 = %d,期望 400", alias, rec.Code)
			}
			if e := decodeEnvelope(t, rec); e.Message != "unsupported certificate format" {
				t.Errorf("format=%q message = %q", alias, e.Message)
			}
			assertNoCalls(t, manager.calls)
		}
	})
}

// TestCAExportFile 白名单格式映射到稳定的下载文件名。
func TestCAExportFile(t *testing.T) {
	t.Parallel()
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
		{"jks", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.requested, func(t *testing.T) {
			t.Parallel()
			format, filename, valid := caExportFile(tt.requested)
			if format != tt.format || filename != tt.filename || valid != tt.valid {
				t.Errorf("caExportFile(%q) = %q, %q, %v", tt.requested, format, filename, valid)
			}
		})
	}
}

// TestHandleExportCAFailureBranches 管理器错误和空导出均返回可解析的 500 信封，失败响应不带下载头。
func TestHandleExportCAFailureBranches(t *testing.T) {
	t.Parallel()

	t.Run("管理器报错原文透传为 500", func(t *testing.T) {
		t.Parallel()
		_, mux := newTestServer(t, withCerts(&fakeCertificateManager{exportErr: errors.New("keychain locked")}))
		rec := do(t, mux, http.MethodPost, "/api/certificate/export", `{"format":"pem"}`)
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("状态码 = %d,期望 500", rec.Code)
		}
		if e := decodeEnvelope(t, rec); e.Message != "keychain locked" {
			t.Errorf("message = %q", e.Message)
		}
	})

	t.Run("空导出回 500 且不写下载头", func(t *testing.T) {
		t.Parallel()
		_, mux := newTestServer(t, withCerts(&fakeCertificateManager{exportData: []byte{}, exportMIME: "application/x-pem-file"}))
		rec := do(t, mux, http.MethodPost, "/api/certificate/export", `{"format":"pem"}`)
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("状态码 = %d,期望 500", rec.Code)
		}
		if e := decodeEnvelope(t, rec); e.Message != "certificate export is empty" {
			t.Errorf("message = %q", e.Message)
		}
		if got := rec.Header().Get("Content-Disposition"); got != "" {
			t.Errorf("失败响应不应带下载头,got %q", got)
		}
	})

	t.Run("畸形 JSON 回 400 且零调用", func(t *testing.T) {
		t.Parallel()
		manager := &fakeCertificateManager{}
		_, mux := newTestServer(t, withCerts(manager))
		rec := do(t, mux, http.MethodPost, "/api/certificate/export", `{`)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("状态码 = %d,期望 400", rec.Code)
		}
		if e := decodeEnvelope(t, rec); e.Message != "invalid json" {
			t.Errorf("message = %q", e.Message)
		}
		assertNoCalls(t, manager.calls)
	})

	t.Run("超限请求体回 413 且零调用", func(t *testing.T) {
		t.Parallel()
		manager := &fakeCertificateManager{}
		_, mux := newTestServer(t, withCerts(manager))
		body := `{"format":"pem","padding":"` + strings.Repeat("x", 64<<10) + `"}`
		rec := do(t, mux, http.MethodPost, "/api/certificate/export", body)
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Errorf("状态码 = %d,期望 413,响应 %s", rec.Code, rec.Body.String())
		}
		assertNoCalls(t, manager.calls)
	})
}

// TestHandleImportCA 将上传字节与口令原样传给管理器，并把新根证书 PEM 放入响应。
func TestHandleImportCA(t *testing.T) {
	t.Parallel()
	manager := &fakeCertificateManager{importPEM: "new-root-pem"}
	_, mux := newTestServer(t, withCerts(manager))

	body, contentType := multipartUpload(t, "root.p12", []byte("p12-data"), map[string]string{"password": "secret"})
	rec := postMultipart(t, mux, body, contentType)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d,响应 %s", rec.Code, rec.Body.String())
	}
	if string(manager.importedData) != "p12-data" {
		t.Errorf("导入字节 = %q", manager.importedData)
	}
	assertCalls(t, manager.calls, call{Method: "ImportCA", Args: []any{len("p12-data"), "secret"}})

	var data struct {
		CertificatePEM string `json:"certificatePEM"`
	}
	decodeEnvelope(t, rec).into(t, &data)
	if data.CertificatePEM != "new-root-pem" {
		t.Errorf("data.certificatePEM = %q", data.CertificatePEM)
	}
}

// TestHandleImportCARejectsOversizedUploads 导入请求和文件部分均受大小上限保护，超限统一返回 413 且不调用管理器。
func TestHandleImportCARejectsOversizedUploads(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		size int
	}{
		// 整体超过 maxCAImportBytes+1MiB，覆盖请求体读取上限。
		{"整份 multipart 超限", 12 << 20},
		// 整体在 11 MiB 之内而文件超过 10 MiB，覆盖文件部分上限。
		{"文件部分超限", (10 << 20) + (512 << 10)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			manager := &fakeCertificateManager{}
			_, mux := newTestServer(t, withCerts(manager))
			body, contentType := multipartUpload(t, "root.pem", bytes.Repeat([]byte("x"), c.size), nil)
			rec := postMultipart(t, mux, body, contentType)
			if rec.Code != http.StatusRequestEntityTooLarge {
				t.Errorf("状态码 = %d,期望 413,响应 %s", rec.Code, rec.Body.String())
			}
			if e := decodeEnvelope(t, rec); e.Message != "certificate file is too large" {
				t.Errorf("message = %q", e.Message)
			}
			assertNoCalls(t, manager.calls)
		})
	}
}

// TestHandleImportCAInputShapes 非 multipart 请求与缺少 file 部分返回 400；空文件交由管理器校验。
func TestHandleImportCAInputShapes(t *testing.T) {
	t.Parallel()

	t.Run("非 multipart 回 400", func(t *testing.T) {
		t.Parallel()
		manager := &fakeCertificateManager{}
		_, mux := newTestServer(t, withCerts(manager))
		rec := do(t, mux, http.MethodPost, "/api/certificate/import", `{"file":"x"}`,
			withHeader("Content-Type", "application/json"))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("状态码 = %d,期望 400", rec.Code)
		}
		if e := decodeEnvelope(t, rec); e.Message != "invalid multipart form" {
			t.Errorf("message = %q", e.Message)
		}
		assertNoCalls(t, manager.calls)
	})

	t.Run("缺 file 部分回 400", func(t *testing.T) {
		t.Parallel()
		manager := &fakeCertificateManager{importErr: errors.New("不该被调到")}
		_, mux := newTestServer(t, withCerts(manager))
		body, contentType := multipartUpload(t, "", nil, nil)
		rec := postMultipart(t, mux, body, contentType)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("状态码 = %d,期望 400", rec.Code)
		}
		if e := decodeEnvelope(t, rec); e.Message != "missing certificate file" {
			t.Errorf("message = %q", e.Message)
		}
		assertNoCalls(t, manager.calls)
	})

	// 空文件交由管理器判定证书内容，transport 保留其错误分类。
	t.Run("空文件仍交给管理器", func(t *testing.T) {
		t.Parallel()
		manager := &fakeCertificateManager{importErr: &testInvalidInputError{message: "证书内容为空"}}
		_, mux := newTestServer(t, withCerts(manager))
		body, contentType := multipartUpload(t, "root.pem", nil, nil)
		rec := postMultipart(t, mux, body, contentType)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("状态码 = %d,期望 400", rec.Code)
		}
		assertCalls(t, manager.calls, call{Method: "ImportCA", Args: []any{0, ""}})
	})
}

// TestHandleImportCAClassifiesManagerErrors 管理器将输入错误映射为 400，将持久化错误映射为 500，并透传错误文案。
func TestHandleImportCAClassifiesManagerErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		err    error
		status int
	}{
		{"证书非法", &testInvalidInputError{message: "invalid certificate"}, http.StatusBadRequest},
		{"落盘失败", errors.New("disk write failed"), http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			manager := &fakeCertificateManager{importErr: tt.err}
			_, mux := newTestServer(t, withCerts(manager))
			body, contentType := multipartUpload(t, "root.pem", []byte("certificate-data"), nil)
			rec := postMultipart(t, mux, body, contentType)

			if rec.Code != tt.status {
				t.Errorf("状态码 = %d,期望 %d,响应 %s", rec.Code, tt.status, rec.Body.String())
			}
			if e := decodeEnvelope(t, rec); e.Message != tt.err.Error() {
				t.Errorf("message = %q,期望原文透传 %q", e.Message, tt.err)
			}
			if len(manager.calls) == 0 {
				t.Error("有效上传应调用证书管理器")
			}
		})
	}
}

// TestHandleImportCACleansTempFiles 导入过程产生的 multipart 临时文件在成功、超限和错误路径均被清理；文件内容可能包含根 CA 私钥。
func TestHandleImportCACleansTempFiles(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	// 2 MiB 超过 ParseMultipartForm(1<<20) 的内存阈值，确保测试覆盖临时文件路径。
	const spillSize = 2 << 20
	cases := []struct {
		name     string
		size     int
		manager  *fakeCertificateManager
		wantCode int
	}{
		{"成功路径", spillSize, &fakeCertificateManager{importPEM: "pem"}, http.StatusOK},
		{"超限路径", 11 << 20, &fakeCertificateManager{}, http.StatusRequestEntityTooLarge},
		{"输入非法路径", spillSize, &fakeCertificateManager{importErr: &testInvalidInputError{message: "bad"}}, http.StatusBadRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, mux := newTestServer(t, withCerts(c.manager))
			body, contentType := multipartUpload(t, "root.p12", bytes.Repeat([]byte("x"), c.size), nil)
			if rec := postMultipart(t, mux, body, contentType); rec.Code != c.wantCode {
				t.Fatalf("状态码 = %d,期望 %d", rec.Code, c.wantCode)
			}
			entries, err := os.ReadDir(tmp)
			if err != nil {
				t.Fatalf("读取临时目录: %v", err)
			}
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), "multipart-") {
					t.Errorf("处理器返回后仍有 multipart 临时文件: %s", e.Name())
				}
			}
		})
	}
}

// TestCertificateManagementUnavailable 未装配证书管理器时三条端点统一返回 501。
func TestCertificateManagementUnavailable(t *testing.T) {
	t.Parallel()
	_, mux := newTestServer(t) // certs 缺省为 nil
	for _, path := range []string{
		"/api/certificate/regenerate",
		"/api/certificate/export",
		"/api/certificate/import",
	} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			rec := do(t, mux, http.MethodPost, path, "{}")
			if rec.Code != http.StatusNotImplemented {
				t.Errorf("状态码 = %d,期望 501", rec.Code)
			}
			if e := decodeEnvelope(t, rec); e.Message != "certificate management unavailable" {
				t.Errorf("message = %q", e.Message)
			}
		})
	}
}
