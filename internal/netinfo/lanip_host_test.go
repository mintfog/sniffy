// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package netinfo

import (
	"net"
	"testing"
)

// hostIPv4Set 直接从 net.InterfaceAddrs 收集本机应当出现在候选里的 IPv4,
// 作为「不得凭空捏造、也不得静默丢弃地址」的独立参照:本包的过滤规则若被改坏,
// 两侧集合会立刻对不上。不同主机网卡数量不同,故只比较集合而不写死具体地址。
func hostIPv4Set(t *testing.T) map[string]bool {
	t.Helper()
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		// 无网卡的 netns 里 InterfaceAddrs 返回空列表而非错误,真出错说明环境异常,不能静默跳过。
		t.Fatalf("本机 InterfaceAddrs 不可用,无法复算参照集: %v", err)
	}
	set := map[string]bool{}
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip4 := ipnet.IP.To4()
		if ip4 == nil || ip4.IsLoopback() || ip4.IsLinkLocalUnicast() {
			continue
		}
		set[ip4.String()] = true
	}
	return set
}

// assertRoutableIPv4 校验文本是规范化的可路由 IPv4。回环与链路本地地址对同网段设备毫无意义,
// 一旦漏进候选,UI 会把它当成可分享的访问地址给出去。
func assertRoutableIPv4(t *testing.T, s string) net.IP {
	t.Helper()
	ip4 := net.ParseIP(s).To4()
	if ip4 == nil {
		t.Errorf("%q 不是合法 IPv4", s)
		return nil
	}
	if ip4.String() != s {
		t.Errorf("%q 不是规范文本形式(应为 %q)", s, ip4.String())
	}
	if ip4.IsLoopback() || ip4.IsLinkLocalUnicast() {
		t.Errorf("回环/链路本地地址 %q 不应出现在候选里", s)
	}
	return ip4
}

// assertCandidateShape 在可路由性之外再校验 Private 标记与地址网段一致(UI 按 Private 分组展示)。
func assertCandidateShape(t *testing.T, a LANAddr) {
	t.Helper()
	ip4 := assertRoutableIPv4(t, a.IP)
	if ip4 != nil && a.Private != ip4.IsPrivate() {
		t.Errorf("Private(%s) = %v, want %v", a.IP, a.Private, ip4.IsPrivate())
	}
}

// assertRankDescending 钉死排序输出契约:上层直接取首位当推荐地址,
// 顺序一旦不是打分降序,推荐项就是错的。
func assertRankDescending(t *testing.T, addrs []LANAddr) {
	t.Helper()
	for i := 1; i < len(addrs); i++ {
		if rankCandidate(addrs[i-1]) < rankCandidate(addrs[i]) {
			t.Fatalf("排序非降序: [%d]%s(rank=%d) 排在 [%d]%s(rank=%d) 之前",
				i-1, addrs[i-1].IP, rankCandidate(addrs[i-1]),
				i, addrs[i].IP, rankCandidate(addrs[i]))
		}
	}
}

// TestFallbackAddrs 覆盖 net.Interfaces 失败时的降级路径。真实主机上该路径打不到,
// 只能直接调用未导出函数;断言集中在「集合与真机地址一致」和「不伪造网卡名」两条契约上。
func TestFallbackAddrs(t *testing.T) {
	want := hostIPv4Set(t)

	got := fallbackAddrs("")
	gotSet := map[string]bool{}
	for _, a := range got {
		assertCandidateShape(t, a)
		if a.Interface != "" || a.Label != "" {
			t.Errorf("降级路径拿不到网卡名,不得伪造: ip=%s interface=%q label=%q", a.IP, a.Interface, a.Label)
		}
		if a.Preferred {
			t.Errorf("def 为空时不应标记 Preferred (ip=%s)", a.IP)
		}
		gotSet[a.IP] = true
	}
	assertRankDescending(t, got)

	for ip := range want {
		if !gotSet[ip] {
			t.Errorf("本机地址 %s 被降级路径丢弃", ip)
		}
	}
	for ip := range gotSet {
		if !want[ip] {
			t.Errorf("降级路径给出了本机没有的地址 %s", ip)
		}
	}

	if len(want) == 0 {
		if got != nil {
			t.Fatalf("无可用地址时应返回 nil,实际 %+v", got)
		}
		return
	}

	// 取一个真实存在的地址当默认出口,验证 Preferred 只打在它身上,且不影响候选集合。
	var def string
	for ip := range want {
		def = ip
		break
	}
	t.Run("def-marks-only-matching", func(t *testing.T) {
		out := fallbackAddrs(def)
		if len(out) != len(got) {
			t.Fatalf("def 不应改变候选数量: %d vs %d", len(out), len(got))
		}
		preferred := 0
		for _, a := range out {
			if a.Preferred != (a.IP == def) {
				t.Errorf("Preferred(%s) = %v, want %v (def=%s)", a.IP, a.Preferred, a.IP == def, def)
			}
			if a.Preferred {
				preferred++
			}
		}
		if preferred != 1 {
			t.Errorf("Preferred 数量 = %d, want 1", preferred)
		}
		assertRankDescending(t, out)
	})

	t.Run("def-not-on-host", func(t *testing.T) {
		// TEST-NET-3 保留段,不会是本机地址;若真机恰好占用则换一个保留段。
		bogus := "203.0.113.199"
		if want[bogus] {
			bogus = "198.51.100.199"
		}
		for _, a := range fallbackAddrs(bogus) {
			if a.Preferred {
				t.Errorf("def=%s 不在本机上,不应标记 Preferred (ip=%s)", bogus, a.IP)
			}
		}
	})
}

