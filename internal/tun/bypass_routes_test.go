// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package tun

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/mintfog/sniffy/internal/netbind"
)

// fakeRoutes 是内存里的路由表，记下 add / delete 的调用。
type fakeRoutes struct {
	via     [2]netbind.Route
	own     map[netip.Prefix]netbind.Route
	taken   map[netip.Prefix]bool
	scanErr error
	ops     []string
}

func newFakeRoutes() *fakeRoutes {
	return &fakeRoutes{own: map[netip.Prefix]netbind.Route{}, taken: map[netip.Prefix]bool{}}
}

func (r *fakeRoutes) defaultRoute(ipv6 bool) (netbind.Route, error) {
	return r.via[familyIndex(ipv6)], nil
}

func (r *fakeRoutes) scan(ipv6 bool) (routeScan, error) {
	if r.scanErr != nil {
		return routeScan{}, r.scanErr
	}
	s := routeScan{own: map[netip.Prefix]bool{}, taken: map[netip.Prefix]bool{}}
	for p := range r.own {
		if p.Addr().Is6() == ipv6 {
			s.own[p] = true
		}
	}
	for p := range r.taken {
		if p.Addr().Is6() == ipv6 {
			s.taken[p] = true
		}
	}
	return s, nil
}

func (r *fakeRoutes) add(dst netip.Prefix, via netbind.Route) error {
	r.ops = append(r.ops, fmt.Sprintf("add %s via %s", dst, via.Gateway))
	if _, ok := r.own[dst]; ok || r.taken[dst] {
		return errRouteExists
	}
	r.own[dst] = via
	return nil
}

func (r *fakeRoutes) delete(dst netip.Prefix, via netbind.Route) error {
	r.ops = append(r.ops, fmt.Sprintf("delete %s via %s", dst, via.Gateway))
	if got, ok := r.own[dst]; !ok || got != via {
		return errRouteNotFound
	}
	delete(r.own, dst)
	return nil
}

func (r *fakeRoutes) takeOps() []string {
	ops := r.ops
	r.ops = nil
	return ops
}

func route4(index int, gw string) netbind.Route {
	return netbind.Route{Index: index, Gateway: netip.MustParseAddr(gw)}
}

func TestBypassRouter(t *testing.T) {
	a := netip.MustParsePrefix("10.0.0.0/8")
	b := netip.MustParsePrefix("172.16.0.0/12")
	vpn := netip.MustParsePrefix("192.168.100.0/24")
	v6 := netip.MustParsePrefix("2001:db8::/32")
	sys := newFakeRoutes()
	sys.via[0] = route4(2, "192.168.1.1")
	sys.taken[vpn] = true
	r := newBypassRouter(sys, []netip.Prefix{a, b, vpn, v6})

	expectOps := func(step string, want ...string) {
		t.Helper()
		if got := sys.takeOps(); !slices.Equal(got, want) {
			t.Errorf("%s: 调用 %q，期望 %q", step, got, want)
		}
	}

	// 已有别的路由的网段不加；IPv6 没有默认路由时先不装。
	if err := errors.Join(r.install(false), r.install(true)); err != nil {
		t.Fatal(err)
	}
	expectOps("开启", "add 10.0.0.0/8 via 192.168.1.1", "add 172.16.0.0/12 via 192.168.1.1")

	// 默认路由没变：装着的不动，只补上被系统删掉的。
	delete(sys.own, b)
	if err := r.refresh(); err != nil {
		t.Fatal(err)
	}
	expectOps("默认路由没变", "add 172.16.0.0/12 via 192.168.1.1")
	if err := r.refresh(); err != nil {
		t.Fatal(err)
	}
	expectOps("什么都没缺")

	// 网关变了：先删后加；IPv6 默认路由出现后补装。
	sys.via[0] = route4(2, "192.168.1.254")
	sys.via[1] = netbind.Route{Index: 2, Gateway: netip.MustParseAddr("fe80::1")}
	if err := r.refresh(); err != nil {
		t.Fatal(err)
	}
	expectOps("网关变了",
		"delete 10.0.0.0/8 via 192.168.1.1", "delete 172.16.0.0/12 via 192.168.1.1",
		"add 10.0.0.0/8 via 192.168.1.254", "add 172.16.0.0/12 via 192.168.1.254",
		"add 2001:db8::/32 via fe80::1")

	// 默认路由消失时保留原有的。
	sys.via[0] = netbind.Route{}
	if err := r.refresh(); err != nil {
		t.Fatal(err)
	}
	expectOps("默认路由消失")

	// 被 VPN 改写成不带标记的不删。
	delete(sys.own, a)
	sys.taken[a] = true
	if err := r.close(); err != nil {
		t.Fatal(err)
	}
	expectOps("关闭", "delete 172.16.0.0/12 via 192.168.1.254", "delete 2001:db8::/32 via fe80::1")
	if len(sys.own) != 0 {
		t.Errorf("关闭后还剩 %v", sys.own)
	}
}

// 读不到路由表时不删（分不清哪些还是自己的），留到下次开启时按标记清掉；再删时什么都不做。
func TestBypassRouterScanError(t *testing.T) {
	sys := newFakeRoutes()
	sys.via[0] = route4(2, "192.168.1.1")
	r := newBypassRouter(sys, []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")})
	if err := r.install(false); err != nil {
		t.Fatal(err)
	}
	sys.takeOps()
	sys.scanErr = errors.New("读不到")
	if err := r.remove(false); !errors.Is(err, sys.scanErr) {
		t.Fatalf("remove = %v，期望读路由表的错误", err)
	}
	sys.scanErr = nil
	if err := r.remove(false); err != nil || len(sys.takeOps()) != 0 {
		t.Fatalf("再次 remove = %v，不应再删", err)
	}
}

// Start 在开启网卡之前经 CheckBypass 拒绝不合要求的绕过列表，helper 收到的 start 消息也经此把关。
func TestCheckBypass(t *testing.T) {
	ok := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("64.0.0.0/2"), netip.MustParsePrefix("2001:db8::1/128")}
	if err := CheckBypass(ok); err != nil {
		t.Errorf("CheckBypass(%v) = %v", ok, err)
	}
	for _, bad := range []netip.Prefix{
		{},
		netip.MustParsePrefix("::ffff:10.0.0.0/104"),
		netip.MustParsePrefix("10.1.2.3/8"),
		netip.MustParsePrefix("0.0.0.0/1"),
	} {
		if err := CheckBypass([]netip.Prefix{bad}); err == nil {
			t.Errorf("CheckBypass(%s) 应报错", bad)
		}
	}
	tooMany := make([]netip.Prefix, MaxBypass+1)
	for i := range tooMany {
		tooMany[i] = netip.PrefixFrom(netip.AddrFrom4([4]byte{10, byte(i >> 8), byte(i), 0}), 24)
	}
	if err := CheckBypass(tooMany); err == nil {
		t.Errorf("%d 项应超过上限", len(tooMany))
	}
	opts := Options{Bypass: tooMany, Handler: func(net.Conn) {}, ListenUDP: func(string) (PacketConn, error) { return nil, nil }}
	if d, err := Start(opts); err == nil || !strings.Contains(err.Error(), "绕过列表") {
		if d != nil {
			d.Close()
		}
		t.Errorf("Start 收到超限的绕过列表: %v，应以绕过列表为由失败", err)
	}
}
