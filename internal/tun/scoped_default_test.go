// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package tun

import (
	"fmt"
	"net/netip"
	"slices"
	"testing"

	"github.com/mintfog/sniffy/internal/netbind"
)

// fakeScoped 是内存里各网卡名下的默认路由，记下 addScoped / deleteScoped 的调用。
type fakeScoped struct {
	via [2]netbind.Route
	// routes 按地址族存放各网卡名下的默认路由，值表示是不是 addScoped 加的。
	routes [2]map[int]bool
	ops    []string
}

func newFakeScoped() *fakeScoped {
	return &fakeScoped{routes: [2]map[int]bool{{}, {}}}
}

func (r *fakeScoped) defaultRoute(ipv6 bool) (netbind.Route, error) {
	return r.via[familyIndex(ipv6)], nil
}

func (r *fakeScoped) addScoped(ipv6 bool, via netbind.Route) error {
	r.ops = append(r.ops, fmt.Sprintf("add %s if%d via %s", familyName(ipv6), via.Index, via.Gateway))
	routes := r.routes[familyIndex(ipv6)]
	if _, ok := routes[via.Index]; ok {
		return errRouteExists
	}
	routes[via.Index] = true
	return nil
}

func (r *fakeScoped) ownsScoped(ipv6 bool, index int) (bool, error) {
	return r.routes[familyIndex(ipv6)][index], nil
}

func (r *fakeScoped) deleteScoped(ipv6 bool, index int) error {
	r.ops = append(r.ops, fmt.Sprintf("delete %s if%d", familyName(ipv6), index))
	routes := r.routes[familyIndex(ipv6)]
	if _, ok := routes[index]; !ok {
		return errRouteNotFound
	}
	delete(routes, index)
	return nil
}

func (r *fakeScoped) takeOps() []string {
	ops := r.ops
	r.ops = nil
	return ops
}

func familyName(ipv6 bool) string {
	if ipv6 {
		return "v6"
	}
	return "v4"
}

// 出口网卡名下没有默认路由时补上，随默认路由的网关、网卡变化先删后补，被系统删掉的重新补上；网卡名下
// 已有系统配的不补也不删；没有默认路由时保留原有的；停止接管与关闭时删掉补上的。
func TestScopedDefault(t *testing.T) {
	sys := newFakeScoped()
	sys.via[0] = route4(2, "192.168.1.1")
	sys.routes[0][3] = false
	s := &scopedDefault{sys: sys}
	step := func(name string, err error, want ...string) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := sys.takeOps(); !slices.Equal(got, want) {
			t.Errorf("%s: 操作 %q，期望 %q", name, got, want)
		}
	}

	step("开启", s.install(false), "add v4 if2 via 192.168.1.1")
	step("默认路由没变", s.refresh(), "add v4 if2 via 192.168.1.1")
	sys.via[0] = route4(2, "192.168.1.254")
	step("网关变了", s.refresh(), "delete v4 if2", "add v4 if2 via 192.168.1.254")
	delete(sys.routes[0], 2)
	step("被系统删掉", s.refresh(), "add v4 if2 via 192.168.1.254")
	sys.via[0] = netbind.Route{}
	step("默认路由消失", s.refresh())
	if !sys.routes[0][2] {
		t.Error("默认路由消失时应保留补上的路由")
	}
	sys.via[0] = route4(3, "10.0.0.1")
	step("换到系统已配了路由的网卡", s.refresh(), "delete v4 if2", "add v4 if3 via 10.0.0.1")
	sys.via[0] = route4(2, "192.168.1.254")
	step("换回原网卡", s.refresh(), "add v4 if2 via 192.168.1.254")
	if owned, ok := sys.routes[0][3]; !ok || owned {
		t.Error("网卡名下系统配的默认路由不能删")
	}

	gw6 := netip.MustParseAddr("fe80::1")
	sys.via[1] = netbind.Route{Index: 2, Gateway: gw6}
	step("开始接管 IPv6", s.install(true), "add v6 if2 via fe80::1")
	step("停止接管 IPv6", s.remove(true), "delete v6 if2")
	step("不接管的地址族不维护", s.refresh(), "add v4 if2 via 192.168.1.254")

	step("关闭", s.close(), "delete v4 if2")
	if _, ok := sys.routes[0][2]; ok {
		t.Error("关闭后补上的路由还在")
	}
	if _, ok := sys.routes[0][3]; !ok {
		t.Error("关闭时删了系统配的路由")
	}
}

// 开启时出口网卡名下已有系统配的默认路由（非主网卡，或 VPN 接管了默认路由）：不补，关闭时也不删。
func TestScopedDefaultAlreadyPresent(t *testing.T) {
	sys := newFakeScoped()
	sys.via[0] = route4(2, "192.168.1.1")
	sys.routes[0][2] = false
	s := &scopedDefault{sys: sys}
	if err := s.install(false); err != nil {
		t.Fatal(err)
	}
	if err := s.close(); err != nil {
		t.Fatal(err)
	}
	if ops := sys.takeOps(); !slices.Equal(ops, []string{"add v4 if2 via 192.168.1.1"}) {
		t.Errorf("操作 %q，期望只尝试补一次", ops)
	}
	if owned, ok := sys.routes[0][2]; !ok || owned {
		t.Error("系统配的默认路由应原样保留")
	}
}
