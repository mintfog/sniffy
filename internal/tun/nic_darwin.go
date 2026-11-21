// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package tun

import (
	"bytes"
	"errors"
	"fmt"
	"net/netip"
	"os/exec"
	"strings"

	wgtun "golang.zx2c4.com/wireguard/tun"
)

// utun 每个包前面有 4 字节的协议族头，网卡封装要求调用方为它预留空间。
const utunHeaderLen = 4

func openNIC(cfg nicConfig) (*nic, error) {
	// utun 的编号由内核分配，名字在创建后才知道。
	dev, err := wgtun.CreateTUN("utun", cfg.mtu)
	if err != nil {
		return nil, err
	}
	name, err := dev.Name()
	if err == nil {
		err = configureNIC(name)
	}
	ipv6 := false
	if err == nil && cfg.ipv6 {
		ipv6, err = setIPv6(name, true)
	}
	if err != nil {
		dev.Close()
		return nil, err
	}
	return &nic{
		name:    name,
		link:    newDeviceEndpoint(dev, cfg.mtu, utunHeaderLen, cfg.onDown),
		ipv6:    ipv6,
		setIPv6: func(on bool) (bool, error) { return setIPv6(name, on) },
	}, nil
}

func configureNIC(name string) error {
	// utun 是点对点接口，对端地址填自己的地址即可。
	addr := addr4.Addr().String()
	if err := run("/sbin/ifconfig", name, "inet", addr, addr, "up"); err != nil {
		return err
	}
	return addRoutes(name, routes4)
}

// setIPv6 给网卡配上或撤掉 IPv6 的地址与路由，语义同 nic.setIPv6。
func setIPv6(name string, on bool) (bool, error) {
	if !on {
		err := removeIPv6(name)
		return err != nil, err
	}
	if err := run("/sbin/ifconfig", name, "inet6", addr6.String(), "alias"); err != nil {
		return false, err
	}
	if err := addRoutes(name, routes6); err != nil {
		_ = removeIPv6(name)
		return false, err
	}
	return true, nil
}

// removeIPv6 撤掉网卡的 IPv6 路由与地址，已经不在的跳过。
func removeIPv6(name string) error {
	var errs []error
	for _, route := range routes6 {
		if err := run("/sbin/route", "-q", "-n", "delete", "-inet6", route.String(), "-interface", name); err != nil && !isNotInTable(err) {
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
		family := "-inet"
		if route.Addr().Is6() {
			family = "-inet6"
		}
		if err := run("/sbin/route", "-q", "-n", "add", family, route.String(), "-interface", name); err != nil {
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
