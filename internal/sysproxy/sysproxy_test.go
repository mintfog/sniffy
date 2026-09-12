// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package sysproxy

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseNetworkServices(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name, raw string
		want      []string
	}{
		{
			name: "空输入",
			raw:  "",
			want: []string{},
		},
		{
			name: "仅表头",
			raw:  "An asterisk (*) denotes that a network service is disabled.\n",
			want: []string{},
		},
		{
			name: "跳过禁用服务",
			raw:  "说明\nWi-Fi\nEthernet\n*Bluetooth PAN\nThunderbolt Bridge\n",
			want: []string{"Wi-Fi", "Ethernet", "Thunderbolt Bridge"},
		},
		{
			name: "全部禁用",
			raw:  "说明\n*Wi-Fi\n  *Ethernet  \n",
			want: []string{},
		},
		{
			name: "空白与CRLF",
			raw:  "说明\r\n \t Wi-Fi \r\n\r\n\tEthernet\t\r\n",
			want: []string{"Wi-Fi", "Ethernet"},
		},
		{
			name: "保留名称与顺序",
			raw:  "说明\n办公网络 USB 10/100/1000\nVPN's $(name); * 内网\nWi-Fi",
			want: []string{"办公网络 USB 10/100/1000", "VPN's $(name); * 内网", "Wi-Fi"},
		},
		{
			name: "保留重复服务",
			raw:  "说明\nWi-Fi\nWi-Fi",
			want: []string{"Wi-Fi", "Wi-Fi"},
		},
		{
			name: "首行固定为表头",
			raw:  "Wi-Fi\nEthernet",
			want: []string{"Ethernet"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, parseNetworkServices(tt.raw))
		})
	}
}

func TestParseGetWebProxy(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name, raw    string
		enabled      bool
		server, port string
	}{
		{
			name:    "已启用",
			raw:     "Enabled: Yes\nServer: 127.0.0.1\nPort: 8080\nAuthenticated Proxy Enabled: 0\n",
			enabled: true,
			server:  "127.0.0.1",
			port:    "8080",
		},
		{
			name:    "已停用仍保留地址",
			raw:     "Enabled: No\nServer: proxy.example\nPort: 3128",
			enabled: false,
			server:  "proxy.example",
			port:    "3128",
		},
		{
			name:    "空输入",
			raw:     "",
			enabled: false,
			server:  "",
			port:    "",
		},
		{
			name:    "缺失状态",
			raw:     "Server: localhost\nPort: 8080",
			enabled: false,
			server:  "localhost",
			port:    "8080",
		},
		{
			name:    "空字段",
			raw:     "Enabled:\nServer:\nPort:",
			enabled: false,
			server:  "",
			port:    "",
		},
		{
			name:    "乱序空白与大小写",
			raw:     " Port : 443 \r\n Server : ::1 \r\n Enabled : yEs \r\n",
			enabled: true,
			server:  "::1",
			port:    "443",
		},
		{
			name:    "IPv6保留冒号",
			raw:     "Enabled: YES\nServer: 2001:db8::1\nPort: 65535",
			enabled: true,
			server:  "2001:db8::1",
			port:    "65535",
		},
		{
			name:    "忽略未知及畸形行",
			raw:     "Enabled Yes\nUnknown: Yes\nserver: ignored\n: ignored\nPort: not-a-number",
			enabled: false,
			server:  "",
			port:    "not-a-number",
		},
		{
			name:    "非Yes不启用",
			raw:     "Enabled: true\nServer: localhost\nPort: 0",
			enabled: false,
			server:  "localhost",
			port:    "0",
		},
		{
			name:    "重复字段取最后值",
			raw:     "Enabled: Yes\nServer: old\nPort: 80\nEnabled: No\nServer: new\nPort: 81",
			enabled: false,
			server:  "new",
			port:    "81",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			enabled, server, port := parseGetWebProxy(tt.raw)
			assert.Equal(t, tt.enabled, enabled)
			assert.Equal(t, tt.server, server)
			assert.Equal(t, tt.port, port)
		})
	}
}

func FuzzParseNetworkServices(f *testing.F) {
	for _, seed := range []string{"", "说明\nWi-Fi\n*Ethernet\n", "说明\r\n办公网络\r\n", "\n\x00\n"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		got := parseNetworkServices(raw)
		for _, name := range got {
			if name == "" || name != strings.TrimSpace(name) || strings.HasPrefix(name, "*") {
				t.Fatalf("解析产生无效服务名 %q", name)
			}
		}
		assert.Equal(t, got, parseNetworkServices("说明\n"+strings.Join(got, "\n")))
	})
}

func FuzzParseGetWebProxy(f *testing.F) {
	f.Add("Enabled: Yes\nServer: ::1\nPort: 8080\n")
	f.Add("")
	f.Add("Enabled: No\r\nServer: proxy.example\r\nPort: 0")
	f.Fuzz(func(t *testing.T, raw string) {
		enabled, server, port := parseGetWebProxy(raw)
		state := "No"
		if enabled {
			state = "Yes"
		}
		gotEnabled, gotServer, gotPort := parseGetWebProxy("Enabled: " + state + "\nServer: " + server + "\nPort: " + port)
		assert.Equal(t, enabled, gotEnabled)
		assert.Equal(t, server, gotServer)
		assert.Equal(t, port, gotPort)
	})
}
