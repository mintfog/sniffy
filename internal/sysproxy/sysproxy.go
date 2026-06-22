// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

// Package sysproxy 控制操作系统级的 HTTP/HTTPS 代理设置,使本机其它应用的流量
// 经 Sniffy 监听端口转发。Set 把系统代理指向 host:port 并排除本地直连地址,
// Clear 关闭系统代理。两者均为平台相关实现(见 sysproxy_<goos>.go),不支持的
// 平台返回错误。
package sysproxy

import "strings"

// parseNetworkServices 解析 `networksetup -listallnetworkservices`(macOS)的输出:
// 首行是说明性表头需丢弃;以 "*" 前缀标记的是被禁用的服务,同样跳过。
func parseNetworkServices(raw string) []string {
	lines := strings.Split(raw, "\n")
	out := make([]string, 0, len(lines))
	for i, ln := range lines {
		name := strings.TrimSpace(ln)
		if i == 0 || name == "" || strings.HasPrefix(name, "*") {
			continue
		}
		out = append(out, name)
	}
	return out
}

// parseGetWebProxy 解析 `networksetup -getwebproxy <svc>`(macOS)的输出,
// 形如 "Enabled: Yes / Server: 127.0.0.1 / Port: 8080"。供探测系统代理是否指向本程序。
func parseGetWebProxy(raw string) (enabled bool, server, port string) {
	for _, ln := range strings.Split(raw, "\n") {
		key, val, ok := strings.Cut(ln, ":")
		if !ok {
			continue
		}
		val = strings.TrimSpace(val)
		switch strings.TrimSpace(key) {
		case "Enabled":
			enabled = strings.EqualFold(val, "Yes")
		case "Server":
			server = val
		case "Port":
			port = val
		}
	}
	return
}
