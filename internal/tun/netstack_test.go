// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package tun

import (
	"bytes"
	"testing"

	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

type recordingDispatcher struct {
	protos []tcpip.NetworkProtocolNumber
}

func (d *recordingDispatcher) DeliverNetworkPacket(proto tcpip.NetworkProtocolNumber, _ *stack.PacketBuffer) {
	d.protos = append(d.protos, proto)
}

func (d *recordingDispatcher) DeliverLinkPacket(tcpip.NetworkProtocolNumber, *stack.PacketBuffer) {}

func TestDeliverFiltersPackets(t *testing.T) {
	ipv4 := func(proto byte) []byte {
		b := make([]byte, header.IPv4MinimumSize)
		b[0], b[9] = 0x45, proto
		return b
	}
	ipv6 := func(next byte) []byte {
		b := make([]byte, header.IPv6MinimumSize)
		b[0], b[6] = 0x60, next
		return b
	}
	// ipv6Ext 在固定头后依次接上 exts 列出的扩展头，最后一个指向 upper。
	ipv6Ext := func(upper byte, exts ...byte) []byte {
		b := ipv6(exts[0])
		for i := range exts {
			next := upper
			if i+1 < len(exts) {
				next = exts[i+1]
			}
			b = append(b, next, 0, 0, 0, 0, 0, 0, 0)
		}
		return b
	}
	cases := []struct {
		name string
		pkt  []byte
		want []tcpip.NetworkProtocolNumber
	}{
		{"IPv4 TCP", ipv4(6), []tcpip.NetworkProtocolNumber{header.IPv4ProtocolNumber}},
		{"IPv4 UDP", ipv4(17), []tcpip.NetworkProtocolNumber{header.IPv4ProtocolNumber}},
		{"IPv6 TCP", ipv6(6), []tcpip.NetworkProtocolNumber{header.IPv6ProtocolNumber}},
		{"IPv4 ICMP", ipv4(1), nil},
		{"IPv6 ICMPv6", ipv6(58), nil},
		{"IPv6 Hop-by-Hop 头后的 ICMPv6", ipv6Ext(58, 0), nil},
		{"IPv6 分片的 ICMPv6", ipv6Ext(58, 44), nil},
		{"IPv6 多个扩展头后的 ICMPv6", ipv6Ext(58, 0, 43, 60, 44), nil},
		{"IPv6 扩展头后的 TCP", ipv6Ext(6, 0, 44), []tcpip.NetworkProtocolNumber{header.IPv6ProtocolNumber}},
		{"IPv6 扩展头不完整", ipv6(0), []tcpip.NetworkProtocolNumber{header.IPv6ProtocolNumber}},
		// 非首片：分片头之后是载荷，开头的字节碰巧像一个指向 ICMPv6 的扩展头。
		{"IPv6 非首片", append(ipv6(44), 60, 0, 0, 8, 0, 0, 0, 1, 58, 0, 0, 0, 0, 0, 0, 0), []tcpip.NetworkProtocolNumber{header.IPv6ProtocolNumber}},
		{"IPv6 ICMPv6 的非首片", append(ipv6(44), 58, 0, 0, 8, 0, 0, 0, 1), nil},
		{"版本号不是 4 或 6", []byte{0x10, 0, 0, 0}, nil},
		{"IPv4 头不完整", ipv4(6)[:10], nil},
		{"空包", nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var d recordingDispatcher
			deliver(&d, buffer.NewViewWithData(tc.pkt))
			if len(d.protos) != len(tc.want) || (len(tc.want) == 1 && d.protos[0] != tc.want[0]) {
				t.Errorf("交给协议栈的包 = %v，应为 %v", d.protos, tc.want)
			}
		})
	}
}

func TestTCPHeadersEnd(t *testing.T) {
	ipv4 := func(ihl int, proto byte, payload int) []byte {
		b := make([]byte, ihl+header.TCPMinimumSize+payload)
		b[0], b[9] = 0x40|byte(ihl/4), proto
		b[ihl+12] = 5 << 4
		return b
	}
	ipv6 := func(next byte, tcpLen int) []byte {
		b := make([]byte, header.IPv6MinimumSize+tcpLen)
		b[0], b[6] = 0x60, next
		b[header.IPv6MinimumSize+12] = byte(tcpLen/4) << 4
		return b
	}
	fragment := ipv4(20, 6, 0)
	fragment[6] = 0x20 // MF
	tcpOptions := ipv4(20, 6, 12)
	tcpOptions[20+12] = 8 << 4
	badOffset := ipv4(20, 6, 0)
	badOffset[20+12] = 15 << 4
	cases := []struct {
		name  string
		pkt   []byte
		proto tcpip.NetworkProtocolNumber
		want  int
	}{
		{"IPv4", ipv4(20, 6, 100), header.IPv4ProtocolNumber, 40},
		{"IPv4 带选项", ipv4(24, 6, 100), header.IPv4ProtocolNumber, 44},
		{"TCP 带选项", tcpOptions, header.IPv4ProtocolNumber, 52},
		{"IPv6", ipv6(6, 32), header.IPv6ProtocolNumber, 72},
		{"IPv4 UDP", ipv4(20, 17, 100), header.IPv4ProtocolNumber, 0},
		{"IPv4 分片", fragment, header.IPv4ProtocolNumber, 0},
		{"IPv6 扩展头", ipv6(0, 32), header.IPv6ProtocolNumber, 0},
		{"TCP 头不完整", ipv4(20, 6, 0)[:30], header.IPv4ProtocolNumber, 0},
		{"数据偏移超出包长", badOffset, header.IPv4ProtocolNumber, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tcpHeadersEnd(tc.pkt, tc.proto); got != tc.want {
				t.Errorf("tcpHeadersEnd = %d，应为 %d", got, tc.want)
			}
		})
	}
}

func TestSplitHeaders(t *testing.T) {
	data := make([]byte, 40+4096)
	for i := range data {
		data[i] = byte(i)
	}
	views := func(b buffer.Buffer) []int {
		var sizes []int
		b.Apply(func(v *buffer.View) { sizes = append(sizes, v.Size()) })
		return sizes
	}

	b := splitHeaders(buffer.NewViewWithData(data), 40)
	if got := views(b); len(got) != 2 || got[0] != 40 || got[1] != 4096 {
		t.Errorf("拆分后各块大小 = %v，应为 [40 4096]", got)
	}
	if !bytes.Equal(b.Flatten(), data) {
		t.Error("拆分后内容与原包不一致")
	}
	b.Release()

	for _, n := range []int{0, len(data) - splitMinPayload + 1} {
		b := splitHeaders(buffer.NewViewWithData(data), n)
		if got := views(b); len(got) != 1 {
			t.Errorf("n = %d 时应不拆分，各块大小 = %v", n, got)
		}
		b.Release()
	}
}
