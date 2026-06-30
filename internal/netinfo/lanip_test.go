// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package netinfo

import (
	"net"
	"testing"
)

func TestIsVirtualIface(t *testing.T) {
	cases := map[string]bool{
		"en0":            false, // macOS Wi-Fi/有线(物理)
		"en1":            false,
		"eth0":           false, // Linux 有线
		"wlan0":          false, // Linux 无线
		"enp3s0":         false, // Linux predictable name
		"以太网":            false, // Windows 友好名
		"WLAN":           false,
		"utun3":          true, // VPN 隧道
		"awdl0":          true, // AirDrop
		"llw0":           true,
		"bridge100":      true, // 虚拟机桥接
		"vmnet8":         true,
		"docker0":        true,
		"veth1a2b":       true,
		"br-0f1e":        true,
		"virbr0":         true,
		"tailscale0":     true,
		"wg0":            true,
		"zt0":            true,
		"ppp0":           true, // L2TP/PPP VPN
		"ipsec0":         true, // IKEv2 VPN
		"gif0":           true, // 通用隧道
		"VMware Network": true, // Windows 友好名按子串
		"vEthernet (WSL)": true,
	}
	for name, want := range cases {
		if got := isVirtualIface(name); got != want {
			t.Errorf("isVirtualIface(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestSortByRank(t *testing.T) {
	// 输入故意打乱:公网物理、私有虚拟(VPN)、私有物理(默认出口)、私有物理(非出口)。
	in := []LANAddr{
		{IP: "203.0.113.5", Interface: "en4", Private: false},
		{IP: "10.8.0.2", Interface: "utun3", Private: true, Preferred: true},
		{IP: "192.168.1.20", Interface: "en0", Private: true, Preferred: false},
		{IP: "192.168.2.30", Interface: "en1", Private: true, Preferred: true},
	}
	sortByRank(in)

	// 期望:两条私有物理排前(默认出口的更靠前),其次私有虚拟,最后公网物理。
	want := []string{"192.168.2.30", "192.168.1.20", "10.8.0.2", "203.0.113.5"}
	for i, w := range want {
		if in[i].IP != w {
			t.Fatalf("rank order[%d] = %s, want %s (full: %+v)", i, in[i].IP, w, in)
		}
	}
}

func TestRankPrefersPhysicalPrivateOverVPN(t *testing.T) {
	// macOS 默认路由走 VPN 时,VPN 地址(私有但虚拟)虽是默认出口,物理私有网卡仍应胜出,
	// 避免取到非局域网地址。覆盖 utun/ppp/ipsec 三类隧道设备名。
	physical := LANAddr{IP: "192.168.1.20", Interface: "en0", Private: true}
	for _, vpnIface := range []string{"utun3", "ppp0", "ipsec0"} {
		vpn := LANAddr{IP: "10.8.0.2", Interface: vpnIface, Private: true, Preferred: true}
		if rankCandidate(physical) <= rankCandidate(vpn) {
			t.Fatalf("physical(%d) should outrank vpn %s(%d)", rankCandidate(physical), vpnIface, rankCandidate(vpn))
		}
	}
}

// TestLANIPsSmoke 确保在真实主机上枚举不 panic,且产出的地址可解析为 IPv4。
func TestLANIPsSmoke(t *testing.T) {
	for _, a := range LANIPs() {
		ip := net.ParseIP(a.IP)
		if ip == nil || ip.To4() == nil {
			t.Errorf("LANIPs returned non-IPv4 %q", a.IP)
		}
		if a.IP == "" {
			t.Error("LANIPs returned empty IP")
		}
	}
	if PreferredLANIP() == "" {
		t.Error("PreferredLANIP returned empty string")
	}
}
