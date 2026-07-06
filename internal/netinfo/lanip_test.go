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

// TestSortByRank_StableAmongEqualRank 一并覆盖 len<2 短路与「分数相同维持枚举顺序」契约。
// 若被改成 sort.Slice(非稳定),UI 里多张同网段网卡的顺序会随机漂移。
func TestSortByRank_StableAmongEqualRank(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		var in []LANAddr
		sortByRank(in)
		if in != nil {
			t.Fatalf("nil slice mutated: %+v", in)
		}
	})
	t.Run("empty", func(t *testing.T) {
		in := []LANAddr{}
		sortByRank(in)
		if len(in) != 0 {
			t.Fatalf("empty slice mutated: %+v", in)
		}
	})
	t.Run("single", func(t *testing.T) {
		in := []LANAddr{{IP: "10.0.0.1", Interface: "en0", Private: true}}
		sortByRank(in)
		if len(in) != 1 || in[0].IP != "10.0.0.1" {
			t.Fatalf("single-element slice mutated: %+v", in)
		}
	})
	t.Run("stable-equal-rank", func(t *testing.T) {
		in := []LANAddr{
			{IP: "192.168.1.10", Interface: "en0", Private: true},
			{IP: "192.168.1.11", Interface: "en1", Private: true},
			{IP: "192.168.1.12", Interface: "en2", Private: true},
		}
		// 前置断言三者 rank 相同,避免今后 rankCandidate 加维度让本用例失去意义。
		r0 := rankCandidate(in[0])
		for _, a := range in[1:] {
			if rankCandidate(a) != r0 {
				t.Fatalf("test setup broken: ranks differ, got %d vs %d", r0, rankCandidate(a))
			}
		}

		sortByRank(in)
		want := []string{"192.168.1.10", "192.168.1.11", "192.168.1.12"}
		for i, w := range want {
			if in[i].IP != w {
				t.Fatalf("stable order[%d] = %s, want %s (full: %+v)", i, in[i].IP, w, in)
			}
		}
	})
}

// stubAddr 实现 net.Addr 但不是 *net.IPNet,用于验证 filterFallback 会丢弃它。
type stubAddr struct{}

func (stubAddr) Network() string { return "stub" }
func (stubAddr) String() string  { return "stub" }

// mustCIDR 解析 "ip/mask" 为 *net.IPNet,并把 IP 保留为原始(而非网络号)以贴近真实 InterfaceAddrs 语义。
func mustCIDR(t *testing.T, s string) *net.IPNet {
	t.Helper()
	ip, ipnet, err := net.ParseCIDR(s)
	if err != nil {
		t.Fatalf("ParseCIDR(%q): %v", s, err)
	}
	ipnet.IP = ip
	return ipnet
}

// TestFilterFallback 用手工构造的 addrs 钉死 fallback 的过滤与标记契约,
// 不依赖真机 InterfaceAddrs 输出。
func TestFilterFallback(t *testing.T) {
	priv := mustCIDR(t, "10.0.0.1/24")
	pub := mustCIDR(t, "203.0.113.5/24")
	loop := mustCIDR(t, "127.0.0.1/8")
	link := mustCIDR(t, "169.254.1.1/16")
	v6 := mustCIDR(t, "fe80::1/64")

	addrs := []net.Addr{priv, pub, loop, link, v6, stubAddr{}}

	findIP := func(out []LANAddr, ip string) (LANAddr, bool) {
		for _, a := range out {
			if a.IP == ip {
				return a, true
			}
		}
		return LANAddr{}, false
	}

	assertShape := func(t *testing.T, out []LANAddr) {
		t.Helper()
		if len(out) != 2 {
			t.Fatalf("filterFallback len = %d, want 2 (got %+v)", len(out), out)
		}
		p, ok := findIP(out, "10.0.0.1")
		if !ok {
			t.Fatalf("missing private 10.0.0.1 in %+v", out)
		}
		if !p.Private {
			t.Errorf("10.0.0.1 Private = false, want true")
		}
		q, ok := findIP(out, "203.0.113.5")
		if !ok {
			t.Fatalf("missing public 203.0.113.5 in %+v", out)
		}
		if q.Private {
			t.Errorf("203.0.113.5 Private = true, want false")
		}
		for _, a := range out {
			if a.Interface != "" {
				t.Errorf("fallback must not fabricate Interface, got %q for %s", a.Interface, a.IP)
			}
			if a.Label != "" {
				t.Errorf("fallback must not fabricate Label, got %q for %s", a.Label, a.IP)
			}
		}
	}

	t.Run("def-empty", func(t *testing.T) {
		out := filterFallback(addrs, "")
		assertShape(t, out)
		for _, a := range out {
			if a.Preferred {
				t.Errorf("empty def must not flag Preferred (ip=%s)", a.IP)
			}
		}
	})

	t.Run("def-matches-private", func(t *testing.T) {
		out := filterFallback(addrs, "10.0.0.1")
		assertShape(t, out)
		for _, a := range out {
			want := a.IP == "10.0.0.1"
			if a.Preferred != want {
				t.Errorf("Preferred(%s) = %v, want %v (def=10.0.0.1)", a.IP, a.Preferred, want)
			}
		}
	})

	t.Run("def-matches-public", func(t *testing.T) {
		// 防止 Private 隐式过滤 Preferred 标记:公网候选也必须能被标为 Preferred。
		out := filterFallback(addrs, "203.0.113.5")
		assertShape(t, out)
		for _, a := range out {
			want := a.IP == "203.0.113.5"
			if a.Preferred != want {
				t.Errorf("Preferred(%s) = %v, want %v (def=203.0.113.5)", a.IP, a.Preferred, want)
			}
		}
	})

	t.Run("def-is-prefix-only", func(t *testing.T) {
		// 钉死 s==def 的完全匹配契约:若哪天改成 HasPrefix,"10.0.0" 会误标 10.0.0.1。
		out := filterFallback(addrs, "10.0.0")
		assertShape(t, out)
		for _, a := range out {
			if a.Preferred {
				t.Errorf("prefix-only def must not flag Preferred (ip=%s)", a.IP)
			}
		}
	})
}

// TestPreferredFrom 抽出的纯函数覆盖 PreferredLANIP 真实主机上无法触达的「空列表回退回环」分支。
func TestPreferredFrom(t *testing.T) {
	if got := preferredFrom(nil); got != "127.0.0.1" {
		t.Errorf("preferredFrom(nil) = %q, want 127.0.0.1", got)
	}
	if got := preferredFrom([]LANAddr{}); got != "127.0.0.1" {
		t.Errorf("preferredFrom(empty) = %q, want 127.0.0.1", got)
	}
	list := []LANAddr{{IP: "192.168.1.1"}, {IP: "10.0.0.1"}}
	if got := preferredFrom(list); got != "192.168.1.1" {
		t.Errorf("preferredFrom(non-empty) = %q, want 192.168.1.1", got)
	}
}
