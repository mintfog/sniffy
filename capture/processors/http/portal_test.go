// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package http

import (
	"bufio"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mintfog/sniffy/internal/netinfo"
)

type portalConn struct {
	*mockConn
	local *net.TCPAddr
}

func (c *portalConn) LocalAddr() net.Addr { return c.local }

type portalTestConfig struct {
	timeoutConfig
	address string
}

func (c *portalTestConfig) GetAddress() string { return c.address }

func TestPortalRequestBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, host, local, target, method, upgrade, listen string
		tunnel, tls, want                                  bool
	}{
		{name: "IPv4", host: "127.0.0.1:8080", want: true},
		{name: "localhost", host: "LOCALHOST.:8080", want: true},
		{name: "IPv6", host: "[::1]:8080", local: "[::1]:8080", want: true},
		{name: "字母开头IPv6", host: "[fd00::1]:8080", local: "[fd00::1]:8080", want: true},
		{name: "IPv4映射", host: "[::ffff:127.0.0.1]:8080", want: true},
		{name: "局域网", host: "192.168.1.12:8090", local: "192.168.1.12:8090", want: true},
		{name: "默认端口", host: "localhost", local: "127.0.0.1:80", want: true},
		{name: "外部域名", host: "example.test:8080"},
		{name: "数字开头域名", host: "123.example.test:8080"},
		{name: "IPv4前缀域名", host: "127.0.0.1.example.test:8080"},
		{name: "其他IP", host: "192.168.1.12:8080"},
		{name: "其他端口", host: "127.0.0.1:8090"},
		{name: "缺省端口不匹配", host: "localhost"},
		{name: "localhost经局域网", host: "localhost:8080", local: "192.168.1.12:8080"},
		{name: "通配地址", host: "0.0.0.0:8080"},
		{name: "代理形式", host: "127.0.0.1:8080", target: "http://127.0.0.1:8080/"},
		{name: "CONNECT", host: "127.0.0.1:8080", method: http.MethodConnect},
		{name: "WebSocket", host: "127.0.0.1:8080", upgrade: "websocket"},
		{name: "h2c", host: "127.0.0.1:8080", upgrade: "h2c"},
		{name: "隧道", host: "127.0.0.1:8080", tunnel: true},
		{name: "TLS", host: "127.0.0.1:8080", tls: true},
		{name: "通配监听其他回环IP", host: "127.0.0.2:8080", listen: "0.0.0.0", want: true},
		{name: "指定监听其他回环IP", host: "127.0.0.2:8080", listen: "127.0.0.1"},
		{name: "通配监听localhost经局域网", host: "localhost:8080", local: "192.168.1.12:8080", listen: "0.0.0.0", want: true},
		{name: "指定监听localhost经局域网", host: "localhost:8080", local: "192.168.1.12:8080", listen: "192.168.1.12"},
		{name: "IPv4通配不包含IPv6", host: "[::1]:8080", listen: "0.0.0.0"},
		{name: "IPv6通配包含回环", host: "[::1]:8080", listen: "[::]", want: true},
		{name: "IPv6通配包含IPv4", host: "127.0.0.1:8080", local: "[::1]:8080", listen: "[::]", want: true},
		{name: "通配监听外部域名", host: "example.test:8080", listen: "0.0.0.0"},
		{name: "通配监听外部IP", host: "192.0.2.254:8080", listen: "0.0.0.0"},
		{name: "通配监听错误端口", host: "127.0.0.2:8090", listen: "0.0.0.0"},
		{name: "通配监听未指定IP", host: "0.0.0.0:8080", listen: "0.0.0.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.local == "" {
				tc.local = "127.0.0.1:8080"
			}
			if tc.target == "" {
				tc.target = "/"
			}
			local, err := net.ResolveTCPAddr("tcp", tc.local)
			if err != nil {
				t.Fatal(err)
			}
			conn := &portalConn{mockConn: newMockConn(""), local: local}
			server := &timeoutServer{mockServer: newMockServer()}
			if tc.listen != "" {
				server.cfg = &portalTestConfig{address: tc.listen}
			}
			p := &Processor{conn: newMockConnection(conn, server), proxyTunnel: tc.tunnel, isHttps: tc.tls}
			r := httptest.NewRequest(tc.method, tc.target, nil)
			r.Host = tc.host
			r.Header.Set("Upgrade", tc.upgrade)
			if got := p.isPortalRequest(r); got != tc.want {
				t.Fatalf("入口判断=%v，期望 %v", got, tc.want)
			}
		})
	}
}

