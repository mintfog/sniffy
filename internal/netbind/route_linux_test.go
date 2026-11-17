// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package netbind

import (
	"encoding/binary"
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
	multipath []testNexthop
}

type testNexthop struct {
	index uint32
	flags uint8
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
	attr(unix.RTA_PRIORITY, u32(r.metric))
	if len(r.multipath) > 0 {
		var nh []byte
		for _, h := range r.multipath {
			// 每个下一跳后面带一个网关属性，模拟内核转储的实际布局。
			gateway := make([]byte, unix.SizeofRtAttr+4)
			binary.NativeEndian.PutUint16(gateway, uint16(len(gateway)))
			binary.NativeEndian.PutUint16(gateway[2:], unix.RTA_GATEWAY)
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
		want   int
	}{
		{"没有路由", nil, 0},
		{"只有更具体的路由", []testRoute{
			{dstLen: 32, table: mainTable, routeType: unicast, oif: 7}, // 如 VPN 给 8.8.8.8 配的专用路由
			{dstLen: 24, table: mainTable, routeType: unicast, oif: 3},
		}, 0},
		{"取度量值最小的默认路由", []testRoute{
			{dstLen: 32, table: mainTable, routeType: unicast, oif: 7},
			{table: mainTable, routeType: unicast, oif: 3, metric: 600},
			{table: mainTable, routeType: unicast, oif: 4, metric: 100},
		}, 4},
		{"不看 main 之外的表", []testRoute{
			{table: 0x736e, routeType: unicast, oif: 9},
			{table: mainTable, routeType: unicast, oif: 3, metric: 100},
		}, 3},
		{"跳过链路已断开的", []testRoute{
			{table: mainTable, routeType: unicast, oif: 5, flags: unix.RTNH_F_LINKDOWN},
			{table: mainTable, routeType: unicast, oif: 3, metric: 100},
		}, 3},
		{"跳过非单播的", []testRoute{
			{table: mainTable, routeType: unix.RTN_UNREACHABLE, metric: 0},
			{table: mainTable, routeType: unicast, oif: 3, metric: 100},
		}, 3},
		{"多下一跳取第一条", []testRoute{
			{table: mainTable, routeType: unicast, multipath: []testNexthop{{index: 6}, {index: 8}}},
		}, 6},
		{"多下一跳跳过已断开的", []testRoute{
			{table: mainTable, routeType: unicast, multipath: []testNexthop{
				{index: 6, flags: unix.RTNH_F_DEAD | unix.RTNH_F_LINKDOWN},
				{index: 7, flags: unix.RTNH_F_LINKDOWN},
				{index: 8},
			}},
		}, 8},
		{"多下一跳全部断开", []testRoute{
			{table: mainTable, routeType: unicast, multipath: []testNexthop{{index: 6, flags: unix.RTNH_F_DEAD}}},
			{table: mainTable, routeType: unicast, oif: 3, metric: 900},
		}, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var msgs []syscall.NetlinkMessage
			for _, r := range tc.routes {
				msgs = append(msgs, netlinkRoute(r))
			}
			if got := pickDefaultRoute(msgs); got != tc.want {
				t.Errorf("网卡索引 = %d，期望 %d", got, tc.want)
			}
		})
	}
}
