// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build linux

package process

import (
	"net"
	"os"
	"testing"
	"time"
)

// TestGetProcessByConnectionResolvesSelf 验证快路径能据本地 socket 定位到本进程,
// 并且明显快于历史上的全量扫描(回归保护:防止退回 O(连接数×进程数) 的慢路径)。
func TestGetProcessByConnectionResolvesSelf(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close()
		}
	}()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	time.Sleep(150 * time.Millisecond) // 等待进入 ESTABLISHED

	d, err := NewDetector()
	if err != nil {
		t.Fatalf("new detector: %v", err)
	}

	// 客户端 socket:本地=我们的临时端口(conn.LocalAddr),远端=监听端口(conn.RemoteAddr)。
	start := time.Now()
	pi, err := d.GetProcessByConnection(conn.LocalAddr(), conn.RemoteAddr())
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("GetProcessByConnection: %v", err)
	}
	if pi == nil {
		t.Fatal("expected process info, got nil")
	}
	if pi.PID != uint32(os.Getpid()) {
		t.Errorf("PID = %d, want %d (%q)", pi.PID, os.Getpid(), pi.Name)
	}
	if elapsed > 2*time.Second {
		t.Errorf("lookup took %v, expected fast path well under 2s", elapsed)
	}
}
