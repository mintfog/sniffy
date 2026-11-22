// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package tun

import (
	"errors"
	"fmt"
	"io/fs"
	"net/netip"
	"unsafe"

	"golang.org/x/sys/windows"
	wgtun "golang.zx2c4.com/wireguard/tun"

	"github.com/mintfog/sniffy/internal/netbind"
)

const ifaceName = "sniffy"

// 网卡使用固定的 GUID：Windows 按 GUID 记住网络位置，每次随机生成会在系统里累积出
// 「网络 2」「网络 3」……
var adapterGUID = windows.GUID{
	Data1: 0x6e736e69,
	Data2: 0x6666,
	Data3: 0x4d79,
	Data4: [8]byte{0xa3, 0x1c, 0x5e, 0x0b, 0x7f, 0x92, 0xd4, 0x18},
}

var (
	modiphlpapi = windows.NewLazySystemDLL("iphlpapi.dll")

	procInitializeUnicastIpAddressEntry = modiphlpapi.NewProc("InitializeUnicastIpAddressEntry")
	procCreateUnicastIpAddressEntry     = modiphlpapi.NewProc("CreateUnicastIpAddressEntry")
	procDeleteUnicastIpAddressEntry     = modiphlpapi.NewProc("DeleteUnicastIpAddressEntry")
	procInitializeIpForwardEntry        = modiphlpapi.NewProc("InitializeIpForwardEntry")
	procCreateIpForwardEntry2           = modiphlpapi.NewProc("CreateIpForwardEntry2")
	procDeleteIpForwardEntry2           = modiphlpapi.NewProc("DeleteIpForwardEntry2")
	procSetIpInterfaceEntry             = modiphlpapi.NewProc("SetIpInterfaceEntry")
	procConvertInterfaceIndexToLuid     = modiphlpapi.NewProc("ConvertInterfaceIndexToLuid")
)

func openNIC(cfg nicConfig) (*nic, error) {
	// wintun 在未提权时报什么错没有文档保证，先自行检查，好让调用方按 fs.ErrPermission 改走 helper。
	if !windows.GetCurrentProcessToken().IsElevated() {
		return nil, fmt.Errorf("需要管理员权限: %w", fs.ErrPermission)
	}
	wgtun.WintunTunnelType = "sniffy"
	dev, err := wgtun.CreateTUNWithRequestedGUID(ifaceName, &adapterGUID, cfg.mtu)
	if errors.Is(err, windows.ERROR_MOD_NOT_FOUND) {
		return nil, fmt.Errorf("找不到 wintun.dll，它须与 sniffy 程序放在同一目录: %w", err)
	}
	if err != nil {
		return nil, err
	}
	clearBypassRoutes()
	bypass := newBypassRouter(systemRoutes{}, cfg.bypass)
	luid := dev.(*wgtun.NativeTun).LUID()
	err = configureNIC(luid, cfg.mtu, bypass)
	ipv6 := false
	if err == nil && cfg.ipv6 {
		ipv6, err = setIPv6(luid, cfg.mtu, bypass, true)
	}
	if err != nil {
		dev.Close()
		_ = bypass.close()
		return nil, err
	}
	return &nic{
		name:          ifaceName,
		link:          newDeviceEndpoint(dev, cfg.mtu, 0, cfg.onDown),
		ipv6:          ipv6,
		setIPv6:       func(on bool) (bool, error) { return setIPv6(luid, cfg.mtu, bypass, on) },
		refreshBypass: bypass.refresh,
		cleanup:       func() { _ = bypass.close() },
	}, nil
}

// configureNIC 配置网卡的 IPv4 地址与路由。绕过列表的路由先于 /1 路由装上，其间发往绕过网段的
// 连接不会进虚拟网卡。
func configureNIC(luid uint64, mtu int, bypass *bypassRouter) error {
	if err := setMTU(luid, windows.AF_INET, mtu); err != nil {
		return fmt.Errorf("设置 IPv4 MTU: %w", err)
	}
	if err := addAddr(luid, addr4); err != nil {
		return fmt.Errorf("配置地址 %s: %w", addr4, err)
	}
	if err := bypass.install(false); err != nil {
		return fmt.Errorf("绕过列表: %w", err)
	}
	return addRoutes(luid, routes4)
}

