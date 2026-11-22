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
	// 两者都取 0x736e（"sn"），避开常见软件用的表号与优先级。bypassPriority 是绕过列表的规则的
	// 优先级，见 bypassRules。
	routeTable     = 0x736e
	rulePriority   = 0x736e
	bypassPriority = rulePriority - 1
)

func openNIC(cfg nicConfig) (*nic, error) {
	fd, err := openTUN(ifaceName)
	if err != nil {
		return nil, err
	}
	n, err := configureNIC(ifaceName, cfg)
	if err != nil {
		unix.Close(fd)
		return nil, err
	}
	n.link = newVnetEndpoint(fd, cfg.mtu, cfg.onDown)
	return n, nil
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

// configureNIC 配置网卡的地址、路由与策略规则，返回尚未挂上链路层的 nic。地址与路由随网卡
// 销毁而消失，策略规则不会，由 nic.cleanup 撤销。
func configureNIC(name string, cfg nicConfig) (*nic, error) {
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
	if err := nl.setLinkUp(iface.Index, cfg.mtu); err != nil {
		return nil, fmt.Errorf("启用网卡: %w", err)
	}
	for _, route := range routes4 {
		if err := nl.addRoute(iface.Index, route, routeTable); err != nil {
			return nil, fmt.Errorf("添加路由 %s: %w", route, err)
		}
	}
	// 进程被强杀时上次的规则留了下来，先清掉。两个地址族都清：上次可能开了这次没开的那个。
	nl.deleteRules(allRules())
	// 绕过列表的规则先于引用 routeTable 的规则装上，其间发往绕过网段的连接不会进虚拟网卡。
	if err := nl.addRules(append(bypassRules(cfg.bypass, false), policyRules(addr4)...)); err != nil {
		return nil, err
	}
	n := &nic{name: name, cleanup: removeRules, setIPv6: func(on bool) (bool, error) { return setIPv6(iface.Index, cfg.bypass, on) }}
	if cfg.ipv6 {
		if n.ipv6, err = setIPv6(iface.Index, cfg.bypass, true); err != nil {
			removeRules()
			return nil, err
		}
	}
	return n, nil
}

// setIPv6 给网卡 index 配上或撤掉 IPv6 的地址、路由与策略规则（含 bypass 里 IPv6 网段的规则），
// 语义同 nic.setIPv6。
func setIPv6(index int, bypass []netip.Prefix, on bool) (bool, error) {
	nl, err := dialNetlink()
	if err != nil {
		// 什么都还没改，接管状态不变。
		return !on, err
	}
	defer nl.close()
	if !on {
		err := nl.removeIPv6(index)
		return err != nil, err
	}
	switch err := nl.addAddr(index, addr6); {
	case isIPv6Unavailable(err):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("配置地址 %s: %w", addr6, err)
	}
	for _, route := range routes6 {
		if err := nl.addRoute(index, route, routeTable); err != nil {
			_ = nl.removeIPv6(index)
			return false, fmt.Errorf("添加路由 %s: %w", route, err)
		}
	}
	if err := nl.addRules(append(bypassRules(bypass, true), policyRules(addr6)...)); err != nil {
		_ = nl.removeIPv6(index)
		return false, err
	}
	return true, nil
}

// removeIPv6 撤掉网卡 index 的 IPv6 策略规则、路由与地址，不存在的跳过。先撤规则：此后新的选路
// 不再查 routeTable。
func (c *netlinkConn) removeIPv6(index int) error {
	c.deleteRules(append(policyRules(addr6), anyBypassRule(unix.AF_INET6)))
	var errs []error
	for _, route := range routes6 {
		if err := c.delRoute(index, route, routeTable); err != nil && !errors.Is(err, unix.ESRCH) {
			errs = append(errs, fmt.Errorf("删除路由 %s: %w", route, err))
		}
	}
	if err := c.delAddr(index, addr6); err != nil && !errors.Is(err, unix.EADDRNOTAVAIL) {
		errs = append(errs, fmt.Errorf("删除地址 %s: %w", addr6, err))
	}
	return errors.Join(errs...)
}

// addRules 依次添加 rules，有一条失败就把它们全部删掉。
func (c *netlinkConn) addRules(rules []policyRule) error {
	for _, rule := range rules {
		if err := c.addRule(rule); err != nil {
			c.deleteRules(rules)
			return fmt.Errorf("添加策略路由规则: %w", err)
		}
	}
	return nil
}

// allRules 返回两个地址族的全部规则，绕过列表的规则不论网段，见 anyBypassRule。
func allRules() []policyRule {
	return append(append(policyRules(addr4), policyRules(addr6)...), anyBypassRule(unix.AF_INET), anyBypassRule(unix.AF_INET6))
}

// bypassRules 返回 prefixes 里 IPv6（ipv6 为 true）或 IPv4 网段的规则：目标落在其中的选路照常查
// main 表（含默认路由），不进 routeTable。优先级在 policyRules 之前；main 表里没有可用的路由（如
// 断网）时落到后面的规则，按 policyRules 处理。
func bypassRules(prefixes []netip.Prefix, ipv6 bool) []policyRule {
	family := uint8(unix.AF_INET)
	if ipv6 {
		family = unix.AF_INET6
	}
	var rules []policyRule
	for _, p := range prefixes {
		if p.Addr().Is6() != ipv6 {
			continue
		}
		rule := anyBypassRule(family)
		rule.dst = p
		rules = append(rules, rule)
	}
	return rules
}

// anyBypassRule 删除时匹配地址族 family 里绕过列表的全部规则，不论网段：内核删除规则时只比较请求里
// 给出了的条件。进程被强杀后残留的是上次的绕过列表，与这次的可能不同。
func anyBypassRule(family uint8) policyRule {
	return policyRule{family: family, priority: bypassPriority, table: unix.RT_TABLE_MAIN}
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

// removeRules 撤销两个地址族的策略规则。
func removeRules() {
	nl, err := dialNetlink()
	if err != nil {
		return
	}
	defer nl.close()
	nl.deleteRules(allRules())
}

// isIPv6Unavailable 判断添加 IPv6 地址的错误是否源于宿主系统没有启用 IPv6：内核未编入
// IPv6 时报协议族不支持，被 disable_ipv6 关闭时报权限不足。
func isIPv6Unavailable(err error) bool {
	return errors.Is(err, unix.EAFNOSUPPORT) || errors.Is(err, unix.EOPNOTSUPP) ||
		errors.Is(err, unix.EACCES) || errors.Is(err, unix.EPERM)
}
