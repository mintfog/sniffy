// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build !darwin

package netinfo

// interfaceLabels 仅 macOS 需要把 BSD 设备名翻成友好名;其它平台网卡名本身已可读
// (Windows 为连接友好名,Linux 为 eth0/wlan0 等),无需映射。
func interfaceLabels() map[string]string { return nil }
