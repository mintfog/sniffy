// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package capture

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

// missingInterface 是一块不存在的网卡：绑定它的 socket 在建连前就失败，本机目标不绑定所以不受影响。
var missingInterface = &net.Interface{Index: 1 << 30, Name: "sniffy-none0"}

// dialExternalUDP 经监听器建一个指向外部地址的 UDP socket，不发包。
func dialExternalUDP(tl *TCPListener) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, err := tl.DialContext(ctx, "udp", "192.0.2.1:9")
	if err == nil {
		_ = conn.Close()
	}
	return err
}

func isBindError(err error) bool {
	return err != nil && strings.Contains(err.Error(), missingInterface.Name)
}

func TestBindOutbound(t *testing.T) {
	origin := startEchoOrigin(t)
	tl := newTestTCPListener(defaultTestConfig())
	if err := tl.BindOutbound(missingInterface); err != nil {
		t.Fatalf("BindOutbound: %v", err)
	}

	if err := dialExternalUDP(tl); !isBindError(err) {
		t.Fatalf("外部目标应被绑定到网卡，拨号结果: %v", err)
	}
	conn, err := tl.DialContext(context.Background(), "tcp", origin.Addr().String())
	if err != nil {
		t.Fatalf("绑定后本机服务应仍能拨通: %v", err)
	}
	_ = conn.Close()

	if err := tl.BindOutbound(nil); err != nil {
		t.Fatalf("取消绑定: %v", err)
	}
	if err := dialExternalUDP(tl); isBindError(err) {
		t.Fatalf("取消绑定后外部目标仍被绑定: %v", err)
	}
}

// BenchmarkDialContext 每轮经监听器新建一条到本机服务的 TCP 连接。
func BenchmarkDialContext(b *testing.B) {
	origin, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatal(err)
	}
	defer origin.Close()
	go func() {
		for {
			conn, err := origin.Accept()
			if err != nil {
				return
			}
			go func() {
				_, _ = conn.Read(make([]byte, 1))
				_ = conn.Close()
			}()
		}
	}()
	target := origin.Addr().String()

	for _, bc := range []struct {
		name  string
		iface *net.Interface
	}{
		{"未绑定", nil},
		{"绑定", missingInterface},
	} {
		b.Run(bc.name, func(b *testing.B) {
			tl := newTestTCPListener(defaultTestConfig())
			if err := tl.BindOutbound(bc.iface); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				conn, err := tl.DialContext(context.Background(), "tcp", target)
				if err != nil {
					b.Fatal(err)
				}
				// 以 RST 关闭，不留 TIME_WAIT：每轮新建一条连接，否则几万轮后临时端口耗尽。
				_ = conn.(*net.TCPConn).SetLinger(0)
				_ = conn.Close()
			}
		})
	}
}
