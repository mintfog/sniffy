// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package main

import (
	"net"
	"testing"

	"github.com/mintfog/sniffy/capture"
)

func TestConfigWildcardAddress(t *testing.T) {
	for _, address := range []string{"", "*", "0.0.0.0", "::"} {
		t.Run(address, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Address = address
			if err := cfg.Validate(); err != nil {
				t.Fatal(err)
			}
			if cfg.GetAddress() != "" || cfg.GetListenAddress() != ":8080" {
				t.Fatalf("监听地址=%q，期望 :8080", cfg.GetListenAddress())
			}
		})
	}
	for _, address := range []string{"127.0.0.1", "127.0.1.1", "::1", "192.168.1.10", "localhost"} {
		t.Run(address, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Address = address
			if err := cfg.Validate(); err == nil {
				t.Fatalf("具体监听地址 %q 应被拒绝", address)
			}
		})
	}
}

func TestProxyWildcardRejectsOccupiedPort(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "127.0.1.1", "::1"} {
		t.Run(host, func(t *testing.T) {
			occupied, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
			if err != nil {
				t.Skipf("当前环境无法监听 %s: %v", host, err)
			}
			defer occupied.Close()
			cfg := DefaultConfig()
			cfg.Port = occupied.Addr().(*net.TCPAddr).Port
			if err := cfg.Validate(); err != nil {
				t.Fatal(err)
			}
			listener := capture.NewTCPListener(cfg)
			t.Cleanup(func() { _ = listener.Stop() })
			if err := listener.Start(); err == nil {
				t.Fatalf("%s 已占用端口，代理启动应报错", occupied.Addr())
			}
		})
	}
}