func TestProxyPortalSkipsAddressLookupForUpstream(t *testing.T) {
	var lookups int
	previous := portalLocalIPs
	portalLocalIPs = &portalIPCache{readAddrs: func() ([]net.Addr, error) {
		lookups++
		return nil, nil
	}}
	t.Cleanup(func() { portalLocalIPs = previous })
	server := &timeoutServer{mockServer: newMockServer(), cfg: &portalTestConfig{address: "0.0.0.0"}}
	local := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8080}
	upstream := &url.URL{Scheme: "http", Host: "upstream.test:8080"}
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(upstream)}}
	for _, target := range []string{"http://127.0.0.2:8080/", "http://192.0.2.254:8080/"} {
		request := httptest.NewRequest(http.MethodGet, target, nil)
		if response := proxyPortalResponse(server, request, local, nil, client); response != nil {
			response.Body.Close()
			t.Fatalf("配置上游代理时，目标 %s 应交由上游处理", target)
		}
	}
	if lookups != 0 {
		t.Fatalf("上游路由触发网卡枚举 %d 次，期望 0 次", lookups)
	}
}

func TestPortalIPCacheRefresh(t *testing.T) {
	oldIP := net.ParseIP("192.0.2.1")
	newIP := net.ParseIP("192.0.2.2")
	addresses := []net.Addr{&net.IPNet{IP: oldIP}}
	var lookups int
	cache := &portalIPCache{readAddrs: func() ([]net.Addr, error) {
		lookups++
		return addresses, nil
	}}
	now := time.Now()
	if !cache.contains(oldIP, now) || lookups != 1 {
		t.Fatalf("首次读取应加载地址：枚举次数=%d，期望 1", lookups)
	}
	addresses = []net.Addr{&net.IPNet{IP: newIP}}
	beforeExpiry := now.Add(portalAddressTTL / 2)
	if !cache.contains(oldIP, beforeExpiry) || cache.contains(newIP, beforeExpiry) || lookups != 1 {
		t.Fatalf("有效期内应复用地址集合：枚举次数=%d，期望 1", lookups)
	}
	afterExpiry := now.Add(portalAddressTTL)
	if cache.contains(oldIP, afterExpiry) || !cache.contains(newIP, afterExpiry) || lookups != 2 {
		t.Fatalf("到期后应反映地址增删：枚举次数=%d，期望 2", lookups)
	}
}

func TestPortalIPCacheRetriesAfterFailure(t *testing.T) {
	ip := net.ParseIP("192.0.2.1")
	var lookups int
	var readErr error
	cache := &portalIPCache{readAddrs: func() ([]net.Addr, error) {
		lookups++
		return []net.Addr{&net.IPNet{IP: ip}}, readErr
	}}
	now := time.Now()
	if !cache.contains(ip, now) {
		t.Fatal("首次读取未识别本机地址")
	}
	readErr = errors.New("网卡枚举失败")
	failedAt := now.Add(portalAddressTTL)
	if cache.contains(ip, failedAt) || cache.contains(ip, failedAt) || lookups != 2 {
		t.Fatalf("枚举失败后应拒绝旧地址并缓存失败结果：枚举次数=%d，期望 2", lookups)
	}
	readErr = nil
	if !cache.contains(ip, failedAt.Add(portalAddressTTL)) || lookups != 3 {
		t.Fatalf("到期后应重试枚举：枚举次数=%d，期望 3", lookups)
	}
}