// setIPv6 给网卡配上或撤掉 IPv6 的地址与路由（含绕过列表里 IPv6 网段的路由），语义同 nic.setIPv6。
func setIPv6(luid uint64, mtu int, bypass *bypassRouter, on bool) (bool, error) {
	if !on {
		// 没撤干净时指向虚拟网卡的 IPv6 路由可能还在，绕过路由要留着，否则发往绕过网段的连接会进
		// 虚拟网卡；反过来绕过路由删不掉不影响撤掉接管。
		if err := removeIPv6(luid); err != nil {
			return true, err
		}
		return false, bypass.remove(true)
	}
	// 网卡上查不到 IPv6 接口说明系统没有启用 IPv6。
	switch err := setMTU(luid, windows.AF_INET6, mtu); {
	case errors.Is(err, windows.ERROR_NOT_FOUND):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("设置 IPv6 MTU: %w", err)
	}
	if err := addAddr(luid, addr6); err != nil {
		return false, fmt.Errorf("配置地址 %s: %w", addr6, err)
	}
	if err := bypass.install(true); err != nil {
		_ = removeIPv6(luid)
		_ = bypass.remove(true)
		return false, fmt.Errorf("绕过列表: %w", err)
	}
	if err := addRoutes(luid, routes6); err != nil {
		_ = removeIPv6(luid)
		_ = bypass.remove(true)
		return false, err
	}
	return true, nil
}

// removeIPv6 撤掉网卡的 IPv6 路由与地址，已经不在的跳过。
func removeIPv6(luid uint64) error {
	var errs []error
	for _, route := range routes6 {
		row := routeRow(luid, route)
		if err := callIPHelper(procDeleteIpForwardEntry2, unsafe.Pointer(&row)); err != nil && !errors.Is(err, windows.ERROR_NOT_FOUND) {
			errs = append(errs, fmt.Errorf("删除路由 %s: %w", route, err))
		}
	}
	row := addrRow(luid, addr6)
	if err := callIPHelper(procDeleteUnicastIpAddressEntry, unsafe.Pointer(&row)); err != nil && !errors.Is(err, windows.ERROR_NOT_FOUND) {
		errs = append(errs, fmt.Errorf("删除地址 %s: %w", addr6, err))
	}
	return errors.Join(errs...)
}

func addRoutes(luid uint64, routes []netip.Prefix) error {
	for _, route := range routes {
		row := routeRow(luid, route)
		if err := callIPHelper(procCreateIpForwardEntry2, unsafe.Pointer(&row)); err != nil {
			return fmt.Errorf("添加路由 %s: %w", route, err)
		}
	}
	return nil
}

func setMTU(luid uint64, family uint16, mtu int) error {
	row := windows.MibIpInterfaceRow{Family: family, InterfaceLuid: luid}
	if err := windows.GetIpInterfaceEntry(&row); err != nil {
		return err
	}
	row.NlMtu = uint32(mtu)
	if family == windows.AF_INET {
		// SetIpInterfaceEntry 对 IPv4 要求这个字段为 0，而读回来的值不是。
		row.SitePrefixLength = 0
	}
	return callIPHelper(procSetIpInterfaceEntry, unsafe.Pointer(&row))
}

func addAddr(luid uint64, prefix netip.Prefix) error {
	row := addrRow(luid, prefix)
	return callIPHelper(procCreateUnicastIpAddressEntry, unsafe.Pointer(&row))
}

func addrRow(luid uint64, prefix netip.Prefix) windows.MibUnicastIpAddressRow {
	var row windows.MibUnicastIpAddressRow
	procInitializeUnicastIpAddressEntry.Call(uintptr(unsafe.Pointer(&row)))
	row.InterfaceLuid = luid
	row.OnLinkPrefixLength = uint8(prefix.Bits())
	// 点对点的虚拟链路上没有别的节点，跳过重复地址检测，地址立即可用。
	row.DadState = windows.IpDadStatePreferred
	setSockaddr(unsafe.Pointer(&row.Address), prefix.Addr())
	return row
}

