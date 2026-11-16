// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package tun

import (
	"errors"
	"fmt"
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
	procInitializeIpForwardEntry        = modiphlpapi.NewProc("InitializeIpForwardEntry")
	procCreateIpForwardEntry2           = modiphlpapi.NewProc("CreateIpForwardEntry2")
	procSetIpInterfaceEntry             = modiphlpapi.NewProc("SetIpInterfaceEntry")
)

func openNIC(cfg nicConfig) (*nic, error) {
	wgtun.WintunTunnelType = "sniffy"
	dev, err := wgtun.CreateTUNWithRequestedGUID(ifaceName, &adapterGUID, cfg.mtu)
	if err != nil {
		return nil, err
	}
	luid := dev.(*wgtun.NativeTun).LUID()
	if err := configureNIC(luid, cfg); err != nil {
		dev.Close()
		return nil, err
	}
	return &nic{name: ifaceName, link: newDeviceEndpoint(dev, cfg.mtu, 0, cfg.onDown)}, nil
}

func configureNIC(luid uint64, cfg nicConfig) error {
	if err := setMTU(luid, windows.AF_INET, cfg.mtu); err != nil {
		return fmt.Errorf("设置 IPv4 MTU: %w", err)
	}
	if err := addAddr(luid, addr4); err != nil {
		return fmt.Errorf("配置地址 %s: %w", addr4, err)
	}
	routes := routes4
	if cfg.ipv6 {
		// 网卡上查不到 IPv6 接口说明系统没有启用 IPv6，此时只配置 IPv4。
		switch err := setMTU(luid, windows.AF_INET6, cfg.mtu); {
		case errors.Is(err, windows.ERROR_NOT_FOUND):
		case err != nil:
			return fmt.Errorf("设置 IPv6 MTU: %w", err)
		default:
			if err := addAddr(luid, addr6); err != nil {
				return fmt.Errorf("配置地址 %s: %w", addr6, err)
			}
			routes = append(routes[:len(routes):len(routes)], routes6...)
		}
	}
	for _, route := range routes {
		if err := addRoute(luid, route); err != nil {
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
	var row windows.MibUnicastIpAddressRow
	procInitializeUnicastIpAddressEntry.Call(uintptr(unsafe.Pointer(&row)))
	row.InterfaceLuid = luid
	row.OnLinkPrefixLength = uint8(prefix.Bits())
	// 点对点的虚拟链路上没有别的节点，跳过重复地址检测，地址立即可用。
	row.DadState = windows.IpDadStatePreferred
	setSockaddr(unsafe.Pointer(&row.Address), prefix.Addr())
	return callIPHelper(procCreateUnicastIpAddressEntry, unsafe.Pointer(&row))
}

// addRoute 添加一条经网卡直接可达的路由（下一跳为全零地址）。
func addRoute(luid uint64, dst netip.Prefix) error {
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
	return callIPHelper(procCreateIpForwardEntry2, unsafe.Pointer(&row))
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