func TestPortalIPCacheConcurrentLookups(t *testing.T) {
	ip := net.ParseIP("192.0.2.1")
	var lookups atomic.Int32
	cache := &portalIPCache{readAddrs: func() ([]net.Addr, error) {
		lookups.Add(1)
		return []net.Addr{&net.IPNet{IP: ip}}, nil
	}}
	now := time.Now()
	start := make(chan struct{})
	var workers sync.WaitGroup
	for range 32 {
		workers.Go(func() {
			<-start
			if !cache.contains(ip, now) {
				t.Error("并发查询未识别本机地址")
			}
		})
	}
	close(start)
	done := make(chan struct{})
	go func() {
		workers.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("并发查询未按时结束")
	}
	if got := lookups.Load(); got != 1 {
		t.Fatalf("并发请求触发网卡枚举 %d 次，期望 1 次", got)
	}
}

func TestPortalResponses(t *testing.T) {
	for _, tc := range []struct {
		method, path, contentType string
		status                    int
	}{
		{http.MethodGet, "/", "text/html", http.StatusOK},
		{http.MethodHead, "/", "text/html", http.StatusOK},
		{http.MethodGet, "/sniffy-ca.crt", "application/x-x509-ca-cert", http.StatusOK},
		{http.MethodHead, "/sniffy-ca.crt", "application/x-x509-ca-cert", http.StatusOK},
		{http.MethodGet, "/sniffy.mobileconfig", "application/x-apple-aspen-config", http.StatusOK},
		{http.MethodHead, "/sniffy.mobileconfig", "application/x-apple-aspen-config", http.StatusOK},
		{http.MethodGet, "/favicon.ico", "text/plain", http.StatusNoContent},
		{http.MethodGet, "/sniffy-portal.js", "text/javascript", http.StatusOK},
		{http.MethodHead, "/sniffy-portal.js", "text/javascript", http.StatusOK},
		{http.MethodGet, "/missing", "text/plain", http.StatusNotFound},
		{http.MethodGet, "/sniffy-ca.crt/extra", "text/plain", http.StatusNotFound},
		{http.MethodGet, "/%73niffy-ca.crt", "text/plain", http.StatusNotFound},
		{http.MethodPost, "/", "text/plain", http.StatusMethodNotAllowed},
		{http.MethodDelete, "/sniffy-ca.crt", "text/plain", http.StatusMethodNotAllowed},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			response, body := portalExchange(t, tc.method, tc.path, "")
			if response.StatusCode != tc.status || !strings.HasPrefix(response.Header.Get("Content-Type"), tc.contentType) {
				t.Fatalf("status=%d type=%q，期望 %d %q", response.StatusCode, response.Header.Get("Content-Type"), tc.status, tc.contentType)
			}
			if tc.method == http.MethodHead && (len(body) != 0 || response.ContentLength <= 0) {
				t.Fatalf("HEAD 响应: body=%q length=%d", body, response.ContentLength)
			}
			if tc.status == http.StatusMethodNotAllowed && response.Header.Get("Allow") != "GET, HEAD" {
				t.Fatalf("Allow=%q", response.Header.Get("Allow"))
			}
			if !response.Close || response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("X-Content-Type-Options") != "nosniff" {
				t.Fatalf("入口响应头不完整: %+v", response)
			}
		})
	}
}

func TestPortalClosesWithUnreadBody(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		t.Run(method, func(t *testing.T) {
			portalExchange(t, method, "/", "Content-Length: 50\r\nExpect: 100-continue\r\n")
		})
	}
}

func TestPortalUnavailableCA(t *testing.T) {
	withGapCA(t, nil)
	response, body := portalExchange(t, http.MethodGet, "/", "")
	if response.StatusCode != http.StatusOK || !strings.Contains(body, "根证书暂不可用") || strings.Contains(body, `href="/sniffy-ca.crt"`) {
		t.Fatalf("证书不可用时页面未禁用下载: status=%d", response.StatusCode)
	}
	for _, path := range []string{"/sniffy-ca.crt", "/sniffy.mobileconfig"} {
		response, _ := portalExchange(t, http.MethodGet, path, "")
		if response.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("%s: status=%d，期望 503", path, response.StatusCode)
		}
	}
}

