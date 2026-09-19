// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package process

import (
	"net"
	"strconv"
	"testing"
)

func TestConnectionAddrPort(t *testing.T) {
	tests := []struct {
		name string
		addr net.Addr
		want string
	}{
		{name: "IPv4", addr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8080}, want: "127.0.0.1:8080"},
		{name: "映射IPv4", addr: stringAddr("[::ffff:127.0.0.1]:8080"), want: "127.0.0.1:8080"},
		{name: "IPv6", addr: stringAddr("[::1]:443"), want: "[::1]:443"},
		{name: "IPv6 scope", addr: stringAddr("[fe80::1%012]:443"), want: "[fe80::1%12]:443"},
		{name: "IPv6零scope", addr: stringAddr("[::1%0]:443"), want: "[::1]:443"},
		{name: "nil"},
		{name: "类型化nil", addr: (*net.TCPAddr)(nil)},
		{name: "缺少IP", addr: &net.TCPAddr{Port: 8080}},
		{name: "端口为零", addr: stringAddr("127.0.0.1:0")},
		{name: "负端口", addr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: -1}},
		{name: "端口溢出", addr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 65536}},
		{name: "无效地址", addr: stringAddr("host.invalid:80")},
		{name: "无效scope", addr: stringAddr("[fe80::1%sniffy-nonexistent-interface]:443")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := connectionAddrPort(tt.addr)
			wantErr := tt.want == ""
			if (err != nil) != wantErr {
				t.Fatalf("connectionAddrPort(%v) = (%v, %v)，期望错误: %t", tt.addr, got, err, wantErr)
			}
			if tt.want == "" {
				return
			}
			if got.String() != tt.want {
				t.Fatalf("connectionAddrPort(%v) = (%v, %v)，期望 %s", tt.addr, got, err, tt.want)
			}
		})
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	for _, iface := range interfaces {
		addr := &net.TCPAddr{IP: net.ParseIP("fe80::1"), Port: 443, Zone: iface.Name}
		got, err := connectionAddrPort(addr)
		if err != nil || got.Addr().Zone() != strconv.Itoa(iface.Index) {
			t.Fatalf("接口名称 %q 转换 scope = (%v, %v)", iface.Name, got, err)
		}
	}
}
