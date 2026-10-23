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
	// Windows 和 macOS 允许具体地址与通配地址共用端口，
	// 使用与代理相同的通配地址才能构造跨平台的绑定冲突。
	occupied, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
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
		t.Fatalf("%s 已占用通配监听地址，代理启动应报错", occupied.Addr())
	}
	if listener.IsRunning() {
		t.Fatal("绑定失败后代理不应处于运行状态")
	}
}