func TestPortalEscapesCertificateSubject(t *testing.T) {
	withGapCA(t, &gapCA{cert: &x509.Certificate{Subject: pkix.Name{CommonName: `<script>alert("CA")</script>`}}})
	_, body := portalExchange(t, http.MethodGet, "/", "")
	if strings.Contains(body, "<script>") || !strings.Contains(body, "&lt;script&gt;") {
		t.Fatal("证书名称未按 HTML 文本转义")
	}
}

func portalExchange(t *testing.T, method, path, headers string) (*http.Response, string) {
	t.Helper()
	raw := method + " " + path + " HTTP/1.1\r\nHost: localhost:8080\r\n" + headers + "\r\n"
	conn := newMockConn(raw)
	p := New(newMockConnection(conn, newMockServer())).(*Processor)
	if err := p.Process(); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(strings.NewReader(conn.WrittenData()))
	response, err := http.ReadResponse(reader, &http.Request{Method: method})
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if extra, _ := io.ReadAll(reader); len(extra) != 0 {
		t.Fatalf("响应后有多余字节: %q", extra)
	}
	if !p.closeAfterResponse {
		t.Fatal("入口响应未关闭连接")
	}
	return response, string(body)
}

func BenchmarkPortalRequestCheck(b *testing.B) {
	for _, target := range []string{"http://example.test/", "/"} {
		b.Run(target, func(b *testing.B) {
			request := httptest.NewRequest(http.MethodGet, target, nil)
			request.Host = "example.test"
			conn := &portalConn{mockConn: newMockConn(""), local: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8080}}
			p := New(newMockConnection(conn, newMockServer())).(*Processor)
			b.ReportAllocs()
			for b.Loop() {
				p.isPortalRequest(request)
			}
		})
	}
}

func BenchmarkProxyPortalCheck(b *testing.B) {
	local := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8080}
	client := &http.Client{Transport: &http.Transport{}}
	for _, target := range []string{
		"http://example.test/",
		"http://example.test/api",
		"http://example.test:8080/",
		"http://123.example.test:8080/",
		"https://example.test/",
	} {
		b.Run(target, func(b *testing.B) {
			request := httptest.NewRequest(http.MethodGet, target, nil)
			server := newMockServer()
			b.ReportAllocs()
			for b.Loop() {
				proxyPortalResponse(server, request, local, nil, client)
			}
		})
	}
}

func BenchmarkPortalWildcardHostCheck(b *testing.B) {
	server := &timeoutServer{mockServer: newMockServer(), cfg: &portalTestConfig{address: "0.0.0.0"}}
	local := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8080}
	for _, host := range []string{"example.test:8080", "127.0.0.2:8080", "192.0.2.254:8080"} {
		b.Run(host, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				isPortalHost(server, host, local)
			}
		})
	}
}

func BenchmarkProxyPortalWildcardCheck(b *testing.B) {
	server := &timeoutServer{mockServer: newMockServer(), cfg: &portalTestConfig{address: "0.0.0.0"}}
	local := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8080}
	request := httptest.NewRequest(http.MethodGet, "http://192.0.2.254:8080/", nil)
	upstream := &url.URL{Scheme: "http", Host: "upstream.test:8080"}
	for name, transport := range map[string]*http.Transport{
		"直连": {},
		"上游": {Proxy: http.ProxyURL(upstream)},
	} {
		b.Run(name, func(b *testing.B) {
			client := &http.Client{Transport: transport}
			b.ReportAllocs()
			for b.Loop() {
				proxyPortalResponse(server, request, local, nil, client)
			}
		})
	}
}

