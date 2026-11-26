// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package tun

import (
	"errors"
	"net"
	"net/netip"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/mintfog/sniffy/internal/netbind"
)

// 装上指向虚拟网卡的路由后，绑定出口网卡的出站照样选得到出口网卡的路由，靠的是开启时补上的出口网卡
// 名下的默认路由：删掉它就选不到路由，RefreshRoutes 补回，关闭后删除。需要 root，在有 IPv4 默认路由、
// 且出口网卡名下本来没有这种路由（主网卡通常如此）的 Mac 上运行。
func TestBoundEgressRoutesAroundTun(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("需要 root 权限")
	}
	useDocumentationRoutes(t)
	// 绑定网卡的出站在出口网卡名下查不到路由时，内核退回按 0.0.0.0 查不限网卡的路由，网卡对得上才用。
	// 0.0.0.0/8 让这一步像装着 /1 时那样落到虚拟网卡上，又不接管实际流量。
	routes4 = append(routes4, netip.MustParsePrefix("0.0.0.0/8"))
	egress, err := netbind.DefaultEgress()
	if err != nil {
		t.Fatal(err)
	}
	if egress.IPv4 == nil {
		t.Skip("没有 IPv4 默认路由")
	}
	if hasScopedDefault(t, egress.IPv4.Index) {
		t.Skipf("%s 名下已有系统配的默认路由，用不上补的路由", egress.IPv4.Name)
	}
	d, err := Start(Options{
		Handler:   func(conn net.Conn) { conn.Close() },
		ListenUDP: func(string) (PacketConn, error) { return nil, errors.New("未提供") },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	// UDP 的 connect 只选路、不发包，选不到路由时立即返回 ENETUNREACH。
	dst := netip.MustParseAddrPort("203.0.113.7:9")
	if src, err := udpSource(&net.Dialer{}, dst); err != nil || src != addr4.Addr() {
		t.Fatalf("不绑定网卡时发往 %s 的源地址为 %s（%v），路由没进虚拟网卡", dst, src, err)
	}
	egress.IPv6 = nil
	egress.Tunnel = Prefixes()
	bound := netbind.Dialer(egress, time.Second)
	expectRouted := func(when string) {
		t.Helper()
		src, err := udpSource(bound, dst)
		if err != nil || addr4.Contains(src) {
			t.Fatalf("%s：绑定 %s 后发往 %s 的源地址为 %s（%v），应经出口网卡", when, egress.IPv4.Name, dst, src, err)
		}
	}
	expectRouted("开启后")

	if err := (systemRoutes{}).deleteScoped(false, egress.IPv4.Index); err != nil {
		t.Fatal(err)
	}
	if _, err := udpSource(bound, dst); !errors.Is(err, syscall.ENETUNREACH) {
		t.Fatalf("删掉补上的路由后绑定的出站返回 %v，期望 ENETUNREACH：测试没能复现装着 /1 时的选路", err)
	}
	if err := d.RefreshRoutes(); err != nil {
		t.Fatal(err)
	}
	expectRouted("RefreshRoutes 后")

	d.Close()
	if hasScopedDefault(t, egress.IPv4.Index) {
		t.Errorf("Close 后 %s 名下补上的默认路由还在", egress.IPv4.Name)
	}
}

// hasScopedDefault 报告网卡 index 名下有没有限定该网卡的 IPv4 默认路由，不论是谁装的。
func hasScopedDefault(t *testing.T, index int) bool {
	t.Helper()
	scoped, err := scopedDefaults(false)
	if err != nil {
		t.Fatal(err)
	}
	_, ok := scoped[index]
	return ok
}

// udpSource 用 dialer 向 dst 建 UDP「连接」，返回内核选中的源地址。
func udpSource(dialer *net.Dialer, dst netip.AddrPort) (netip.Addr, error) {
	conn, err := dialer.Dial("udp4", dst.String())
	if err != nil {
		return netip.Addr{}, err
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).AddrPort().Addr().Unmap(), nil
}
