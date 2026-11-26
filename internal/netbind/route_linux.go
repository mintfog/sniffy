// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package netbind

import (
	"bytes"
	"encoding/binary"
	"net/netip"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// defaultRoute 返回本机新建的连接（不带标记、未绑定源地址与网卡）发往公网时用的默认路由，没有时
// Index 为 0：按优先级依次过一遍策略路由规则，第一张有可用默认路由的表即是，见 resolveDefaultRoute。
// wg-quick 这类 VPN 把默认路由放进单独的路由表、由策略规则引过去，main 表里的默认路由仍指向物理网卡。
func defaultRoute(ipv6 bool) (Route, error) {
	family := syscall.AF_INET
	if ipv6 {
		family = syscall.AF_INET6
	}
	rules, err := dumpRIB(syscall.RTM_GETRULE, family)
	if err != nil {
		return Route{}, err
	}
	routes, err := dumpRIB(syscall.RTM_GETROUTE, family)
	if err != nil {
		return Route{}, err
	}
	return resolveDefaultRoute(parseRules(rules), routes, uint32(os.Getuid())), nil
}

func dumpRIB(proto, family int) ([]syscall.NetlinkMessage, error) {
	rib, err := syscall.NetlinkRIB(proto, family)
	if err != nil {
		return nil, err
	}
	return syscall.ParseNetlinkMessage(rib)
}

// fibRule 是一条策略路由规则里与选默认路由有关的部分。
type fibRule struct {
	action uint8
	table  uint32
	invert bool
	// srcZero 表示源地址条件（没有时为真）容得下未指定的源地址。
	srcZero bool
	dstLen  uint8
	iif     string
	oif     string
	hasMark bool
	mark    uint32
	mask    uint32
	hasUID  bool
	uidLo   uint32
	uidHi   uint32
	// suppress 表示这条规则查表时忽略前缀长度不超过某个值的路由，默认路由总在其中。
	suppress bool
	// unknown 表示带着无从判断的条件（协议、端口、TOS、VRF 等），按不匹配处理。
	unknown bool
}

// fibRuleHdrLen 是 struct fib_rule_hdr 的长度：family、dst_len、src_len、tos、table、res1、res2、
// action 各 1 字节，flags 4 字节。
const fibRuleHdrLen = 12

// parseRules 解析规则表的转储，保持内核给出的顺序（按优先级）。
func parseRules(msgs []syscall.NetlinkMessage) []fibRule {
	var rules []fibRule
	for _, m := range msgs {
		if m.Header.Type == unix.RTM_NEWRULE && len(m.Data) >= fibRuleHdrLen {
			rules = append(rules, parseRule(m.Data))
		}
	}
	return rules
}

// parseRule 解析一条规则：struct fib_rule_hdr 后跟一串属性。
func parseRule(data []byte) fibRule {
	h := data[:fibRuleHdrLen]
	r := fibRule{
		dstLen:  h[1],
		table:   uint32(h[4]),
		action:  h[7],
		invert:  binary.NativeEndian.Uint32(h[8:])&unix.FIB_RULE_INVERT != 0,
		srcZero: true,
		unknown: h[3] != 0,
	}
	srcLen := int(h[2])
	for _, a := range ruleAttrs(data[fibRuleHdrLen:]) {
		v := a.Value
		switch a.Attr.Type {
		case unix.FRA_TABLE:
			r.table = attrUint32(v)
		case unix.FRA_SRC:
			addr, _ := netip.AddrFromSlice(v)
			src := netip.PrefixFrom(addr, srcLen).Masked()
			r.srcZero = src.Addr().IsUnspecified()
		case unix.FRA_IIFNAME:
			r.iif = string(bytes.TrimRight(v, "\x00"))
		case unix.FRA_OIFNAME:
			r.oif = string(bytes.TrimRight(v, "\x00"))
		case unix.FRA_FWMARK:
			r.hasMark, r.mark = true, attrUint32(v)
		case unix.FRA_FWMASK:
			r.hasMark, r.mask = true, attrUint32(v)
		case unix.FRA_UID_RANGE:
			if len(v) >= 8 {
				r.hasUID, r.uidLo, r.uidHi = true, binary.NativeEndian.Uint32(v), binary.NativeEndian.Uint32(v[4:])
			}
		case unix.FRA_SUPPRESS_PREFIXLEN:
			r.suppress = int32(attrUint32(v)) >= 0
		case unix.FRA_SUPPRESS_IFGROUP:
			r.unknown = r.unknown || int32(attrUint32(v)) != -1
		case unix.FRA_IP_PROTO, unix.FRA_SPORT_RANGE, unix.FRA_DPORT_RANGE, unix.FRA_L3MDEV, unix.FRA_TUN_ID, unix.FRA_FLOW:
			r.unknown = true
		}
	}
	return r
}

// ruleAttrs 切分规则消息里的一串属性，遇到长度不合法的属性就此截止。syscall.ParseNetlinkRouteAttr
// 不认规则消息，只能自己切。
func ruleAttrs(b []byte) []syscall.NetlinkRouteAttr {
	var attrs []syscall.NetlinkRouteAttr
	for len(b) >= unix.SizeofRtAttr {
		l := int(binary.NativeEndian.Uint16(b))
		if l < unix.SizeofRtAttr || l > len(b) {
			break
		}
		attrs = append(attrs, syscall.NetlinkRouteAttr{
			Attr:  syscall.RtAttr{Len: uint16(l), Type: binary.NativeEndian.Uint16(b[2:])},
			Value: b[unix.SizeofRtAttr:l],
		})
		b = b[min((l+unix.NLMSG_ALIGNTO-1)&^(unix.NLMSG_ALIGNTO-1), len(b)):]
	}
	return attrs
}

// attrUint32 读取 32 位的属性值，长度不足时为 0。
func attrUint32(v []byte) uint32 {
	if len(v) < 4 {
		return 0
	}
	return binary.NativeEndian.Uint32(v)
}

// matchesLocalDefault 报告 r 是否匹配本机新建、发往公网的连接的选路：不带标记，未绑定源地址与网卡，
// 入口网卡是 lo，以 uid 的身份发出。目标是任意公网地址，带目标网段条件的规则不算匹配。
func (r fibRule) matchesLocalDefault(uid uint32) bool {
	if r.unknown {
		return false
	}
	match := r.dstLen == 0 && r.srcZero && r.oif == "" && (r.iif == "" || r.iif == "lo") &&
		(!r.hasMark || r.mark&r.mask == 0) && (!r.hasUID || r.uidLo <= uid && uid <= r.uidHi)
	return match != r.invert
}

// resolveDefaultRoute 按 rules 的顺序模拟内核对本机新建连接的选路，返回第一张有可用默认路由的表里的
// 那条；路由表的取法见 pickDefaultRoute。规则命中 blackhole、unreachable、prohibit 时选路就此失败，
// 返回 Index 为 0。goto 规则不跟随跳转、按什么都不做处理：常见的 VPN（wg-quick、Tailscale）都不用它。
func resolveDefaultRoute(rules []fibRule, routes []syscall.NetlinkMessage, uid uint32) Route {
	for _, r := range rules {
		if !r.matchesLocalDefault(uid) {
			continue
		}
		switch r.action {
		case unix.FR_ACT_TO_TBL:
			if r.suppress {
				continue
			}
			if route := pickDefaultRoute(routes, r.table); route.Index != 0 {
				return route
			}
		case unix.FR_ACT_GOTO, unix.FR_ACT_NOP:
		default:
			return Route{}
		}
	}
	return Route{}
}

// pickDefaultRoute 从路由表的转储里挑出表 table 中可用的默认路由，有多条时取度量值最小的
// 一条（内核优先用它）；没有时 Index 为 0。
func pickDefaultRoute(msgs []syscall.NetlinkMessage, table uint32) Route {
	var best Route
	bestMetric := uint32(0)
	for i := range msgs {
		m := &msgs[i]
		if m.Header.Type != syscall.RTM_NEWROUTE || len(m.Data) < unix.SizeofRtMsg {
			continue
		}
		// struct rtmsg：family、dst_len、src_len、tos、table、protocol、scope、type、flags。
		dstLen, routeTable, routeType := m.Data[1], uint32(m.Data[4]), m.Data[7]
		flags := binary.NativeEndian.Uint32(m.Data[8:])
		if dstLen != 0 || routeType != unix.RTN_UNICAST || flags&(unix.RTNH_F_DEAD|unix.RTNH_F_LINKDOWN) != 0 {
			continue
		}
		attrs, err := syscall.ParseNetlinkRouteAttr(m)
		if err != nil {
			continue
		}
		var r Route
		metric := uint32(0)
		for _, a := range attrs {
			switch a.Attr.Type {
			case unix.RTA_TABLE:
				routeTable = binary.NativeEndian.Uint32(a.Value)
			case unix.RTA_OIF:
				r.Index = int(binary.NativeEndian.Uint32(a.Value))
			case unix.RTA_GATEWAY:
				r.Gateway, _ = netip.AddrFromSlice(a.Value)
			case unix.RTA_PRIORITY:
				metric = binary.NativeEndian.Uint32(a.Value)
			case unix.RTA_MULTIPATH:
				if r.Index == 0 {
					r = usableNexthop(a.Value)
				}
			}
		}
		if routeTable != table || r.Index == 0 {
			continue
		}
		if best.Index == 0 || metric < bestMetric {
			best, bestMetric = r, metric
		}
	}
	return best
}

// usableNexthop 返回多下一跳路由里第一个可用（未标记 DEAD、LINKDOWN）的下一跳，都不可用时
// Index 为 0。b 是一串 struct rtnexthop（len、flags、hops、ifindex），各自后跟该下一跳的属性，
// 按 4 字节对齐。
func usableNexthop(b []byte) Route {
	for len(b) >= unix.SizeofRtNexthop {
		l := int(binary.NativeEndian.Uint16(b))
		if l < unix.SizeofRtNexthop || l > len(b) {
			return Route{}
		}
		if b[2]&(unix.RTNH_F_DEAD|unix.RTNH_F_LINKDOWN) == 0 {
			return Route{Index: int(binary.NativeEndian.Uint32(b[4:])), Gateway: nexthopGateway(b[unix.SizeofRtNexthop:l])}
		}
		b = b[min((l+3)&^3, len(b)):]
	}
	return Route{}
}

// nexthopGateway 从一个下一跳的属性里取出网关，没有时返回无效地址。
func nexthopGateway(b []byte) netip.Addr {
	for len(b) >= unix.SizeofRtAttr {
		l := int(binary.NativeEndian.Uint16(b))
		if l < unix.SizeofRtAttr || l > len(b) {
			break
		}
		if binary.NativeEndian.Uint16(b[2:]) == unix.RTA_GATEWAY {
			addr, _ := netip.AddrFromSlice(b[unix.SizeofRtAttr:l])
			return addr
		}
		b = b[min((l+3)&^3, len(b)):]
	}
	return netip.Addr{}
}
