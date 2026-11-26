// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package tun

import (
	"errors"
	"fmt"

	"github.com/mintfog/sniffy/internal/netbind"
)

// scopedSystem 是 scopedDefault 对系统路由表的操作，由 macOS 实现。
type scopedSystem interface {
	defaultRoute(ipv6 bool) (netbind.Route, error)
	// addScoped 给 via 所在的网卡加一条经 via、限定该网卡的带标记的默认路由，该网卡名下已有默认
	// 路由时返回 errRouteExists。
	addScoped(ipv6 bool, via netbind.Route) error
	// ownsScoped 报告网卡 index 名下有没有 addScoped 加的默认路由。
	ownsScoped(ipv6 bool, index int) (bool, error)
	// deleteScoped 删除网卡 index 名下限定该网卡的默认路由，不在了返回 errRouteNotFound。
	deleteScoped(ipv6 bool, index int) error
}

// scopedDefault 给出口网卡（默认路由所在的网卡）补上它名下的默认路由，即 macOS 上带 RTF_IFSCOPE 的
// 默认路由。
//
// sniffy 的出站与 UDP 中继用 IP_BOUND_IF 绑定出口网卡，内核按该网卡名下的路由选路：不限网卡的查询
// 命中别的网卡上的路由时结果作废，改查该网卡名下的；再查不到，就对 0.0.0.0 取不限网卡的路由兜底，
// 网卡对得上才用。系统通常只给非主网卡配这种默认路由，主网卡平时靠兜底的那一步；装上覆盖 0.0.0.0 的
// /1 路由后，兜底落到虚拟网卡上，绑定的出站发往局域网以外一律 ENETUNREACH。
//
// 网卡名下已有默认路由的（非主网卡，或 VPN 接管默认路由后系统给原网卡配的）不补也不删。补上的不随
// 虚拟网卡消失：关闭时删除，进程被强杀后的残留下次开启时按标记清掉。方法由调用方串行调用。
type scopedDefault struct {
	sys scopedSystem
	// 以下各项按地址族存放，[0] 为 IPv4、[1] 为 IPv6。
	// active 表示该地址族正被虚拟网卡接管。
	active [2]bool
	// via 是上次补路由时所经的默认路由，网卡名下已有路由、不必补的同样记下；没补过时为零值。
	via [2]netbind.Route
}

// install 开始给 IPv6（ipv6 为 true）或 IPv4 的出口网卡补默认路由，按当前的默认路由补上。该地址族
// 没有默认路由时先不补，refresh 时补上。
func (s *scopedDefault) install(ipv6 bool) error {
	f := familyIndex(ipv6)
	s.active[f] = true
	via, err := s.sys.defaultRoute(ipv6)
	if err != nil {
		return err
	}
	return s.sync(f, via)
}

// remove 删除该地址族补上的路由并停止维护。
func (s *scopedDefault) remove(ipv6 bool) error {
	f := familyIndex(ipv6)
	s.active[f] = false
	return s.del(f)
}

// refresh 让补上的路由跟上当前的默认路由：默认路由的网卡或网关变了的地址族先删后补，没变的补上缺的
// （如随网卡断开被系统删掉的）。某个地址族没有默认路由时保留原有的，理由同 bypassRouter.refresh。
func (s *scopedDefault) refresh() error {
	var errs []error
	for f, ipv6 := range []bool{false, true} {
		if !s.active[f] {
			continue
		}
		via, err := s.sys.defaultRoute(ipv6)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if via.Index == 0 {
			continue
		}
		if via != s.via[f] {
			errs = append(errs, s.del(f))
		}
		errs = append(errs, s.sync(f, via))
	}
	return errors.Join(errs...)
}

// close 删除两个地址族补上的路由。
func (s *scopedDefault) close() error {
	return errors.Join(s.remove(false), s.remove(true))
}

// sync 给 via 所在的网卡补上经 via 的默认路由，网卡名下已有的跳过。
func (s *scopedDefault) sync(f int, via netbind.Route) error {
	if via.Index == 0 {
		return nil
	}
	s.via[f] = via
	if err := s.sys.addScoped(f == 1, via); err != nil && !errors.Is(err, errRouteExists) {
		return fmt.Errorf("给出口网卡补默认路由: %w", err)
	}
	return nil
}

// del 删除该地址族补上的路由。已不是本程序加的（随网卡删掉了，或被系统换成了自己的）不删；删不掉的
// 留到下次开启时按标记清掉。
func (s *scopedDefault) del(f int) error {
	via := s.via[f]
	s.via[f] = netbind.Route{}
	if via.Index == 0 {
		return nil
	}
	owned, err := s.sys.ownsScoped(f == 1, via.Index)
	if err != nil || !owned {
		return err
	}
	if err := s.sys.deleteScoped(f == 1, via.Index); err != nil && !errors.Is(err, errRouteNotFound) {
		return fmt.Errorf("删除出口网卡名下的默认路由: %w", err)
	}
	return nil
}
