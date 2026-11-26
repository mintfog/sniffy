// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package tun

import (
	"encoding/binary"
	"errors"
	"net"
	"time"

	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/qdisc/fifo"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv6"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
	"gvisor.dev/gvisor/pkg/waiter"
)

const (
	nicID = 1

	// 同时处于握手阶段的连接上限，超出的 SYN 被丢弃、由对端重传。
	maxInFlightHandshakes = 4096

	// 协议栈发往网卡的包先进队列，由每个队列各自的 goroutine 写出，同一条连接的包固定走同一个
	// 队列。写网卡的系统调用里内核会同步做完应用一侧的收包处理，不排队的话这段时间算在调用
	// 方头上——对下行数据就是宿主的中继 goroutine，它因此腾不出手去读下一块数据。
	txQueues   = 8
	txQueueLen = 1024

	// 应用进程退出时内核会替它发 FIN / RST，保活只用来回收那些通知在途中丢失的连接。
	keepaliveIdle     = 60 * time.Second
	keepaliveInterval = 30 * time.Second
	keepaliveCount    = 4
)

// TCP 缓冲区范围。Default 是连接的初始值，随后按实际吞吐自动调整到 Max 以内。
//
// 这些连接的对端是本机内核，中间没有时延，缓冲只用来吸收宿主中继暂时跟不上的那部分数据；
// 实测 Default 从 64KB 到 4MB 吞吐没有差别，取较小的值以限制大量连接同时积压时的内存。
//
// 发送侧的上限比接收侧小：下行数据在中继、协议栈、内核之间要拷贝好几次，多条连接同时下行
// 时，积压在发送缓冲里的数据越多，这些拷贝越难命中 CPU 缓存。实测 4 条连接同时下行，上限从
// 4MB 降到 1MB 后每字节的 CPU 开销降约两成、吞吐升约一成；单条连接要到 512KB 以下才开始掉速。
// 接收侧上限降到 512KB 时单连接上行吞吐降约两成，保持 4MB。
var (
	tcpReceiveBuffer = tcpip.TCPReceiveBufferSizeRangeOption{Min: 4 << 10, Default: 256 << 10, Max: 4 << 20}
	tcpSendBuffer    = tcpip.TCPSendBufferSizeRangeOption{Min: 4 << 10, Default: 256 << 10, Max: 1 << 20}
)

func newStack(link stack.LinkEndpoint, handler func(net.Conn), relay *udpRelay) (*stack.Stack, error) {
	s := stack.New(stack.Options{
		NetworkProtocols:   []stack.NetworkProtocolFactory{ipv4.NewProtocol, ipv6.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol, udp.NewProtocol},
	})

	sack := tcpip.TCPSACKEnabled(true)
	moderate := tcpip.TCPModerateReceiveBufferOption(true)
	for _, opt := range []tcpip.SettableTransportProtocolOption{&sack, &moderate, &tcpReceiveBuffer, &tcpSendBuffer} {
		if err := s.SetTransportProtocolOption(tcp.ProtocolNumber, opt); err != nil {
			s.Destroy()
			return nil, errors.New(err.String())
		}
	}

	relay.attach(s)
	s.SetTransportProtocolHandler(udp.ProtocolNumber, relay.handlePacket)

	forwarder := tcp.NewForwarder(s, 0, maxInFlightHandshakes, func(r *tcp.ForwarderRequest) {
		var wq waiter.Queue
		ep, err := r.CreateEndpoint(&wq)
		if err != nil {
			r.Complete(true)
			return
		}
		r.Complete(false)
		setKeepalive(ep)
		handler(gonet.NewTCPConn(&wq, ep))
	})
	s.SetTransportProtocolHandler(tcp.ProtocolNumber, forwarder.HandlePacket)

	// 网卡以停用状态创建，全部配置好之后再启用：路由此刻已经指向网卡，可能已有包在排队，
	// 启用前收到的包一律丢弃，不会被半配置的协议栈处理。
	queue := fifo.New(link, txQueues, txQueueLen)
	if err := s.CreateNICWithOptions(nicID, link, stack.NICOptions{QDisc: queue, Disabled: true}); err != nil {
		queue.Close()
		s.Destroy()
		return nil, errors.New(err.String())
	}
	// 网卡收到的包发往任意地址：混杂模式让协议栈全部接收，spoofing 允许以这些地址为源回包。
	s.SetPromiscuousMode(nicID, true)
	s.SetSpoofing(nicID, true)
	s.SetRouteTable([]tcpip.Route{
		{Destination: header.IPv4EmptySubnet, NIC: nicID},
		{Destination: header.IPv6EmptySubnet, NIC: nicID},
	})
	if err := s.EnableNIC(nicID); err != nil {
		s.Destroy()
		return nil, errors.New(err.String())
	}
	return s, nil
}

func setKeepalive(ep tcpip.Endpoint) {
	ep.SocketOptions().SetKeepAlive(true)
	idle := tcpip.KeepaliveIdleOption(keepaliveIdle)
	interval := tcpip.KeepaliveIntervalOption(keepaliveInterval)
	ep.SetSockOpt(&idle)
	ep.SetSockOpt(&interval)
	ep.SetSockOptInt(tcpip.KeepaliveCountOption, keepaliveCount)
}

