// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build darwin

package sysproxy

import (
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// bypassDomains 是设置代理时排除的本地域/网段(直连,不走代理)。
var bypassDomains = []string{"localhost", "127.0.0.1", "::1", "*.local", "169.254/16"}

// Set 把所有启用的网络服务的 Web/安全 Web 代理指向 host:port。
func Set(host string, port int) error {
	svcs, err := activeServices()
	if err != nil {
		return err
	}
	p := strconv.Itoa(port)
	var errs []error
	for _, svc := range svcs {
		// setwebproxy/setsecurewebproxy 在写入地址的同时会把对应代理状态置为 on。
		errs = append(errs,
			run("-setwebproxy", svc, host, p),
			run("-setsecurewebproxy", svc, host, p),
			run(append([]string{"-setproxybypassdomains", svc}, bypassDomains...)...),
		)
	}
	return errors.Join(errs...)
}

// Clear 关闭所有网络服务的 Web/安全 Web 代理。
func Clear() error {
	svcs, err := activeServices()
	if err != nil {
		return err
	}
	var errs []error
	for _, svc := range svcs {
		errs = append(errs,
			run("-setwebproxystate", svc, "off"),
			run("-setsecurewebproxystate", svc, "off"),
		)
	}
	return errors.Join(errs...)
}

// PointsTo 报告是否有任一启用的网络服务的 Web 代理当前指向 host:port。
func PointsTo(host string, port int) bool {
	svcs, err := activeServices()
	if err != nil {
		return false
	}
	want := strconv.Itoa(port)
	for _, svc := range svcs {
		out, err := exec.Command("networksetup", "-getwebproxy", svc).Output()
		if err != nil {
			continue
		}
		if enabled, server, p := parseGetWebProxy(string(out)); enabled && server == host && p == want {
			return true
		}
	}
	return false
}

// activeServices 返回当前启用的网络服务名(如 Wi-Fi、Ethernet)。
func activeServices() ([]string, error) {
	out, err := exec.Command("networksetup", "-listallnetworkservices").Output()
	if err != nil {
		return nil, fmt.Errorf("列举网络服务失败: %w", err)
	}
	svcs := parseNetworkServices(string(out))
	if len(svcs) == 0 {
		return nil, errors.New("未找到可用网络服务")
	}
	return svcs, nil
}

func run(args ...string) error {
	if out, err := exec.Command("networksetup", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("networksetup %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}
