// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package process

import (
	"net"
	"net/netip"
	"strings"
	"testing"
)

type stringAddr string

func (a stringAddr) Network() string { return "test" }
func (a stringAddr) String() string  { return string(a) }

func TestFormatAddr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		addr net.Addr
		want string
	}{
		{name: "nil", want: ""},
		{name: "TCP", addr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8080}, want: "127.0.0.1:8080"},
		{name: "自定义地址", addr: stringAddr("socket"), want: "socket"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := FormatAddr(tt.addr); got != tt.want {
				t.Fatalf("FormatAddr() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseTCPAddr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		wantIP   string
		wantPort int
		wantErr  bool
	}{
		{name: "IPv4", input: "127.0.0.1:8080", wantIP: "127.0.0.1", wantPort: 8080},
		{name: "IPv6", input: "[::1]:443", wantIP: "::1", wantPort: 443},
		{name: "缺少端口", input: "127.0.0.1", wantErr: true},
		{name: "非法端口", input: "127.0.0.1:http-not-a-service", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseTCPAddr(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseTCPAddr(%q) unexpectedly succeeded: %v", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseTCPAddr(%q): %v", tt.input, err)
			}
			if got.IP.String() != tt.wantIP || got.Port != tt.wantPort {
				t.Fatalf("ParseTCPAddr(%q) = %v, want %s:%d", tt.input, got, tt.wantIP, tt.wantPort)
			}
		})
	}
}

func TestPortOf(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		addr net.Addr
		want int
	}{
		{name: "nil", want: -1},
		{name: "TCP", addr: &net.TCPAddr{Port: 65535}, want: 65535},
		{name: "字符串IPv4", addr: stringAddr("127.0.0.1:1234"), want: 1234},
		{name: "字符串IPv6", addr: stringAddr("[2001:db8::1]:8443"), want: 8443},
		{name: "缺少端口", addr: stringAddr("127.0.0.1"), want: -1},
		{name: "非法端口", addr: stringAddr("127.0.0.1:none"), want: -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := portOf(tt.addr); got != tt.want {
				t.Fatalf("portOf(%v) = %d, want %d", tt.addr, got, tt.want)
			}
		})
	}
}

func FuzzPortOf(f *testing.F) {
	for _, seed := range []string{"127.0.0.1:80", "[::1]:443", "", ":", "host:not-a-port"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		got := portOf(stringAddr(value))
		ap, err := netip.ParseAddrPort(value)
		if err != nil {
			return
		}
		// netip 与 net.SplitHostPort 对 zone 内分隔符的接受范围不同，
		// 一致性校验限定在两者共同支持的地址格式内。
		if strings.ContainsAny(ap.Addr().Zone(), "[]%:") {
			return
		}
		// 以结构化地址的端口为参照，检测字符串地址解析失败或端口提取错误。
		if fast := portOf(net.TCPAddrFromAddrPort(ap)); fast != got {
			t.Fatalf("portOf(%q): 字符串路径 = %d，*net.TCPAddr 路径 = %d", value, got, fast)
		}
	})
}
