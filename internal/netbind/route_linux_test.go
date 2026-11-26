// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package netbind

import (
	"encoding/binary"
	"net/netip"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

type testRoute struct {
	dstLen    uint8
	table     uint32
	routeType uint8
	flags     uint32
	oif       uint32
	metric    uint32
	gateway   netip.Addr
	multipath []testNexthop
}

type testNexthop struct {
	index   uint32
	flags   uint8
	gateway netip.Addr
}

// netlinkRoute 把 r 编码成路由表转储里的一条 RTM_NEWROUTE 消息。
func netlinkRoute(r testRoute) syscall.NetlinkMessage {
	data := make([]byte, unix.SizeofRtMsg)
	data[0], data[1], data[7] = unix.AF_INET, r.dstLen, r.routeType
	data[4] = unix.RT_TABLE_UNSPEC
	binary.NativeEndian.PutUint32(data[8:], r.flags)
	attr := func(t uint16, v []byte) {
		h := make([]byte, unix.SizeofRtAttr)
		binary.NativeEndian.PutUint16(h, uint16(unix.SizeofRtAttr+len(v)))
		binary.NativeEndian.PutUint16(h[2:], t)
		data = append(append(data, h...), v...)
		for len(data)%4 != 0 {
			data = append(data, 0)
		}
	}
	u32 := func(v uint32) []byte { return binary.NativeEndian.AppendUint32(nil, v) }
	attr(unix.RTA_TABLE, u32(r.table))
	if r.oif != 0 {
		attr(unix.RTA_OIF, u32(r.oif))
	}
	if r.gateway.IsValid() {
		attr(unix.RTA_GATEWAY, r.gateway.AsSlice())
	}
	attr(unix.RTA_PRIORITY, u32(r.metric))
	if len(r.multipath) > 0 {
		var nh []byte
		for _, h := range r.multipath {
			// 下一跳后面带网关属性，模拟内核转储的实际布局。
			var gateway []byte
			if h.gateway.IsValid() {
				gateway = make([]byte, unix.SizeofRtAttr)
				gateway = append(gateway, h.gateway.AsSlice()...)
				binary.NativeEndian.PutUint16(gateway, uint16(len(gateway)))
				binary.NativeEndian.PutUint16(gateway[2:], unix.RTA_GATEWAY)
			}
			hop := make([]byte, unix.SizeofRtNexthop)
			binary.NativeEndian.PutUint16(hop, uint16(len(hop)+len(gateway)))
			hop[2] = h.flags
			binary.NativeEndian.PutUint32(hop[4:], h.index)
			nh = append(append(nh, hop...), gateway...)
		}
		attr(unix.RTA_MULTIPATH, nh)
	}
	return syscall.NetlinkMessage{Header: syscall.NlMsghdr{Type: syscall.RTM_NEWROUTE}, Data: data}
}

func TestPickDefaultRoute(t *testing.T) {
	mainTable := uint32(unix.RT_TABLE_MAIN)
	unicast := uint8(unix.RTN_UNICAST)
	cases := []struct {
		name   string
		routes []testRoute
		want   Route
	}{
		{"没有路由", nil, Route{}},
		{"只有更具体的路由", []testRoute{
			{dstLen: 32, table: mainTable, routeType: unicast, oif: 7}, // 如 VPN 给 8.8.8.8 配的专用路由
			{dstLen: 24, table: mainTable, routeType: unicast, oif: 3},
		}, Route{}},
		{"取度量值最小的默认路由", []testRoute{
			{dstLen: 32, table: mainTable, routeType: unicast, oif: 7},
			{table: mainTable, routeType: unicast, oif: 3, metric: 600},
			{table: mainTable, routeType: unicast, oif: 4, metric: 100},
		}, Route{Index: 4}},
		{"不看 main 之外的表", []testRoute{
			{table: 0x736e, routeType: unicast, oif: 9},
			{table: mainTable, routeType: unicast, oif: 3, metric: 100},
		}, Route{Index: 3}},
		{"跳过链路已断开的", []testRoute{
			{table: mainTable, routeType: unicast, oif: 5, flags: unix.RTNH_F_LINKDOWN},
			{table: mainTable, routeType: unicast, oif: 3, metric: 100},
		}, Route{Index: 3}},
		{"跳过非单播的", []testRoute{
			{table: mainTable, routeType: unix.RTN_UNREACHABLE, metric: 0},
			{table: mainTable, routeType: unicast, oif: 3, metric: 100},
		}, Route{Index: 3}},
		{"多下一跳取第一条", []testRoute{
			{table: mainTable, routeType: unicast, multipath: []testNexthop{{index: 6}, {index: 8}}},
		}, Route{Index: 6}},
		{"多下一跳跳过已断开的", []testRoute{
			{table: mainTable, routeType: unicast, multipath: []testNexthop{
				{index: 6, flags: unix.RTNH_F_DEAD | unix.RTNH_F_LINKDOWN},
				{index: 7, flags: unix.RTNH_F_LINKDOWN},
				{index: 8},
			}},
		}, Route{Index: 8}},
		{"多下一跳全部断开", []testRoute{
			{table: mainTable, routeType: unicast, multipath: []testNexthop{{index: 6, flags: unix.RTNH_F_DEAD}}},
			{table: mainTable, routeType: unicast, oif: 3, metric: 900},
		}, Route{Index: 3}},
		{"带出网关", []testRoute{
			{table: mainTable, routeType: unicast, oif: 3, gateway: netip.MustParseAddr("192.168.1.1")},
		}, Route{Index: 3, Gateway: netip.MustParseAddr("192.168.1.1")}},
		{"多下一跳带出所选下一跳的网关", []testRoute{
			{table: mainTable, routeType: unicast, multipath: []testNexthop{
				{index: 6, flags: unix.RTNH_F_LINKDOWN, gateway: netip.MustParseAddr("10.0.0.1")},
				{index: 8, gateway: netip.MustParseAddr("10.0.1.1")},
			}},
		}, Route{Index: 8, Gateway: netip.MustParseAddr("10.0.1.1")}},
		{"点对点链路没有网关", []testRoute{
			{table: mainTable, routeType: unicast, oif: 4},
		}, Route{Index: 4}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var msgs []syscall.NetlinkMessage
			for _, r := range tc.routes {
				msgs = append(msgs, netlinkRoute(r))
			}
			if got := pickDefaultRoute(msgs, unix.RT_TABLE_MAIN); got != tc.want {
				t.Errorf("默认路由 = %+v，期望 %+v", got, tc.want)
			}
		})
	}
}

