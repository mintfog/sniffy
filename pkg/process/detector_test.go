// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package process

import (
	"net"
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
