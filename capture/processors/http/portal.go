// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package http

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	_ "embed"
	"fmt"
	"html/template"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mintfog/sniffy/ca"
	"github.com/mintfog/sniffy/capture/types"
	"github.com/mintfog/sniffy/internal/netinfo"
	qrcode "github.com/skip2/go-qrcode"
)

//go:embed portal.html
var portalHTML string

//go:embed portal.js
var portalJS string

var portalTemplate = template.Must(template.New("portal").Parse(portalHTML))

type portalData struct {
	Host, Port                   string
	Subject, Fingerprint, Expiry string
	Available, AuthEnabled       bool
	Networks                     []portalNetwork
	ShareURL                     string
	Platform                     string
	InstallToken                 string
	InstallPlatform              string
}

type portalNetwork struct {
	IP, Label, Interface string
	Current, Selected    bool
}

type portalInstallHandler struct {
	request func()
	token   string
}

var portalInstaller atomic.Pointer[portalInstallHandler]

const portalAddressTTL = time.Second

// 网卡枚举较慢，TTL 内复用地址集合，并串行刷新以免并发请求重复枚举。
type portalIPCache struct {
	mu        sync.Mutex
	expires   time.Time
	addresses map[netip.Addr]struct{}
	readAddrs func() ([]net.Addr, error)
}

var portalLocalIPs = &portalIPCache{readAddrs: net.InterfaceAddrs}

func (c *portalIPCache) contains(ip net.IP, now time.Time) bool {
	target, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !now.Before(c.expires) {
		addresses, err := c.readAddrs()
		c.expires = now.Add(portalAddressTTL)
		c.addresses = nil
		if err != nil {
			return false
		}
		c.addresses = make(map[netip.Addr]struct{}, len(addresses))
		for _, address := range addresses {
			network, ok := address.(*net.IPNet)
			if !ok {
				continue
			}
			if ip, ok := netip.AddrFromSlice(network.IP); ok {
				c.addresses[ip.Unmap()] = struct{}{}
			}
		}
	}
	_, ok = c.addresses[target.Unmap()]
	return ok
}

// SetPortalInstallHandler 接入桌面端安装确认入口；nil 表示当前进程不提供本机安装。
func SetPortalInstallHandler(handler func()) {
	if handler == nil {
		portalInstaller.Store(nil)
		return
	}
	portalInstaller.Store(&portalInstallHandler{request: handler, token: rand.Text()})
}

// 直开入口允许在认证前访问；代理形式的 URL 仍走代理认证、插件和路由判断。
func (p *Processor) isPortalRequest(request *http.Request) bool {
	if request.URL.IsAbs() || request.URL.Host != "" || p.proxyTunnel || p.isHttps ||
		request.Method == http.MethodConnect || !strings.HasPrefix(request.RequestURI, "/") || request.Header.Get("Upgrade") != "" {
		return false
	}
	conn := p.conn.GetConn()
	if conn == nil {
		return false
	}
	return isPortalHost(p.conn.GetServer(), request.Host, conn.LocalAddr())
}

func isPortalHost(server types.Server, host string, addr net.Addr) bool {
	local, ok := addr.(*net.TCPAddr)
	if !ok {
		return false
	}
	target := url.URL{Host: host}
	port := target.Port()
	if port == "" {
		port = "80"
	}
	targetPort, err := strconv.Atoi(port)
	if err != nil || targetPort != local.Port {
		return false
	}
	host = target.Hostname()
	localhost := strings.EqualFold(strings.TrimSuffix(host, "."), "localhost")
	if localhost && local.IP.IsLoopback() {
		return true
	}
	// 普通域名不参与入口 IP 匹配，避免 net.ParseIP 为它们分配解析错误。
	if !localhost && !strings.Contains(host, ":") {
		for i := range len(host) {
			if c := host[i]; c != '.' && (c < '0' || c > '9') {
				return false
			}
		}
	}
	ip := net.ParseIP(host)
	if !localhost && (ip == nil || ip.IsUnspecified()) {
		return false
	}
	if ip != nil && ip.Equal(local.IP) {
		return true
	}
	config := server.GetConfig()
	if config == nil {
		return false
	}
	listen := config.GetAddress()
	bound := net.ParseIP(strings.Trim(listen, "[]"))
	if listen != "" && (bound == nil || !bound.IsUnspecified()) {
		return false
	}
	if localhost {
		return true
	}
	if bound != nil && bound.To4() != nil && ip.To4() == nil {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	return portalLocalIPs.contains(ip, time.Now())
}

func proxyPortalResponse(server types.Server, request *http.Request, addr, remote net.Addr, client *http.Client) *http.Response {
	if request.URL.Scheme != "http" {
		return nil
	}
	switch request.URL.EscapedPath() {
	case "/", "/sniffy-ca.crt", "/sniffy.mobileconfig", "/sniffy-portal.js", "/sniffy-qr.png", "/favicon.ico":
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			return nil
		}
	case "/sniffy/install-ca":
	default:
		return nil
	}
	// 上游代理的回环地址属于上游机器，仍须交给它处理。
	if request.Header.Get("Upgrade") != "" || !clientRoutesDirectly(client, request) || !isPortalHost(server, request.URL.Host, addr) {
		return nil
	}
	local := addr.(*net.TCPAddr)
	return portalResponse(request, local, remote, portalListenAddress(server, local))
}

