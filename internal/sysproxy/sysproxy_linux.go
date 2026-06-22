// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build linux

package sysproxy

import (
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// ignoreHosts 是 GNOME 代理排除项(直连),为 GVariant 数组字面量。
const ignoreHosts = "['localhost', '127.0.0.0/8', '::1']"

// errNoGsettings 在缺少 gsettings 时返回:目前仅支持基于 GNOME 的桌面环境。
var errNoGsettings = errors.New("未找到 gsettings,仅支持基于 GNOME 的桌面环境设置系统代理")

// Set 通过 GNOME 代理设置把 HTTP/HTTPS 代理指向 host:port,并切到 manual 模式。
func Set(host string, port int) error {
	if !hasGsettings() {
		return errNoGsettings
	}
	p := strconv.Itoa(port)
	steps := [][]string{
		{"set", "org.gnome.system.proxy.http", "host", host},
		{"set", "org.gnome.system.proxy.http", "port", p},
		{"set", "org.gnome.system.proxy.https", "host", host},
		{"set", "org.gnome.system.proxy.https", "port", p},
		// 统一所有协议走 HTTP 代理,避免用户既有 socks/ftp 配置在 manual 模式下意外生效。
		{"set", "org.gnome.system.proxy", "use-same-proxy", "true"},
		{"set", "org.gnome.system.proxy", "ignore-hosts", ignoreHosts},
		// mode 最后置 manual:地址就绪后再切换,避免短暂指向空地址。
		{"set", "org.gnome.system.proxy", "mode", "manual"},
	}
	for _, s := range steps {
		if err := run(s...); err != nil {
			return err
		}
	}
	return nil
}

// Clear 把 GNOME 代理模式切回 none(直连)。
func Clear() error {
	if !hasGsettings() {
		return errNoGsettings
	}
	return run("set", "org.gnome.system.proxy", "mode", "none")
}

// PointsTo 报告 GNOME 系统代理当前是否为 manual 且 HTTP 代理指向 host:port。
func PointsTo(host string, port int) bool {
	if !hasGsettings() {
		return false
	}
	if getString("org.gnome.system.proxy", "mode") != "manual" {
		return false
	}
	return getString("org.gnome.system.proxy.http", "host") == host &&
		getString("org.gnome.system.proxy.http", "port") == strconv.Itoa(port)
}

func hasGsettings() bool {
	_, err := exec.LookPath("gsettings")
	return err == nil
}

// getString 读取一个 gsettings 值;字符串值带单引号、整数值为裸数字,统一去掉引号与空白。
func getString(schema, key string) string {
	out, err := exec.Command("gsettings", "get", schema, key).Output()
	if err != nil {
		return ""
	}
	return strings.Trim(strings.TrimSpace(string(out)), "'")
}

func run(args ...string) error {
	if out, err := exec.Command("gsettings", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("gsettings %v: %w: %s", args, err, out)
	}
	return nil
}
