// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package tun

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os/exec"
	"strings"
	"syscall"

	"golang.org/x/net/route"
	"golang.org/x/sys/unix"
	wgtun "golang.zx2c4.com/wireguard/tun"

	"github.com/mintfog/sniffy/internal/netbind"
)

// utun 每个包前面有 4 字节的协议族头，网卡封装要求调用方为它预留空间。
const utunHeaderLen = 4

func openNIC(cfg nicConfig) (*nic, error) {
	// utun 的编号由内核分配，名字在创建后才知道。
	dev, err := wgtun.CreateTUN("utun", cfg.mtu)
	if err != nil {
		return nil, err
	}
	clearMarkedRoutes()
	routes := gatewayRoutes{
		bypass: newBypassRouter(systemRoutes{}, cfg.bypass),
		scoped: &scopedDefault{sys: systemRoutes{}},
	}
	name, err := dev.Name()
	if err == nil {
		err = configureNIC(name, routes)
	}
	ipv6 := false
	if err == nil && cfg.ipv6 {
		ipv6, err = setIPv6(name, routes, true)
	}
	if err != nil {
		dev.Close()
		_ = routes.close()
		return nil, err
	}
	return &nic{
		name:          name,
		link:          newDeviceEndpoint(dev, cfg.mtu, utunHeaderLen, cfg.onDown),
		ipv6:          ipv6,
		setIPv6:       func(on bool) (bool, error) { return setIPv6(name, routes, on) },
		refreshRoutes: routes.refresh,
		cleanup:       func() { _ = routes.close() },
	}, nil
}

// gatewayRoutes 是经默认路由的网关装上的两组路由：绕过列表的，与出口网卡名下的默认路由（见
// scopedDefault）。两组都随默认路由的变化重写，都先于指向虚拟网卡的 /1 路由装上：其间发往绕过网段的
// 连接不会进虚拟网卡，sniffy 绑定出口网卡的出站也不会因选不到路由而失败。
type gatewayRoutes struct {
	bypass *bypassRouter
	scoped *scopedDefault
}

func (g gatewayRoutes) install(ipv6 bool) error {
	err := g.scoped.install(ipv6)
	if bypassErr := g.bypass.install(ipv6); bypassErr != nil {
		err = errors.Join(err, fmt.Errorf("绕过列表: %w", bypassErr))
	}
	return err
}

func (g gatewayRoutes) remove(ipv6 bool) error {
	return errors.Join(g.scoped.remove(ipv6), g.bypass.remove(ipv6))
}

func (g gatewayRoutes) refresh() error {
	return errors.Join(g.scoped.refresh(), g.bypass.refresh())
}

func (g gatewayRoutes) close() error {
	return errors.Join(g.scoped.close(), g.bypass.close())
}

// configureNIC 配置网卡的 IPv4 地址与路由。
func configureNIC(name string, routes gatewayRoutes) error {
	// utun 是点对点接口，对端地址填自己的地址即可。
	addr := addr4.Addr().String()
	if err := run("/sbin/ifconfig", name, "inet", addr, addr, "up"); err != nil {
		return err
	}
	if err := routes.install(false); err != nil {
		return err
	}
	return addRoutes(name, routes4)
}

// setIPv6 给网卡配上或撤掉 IPv6 的地址与路由（含经网关装上的 IPv6 路由），语义同 nic.setIPv6。
func setIPv6(name string, routes gatewayRoutes, on bool) (bool, error) {
	if !on {
		// 没撤干净时指向虚拟网卡的 IPv6 路由可能还在，经网关装上的要留着，否则发往绕过网段的连接会进
		// 虚拟网卡、绑定出口网卡的出站会选不到路由；反过来它们删不掉不影响撤掉接管。
		if err := removeIPv6(name); err != nil {
			return true, err
		}
		return false, routes.remove(true)
	}
	if err := run("/sbin/ifconfig", name, "inet6", addr6.String(), "alias"); err != nil {
		return false, err
	}
	if err := routes.install(true); err != nil {
		_ = removeIPv6(name)
		_ = routes.remove(true)
		return false, err
	}
	if err := addRoutes(name, routes6); err != nil {
		_ = removeIPv6(name)
		_ = routes.remove(true)
		return false, err
	}
	return true, nil
}