func (p *Processor) servePortal(server types.Server, request *http.Request) error {
	// 直接结束连接，避免未读的请求体被当作下一条请求。
	p.closeAfterResponse = true
	p.armWriteDeadline(server)
	conn := p.conn.GetConn()
	local := conn.LocalAddr().(*net.TCPAddr)
	response := portalResponse(request, local, conn.RemoteAddr(), portalListenAddress(server, local))
	writer := p.conn.GetWriter()
	if err := response.Write(writer); err != nil {
		return err
	}
	return writer.Flush()
}

func portalListenAddress(server types.Server, local *net.TCPAddr) string {
	if config := server.GetConfig(); config != nil {
		return config.GetAddress()
	}
	return local.IP.String()
}

func portalResponse(request *http.Request, local *net.TCPAddr, remote net.Addr, listen string) *http.Response {
	header := http.Header{
		"Connection":              {"close"},
		"Content-Type":            {"text/plain; charset=utf-8"},
		"Cache-Control":           {"no-store"},
		"X-Content-Type-Options":  {"nosniff"},
		"Content-Security-Policy": {"default-src 'none'; style-src 'unsafe-inline'; script-src 'self'; img-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"},
		"Referrer-Policy":         {"no-referrer"},
	}
	status, body := portalContent(request, local, remote, listen, header)
	return &http.Response{
		StatusCode:    status,
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        header,
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
		Close:         true,
		Request:       request,
	}
}

func portalContent(request *http.Request, local *net.TCPAddr, remote net.Addr, listen string, header http.Header) (int, []byte) {
	path := request.URL.EscapedPath()
	if path == "/sniffy/install-ca" {
		return requestPortalInstall(request, local, remote, header)
	}
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		header.Set("Allow", "GET, HEAD")
		return http.StatusMethodNotAllowed, []byte("请使用 GET 或 HEAD 访问连接指南。\n")
	}
	switch path {
	case "/":
		status, body := portalPage(request, local, remote, listen)
		if status == http.StatusOK {
			header.Set("Content-Type", "text/html; charset=utf-8")
		}
		return status, body
	case "/sniffy-portal.js":
		header.Set("Content-Type", "text/javascript; charset=utf-8")
		return http.StatusOK, []byte(portalJS)
	case "/sniffy-qr.png":
		status, body := portalQRCode(request.URL.Query().Get("host"), local, listen)
		if status == http.StatusOK {
			header.Set("Content-Type", "image/png")
		}
		return status, body
	case "/sniffy-ca.crt", "/sniffy.mobileconfig":
		root := currentCA()
		if root == nil || root.GetCA() == nil {
			return http.StatusServiceUnavailable, []byte("根证书暂不可用，请在 Sniffy 中检查证书后重试。\n")
		}
		if path == "/sniffy.mobileconfig" {
			header.Set("Content-Type", "application/x-apple-aspen-config")
			header.Set("Content-Disposition", `attachment; filename="sniffy.mobileconfig"`)
			return http.StatusOK, ca.Mobileconfig(root.GetCA())
		}
		header.Set("Content-Type", "application/x-x509-ca-cert")
		header.Set("Content-Disposition", `attachment; filename="sniffy-ca.crt"`)
		return http.StatusOK, ca.ExportRootCertPEM(root)
	case "/favicon.ico":
		return http.StatusNoContent, nil
	default:
		return http.StatusNotFound, []byte("页面不存在，请访问 / 打开 Sniffy 连接指南。\n")
	}
}

func portalPage(request *http.Request, local *net.TCPAddr, remote net.Addr, listen string) (int, []byte) {
	data := portalData{
		Port:     strconv.Itoa(local.Port),
		Platform: portalPlatform(request.UserAgent()),
	}
	data.Networks, data.Host = portalNetworks(listen, local.IP, netinfo.LANIPs())
	if !net.ParseIP(data.Host).IsLoopback() {
		data.ShareURL = "http://" + net.JoinHostPort(data.Host, data.Port) + "/"
	}
	if installer := portalInstaller.Load(); installer != nil && portalLocalClient(local, remote) {
		data.InstallToken = installer.token
		data.InstallPlatform = runtime.GOOS
	}
	if auth := listenerProxyAuth.Load(); auth != nil {
		data.AuthEnabled = auth.enabled
	}
	if root := currentCA(); root != nil {
		if cert := root.GetCA(); cert != nil {
			data.Available = true
			data.Subject = cert.Subject.CommonName
			data.Fingerprint = fmt.Sprintf("%X", sha256.Sum256(cert.Raw))
			data.Expiry = cert.NotAfter.UTC().Format("2006-01-02 15:04 UTC")
		}
	}
	var body bytes.Buffer
	if err := portalTemplate.Execute(&body, data); err != nil {
		return http.StatusInternalServerError, []byte("连接指南暂时无法显示，请稍后重试。\n")
	}
	return http.StatusOK, body.Bytes()
}

