// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build desktop

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/mintfog/sniffy/ca"
	"github.com/mintfog/sniffy/capture/processors/http/websocket"
	"github.com/mintfog/sniffy/internal/app"
	"github.com/mintfog/sniffy/internal/desktop"
	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/internal/platform"
	"github.com/mintfog/sniffy/internal/service"
	"github.com/wailsapp/wails/v3/pkg/application"
)

type demoSessions struct {
	*service.Service
	client flow.ProcessInfo
}

func (s *demoSessions) RecordFlowStarted(f *flow.Flow) {
	f.SetProcess(&s.client)
	s.Service.RecordFlowStarted(f)
}

func (s *demoSessions) RecordWSSession(session *flow.WSSession, added *flow.WSMessage) {
	session.Process = &s.client
	s.Service.RecordWSSession(session, added)
}

func (s *demoSessions) RecordStreamSession(session *flow.StreamSession, added *flow.StreamMessage) {
	session.Process = &s.client
	s.Service.RecordStreamSession(session, added)
}

func main() {
	assets := flag.String("assets", "web/dist", "前端构建目录")
	proxyPort := flag.Int("proxy-port", 8080, "Sniffy 代理端口")
	browserPort := flag.Int("browser-port", 18180, "Wails 浏览器端口")
	upstream := flag.String("upstream", "127.0.0.1:18443", "演示域名的上游代理地址")
	origin := flag.String("origin", "127.0.0.1:18444", "演示 HTTPS 源站地址")
	state := flag.String("state", "", "新建的演示配置目录")
	flag.Parse()

	if err := run(*assets, *upstream, *origin, *state, *proxyPort, *browserPort); err != nil {
		log.Fatal(err)
	}
}

func run(assets, upstream, origin, state string, proxyPort, browserPort int) error {
	if state == "" {
		return fmt.Errorf("请为演示实例指定新的 -state 目录")
	}
	// macOS 可能允许 IPv4 回环监听与已有通配监听共存，不能只依赖后续 bind 报错。
	for _, host := range []string{"127.0.0.1", "::1"} {
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, fmt.Sprint(proxyPort)), 300*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return fmt.Errorf("Sniffy 代理端口 %d 已被占用", proxyPort)
		}
	}
	if err := os.Mkdir(state, 0o700); err != nil {
		return err
	}
	for _, name := range []string{"HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "APPDATA", "LOCALAPPDATA"} {
		if err := os.Setenv(name, state); err != nil {
			return err
		}
	}

	configDir, err := platform.ConfigDir()
	if err != nil {
		return err
	}
	config, err := json.Marshal(map[string]any{
		"port": proxyPort, "enableHTTPS": true, "recording": true,
		"autoSystemProxy": false, "systemProxy": false, "updateCheck": false,
		"upstream": true, "upstreamAddr": "http://" + upstream,
		"tlsInsecureHosts": []string{"api.shop.test", "cdn.shop.test", "live.shop.test", "ai.shop.test", "inventory.shop.test"},
	})
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.json"), config, 0o600); err != nil {
		return err
	}

	cfg := app.DefaultConfig()
	cfg.Address, cfg.Port = "127.0.0.1", proxyPort
	runtime, err := app.Build(cfg, false)
	if err != nil {
		return err
	}
	runtime.Engine.SetProcessResolver(nil)
	sessions := &demoSessions{Service: runtime.Service, client: flow.ProcessInfo{Name: "Shop Desktop"}}
	runtime.Engine.SetFlowSink(sessions)
	runtime.Engine.SetStreamSink(sessions)
	// WebSocket 的保真握手走独立拨号通道，保留原 Host 和 TLS SNI。
	websocket.SetDialContext(func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "live.shop.test:443" {
			return nil, fmt.Errorf("演示代理不转发 WebSocket 目标 %s", address)
		}
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, origin)
	})
	if err := runtime.Start(); err != nil {
		return err
	}
	defer runtime.Stop()

	wails := application.New(application.Options{
		Name:   "Sniffy README",
		Assets: application.AssetOptions{Handler: application.AssetFileServerFS(os.DirFS(assets))},
		Server: application.ServerOptions{Host: "127.0.0.1", Port: browserPort},
		Mac:    application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: true},
	})
	bridge := desktop.New(runtime)
	// desktop.New 注入的是真实的系统代理回调，必须在它之后覆盖：否则启动对齐时会清理录制机上指向同一端口的系统代理。
	runtime.Service.SetSystemProxyApplier(func(bool) error { return nil })
	runtime.Service.SetStaleSystemProxyCleaner(func() {})
	wails.RegisterService(application.NewService(bridge))
	if err := os.WriteFile(filepath.Join(state, "sniffy-ca.pem"), ca.ExportRootCertPEM(runtime.Engine.CA()), 0o600); err != nil {
		return err
	}

	window := application.WebviewWindowOptions{
		Name: "main", Title: "Sniffy README", Width: 1440, Height: 900,
		URL: "/?theme=dark", BackgroundColour: application.NewRGB(17, 17, 23),
	}
	desktop.ApplyPlatformChrome(&window)
	wails.Window.NewWithOptions(window)
	log.Printf("演示代理监听 %s:%d", cfg.Address, proxyPort)
	return wails.Run()
}