type testRule struct {
	priority uint32
	action   uint8
	table    uint32
	invert   bool
	src      netip.Prefix
	dst      netip.Prefix
	iif, oif string
	mark     *[2]uint32 // 值与掩码
	uid      *[2]uint32
	suppress bool
	ipProto  bool
}

// netlinkRule 把 r 编码成规则表转储里的一条 RTM_NEWRULE 消息，属性按内核转储的实际布局给出。
func netlinkRule(r testRule) syscall.NetlinkMessage {
	data := make([]byte, fibRuleHdrLen)
	data[0], data[4], data[7] = unix.AF_INET, unix.RT_TABLE_UNSPEC, r.action
	if r.dst.IsValid() {
		data[1] = uint8(r.dst.Bits())
	}
	if r.src.IsValid() {
		data[2] = uint8(r.src.Bits())
	}
	if r.invert {
		binary.NativeEndian.PutUint32(data[8:], unix.FIB_RULE_INVERT)
	}
	attr := func(t uint16, v []byte) {
		h := make([]byte, unix.SizeofRtAttr)
		binary.NativeEndian.PutUint16(h, uint16(unix.SizeofRtAttr+len(v)))
		binary.NativeEndian.PutUint16(h[2:], t)
		data = append(append(data, h...), v...)
		for len(data)%4 != 0 {
			data = append(data, 0)
		}
	}
	u32 := func(v uint32) []byte { return binary.NativeEndian.AppendUint32(nil, v) }
	attr(unix.FRA_TABLE, u32(r.table))
	attr(unix.FRA_PRIORITY, u32(r.priority))
	suppress := ^uint32(0)
	if r.suppress {
		suppress = 0
	}
	attr(unix.FRA_SUPPRESS_PREFIXLEN, u32(suppress))
	attr(unix.FRA_SUPPRESS_IFGROUP, u32(^uint32(0)))
	if r.src.IsValid() {
		attr(unix.FRA_SRC, r.src.Addr().AsSlice())
	}
	if r.dst.IsValid() {
		attr(unix.FRA_DST, r.dst.Addr().AsSlice())
	}
	if r.iif != "" {
		attr(unix.FRA_IIFNAME, append([]byte(r.iif), 0))
	}
	if r.oif != "" {
		attr(unix.FRA_OIFNAME, append([]byte(r.oif), 0))
	}
	if r.mark != nil {
		attr(unix.FRA_FWMARK, u32(r.mark[0]))
		attr(unix.FRA_FWMASK, u32(r.mark[1]))
	}
	if r.uid != nil {
		attr(unix.FRA_UID_RANGE, append(u32(r.uid[0]), u32(r.uid[1])...))
	}
	if r.ipProto {
		attr(unix.FRA_IP_PROTO, []byte{unix.IPPROTO_TCP})
	}
	return syscall.NetlinkMessage{Header: syscall.NlMsghdr{Type: syscall.RTM_NEWRULE}, Data: data}
}