func TestPortalNetworks(t *testing.T) {
	candidates := []netinfo.LANAddr{
		{IP: "192.168.1.8", Label: "Wi-Fi", Interface: "wlan0"},
		{IP: "192.168.31.12", Label: "以太网", Interface: "eth0"},
		{IP: "10.8.0.2", Label: "VPN", Interface: "tun0"},
		{IP: "2001:db8::8", Label: "IPv6", Interface: "eth0"},
		{IP: "invalid"},
	}
	for _, tc := range []struct {
		name, listen, local, selected string
		count                         int
	}{
		{"全部网卡", "0.0.0.0", "127.0.0.1", "192.168.1.8", 3},
		{"当前访问网卡", "0.0.0.0", "192.168.31.12", "192.168.31.12", 3},
		{"VPN可以选择", "0.0.0.0", "10.8.0.2", "10.8.0.2", 3},
		{"指定网卡", "192.168.31.12", "192.168.31.12", "192.168.31.12", 1},
		{"回环监听", "127.0.0.1", "127.0.0.1", "127.0.0.1", 1},
		{"localhost监听", "localhost", "127.0.0.1", "127.0.0.1", 1},
		{"IPv6回环", "[::1]", "::1", "::1", 1},
		{"IPv6当前网络", "::", "2001:db8::8", "2001:db8::8", 4},
		{"IPv6指定网卡", "[2001:db8::8]", "2001:db8::8", "2001:db8::8", 1},
		{"当前地址未被枚举", "::", "2001:db8::9", "2001:db8::9", 5},
		{"默认通配监听", "", "127.0.0.1", "192.168.1.8", 4},
	} {
		networks, host := portalNetworks(tc.listen, net.ParseIP(tc.local), candidates)
		if len(networks) != tc.count {
			t.Fatalf("%s: 候选=%+v，期望 %d 个地址", tc.name, networks, tc.count)
		}
		var selected []string
		for _, network := range networks {
			if network.Selected {
				selected = append(selected, network.IP)
			}
		}
		if len(selected) != 1 || selected[0] != tc.selected || host != tc.selected {
			t.Fatalf("%s: 选中=%v，服务器地址=%s，期望 %s", tc.name, selected, host, tc.selected)
		}
	}
}

func TestPortalInstallationBoundary(t *testing.T) {
	var calls atomic.Int32
	SetPortalInstallHandler(func() { calls.Add(1) })
	t.Cleanup(func() { SetPortalInstallHandler(nil) })
	token := portalInstaller.Load().token
	local := &net.TCPAddr{IP: net.ParseIP("192.168.1.8"), Port: 8080}
	for _, tc := range []struct {
		name, method, path, peer, origin, site, token string
		status                                        int
	}{
		{"本机回环", "POST", "/sniffy/install-ca", "127.0.0.1", "http://192.168.1.8:8080", "same-origin", token, 202},
		{"本机网卡", "POST", "/sniffy/install-ca", "192.168.1.8", "http://192.168.1.8:8080", "same-origin", token, 202},
		{"远端设备", "POST", "/sniffy/install-ca", "192.168.1.9", "http://192.168.1.8:8080", "same-origin", token, 403},
		{"无能力令牌", "POST", "/sniffy/install-ca", "127.0.0.1", "http://192.168.1.8:8080", "same-origin", "", 403},
		{"错误能力令牌", "POST", "/sniffy/install-ca", "127.0.0.1", "http://192.168.1.8:8080", "same-origin", "wrong", 403},
		{"无来源", "POST", "/sniffy/install-ca", "127.0.0.1", "", "same-origin", token, 403},
		{"跨来源", "POST", "/sniffy/install-ca", "127.0.0.1", "http://example.test", "same-origin", token, 403},
		{"跨站请求", "POST", "/sniffy/install-ca", "127.0.0.1", "http://192.168.1.8:8080", "cross-site", token, 403},
		{"GET不触发安装", "GET", "/sniffy/install-ca", "127.0.0.1", "http://192.168.1.8:8080", "same-origin", token, 405},
		{"HEAD不触发安装", "HEAD", "/sniffy/install-ca", "127.0.0.1", "http://192.168.1.8:8080", "same-origin", token, 405},
		{"多余路径", "GET", "/sniffy/install-ca/extra", "127.0.0.1", "http://192.168.1.8:8080", "same-origin", token, 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(tc.method, "http://192.168.1.8:8080"+tc.path, nil)
			request.Header.Set("Origin", tc.origin)
			request.Header.Set("Sec-Fetch-Site", tc.site)
			request.Header.Set("X-Sniffy-Install", tc.token)
			before := calls.Load()
			response := portalResponse(request, local, &net.TCPAddr{IP: net.ParseIP(tc.peer)}, "0.0.0.0")
			defer response.Body.Close()
			if response.StatusCode != tc.status {
				t.Fatalf("status=%d，期望 %d", response.StatusCode, tc.status)
			}
			wantCalls := int32(0)
			if tc.status == http.StatusAccepted {
				wantCalls = 1
			}
			if got := calls.Load() - before; got != wantCalls {
				t.Fatalf("安装确认触发 %d 次，期望 %d", got, wantCalls)
			}
		})
	}
}

