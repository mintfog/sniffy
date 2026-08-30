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

// 本文件对应 certificate.go 的根 CA 部分:下载、重新生成、导出、导入。
// 服务端证书(/api/server-certs)是另一套 store 与另一组 DTO,拆在 servercerts_test.go。

// multipartUpload 构造一份 multipart/form-data 请求体,返回体与 Content-Type。
// filename 为空表示不带 file 部分。
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

// postMultipart 把一份 multipart 上传发给导入端点。
func postMultipart(t *testing.T, h http.Handler, body io.Reader, contentType string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, testHost+"/api/certificate/import", body)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestCACertificateDownloadContract iOS Safari 只按 application/x-apple-aspen-config 才把响应识别成
// 描述文件;MIME 一旦被统一成 application/json 或漏设,Safari 把 plist 当纯文本显示,用户装不了
// 根证书,整条 iOS 抓包链路断掉。而未就绪分支回归成 200 + 空 body 时,用户下载到 0 字节的
// sniffy-ca.crt,双击安装失败且看不出原因,探活脚本还会判定健康。
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

// TestHandleRegenerateCA 重新生成成功即把新证书热切换进引擎。
func TestHandleRegenerateCA(t *testing.T) {
	t.Parallel()
	manager := &fakeCertificateManager{regenPEM: "certificate"}
	_, mux := newTestServer(t, withCerts(manager))

	rec := do(t, mux, http.MethodPost, "/api/certificate/regenerate", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d,响应 %s", rec.Code, rec.Body.String())
	}
	assertCalls(t, manager.calls, call{Method: "RegenerateCA"})
}

// TestHandleRegenerateCAFailure 持久化失败归 500:重新生成是全有或全无,报成 400 会让用户
// 反复检查自己的输入,而根本没有输入可改。
func TestHandleRegenerateCAFailure(t *testing.T) {
	t.Parallel()
	_, mux := newTestServer(t, withCerts(&fakeCertificateManager{regenErr: errors.New("write failed")}))
	rec := do(t, mux, http.MethodPost, "/api/certificate/regenerate", "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("状态码 = %d,期望 500,响应 %s", rec.Code, rec.Body.String())
	}
}

// TestHandleExportCA 导出成功时把管理器给的字节原样写出,并带齐下载端点的四条头
// (attachment + no-store + nosniff + 管理器给出的 MIME)。少测一条就是给漏掉的那条留后门。
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

// TestHandleExportCAPEMNeedsNoPassword 只有 p12 强制要求口令(它打包了私钥);
// pem 是公开证书,要求口令会让「下载根证书」这条最常用的路径平白多一步。
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

// TestHandleExportCARejectsUnsafeRequests p12 缺口令、格式不认识都必须在调到管理器之前就拒掉。
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

// TestCAExportFormatWhitelistRejectsAliases 底层 service.CertificateExportAs 还认 cer / pfx / bundle /
// pem-bundle,其中后两者返回「证书 + 私钥的联合 PEM」。API 层这张白名单是根 CA 私钥外流的唯一关口:
// 谁「顺手把别名补齐」,一次 POST 就能明文导出根 CA 私钥,拿到它可对任意域名签发被本机信任的证书;
// pfx 还会绕过写死判 format=="p12" 的口令关口。
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

// TestCAExportFile 白名单内的取值决定下载文件名,写错会让用户存下一个扩展名不对的证书。
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

// TestHandleExportCAFailureBranches 空数据分支存在的意义就是「宁可 500 也不给用户一个 0 字节的
// sniffy-ca.p12」:它一旦回归,用户下载到空文件、导入系统钥匙串报错,却完全看不出是服务端的问题。
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

// TestHandleImportCA 上传的字节与口令必须逐字送到管理器,返回的新根证书 PEM 回给调用方。
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

// TestHandleImportCARejectsOversizedUploads 这两道关是「一次导入请求能吃掉多少进程内存与磁盘」的
// 唯一上限,第二道是第一道漏网时的兜底。任何一道被摘掉,一个 multipart 上传就能把 headless 进程
// 推到 OOM,抓包代理随之整体不可用 —— 而不是只失败这一次导入。
func TestHandleImportCARejectsOversizedUploads(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		size int
	}{
		// 整体超过 maxCAImportBytes+1MiB:被 MaxBytesReader 在读取阶段拦下。
		{"整份 multipart 超限", 12 << 20},
		// 整体在 11 MiB 之内、文件本身超过 10 MiB:被 header.Size 那道关拦下。
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

// TestHandleImportCAInputShapes 用错 Content-Type 是脚本调用方最常见的失误,回 500 会把用户引向
// 「服务端坏了 / 证书文件有问题」而反复换文件重试。
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

	// 空文件交给管理器判定:它才知道「这不是一份证书」,transport 替它下结论只会掩盖真实原因。
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

// TestHandleImportCAClassifiesManagerErrors 用户贴错证书却拿到 500,会去翻服务端日志、以为程序坏了;
// 反过来磁盘写失败被报成 400,用户会反复重贴同一份完全正确的证书,永远好不了。
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

// TestHandleImportCACleansTempFiles 导入的是 PKCS12 / PEM bundle,里面就是根 CA 私钥。临时副本留在
// /tmp 等于把「可对任意域名签发受信证书」的私钥落在全机可读目录里,进程重启也不会清。
// 代码把 defer RemoveAll 特意放在错误判断之前,正是这条要钉住的顺序。
func TestHandleImportCACleansTempFiles(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	// 2 MiB 超过 ParseMultipartForm(1<<20) 的内存阈值,强制落盘。
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

// TestCertificateManagementUnavailable 未装配证书管理器时三条端点统一回 501,而不是 nil 指针 panic。
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
