// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package netinfo

import (
	"bufio"
	"io"
	"strings"
)

// parseHardwarePorts 解析 `networksetup -listallhardwareports` 的输出为 device→port 映射。
// 抽成无 build tag 的纯函数便于跨平台单测:实际 exec 仅 macOS 使用,解析算法本身与平台无关。
// 遇到只有 Hardware Port 或只有 Device 的残缺块时,主动清零已缓存的 port,防止串到下一条。
func parseHardwarePorts(r io.Reader) map[string]string {
	labels := map[string]string{}
	var port string
	sc := bufio.NewScanner(r)
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
