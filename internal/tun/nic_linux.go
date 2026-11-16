// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package tun

import (
	"errors"
	"fmt"
	"net"

	"golang.org/x/sys/unix"
)

const (
	ifaceName = "sniffy0"

	tunOffloads = unix.TUN_F_CSUM | unix.TUN_F_TSO4 | unix.TUN_F_TSO6
)

func openNIC(cfg nicConfig) (*nic, error) {
	fd, err := openTUN(ifaceName)
	if err != nil {
		return nil, err
	}
	if err := configureNIC(ifaceName, cfg); err != nil {
		unix.Close(fd)
		return nil, err
	}
	return &nic{name: ifaceName, link: newVnetEndpoint(fd, cfg.mtu, cfg.onDown)}, nil
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

func configureNIC(name string, cfg nicConfig) error {
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return err
	}
	nl, err := dialNetlink()
	if err != nil {
		return err
	}
	defer nl.close()

	if err := nl.addAddr(iface.Index, addr4); err != nil {
		return fmt.Errorf("配置地址 %s: %w", addr4, err)
	}
	ipv6 := cfg.ipv6
	if ipv6 {
		switch err := nl.addAddr(iface.Index, addr6); {
		case isIPv6Unavailable(err):
			ipv6 = false
		case err != nil:
			return fmt.Errorf("配置地址 %s: %w", addr6, err)
		}
	}
	if err := nl.setLinkUp(iface.Index, cfg.mtu); err != nil {
		return fmt.Errorf("启用网卡: %w", err)
	}
	routes := routes4
	if ipv6 {
		routes = append(routes[:len(routes):len(routes)], routes6...)
	}
	for _, route := range routes {
		if err := nl.addRoute(iface.Index, route); err != nil {
			return fmt.Errorf("添加路由 %s: %w", route, err)
		}
	}
	return nil
}

// isIPv6Unavailable 判断添加 IPv6 地址的错误是否源于宿主系统没有启用 IPv6：内核未编入
// IPv6 时报协议族不支持，被 disable_ipv6 关闭时报权限不足。
func isIPv6Unavailable(err error) bool {
	return errors.Is(err, unix.EAFNOSUPPORT) || errors.Is(err, unix.EOPNOTSUPP) ||
		errors.Is(err, unix.EACCES) || errors.Is(err, unix.EPERM)
}
