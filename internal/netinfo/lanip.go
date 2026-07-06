// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

// Package netinfo 探测本机可用于被同网段设备访问的内网 IPv4 地址。
//
// 同时连接 WiFi 与有线、或叠加 VPN/虚拟网卡时本机会有多个内网地址。本包枚举全部候选,
// 按"私有网段 > 物理网卡 > 内核默认出口"排序给出推荐项,并把完整列表交给上层供用户自选,
// 避免默认路由走 VPN/虚拟网卡时只给出一个用户并不想暴露的地址。
package netinfo

import (
	"net"
	"sort"
	"strings"
)

// LANAddr 是某张本机网卡上的一个可用内网 IPv4 候选。
type LANAddr struct {
	IP        string `json:"ip"`
	Interface string `json:"interface"` // 网卡设备名(en0/eth0/Windows 为连接友好名)
	Label     string `json:"label"`     // 人类可读名(如 macOS 的 Wi-Fi/以太网);取不到时同 Interface
	Private   bool   `json:"private"`   // 是否 RFC1918 私有网段
	Preferred bool   `json:"preferred"` // 是否内核默认出站网卡的源地址
}

// LANIPs 枚举本机所有可用内网 IPv4 候选,推荐项(最可能希望被同网段设备指向的地址)排在最前。
// 不会过滤掉 VPN/虚拟网卡的地址(它们也可能是有效访问路径),仅在排序上降权。
func LANIPs() []LANAddr {
	def := defaultRouteIP() // 内核默认出口源地址,可能为空
	labels := interfaceLabels()

	ifaces, err := net.Interfaces()
	if err != nil {
		return fallbackAddrs(def)
	}

	var out []LANAddr
	seen := map[string]bool{}
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
			if seen[s] {
				continue
			}
			seen[s] = true
			label := labels[ifc.Name]
			if label == "" {
				label = ifc.Name
			}
			out = append(out, LANAddr{
				IP:        s,
				Interface: ifc.Name,
				Label:     label,
				Private:   ip4.IsPrivate(),
				Preferred: def != "" && s == def,
			})
		}
	}
	sortByRank(out)
	return out
}

// PreferredLANIP 返回推荐的内网 IPv4(列表首位);无可用候选时回退回环地址。
func PreferredLANIP() string {
	return preferredFrom(LANIPs())
}

// preferredFrom 抽出纯函数便于单测「空候选回退回环」这条真实主机上打不到的分支。
func preferredFrom(addrs []LANAddr) string {
	if len(addrs) > 0 {
		return addrs[0].IP
	}
	return "127.0.0.1"
}

// sortByRank 按打分降序稳定排序(分数相同维持枚举顺序)。
// rank 预先算好捆到临时切片:rankCandidate 走 ToLower + 20+ 次字串扫描,不宜放进 less。
func sortByRank(addrs []LANAddr) {
	if len(addrs) < 2 {
		return
	}
	type ranked struct {
		addr LANAddr
		rank int
	}
	items := make([]ranked, len(addrs))
	for i, a := range addrs {
		items[i] = ranked{addr: a, rank: rankCandidate(a)}
	}
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].rank > items[j].rank
	})
	for i, it := range items {
		addrs[i] = it.addr
	}
}

// rankCandidate 给候选打分:私有网段最重要(同网段设备真正能访问),其次是物理网卡
// (避开 VPN/虚拟网卡),最后才看是否为内核默认出口。分数高者更可能是用户想暴露的地址。
func rankCandidate(a LANAddr) int {
	score := 0
	if a.Private {
		score += 100
	}
	if !isVirtualIface(a.Interface) {
		score += 40
	}
	if a.Preferred {
		score += 20
	}
	return score
}

// virtualPrefixes 是常见虚拟/隧道网卡设备名前缀(macOS/Linux):VPN、容器、虚拟机桥接等。
var virtualPrefixes = []string{
	"utun", "awdl", "llw", "anpi", // macOS:VPN 隧道 / AirDrop / 低延迟 / Apple 私有
	"ppp", "ipsec", "gif", "stf", "pan", // L2TP/PPP / IKEv2 / 通用隧道 / 6to4 / 蓝牙 PAN
	"bridge", "vmnet", "vnic", "vboxnet", // 虚拟机桥接 / NAT
	"docker", "veth", "br-", "virbr", "kube", // 容器 / 虚拟桥
	"tun", "tap", "wg", "zt", "tailscale", // 通用隧道 / WireGuard / ZeroTier / Tailscale
}

// virtualSubstrings 匹配 Windows 上的友好连接名(无统一前缀,只能按子串识别)。
var virtualSubstrings = []string{
	"virtual", "vmware", "virtualbox", "hyper-v", "vethernet",
	"loopback", "tailscale", "wireguard", "zerotier", "pseudo",
}

// isVirtualIface 判断网卡名是否像虚拟/隧道设备。仅用于排序降权,误判只会轻微影响推荐顺序,
// 不会把地址从列表中剔除,故采用保守而宽松的启发式即可。
func isVirtualIface(name string) bool {
	n := strings.ToLower(name)
	for _, p := range virtualPrefixes {
		if strings.HasPrefix(n, p) {
			return true
		}
	}
	for _, s := range virtualSubstrings {
		if strings.Contains(n, s) {
			return true
		}
	}
	return false
}

// fallbackAddrs 在 net.Interfaces 失败时退回 InterfaceAddrs(拿不到网卡名/友好名)。
func fallbackAddrs(def string) []LANAddr {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	return filterFallback(addrs, def)
}

// filterFallback 抽出纯函数便于单测:真实主机上 InterfaceAddrs 常返回空或无法覆盖各类边界。
func filterFallback(addrs []net.Addr, def string) []LANAddr {
	var out []LANAddr
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
		out = append(out, LANAddr{
			IP:        s,
			Private:   ip4.IsPrivate(),
			Preferred: def != "" && s == def,
		})
	}
	sortByRank(out)
	return out
}

// defaultRouteIP 用一次 UDP "拨号"(不真正发包)让内核按路由表选出默认出口网卡的源地址。
func defaultRouteIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return ""
	}
	defer conn.Close()
	if addr, ok := conn.LocalAddr().(*net.UDPAddr); ok {
		if ip4 := addr.IP.To4(); ip4 != nil && !ip4.IsLoopback() {
			return ip4.String()
		}
	}
	return ""
}
