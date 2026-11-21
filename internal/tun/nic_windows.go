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
	luid := dev.(*wgtun.NativeTun).LUID()
	err = configureNIC(luid, cfg.mtu)
	ipv6 := false
	if err == nil && cfg.ipv6 {
		ipv6, err = setIPv6(luid, cfg.mtu, true)
	}
	if err != nil {
		dev.Close()
		return nil, err
	}
	return &nic{
		name:    ifaceName,
		link:    newDeviceEndpoint(dev, cfg.mtu, 0, cfg.onDown),
		ipv6:    ipv6,
		setIPv6: func(on bool) (bool, error) { return setIPv6(luid, cfg.mtu, on) },
	}, nil
}

func configureNIC(luid uint64, mtu int) error {
	if err := setMTU(luid, windows.AF_INET, mtu); err != nil {
		return fmt.Errorf("设置 IPv4 MTU: %w", err)
	}
	if err := addAddr(luid, addr4); err != nil {
		return fmt.Errorf("配置地址 %s: %w", addr4, err)
	}
	return addRoutes(luid, routes4)
}

// setIPv6 给网卡配上或撤掉 IPv6 的地址与路由，语义同 nic.setIPv6。
func setIPv6(luid uint64, mtu int, on bool) (bool, error) {
	if !on {
		err := removeIPv6(luid)
		return err != nil, err
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
	if err := addRoutes(luid, routes6); err != nil {
		_ = removeIPv6(luid)
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
