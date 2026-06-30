// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build darwin

package netinfo

import (
	"bufio"
	"context"
	"os/exec"
	"strings"
	"time"
)

// interfaceLabels 把 macOS 的 BSD 设备名(en0/en1)映射到硬件端口友好名(Wi-Fi/Thunderbolt Ethernet),
// 便于用户在多网卡时区分无线与有线。best-effort:取不到时返回 nil,调用方回退到设备名。
func interfaceLabels() map[string]string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "networksetup", "-listallhardwareports").Output()
	if err != nil {
		return nil
	}
	labels := map[string]string{}
	var port string
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(line, "Hardware Port:"):
			port = strings.TrimSpace(strings.TrimPrefix(line, "Hardware Port:"))
		case strings.HasPrefix(line, "Device:"):
			dev := strings.TrimSpace(strings.TrimPrefix(line, "Device:"))
			if dev != "" && port != "" {
				labels[dev] = port
			}
			port = ""
		}
	}
	return labels
}
