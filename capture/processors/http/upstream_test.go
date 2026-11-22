// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package http

import (
	"net"
	"net/url"
	"testing"
)

func TestUpstreamProxyLocal(t *testing.T) {
	hosts := map[string]bool{
		"127.0.0.1:10809":    true,
		"127.8.9.10:1080":    true,
		"[::1]:7890":         true,
		"0.0.0.0:7890":       true,
		"[::ffff:127.0.0.1]": true,
		"localhost:7890":     true,
		"LocalHost.:7890":    true,
		"proxy.localhost":    true,
		"203.0.113.7:3128":   false,
		"[2001:db8::7]:3128": false,
		"proxy.example:3128": false,
		"192.0.2.10:8080":    false,
	}
	// 本机网卡上的地址（如局域网 IP）同样是本机。
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if ipNet, ok := a.(*net.IPNet); ok && !ipNet.IP.IsLoopback() && ipNet.IP.To4() != nil {
				hosts[net.JoinHostPort(ipNet.IP.String(), "10809")] = true
				break
			}
		}
	}
	for host, local := range hosts {
		p := NewUpstreamProxy(&url.URL{Scheme: "http", Host: host})
		if p.Local() != local {
			t.Errorf("%s：Local = %t，期望 %t", host, p.Local(), local)
		}
	}
}

func TestUpstreamProxyFor(t *testing.T) {
	if got := (*UpstreamProxy)(nil).For(true); got != nil {
		t.Fatalf("未配置上游时 For = %v", got)
	}
	remote := NewUpstreamProxy(&url.URL{Scheme: "http", Host: "203.0.113.7:3128"})
	local := NewUpstreamProxy(&url.URL{Scheme: "http", Host: "127.0.0.1:10809"})
	for _, tc := range []struct {
		name        string
		proxy       *UpstreamProxy
		transparent bool
		via         bool
	}{
		{"远端上游 · 代理模式", remote, false, true},
		{"远端上游 · 透明连接", remote, true, true},
		{"本机上游 · 代理模式", local, false, true},
		{"本机上游 · 透明连接", local, true, false},
	} {
		if got := tc.proxy.For(tc.transparent); (got != nil) != tc.via {
			t.Errorf("%s：For = %v，期望经上游 %t", tc.name, got, tc.via)
		}
	}

	restore := TreatUpstreamAsRemote()
	forced := NewUpstreamProxy(&url.URL{Scheme: "http", Host: "127.0.0.1:10809"})
	restore()
	if forced.Local() || !NewUpstreamProxy(&url.URL{Scheme: "http", Host: "127.0.0.1:10809"}).Local() {
		t.Fatal("TreatUpstreamAsRemote 只应影响生效期间设置的上游")
	}
}

func TestUpstreamProxyURLIsCopy(t *testing.T) {
	source := &url.URL{Scheme: "http", Host: "127.0.0.1:10809"}
	p := NewUpstreamProxy(source)
	source.Host = "203.0.113.7:3128"
	got := p.URL()
	got.Host = "198.51.100.1:1"
	if p.URL().Host != "127.0.0.1:10809" || !p.Local() {
		t.Fatalf("UpstreamProxy 不应受调用方改动影响：%v", p.URL())
	}
}