func TestPortalInstallAvailability(t *testing.T) {
	withGapCA(t, &gapCA{cert: &x509.Certificate{}})
	SetPortalInstallHandler(func() {})
	t.Cleanup(func() { SetPortalInstallHandler(nil) })
	local := &net.TCPAddr{IP: net.ParseIP("192.168.1.8"), Port: 8080}
	for _, tc := range []struct {
		name, peer string
		available  bool
	}{
		{"本机", "127.0.0.1", true},
		{"同机网卡", "192.168.1.8", true},
		{"手机", "192.168.1.9", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "http://192.168.1.8:8080/", nil)
			response := portalResponse(request, local, &net.TCPAddr{IP: net.ParseIP(tc.peer)}, "0.0.0.0")
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(body), portalInstaller.Load().token) != tc.available {
				t.Fatalf("本机安装入口可用性不符，期望 %v", tc.available)
			}
		})
	}
	SetPortalInstallHandler(nil)
	request := httptest.NewRequest(http.MethodPost, "http://192.168.1.8:8080/sniffy/install-ca", nil)
	response := portalResponse(request, local, local, "0.0.0.0")
	defer response.Body.Close()
	if response.StatusCode != http.StatusNotImplemented {
		t.Fatalf("无桌面接入时 status=%d，期望 501", response.StatusCode)
	}
}

func TestPortalQRCodeBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, host, local, listen string
		status                    int
	}{
		{"当前IPv4地址", "192.0.2.8", "192.0.2.8", "192.0.2.8", http.StatusOK},
		{"IPv4回环", "127.0.0.1", "192.0.2.8", "0.0.0.0", http.StatusBadRequest},
		{"域名", "example.test", "192.0.2.8", "0.0.0.0", http.StatusBadRequest},
		{"监听范围外", "192.0.2.9", "192.0.2.8", "192.0.2.8", http.StatusBadRequest},
		{"空地址", "", "192.0.2.8", "0.0.0.0", http.StatusBadRequest},
		{"当前IPv6地址", "2001:db8::8", "2001:db8::8", "::", http.StatusOK},
		{"IPv6回环", "::1", "::1", "::", http.StatusBadRequest},
		{"IPv4监听排除IPv6", "2001:db8::8", "192.0.2.8", "0.0.0.0", http.StatusBadRequest},
		{"IPv6其他地址", "2001:db8::9", "2001:db8::8", "::", http.StatusBadRequest},
		{"规范地址文本", "2001:0db8::8", "2001:db8::8", "::", http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			local := &net.TCPAddr{IP: net.ParseIP(tc.local), Port: 8080}
			target := "http://" + net.JoinHostPort(tc.local, "8080") + "/sniffy-qr.png?host=" + url.QueryEscape(tc.host)
			request := httptest.NewRequest(http.MethodGet, target, nil)
			response := portalResponse(request, local, local, tc.listen)
			defer response.Body.Close()
			if response.StatusCode != tc.status {
				t.Fatalf("status=%d，期望 %d", response.StatusCode, tc.status)
			}
			if tc.status != http.StatusOK {
				return
			}
			image, err := png.Decode(response.Body)
			if err != nil || image.Bounds().Dx() != 200 {
				t.Fatalf("二维码 PNG 无效: %v", err)
			}
		})
	}
}
