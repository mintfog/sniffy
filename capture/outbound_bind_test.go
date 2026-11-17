// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package capture

import (
	"context"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/mintfog/sniffy/internal/netbind"
)

// missingInterface 是一块不存在的网卡：绑定它的 socket 在建连前就失败，本机目标不绑定所以不受影响。
var missingInterface = &net.Interface{Index: 1 << 30, Name: "sniffy-none0"}

// missingEgress 把外部目标都绑定到 missingInterface：它把本机发往外部地址时选中的源地址当作
// 虚拟网卡的地址，外部目标因此都算「本来会进虚拟网卡」。
func missingEgress(tb testing.TB) netbind.Egress {
	tb.Helper()
	conn, err := net.Dial("udp4", "192.0.2.1:9")
	if err != nil {
		tb.Skipf("没有到外部地址的路由: %v", err)
	}
	src := conn.LocalAddr().(*net.UDPAddr).AddrPort().Addr().Unmap()
	_ = conn.Close()
	return netbind.Egress{
		IPv4:   missingInterface,
		IPv6:   missingInterface,
		Tunnel: []netip.Prefix{netip.PrefixFrom(src, 32)},
	}
}

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
	tl.BindOutbound(missingEgress(t))

	if err := dialExternalUDP(tl); !isBindError(err) {
		t.Fatalf("外部目标应被绑定到网卡，拨号结果: %v", err)
	}
	conn, err := tl.DialContext(context.Background(), "tcp", origin.Addr().String())
	if err != nil {
		t.Fatalf("绑定后本机服务应仍能拨通: %v", err)
	}
	_ = conn.Close()

	tl.BindOutbound(netbind.Egress{})
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
		name   string
		egress netbind.Egress
	}{
		{"未绑定", netbind.Egress{}},
		{"绑定", missingEgress(b)},
	} {
		b.Run(bc.name, func(b *testing.B) {
			tl := newTestTCPListener(defaultTestConfig())
			tl.BindOutbound(bc.egress)
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