// deliver 把网卡收到的一个 IP 包交给协议栈，接管 view。
//
// ICMP 在这里丢弃：混杂模式下协议栈会替任意目标地址应答 echo，ping 外网会得到假的「通」。
func deliver(d stack.NetworkDispatcher, view *buffer.View) {
	pkt := view.AsSlice()
	var proto tcpip.NetworkProtocolNumber
	switch {
	case len(pkt) >= header.IPv4MinimumSize && pkt[0]>>4 == header.IPv4Version:
		if pkt[9] == uint8(header.ICMPv4ProtocolNumber) {
			view.Release()
			return
		}
		proto = header.IPv4ProtocolNumber
	case len(pkt) >= header.IPv6MinimumSize && pkt[0]>>4 == header.IPv6Version:
		if ipv6UpperProtocol(pkt) == uint8(header.ICMPv6ProtocolNumber) {
			view.Release()
			return
		}
		proto = header.IPv6ProtocolNumber
	default:
		view.Release()
		return
	}
	pb := stack.NewPacketBuffer(stack.PacketBufferOptions{Payload: splitHeaders(view, tcpHeadersEnd(pkt, proto))})
	d.DeliverNetworkPacket(proto, pb)
	pb.DecRef()
}

// ipv6UpperProtocol 返回跳过扩展头后的上层协议号。协议栈在扩展头后照样处理 ICMPv6，不能
// 只看固定头。只跳协议栈会解析的几种扩展头；头不完整时返回停下处的类型，协议栈会把这种包
// 当畸形包丢弃。
func ipv6UpperProtocol(pkt []byte) uint8 {
	next, off := pkt[6], header.IPv6MinimumSize
	for {
		var size int
		switch header.IPv6ExtensionHeaderIdentifier(next) {
		case header.IPv6FragmentExtHdrIdentifier:
			size = header.IPv6FragmentExtHdrLength
		case header.IPv6HopByHopOptionsExtHdrIdentifier, header.IPv6RoutingExtHdrIdentifier, header.IPv6DestinationOptionsExtHdrIdentifier:
			if len(pkt) < off+2 {
				return next
			}
			size = (int(pkt[off+1]) + 1) * 8
		default:
			return next
		}
		if len(pkt) < off+size {
			return next
		}
		// 非首片的分片头之后是载荷中段，只能取分片头里的 Next Header。首片带着直到上层协议头的
		// 全部头部（RFC 8200 §4.5），藏在后续扩展头之后的 ICMPv6 由首片拦下，整个报文就无法重组。
		if next == uint8(header.IPv6FragmentExtHdrIdentifier) && binary.BigEndian.Uint16(pkt[off+2:])>>3 != 0 {
			return pkt[off]
		}
		next, off = pkt[off], off+size
	}
}

// 载荷不小于这个长度时才把头部单独拆出来，更小的包整块克隆也不比拆分贵。
const splitMinPayload = 512

// splitHeaders 把 view 的前 n 字节（IP 头加 TCP 头）拷进单独的小块，载荷留在原块里。
//
// 协议栈解析 IP 头时会对头所在的块再取一个引用，随后取头又发现这块被共享，于是把整块克隆
// 一份——不拆开的话，每个入站的大段都要被完整复制一次。拆分点必须恰好是 TCP 头的末尾：
// 头部跨块时协议栈会把涉及的块合并成一块，同样复制了载荷。
func splitHeaders(view *buffer.View, n int) buffer.Buffer {
	if n == 0 || view.Size()-n < splitMinPayload {
		return buffer.MakeWithView(view)
	}
	b := buffer.MakeWithView(buffer.NewViewWithData(view.AsSlice()[:n]))
	view.TrimFront(n)
	// Append 会先把载荷挪进头块的剩余容量，Merge 原样挂上。
	payload := buffer.MakeWithView(view)
	b.Merge(&payload)
	return b
}

// tcpHeadersEnd 返回 TCP 包里 IP 头加 TCP 头的长度；不是 TCP、是分片或头不完整时返回 0。
// IPv6 只认 TCP 紧跟在固定头之后的情形。
func tcpHeadersEnd(pkt []byte, proto tcpip.NetworkProtocolNumber) int {
	ipLen := header.IPv6MinimumSize
	if proto == header.IPv4ProtocolNumber {
		ip := header.IPv4(pkt)
		if ip.Protocol() != uint8(header.TCPProtocolNumber) || ip.More() || ip.FragmentOffset() != 0 {
			return 0
		}
		ipLen = int(ip.HeaderLength())
	} else if header.IPv6(pkt).NextHeader() != uint8(header.TCPProtocolNumber) {
		return 0
	}
	if len(pkt) < ipLen+header.TCPMinimumSize {
		return 0
	}
	end := ipLen + int(header.TCP(pkt[ipLen:]).DataOffset())
	if end < ipLen+header.TCPMinimumSize || end > len(pkt) {
		return 0
	}
	return end
}