func portalQRCode(host string, local *net.TCPAddr, listen string) (int, []byte) {
	ip := net.ParseIP(host)
	if ip == nil || ip.IsLoopback() {
		return http.StatusBadRequest, []byte("请选择当前代理可用的网络地址。\n")
	}
	valid := host == local.IP.String()
	if !valid && portalAddressAllowed(listen, local.IP, ip) {
		for _, candidate := range netinfo.LANAddresses() {
			if candidate.IP == host {
				valid = true
				break
			}
		}
	}
	if !valid {
		return http.StatusBadRequest, []byte("请选择当前代理可用的网络地址。\n")
	}
	address := "http://" + net.JoinHostPort(host, strconv.Itoa(local.Port)) + "/"
	image, err := qrcode.Encode(address, qrcode.Medium, 200)
	if err != nil {
		return http.StatusInternalServerError, []byte("二维码暂不可用，请直接输入地址打开。\n")
	}
	return http.StatusOK, image
}

func portalAddressAllowed(listen string, local, ip net.IP) bool {
	if ip == nil {
		return false
	}
	bound := net.ParseIP(strings.Trim(listen, "[]"))
	wildcard := listen == "" || (bound != nil && bound.IsUnspecified())
	ipv4Only := bound != nil && bound.To4() != nil
	return (!ipv4Only || ip.To4() != nil) && (wildcard || ip.Equal(local))
}

func portalNetworks(listen string, local net.IP, candidates []netinfo.LANAddr) ([]portalNetwork, string) {
	var networks []portalNetwork
	selected := -1
	for _, candidate := range candidates {
		ip := net.ParseIP(candidate.IP)
		if !portalAddressAllowed(listen, local, ip) {
			continue
		}
		current := ip.Equal(local)
		if current {
			selected = len(networks)
		}
		networks = append(networks, portalNetwork{IP: candidate.IP, Label: candidate.Label, Interface: candidate.Interface, Current: current})
	}
	if selected < 0 && (!local.IsLoopback() || len(networks) == 0) {
		label := "当前网络"
		if local.IsLoopback() {
			label = "仅本机访问"
		}
		selected = len(networks)
		networks = append(networks, portalNetwork{IP: local.String(), Label: label, Current: true})
	}
	if selected < 0 {
		selected = 0
	}
	networks[selected].Selected = true
	return networks, networks[selected].IP
}

func portalPlatform(agent string) string {
	switch {
	case strings.Contains(agent, "iPhone"), strings.Contains(agent, "iPad"), strings.Contains(agent, "iPod"):
		return "ios"
	case strings.Contains(agent, "Android"):
		return "android"
	case strings.Contains(agent, "Macintosh"):
		return "macos"
	case strings.Contains(agent, "Linux"):
		return "linux"
	default:
		return "windows"
	}
}

func portalLocalClient(local *net.TCPAddr, remote net.Addr) bool {
	peer, ok := remote.(*net.TCPAddr)
	return ok && (peer.IP.IsLoopback() || peer.IP.Equal(local.IP))
}

func requestPortalInstall(request *http.Request, local *net.TCPAddr, remote net.Addr, header http.Header) (int, []byte) {
	if request.Method != http.MethodPost {
		header.Set("Allow", http.MethodPost)
		return http.StatusMethodNotAllowed, []byte("请通过安装按钮发起请求。\n")
	}
	installer := portalInstaller.Load()
	if installer == nil {
		return http.StatusNotImplemented, []byte("请在 Sniffy 桌面端安装证书，或下载后手动安装。\n")
	}
	origin, err := url.Parse(request.Header.Get("Origin"))
	sameOrigin := err == nil && origin.Scheme == "http" && strings.EqualFold(origin.Host, request.Host) &&
		origin.User == nil && origin.Path == "" && origin.RawQuery == "" && origin.Fragment == ""
	site := request.Header.Get("Sec-Fetch-Site")
	validToken := subtle.ConstantTimeCompare([]byte(request.Header.Get("X-Sniffy-Install")), []byte(installer.token)) == 1
	if !portalLocalClient(local, remote) || !sameOrigin || (site != "" && site != "same-origin") || !validToken {
		return http.StatusForbidden, []byte("请在运行 Sniffy 的电脑上打开连接指南并点击安装。\n")
	}
	installer.request()
	return http.StatusAccepted, []byte("已打开 Sniffy，请在客户端确认安装。\n")
}