// routeRow 返回一条经网卡直接可达的路由（下一跳为全零地址）。
func routeRow(luid uint64, dst netip.Prefix) windows.MibIpForwardRow2 {
	var row windows.MibIpForwardRow2
	procInitializeIpForwardEntry.Call(uintptr(unsafe.Pointer(&row)))
	row.InterfaceLuid = luid
	row.DestinationPrefix.PrefixLength = uint8(dst.Bits())
	setSockaddr(unsafe.Pointer(&row.DestinationPrefix.Prefix), dst.Addr())
	unspecified := netip.IPv4Unspecified()
	if dst.Addr().Is6() {
		unspecified = netip.IPv6Unspecified()
	}
	setSockaddr(unsafe.Pointer(&row.NextHop), unspecified)
	return row
}

// setSockaddr 把 addr 写进 p 指向的 SOCKADDR_INET 联合体。
func setSockaddr(p unsafe.Pointer, addr netip.Addr) {
	if addr.Is4() {
		sa := (*windows.RawSockaddrInet4)(p)
		sa.Family = windows.AF_INET
		sa.Addr = addr.As4()
		return
	}
	sa := (*windows.RawSockaddrInet6)(p)
	sa.Family = windows.AF_INET6
	sa.Addr = addr.As16()
}

func callIPHelper(proc *windows.LazyProc, row unsafe.Pointer) error {
	if ret, _, _ := proc.Call(uintptr(row)); ret != 0 {
		return windows.Errno(ret)
	}
	return nil
}

// bypassRouteMetric 是绕过列表路由的度量值，同时作为标记，进程被强杀后据此找出残留。选路先比前缀
// 长度，度量值只在同一网段的路由之间起作用，而已有同一网段路由的网段不再添加，取什么值都不影响
// 选路；取 route 命令能设的范围（1–9999）里手工配置很少用到的值。
const bypassRouteMetric = 7365

// systemRoutes 是 Windows 上的 bypassSystem。
type systemRoutes struct{}

func (systemRoutes) defaultRoute(ipv6 bool) (netbind.Route, error) { return netbind.DefaultRoute(ipv6) }

// add 经 via 的网关添加路由，via 没有网关（点对点链路）时经它的网卡直达。
func (systemRoutes) add(dst netip.Prefix, via netbind.Route) error {
	row, err := bypassRow(dst, via)
	if err != nil {
		return err
	}
	err = callIPHelper(procCreateIpForwardEntry2, unsafe.Pointer(&row))
	if errors.Is(err, windows.ERROR_OBJECT_ALREADY_EXISTS) {
		return errRouteExists
	}
	return err
}

// delete 删除经 via 的那条。via 的网卡已经不在（被拔掉、禁用）时报 ERROR_FILE_NOT_FOUND，它上面的
// 路由已随之删掉，按路由不存在处理。
func (systemRoutes) delete(dst netip.Prefix, via netbind.Route) error {
	row, err := bypassRow(dst, via)
	if err == nil {
		err = deleteRouteRow(&row)
	}
	if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
		return errRouteNotFound
	}
	return err
}

// scan 里别的路由只算接在已连接的网卡上的：未连接网卡上的路由不参与选路，不能指望它让流量绕开
// 虚拟网卡。
func (systemRoutes) scan(ipv6 bool) (routeScan, error) {
	family := uint16(windows.AF_INET)
	if ipv6 {
		family = windows.AF_INET6
	}
	scan := routeScan{own: make(map[netip.Prefix]bool), taken: make(map[netip.Prefix]bool)}
	up := make(map[uint64]bool)
	err := eachRoute(family, func(row *windows.MibIpForwardRow2) {
		dst := prefixOf(&row.DestinationPrefix)
		if isBypassRoute(row) {
			scan.own[dst] = true
			return
		}
		connected, ok := up[row.InterfaceLuid]
		if !ok {
			connected = interfaceUp(row.InterfaceLuid)
			up[row.InterfaceLuid] = connected
		}
		if connected {
			scan.taken[dst] = true
		}
	})
	if err != nil {
		return routeScan{}, fmt.Errorf("读路由表: %w", err)
	}
	return scan, nil
}