// removeIPv6 撤掉网卡的 IPv6 路由与地址，已经不在的跳过。
func removeIPv6(name string) error {
	var errs []error
	for _, route := range routes6 {
		if err := runRoute("-n", "delete", "-inet6", route.String(), "-interface", name); err != nil && !isNotInTable(err) {
			errs = append(errs, err)
		}
	}
	if err := run("/sbin/ifconfig", name, "inet6", addr6.Addr().String(), "-alias"); err != nil && !isNotInTable(err) {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func addRoutes(name string, routes []netip.Prefix) error {
	for _, route := range routes {
		family := familyArg(route.Addr().Is6())
		if err := runRoute("-n", "add", family, route.String(), "-interface", name); err != nil {
			return err
		}
	}
	return nil
}

// isNotInTable 判断 route / ifconfig 的失败是否只因要删的路由或地址已经不在。
func isNotInTable(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "not in table") || strings.Contains(msg, "Can't assign requested address")
}

func run(path string, args ...string) error {
	out, err := exec.Command(path, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %v: %w: %s", path, args, err, bytes.TrimSpace(out))
	}
	return nil
}

// runRoute 运行 /sbin/route。route 的 add / delete 不论成败都以 0 退出，结果行写到
// stdout，写路由 socket 失败时往 stderr 写一条警告，故以 stderr 非空判失败。
func runRoute(args ...string) error {
	var stdout, stderr bytes.Buffer
	cmd := exec.Command("/sbin/route", args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err == nil && stderr.Len() > 0 {
		err = errors.New("执行失败")
	}
	if err != nil {
		return fmt.Errorf("/sbin/route %v: %w: %s", args, err, bytes.TrimSpace(append(stderr.Bytes(), stdout.Bytes()...)))
	}
	return nil
}

// 经网关装上的路由（绕过列表的，出口网卡名下的默认路由）带上 RTF_PROTO1 与 RTF_PROTO2 两个标志
// 作为标记，进程被强杀后据此找出残留，装上之后被 VPN 等改写了的也据此认出、不去删。这两个标志留给
// 路由协议自用，内核不解释它们，系统与常见软件也不会同时设上。
const routeMarks = unix.RTF_PROTO1 | unix.RTF_PROTO2

// systemRoutes 是 macOS 上的 bypassSystem 与 scopedSystem。
type systemRoutes struct{}

func (systemRoutes) defaultRoute(ipv6 bool) (netbind.Route, error) { return netbind.DefaultRoute(ipv6) }

// add 经 via 添加发往 dst 的路由。
func (systemRoutes) add(dst netip.Prefix, via netbind.Route) error {
	gateway, _, err := gatewayArgs(via)
	if err != nil {
		return err
	}
	args := append([]string{"-n", "add"}, routeDst(dst)...)
	return addMarked(append(args, gateway...))
}

// addScoped 经 via 给它所在的网卡添加限定该网卡的默认路由。
func (systemRoutes) addScoped(ipv6 bool, via netbind.Route) error {
	gateway, iface, err := gatewayArgs(via)
	if err != nil {
		return err
	}
	args := append([]string{"-n", "add", familyArg(ipv6), "default"}, gateway...)
	return addMarked(append(args, "-ifscope", iface.Name))
}

func (systemRoutes) ownsScoped(ipv6 bool, index int) (bool, error) {
	marked, err := scopedDefaults(ipv6)
	return marked[index], err
}

func (systemRoutes) deleteScoped(ipv6 bool, index int) error {
	iface, err := net.InterfaceByIndex(index)
	if err != nil {
		// 网卡已经不在，它名下的路由随之消失。
		return errRouteNotFound
	}
	err = runRoute("-n", "delete", familyArg(ipv6), "default", "-ifscope", iface.Name)
	if err != nil && isNotInTable(err) {
		return errRouteNotFound
	}
	return err
}

// gatewayArgs 返回 route 命令里经 via 走的参数，以及 via 所在的网卡：有网关时是网关地址（链路本地的
// IPv6 网关带上网卡名作 zone），没有网关（点对点链路）时直接经网卡。
func gatewayArgs(via netbind.Route) ([]string, *net.Interface, error) {
	iface, err := net.InterfaceByIndex(via.Index)
	if err != nil {
		return nil, nil, err
	}
	switch gw := via.Gateway; {
	case !gw.IsValid():
		return []string{"-interface", iface.Name}, iface, nil
	case gw.Is6() && gw.IsLinkLocalUnicast():
		return []string{gw.WithZone(iface.Name).String()}, iface, nil
	default:
		return []string{gw.String()}, iface, nil
	}
}

// addMarked 以 args 添加一条带 routeMarks 的路由，已有同一目标的路由时返回 errRouteExists。
func addMarked(args []string) error {
	err := runRoute(append(args, "-proto1", "-proto2")...)
	if err != nil && strings.Contains(err.Error(), "File exists") {
		return errRouteExists
	}
	return err
}

// delete 按网段删：同一网段只有一条不限定网卡的路由。
func (systemRoutes) delete(dst netip.Prefix, _ netbind.Route) error {
	err := runRoute(append([]string{"-n", "delete"}, routeDst(dst)...)...)
	if err != nil && isNotInTable(err) {
		return errRouteNotFound
	}
	return err
}

func (systemRoutes) scan(ipv6 bool) (routeScan, error) {
	msgs, err := fetchRoutes(ipv6)
	if err != nil {
		return routeScan{}, err
	}
	scan := routeScan{own: make(map[netip.Prefix]bool), taken: make(map[netip.Prefix]bool)}
	for _, m := range msgs {
		dst, owner := classifyRoute(m)
		switch owner {
		case routeOurs:
			scan.own[dst] = true
		case routeOthers:
			scan.taken[dst] = true
		}
	}
	return scan, nil
}

// scopedDefaults 按网卡索引返回名下有限定该网卡的默认路由的网卡，值表示那条路由带不带 routeMarks，
// 即是不是本程序补上的。
func scopedDefaults(ipv6 bool) (map[int]bool, error) {
	msgs, err := fetchRoutes(ipv6)
	if err != nil {
		return nil, err
	}
	marked := make(map[int]bool)
	for _, m := range msgs {
		rm, ok := m.(*route.RouteMessage)
		if !ok {
			continue
		}
		if scoped, ours := classifyScopedDefault(rm); scoped {
			marked[rm.Index] = ours
		}
	}
	return marked, nil
}

// classifyScopedDefault 报告 rm 是不是限定网卡的默认路由，以及它带不带 routeMarks。
func classifyScopedDefault(rm *route.RouteMessage) (scoped, marked bool) {
	if rm.Flags&unix.RTF_IFSCOPE == 0 || !netbind.IsDefaultRoute(rm.Addrs) {
		return false, false
	}
	return true, rm.Flags&routeMarks == routeMarks
}

func fetchRoutes(ipv6 bool) ([]route.Message, error) {
	family := syscall.AF_INET
	if ipv6 {
		family = syscall.AF_INET6
	}
	rib, err := route.FetchRIB(family, route.RIBTypeRoute, 0)
	if err != nil {
		return nil, fmt.Errorf("读路由表: %w", err)
	}
	msgs, err := route.ParseRIB(route.RIBTypeRoute, rib)
	if err != nil {
		return nil, fmt.Errorf("读路由表: %w", err)
	}
	return msgs, nil
}

func familyArg(ipv6 bool) string {
	if ipv6 {
		return "-inet6"
	}
	return "-inet"
}

// routeDst 返回 route 命令里表示目标 dst 的参数。
func routeDst(dst netip.Prefix) []string {
	family := familyArg(dst.Addr().Is6())
	if dst.IsSingleIP() {
		return []string{family, "-host", dst.Addr().String()}
	}
	return []string{family, "-net", dst.String()}
}

// clearMarkedRoutes 删除上次残留的、带着 routeMarks 的路由。
func clearMarkedRoutes() {
	var sys systemRoutes
	for _, ipv6 := range []bool{false, true} {
		scan, _ := sys.scan(ipv6)
		for dst := range scan.own {
			_ = sys.delete(dst, netbind.Route{})
		}
		scoped, _ := scopedDefaults(ipv6)
		for index, marked := range scoped {
			if marked {
				_ = sys.deleteScoped(ipv6, index)
			}
		}
	}
}

type routeOwner int

const (
	// routeIgnored 是与添加绕过路由无关的路由：限定网卡的（不与不限定网卡的冲突），以及内核按需
	// 克隆出的主机路由（添加同一目标的路由时内核先删掉它）。
	routeIgnored routeOwner = iota
	routeOurs
	routeOthers
)

// classifyRoute 判断 m 是不是绕过列表的路由（带着 routeMarks）或别的会与之冲突的路由，
// 并返回它的目标网段。
func classifyRoute(m route.Message) (netip.Prefix, routeOwner) {
	rm, ok := m.(*route.RouteMessage)
	if !ok || rm.Flags&(unix.RTF_IFSCOPE|unix.RTF_WASCLONED) != 0 || len(rm.Addrs) <= unix.RTAX_NETMASK {
		return netip.Prefix{}, routeIgnored
	}
	var addr netip.Addr
	switch a := rm.Addrs[unix.RTAX_DST].(type) {
	case *route.Inet4Addr:
		addr = netip.AddrFrom4(a.IP)
	case *route.Inet6Addr:
		addr = netip.AddrFrom16(a.IP)
	default:
		return netip.Prefix{}, routeIgnored
	}
	bits := addr.BitLen()
	if rm.Flags&unix.RTF_HOST == 0 {
		switch mask := rm.Addrs[unix.RTAX_NETMASK].(type) {
		case *route.Inet4Addr:
			bits, _ = net.IPMask(mask.IP[:]).Size()
		case *route.Inet6Addr:
			bits, _ = net.IPMask(mask.IP[:]).Size()
		default:
			return netip.Prefix{}, routeIgnored
		}
	}
	owner := routeOthers
	if rm.Flags&routeMarks == routeMarks {
		owner = routeOurs
	}
	return netip.PrefixFrom(addr, bits), owner
}
