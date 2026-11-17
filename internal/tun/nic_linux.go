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

	"golang.org/x/sys/unix"
)

const (
	ifaceName = "sniffy0"

	tunOffloads = unix.TUN_F_CSUM | unix.TUN_F_TSO4 | unix.TUN_F_TSO6

	// routeTable 是放 /1 路由的路由表，rulePriority 是引用它的策略规则的优先级，见 policyRules。
	// 两者都取 0x736e（"sn"），避开常见软件用的表号与优先级。
	routeTable   = 0x736e
	rulePriority = 0x736e
)

func openNIC(cfg nicConfig) (*nic, error) {
	fd, err := openTUN(ifaceName)
	if err != nil {
		return nil, err
	}
	cleanup, err := configureNIC(ifaceName, cfg)
	if err != nil {
		unix.Close(fd)
		return nil, err
	}
	return &nic{name: ifaceName, link: newVnetEndpoint(fd, cfg.mtu, cfg.onDown), cleanup: cleanup}, nil
}

// openTUN 创建名为 name 的 TUN 设备并返回非阻塞的 fd。设备不是持久的，fd 关闭即销毁。
func openTUN(name string) (int, error) {
	fd, err := unix.Open("/dev/net/tun", unix.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, fmt.Errorf("打开 /dev/net/tun: %w", err)
	}
	ifr, err := unix.NewIfreq(name)
	if err != nil {
		unix.Close(fd)
		return -1, err
	}
	ifr.SetUint16(unix.IFF_TUN | unix.IFF_NO_PI | unix.IFF_VNET_HDR)
	if err := unix.IoctlIfreq(fd, unix.TUNSETIFF, ifr); err != nil {
		unix.Close(fd)
		return -1, fmt.Errorf("创建网卡 %s: %w", name, err)
	}
	if err := unix.IoctlSetInt(fd, unix.TUNSETOFFLOAD, tunOffloads); err != nil {
		unix.Close(fd)
		return -1, fmt.Errorf("开启分段与校验和卸载: %w", err)
	}
	if err := unix.SetNonblock(fd, true); err != nil {
		unix.Close(fd)
		return -1, err
	}
	return fd, nil
}

// configureNIC 配置网卡的地址、路由与策略规则，返回撤销策略规则的函数。地址与路由随网卡
// 销毁而消失，策略规则不会。
func configureNIC(name string, cfg nicConfig) (cleanup func(), err error) {
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return nil, err
	}
	nl, err := dialNetlink()
	if err != nil {
		return nil, err
	}
	defer nl.close()

	if err := nl.addAddr(iface.Index, addr4); err != nil {
		return nil, fmt.Errorf("配置地址 %s: %w", addr4, err)
	}
	ipv6 := cfg.ipv6
	if ipv6 {
		switch err := nl.addAddr(iface.Index, addr6); {
		case isIPv6Unavailable(err):
			ipv6 = false
		case err != nil:
			return nil, fmt.Errorf("配置地址 %s: %w", addr6, err)
		}
	}
	if err := nl.setLinkUp(iface.Index, cfg.mtu); err != nil {
		return nil, fmt.Errorf("启用网卡: %w", err)
	}
	routes, rules := routes4, policyRules(addr4)
	if ipv6 {
		routes = append(routes[:len(routes):len(routes)], routes6...)
		rules = append(rules, policyRules(addr6)...)
	}
	for _, route := range routes {
		if err := nl.addRoute(iface.Index, route, routeTable); err != nil {
			return nil, fmt.Errorf("添加路由 %s: %w", route, err)
		}
	}

	// 进程被强杀时上次的规则留了下来，先清掉。两个地址族都清：上次可能开了这次没开的那个。
	nl.deleteRules(append(policyRules(addr4), policyRules(addr6)...))
	for _, rule := range rules {
		if err := nl.addRule(rule); err != nil {
			nl.deleteRules(rules)
			return nil, fmt.Errorf("添加策略路由规则: %w", err)
		}
	}
	return func() { removeRules(rules) }, nil
}

// policyRules 返回 own 所在地址族的策略规则，按优先级依次是：
//
//  1. 查 main 表但不用其中的默认路由：局域网等更具体的路由照旧生效，不进虚拟网卡
//  2. 应用新建的连接查 routeTable。内核先不带源地址选一次路，按结果选定源地址（虚拟网卡的
//     地址）后再带着它选一次，两次都要落到 routeTable：IPv4 用「源地址为 0.0.0.0」与「源地址
//     属于虚拟网卡」两条规则分别接住；IPv6 带源地址条件的规则不匹配不带源地址的选路，改用一条
//     findSrc 规则，由内核查表后按选出的源地址判定
//
// /1 路由因此不放 main 表：在 main 表里它们对一切发往公网的包生效，而选路只看目标地址，本机
// 作为服务端回给远程客户端的包、开启前已建立的连接也会被引进虚拟网卡，这些连接随即中断。
// 它们的源地址是物理网卡的地址，不匹配规则 2，照旧走 main 表。
//
// 规则不像路由那样随网卡消失。进程被强杀时它们会留下，但 routeTable 已随网卡清空，规则 1 也
// 只是把 main 表多查一遍，不改变选路结果；下次开启时清掉。
func policyRules(own netip.Prefix) []policyRule {
	mainExceptDefault := policyRule{priority: rulePriority, table: unix.RT_TABLE_MAIN, suppressDefault: true}
	ownSrc := policyRule{priority: rulePriority + 1, table: routeTable, src: own.Masked()}
	if own.Addr().Is6() {
		mainExceptDefault.family, ownSrc.family, ownSrc.findSrc = unix.AF_INET6, unix.AF_INET6, true
		return []policyRule{mainExceptDefault, ownSrc}
	}
	mainExceptDefault.family, ownSrc.family = unix.AF_INET, unix.AF_INET
	noSrc := ownSrc
	noSrc.src = netip.PrefixFrom(netip.IPv4Unspecified(), 32)
	return []policyRule{mainExceptDefault, noSrc, ownSrc}
}

func removeRules(rules []policyRule) {
	nl, err := dialNetlink()
	if err != nil {
		return
	}
	defer nl.close()
	nl.deleteRules(rules)
}

// isIPv6Unavailable 判断添加 IPv6 地址的错误是否源于宿主系统没有启用 IPv6：内核未编入
// IPv6 时报协议族不支持，被 disable_ipv6 关闭时报权限不足。
func isIPv6Unavailable(err error) bool {
	return errors.Is(err, unix.EAFNOSUPPORT) || errors.Is(err, unix.EOPNOTSUPP) ||
		errors.Is(err, unix.EACCES) || errors.Is(err, unix.EPERM)
}
