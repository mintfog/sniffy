// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build windows

package sysproxy

import (
	"fmt"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// internetSettingsPath 是 WinINet 代理配置所在的注册表项(当前用户)。
const internetSettingsPath = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`

// proxyOverride 本地直连排除项;<local> 覆盖不含点号的内网主机名。
const proxyOverride = "localhost;127.0.0.1;<local>"

// Set 写入 ProxyServer/ProxyOverride 并启用代理,然后通知 WinINet 立即重读。
func Set(host string, port int) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, internetSettingsPath, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("打开注册表失败: %w", err)
	}
	defer k.Close()
	if err := k.SetStringValue("ProxyServer", fmt.Sprintf("%s:%d", host, port)); err != nil {
		return err
	}
	if err := k.SetStringValue("ProxyOverride", proxyOverride); err != nil {
		return err
	}
	if err := k.SetDWordValue("ProxyEnable", 1); err != nil {
		return err
	}
	refresh()
	return nil
}

// Clear 关闭代理(ProxyEnable=0)并通知 WinINet 立即重读。
func Clear() error {
	k, err := registry.OpenKey(registry.CURRENT_USER, internetSettingsPath, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("打开注册表失败: %w", err)
	}
	defer k.Close()
	if err := k.SetDWordValue("ProxyEnable", 0); err != nil {
		return err
	}
	refresh()
	return nil
}

// PointsTo 报告 WinINet 代理当前是否已启用且指向 host:port。
func PointsTo(host string, port int) bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, internetSettingsPath, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	if enabled, _, err := k.GetIntegerValue("ProxyEnable"); err != nil || enabled != 1 {
		return false
	}
	server, _, err := k.GetStringValue("ProxyServer")
	if err != nil {
		return false
	}
	return server == fmt.Sprintf("%s:%d", host, port)
}

// refresh 通知 WinINet 立即重读代理设置;否则已运行的应用不会感知本次变更。
// 调用失败不致命:设置已落注册表,新启动的应用仍会生效。
func refresh() {
	const (
		internetOptionSettingsChanged = 39
		internetOptionRefresh         = 37
	)
	proc := windows.NewLazySystemDLL("wininet.dll").NewProc("InternetSetOptionW")
	proc.Call(0, internetOptionSettingsChanged, 0, 0)
	proc.Call(0, internetOptionRefresh, 0, 0)
}
