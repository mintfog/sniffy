// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package app

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mintfog/sniffy/ca"
)

func proxyTestClient(t *testing.T, a *App) *http.Client {
	t.Helper()
	proxyURL, err := url.Parse("http://" + a.Engine.Listener().GetAddress())
	if err != nil {
		t.Fatal(err)
	}
	tr := &http.Transport{
		Proxy:             http.ProxyURL(proxyURL),
		TLSClientConfig:   &tls.Config{InsecureSkipVerify: true},
		DisableKeepAlives: true,
	}
	t.Cleanup(tr.CloseIdleConnections)
	return &http.Client{Transport: tr, Timeout: 5 * time.Second}
}

func TestBuildPersistedDecryptAndServerCertificate(t *testing.T) {
	if !appScenarioProcess(t) {
		return
	}
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "持久化装配")
	}))
	defer origin.Close()
	a := buildRunningApp(t)
	root, err := ca.NewInMemorySelfSignedCA()
	if err != nil {
		t.Fatal(err)
	}
	cert, err := root.IssueCert("127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	key, err := x509.MarshalPKCS8PrivateKey(cert.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key})
	importedCert, err := a.Service.ImportServerCert(string(certPEM), string(keyPEM))
	if err != nil {
		t.Fatal(err)
	}
	a.Service.UpdateConfig(map[string]any{
		"enableHTTPS":      true,
		"decryptScope":     "allow",
		"decryptAllow":     []string{"127.0.0.1"},
		"tlsInsecureHosts": []string{"127.0.0.1"},
	})
	if err := a.Stop(); err != nil {
		t.Fatal(err)
	}
	// 处理器配置是进程全局状态，清空后才能验证重新装配确实读取了磁盘。
	_ = a.Engine.SetDecryptScope(false, "all", nil, nil)
	_ = a.Engine.SetImportedServerCerts(nil)
	_ = a.Engine.SetTLSInsecureHosts(nil)
	a = buildRunningApp(t)
	if !slices.Equal(a.Service.Config().TLSInsecureHosts, []string{"127.0.0.1"}) || !a.Engine.OutboundTLSConfig("127.0.0.1").InsecureSkipVerify {
		t.Fatal("重启未恢复自签测试源站的精确 TLS 例外")
	}
	client := proxyTestClient(t, a)
	checkCertificate := func(target string, want []byte) {
		t.Helper()
		resp, err := client.Get(target)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil || string(body) != "持久化装配" {
			t.Fatalf("TLS 转发失败: %q %v", body, err)
		}
		if !bytes.Equal(resp.TLS.PeerCertificates[0].Raw, want) {
			t.Fatal("代理握手呈现的证书与配置不符")
		}
	}
	checkCertificate(origin.URL, cert.Certificate[0])
	checkCertificate(strings.Replace(origin.URL, "127.0.0.1", "localhost", 1), origin.Certificate().Raw)
	a.Service.UpdateConfig(map[string]any{"decryptAllow": []string{"other.test"}})
	checkCertificate(origin.URL, origin.Certificate().Raw)
	a.Service.UpdateConfig(map[string]any{"decryptScope": "deny", "decryptDeny": []string{"127.0.0.1"}})
	checkCertificate(origin.URL, origin.Certificate().Raw)
	a.Service.UpdateConfig(map[string]any{"decryptDeny": []string{"other.test"}})
	checkCertificate(origin.URL, cert.Certificate[0])
	a.Service.DeleteServerCert(importedCert.ID)
	resp, err := client.Get(origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if bytes.Equal(resp.TLS.PeerCertificates[0].Raw, origin.Certificate().Raw) || bytes.Equal(resp.TLS.PeerCertificates[0].Raw, cert.Certificate[0]) {
		t.Fatal("删除导入证书后未继续解密")
	}
	a.Service.UpdateConfig(map[string]any{"tlsInsecureHosts": []string{}})
	resp, err = client.Get(origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway || a.Engine.OutboundTLSConfig("127.0.0.1").InsecureSkipVerify {
		t.Fatal("撤销测试源站例外后应拒绝下一次 TLS 转发")
	}
	a.Service.UpdateConfig(map[string]any{"enableHTTPS": false})
	checkCertificate(origin.URL, origin.Certificate().Raw)
}

func TestBuildPersistedThrottle(t *testing.T) {
	if !appScenarioProcess(t) {
		return
	}
	payload := bytes.Repeat([]byte("x"), 32*1024)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(payload)
	}))
	defer origin.Close()
	a := buildRunningApp(t)
	a.Service.UpdateConfig(map[string]any{"throttle": true, "throttleKiBps": 32})
	if err := a.Stop(); err != nil {
		t.Fatal(err)
	}
	_ = a.Engine.SetThrottle(false, 0)
	a = buildRunningApp(t)
	client := proxyTestClient(t, a)
	transfer := func() time.Duration {
		t.Helper()
		start := time.Now()
		resp, err := client.Get(origin.URL)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil || !bytes.Equal(body, payload) {
			t.Fatalf("限速转发响应错误: bytes=%d err=%v", len(body), err)
		}
		return time.Since(start)
	}
	slow := transfer()
	if slow < 800*time.Millisecond {
		t.Fatalf("重启未恢复 32 KiB/s 限速: %v", slow)
	}
	a.Service.UpdateConfig(map[string]any{"throttleKiBps": 128})
	faster := transfer()
	if faster < 150*time.Millisecond || faster >= slow*3/4 {
		t.Fatalf("运行时速率修改未生效: 32=%v 128=%v", slow, faster)
	}
	a.Service.UpdateConfig(map[string]any{"throttle": false})
	fast := transfer()
	if fast >= slow/2 {
		t.Fatalf("关闭限速未生效: 启用=%v 关闭=%v", slow, fast)
	}
	t.Logf("32 KiB/s=%v，128 KiB/s=%v，关闭限速=%v", slow, faster, fast)
}