func TestResolveDefaultRoute(t *testing.T) {
	const (
		toTable = unix.FR_ACT_TO_TBL
		wgTable = 51820
		uid     = 1000
	)
	mainTable := uint32(unix.RT_TABLE_MAIN)
	local := testRule{priority: 0, action: toTable, table: unix.RT_TABLE_LOCAL}
	mainRule := testRule{priority: 32766, action: toTable, table: mainTable}
	defaultRule := testRule{priority: 32767, action: toTable, table: unix.RT_TABLE_DEFAULT}
	// wg-quick 全局模式：main 表照旧有物理网卡的默认路由，未打标记的流量经规则引进 51820 表。
	wgQuick := []testRule{
		{priority: 32764, action: toTable, table: mainTable, suppress: true},
		{priority: 32765, action: toTable, table: wgTable, invert: true, mark: &[2]uint32{wgTable, 0xffffffff}},
	}
	// 开启中的 sniffy 自己的规则，其路由表里只有 /1 路由。
	sniffy := []testRule{
		{priority: 29550, action: toTable, table: mainTable, suppress: true},
		{priority: 29551, action: toTable, table: 0x736e, src: netip.MustParsePrefix("0.0.0.0/32"), mark: &[2]uint32{0, 0xffffffff}},
		{priority: 29551, action: toTable, table: 0x736e, src: netip.MustParsePrefix("172.29.113.0/30"), mark: &[2]uint32{0, 0xffffffff}},
	}
	unicast := uint8(unix.RTN_UNICAST)
	routes := []testRoute{
		{table: mainTable, routeType: unicast, oif: 2, gateway: netip.MustParseAddr("192.168.1.1")},
		{table: wgTable, routeType: unicast, oif: 9},
		{dstLen: 1, table: 0x736e, routeType: unicast, oif: 30},
		{table: 100, routeType: unicast, oif: 7},
	}
	physical := Route{Index: 2, Gateway: netip.MustParseAddr("192.168.1.1")}
	vpn := Route{Index: 9}
	other := Route{Index: 7}
	join := func(groups ...[]testRule) []testRule {
		var all []testRule
		for _, g := range groups {
			all = append(all, g...)
		}
		return all
	}
	cases := []struct {
		name  string
		rules []testRule
		want  Route
	}{
		{"只有默认的三条规则", []testRule{local, mainRule, defaultRule}, physical},
		{"wg-quick", join([]testRule{local}, wgQuick, []testRule{mainRule, defaultRule}), vpn},
		{"wg-quick 加上开启中的 sniffy", join([]testRule{local}, sniffy, wgQuick, []testRule{mainRule}), vpn},
		{"开启中的 sniffy，没有 VPN", join([]testRule{local}, sniffy, []testRule{mainRule}), physical},
		{"按标记引流的规则不匹配未打标记的流量", []testRule{local,
			{priority: 5210, action: toTable, table: 100, mark: &[2]uint32{0x80000, 0xff0000}}, mainRule}, physical},
		{"按目标网段引流的规则不匹配", []testRule{local,
			{priority: 100, action: toTable, table: 100, dst: netip.MustParsePrefix("10.0.0.0/8")}, mainRule}, physical},
		{"按目标网段取反的规则匹配", []testRule{local,
			{priority: 100, action: toTable, table: 100, dst: netip.MustParsePrefix("10.0.0.0/8"), invert: true}, mainRule}, other},
		{"按源地址引流的规则不匹配", []testRule{local,
			{priority: 100, action: toTable, table: 100, src: netip.MustParsePrefix("192.168.1.0/24")}, mainRule}, physical},
		{"入口网卡为 lo 的规则匹配", []testRule{local, {priority: 100, action: toTable, table: 100, iif: "lo"}, mainRule}, other},
		{"别的入口网卡不匹配", []testRule{local, {priority: 100, action: toTable, table: 100, iif: "eth0"}, mainRule}, physical},
		{"限定出口网卡的不匹配", []testRule{local, {priority: 100, action: toTable, table: 100, oif: "eth0"}, mainRule}, physical},
		{"用户范围内的匹配", []testRule{local, {priority: 100, action: toTable, table: 100, uid: &[2]uint32{1000, 1000}}, mainRule}, other},
		{"用户范围外的不匹配", []testRule{local, {priority: 100, action: toTable, table: 100, uid: &[2]uint32{0, 999}}, mainRule}, physical},
		{"无从判断的条件按不匹配", []testRule{local, {priority: 100, action: toTable, table: 100, ipProto: true}, mainRule}, physical},
		{"表里没有默认路由时交给后面的规则", []testRule{local, {priority: 100, action: toTable, table: 200}, mainRule}, physical},
		{"unreachable 规则让选路失败", []testRule{local, {priority: 100, action: unix.FR_ACT_UNREACHABLE}, mainRule}, Route{}},
	}
	var routeMsgs []syscall.NetlinkMessage
	for _, r := range routes {
		routeMsgs = append(routeMsgs, netlinkRoute(r))
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var ruleMsgs []syscall.NetlinkMessage
			for _, r := range tc.rules {
				ruleMsgs = append(ruleMsgs, netlinkRule(r))
			}
			if got := resolveDefaultRoute(parseRules(ruleMsgs), routeMsgs, uid); got != tc.want {
				t.Errorf("默认路由 = %+v，期望 %+v", got, tc.want)
			}
		})
	}
}
