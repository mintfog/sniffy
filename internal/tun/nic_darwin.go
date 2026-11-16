// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package tun

import (
	"bytes"
	"fmt"
	"os/exec"

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
		err = configureNIC(name, cfg)
	}
	if err != nil {
		dev.Close()
		return nil, err
	}
	return &nic{name: name, link: newDeviceEndpoint(dev, cfg.mtu, utunHeaderLen, cfg.onDown)}, nil
}

func configureNIC(name string, cfg nicConfig) error {
	// utun 是点对点接口，对端地址填自己的地址即可。
	addr := addr4.Addr().String()
	if err := run("/sbin/ifconfig", name, "inet", addr, addr, "up"); err != nil {
		return err
	}
	routes := routes4
	if cfg.ipv6 {
		if err := run("/sbin/ifconfig", name, "inet6", addr6.String(), "alias"); err != nil {
			return err
		}
		routes = append(routes[:len(routes):len(routes)], routes6...)
	}
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

func run(path string, args ...string) error {
	out, err := exec.Command(path, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %v: %w: %s", path, args, err, bytes.TrimSpace(out))
	}
	return nil
}