// isBypassRoute 判断 row 是不是绕过列表的路由，即手工添加、度量值为 bypassRouteMetric 的路由。
func isBypassRoute(row *windows.MibIpForwardRow2) bool {
	return row.Metric == bypassRouteMetric && row.Origin == windows.NlroManual && row.DestinationPrefix.PrefixLength > 0
}

// interfaceUp 判断网卡是否已连接，查不到的算未连接。
func interfaceUp(luid uint64) bool {
	row := windows.MibIfRow2{InterfaceLuid: luid}
	return windows.GetIfEntry2Ex(windows.MibIfEntryNormal, &row) == nil && row.OperStatus == windows.IfOperStatusUp
}

func deleteRouteRow(row *windows.MibIpForwardRow2) error {
	err := callIPHelper(procDeleteIpForwardEntry2, unsafe.Pointer(row))
	if errors.Is(err, windows.ERROR_NOT_FOUND) {
		return errRouteNotFound
	}
	return err
}

func bypassRow(dst netip.Prefix, via netbind.Route) (windows.MibIpForwardRow2, error) {
	var luid uint64
	if ret, _, _ := procConvertInterfaceIndexToLuid.Call(uintptr(via.Index), uintptr(unsafe.Pointer(&luid))); ret != 0 {
		return windows.MibIpForwardRow2{}, fmt.Errorf("网卡 %d: %w", via.Index, windows.Errno(ret))
	}
	row := routeRow(luid, dst)
	row.Metric = bypassRouteMetric
	if gw := via.Gateway; gw.IsValid() {
		setSockaddr(unsafe.Pointer(&row.NextHop), gw)
		// 只有链路本地地址要靠网卡索引限定作用域。
		if gw.Is6() && gw.IsLinkLocalUnicast() {
			(*windows.RawSockaddrInet6)(unsafe.Pointer(&row.NextHop)).Scope_id = uint32(via.Index)
		}
	}
	return row, nil
}

// clearBypassRoutes 删除上次残留的绕过列表路由。
func clearBypassRoutes() {
	for _, family := range []uint16{windows.AF_INET, windows.AF_INET6} {
		var stale []windows.MibIpForwardRow2
		_ = eachRoute(family, func(row *windows.MibIpForwardRow2) {
			if isBypassRoute(row) {
				stale = append(stale, *row)
			}
		})
		for i := range stale {
			_ = deleteRouteRow(&stale[i])
		}
	}
}

func eachRoute(family uint16, fn func(row *windows.MibIpForwardRow2)) error {
	var table *windows.MibIpForwardTable2
	if err := windows.GetIpForwardTable2(family, &table); err != nil {
		return err
	}
	defer windows.FreeMibTable(unsafe.Pointer(table))
	rows := table.Rows()
	for i := range rows {
		fn(&rows[i])
	}
	return nil
}

// prefixOf 把 IP Helper 的 IP_ADDRESS_PREFIX 转成 netip.Prefix，与 setSockaddr 相反。
func prefixOf(p *windows.IpAddressPrefix) netip.Prefix {
	var addr netip.Addr
	switch p.Prefix.Family {
	case windows.AF_INET:
		addr = netip.AddrFrom4((*windows.RawSockaddrInet4)(unsafe.Pointer(&p.Prefix)).Addr)
	case windows.AF_INET6:
		addr = netip.AddrFrom16((*windows.RawSockaddrInet6)(unsafe.Pointer(&p.Prefix)).Addr)
	default:
		return netip.Prefix{}
	}
	return netip.PrefixFrom(addr, int(p.PrefixLength))
}