// TestDefaultRouteIP 只能对真实内核路由结果做形态断言,但「返回值必须是本机某张网卡上的地址」
// 是硬契约:它会被当作 Preferred 的判定依据,取到非本机地址会让整份推荐失效。
func TestDefaultRouteIP(t *testing.T) {
	def := defaultRouteIP()
	if def == "" {
		// 无默认路由的沙箱环境:只要求返回空串而不是脏值,不做进一步断言。
		return
	}
	assertRoutableIPv4(t, def)
	if !hostIPv4Set(t)[def] {
		t.Errorf("defaultRouteIP() = %s, 不属于本机任何网卡地址", def)
	}
}

// TestLANIPsAgainstInterfaceEnumeration 用 net.Interfaces 独立复算一遍期望集合,
// 钉死枚举主路径的三条契约:同一 IP 只出现一次、网卡名与标签不串位、Preferred 与内核默认出口一致。
func TestLANIPsAgainstInterfaceEnumeration(t *testing.T) {
	ifaces, err := net.Interfaces()
	if err != nil {
		t.Fatalf("本机 Interfaces 不可用,无法复算参照集: %v", err)
	}
	// ip -> 该 IP 可能归属的网卡名集合(同一地址理论上可挂多张网卡)。
	wantOwners := map[string]map[string]bool{}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipnet.IP.To4()
			if ip4 == nil || ip4.IsLoopback() || ip4.IsLinkLocalUnicast() {
				continue
			}
			s := ip4.String()
			if wantOwners[s] == nil {
				wantOwners[s] = map[string]bool{}
			}
			wantOwners[s][ifc.Name] = true
		}
	}

	labels := interfaceLabels()
	got := LANIPs()
	assertRankDescending(t, got)

	seen := map[string]bool{}
	for _, a := range got {
		assertCandidateShape(t, a)
		if seen[a.IP] {
			t.Errorf("同一地址 %s 重复出现,去重失效", a.IP)
		}
		seen[a.IP] = true

		owners, ok := wantOwners[a.IP]
		if !ok {
			t.Errorf("给出了本机不存在(或应被过滤)的地址 %s", a.IP)
			continue
		}
		if !owners[a.Interface] {
			t.Errorf("地址 %s 的 Interface = %q, 不属于其真实网卡 %v", a.IP, a.Interface, owners)
		}
		if a.Interface == "" {
			t.Errorf("地址 %s 的 Interface 为空", a.IP)
		}
		wantLabel := labels[a.Interface]
		if wantLabel == "" {
			wantLabel = a.Interface
		}
		if a.Label != wantLabel {
			t.Errorf("地址 %s 的 Label = %q, want %q", a.IP, a.Label, wantLabel)
		}
	}
	for ip := range wantOwners {
		if !seen[ip] {
			t.Errorf("本机地址 %s 未出现在候选里", ip)
		}
	}

	def := defaultRouteIP()
	preferred := 0
	for _, a := range got {
		if a.Preferred != (def != "" && a.IP == def) {
			t.Errorf("Preferred(%s) = %v, want %v (默认出口=%q)", a.IP, a.Preferred, def != "" && a.IP == def, def)
		}
		if a.Preferred {
			preferred++
		}
	}
	if def != "" && seen[def] && preferred != 1 {
		t.Errorf("默认出口 %s 在候选里,应恰有一条 Preferred,实际 %d 条", def, preferred)
	}

	// PreferredLANIP 必须来自同一份候选,而不是另算一遍。
	pref := PreferredLANIP()
	if len(got) == 0 {
		if pref != "127.0.0.1" {
			t.Errorf("无候选时 PreferredLANIP() = %q, want 127.0.0.1", pref)
		}
	} else if !seen[pref] {
		t.Errorf("PreferredLANIP() = %q, 不在候选集合中", pref)
	}
}
