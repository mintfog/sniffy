// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package tun

import (
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/checksum"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
)

const (
	quicPort = 443
	dnsPort  = 53

	// 映射的空闲回收时间。RFC 4787 要求 NAT 的 UDP 映射至少保持 2 分钟；只发过 DNS 查询的
	// 映射例外——解析器常常每次查询换一个源端口，按 2 分钟保留会积压大量 socket。
	udpIdleTimeout    = 2 * time.Minute
	udpDNSIdleTimeout = 10 * time.Second
	udpSweepInterval  = 5 * time.Second

	// 映射数上限，每条映射占一个出站 socket 与一块 64KB 的读缓冲。达到上限后新映射顶替
	// 空闲最久的那条。
	maxUDPSessions = 2048

	// 每条映射待发数据报的排队上限，出站 socket 来不及发时超出的部分被丢弃。
	udpSendQueueLen = 128

	maxUDPPayload = 65535
)

// udpRelay 中继虚拟网卡上的 UDP。每个应用 socket（源地址 + 源端口）对应一个出站 socket，
// 发往不同目标的数据报共用它，回包不论来自哪个地址都送回应用：映射与目标无关，应用经
// STUN 探测到的外部端口对所有对端一致，P2P 打洞不受影响。
type udpRelay struct {
	listen    func(network string) (PacketConn, error)
	blockQUIC bool
	stack     *stack.Stack

	mu       sync.Mutex
	sessions map[netip.AddrPort]*udpSession
	closed   bool
	stop     chan struct{}
}

type udpSession struct {
	src  netip.AddrPort
	conn PacketConn

	// 应用发出的数据报经 sendQueue 交给 sendLoop 写出站 socket。协议栈的 UDP 入口运行在
	// 链路层的读循环里，出站 socket 的发送缓冲写满时不能让它跟着阻塞，否则所有流量一起停。
	sendQueue chan udpDatagram
	closed    chan struct{}
	closeOnce sync.Once

	lastActive atomic.Int64 // UnixNano
	dnsOnly    atomic.Bool
}

// udpDatagram 是一个待发的数据报：pkt 的数据区开头 size 字节是载荷，持有 pkt 的一个引用。
type udpDatagram struct {
	pkt  *stack.PacketBuffer
	size int
	dst  netip.AddrPort
}

func (s *udpSession) close() {
	s.closeOnce.Do(func() {
		close(s.closed)
		s.conn.Close()
	})
}

func newUDPRelay(listen func(network string) (PacketConn, error), blockQUIC bool) *udpRelay {
	return &udpRelay{
		listen:    listen,
		blockQUIC: blockQUIC,
		sessions:  make(map[netip.AddrPort]*udpSession),
		stop:      make(chan struct{}),
	}
}

func (r *udpRelay) attach(s *stack.Stack) {
	r.stack = s
	go r.sweepLoop()
}

// handlePacket 是协议栈的 UDP 入口，在链路层的读循环里被调用。返回 false 表示不接收，
// 协议栈随即回 ICMP 端口不可达。
func (r *udpRelay) handlePacket(id stack.TransportEndpointID, pkt *stack.PacketBuffer) bool {
	if r.blockQUIC && id.LocalPort == quicPort {
		return false
	}
	hdr := header.UDP(pkt.TransportHeader().Slice())
	netHdr := pkt.Network()
	lengthValid, csumValid := header.UDPValid(
		hdr,
		func() uint16 { return pkt.Data().Checksum() },
		uint16(pkt.Data().Size()),
		pkt.NetworkProtocolNumber,
		netHdr.SourceAddress(),
		netHdr.DestinationAddress(),
		pkt.RXChecksumValidated)
	if !lengthValid || !csumValid {
		return true
	}

	src := netip.AddrPortFrom(toNetip(id.RemoteAddress), id.RemotePort)
	dst := netip.AddrPortFrom(toNetip(id.LocalAddress), id.LocalPort)
	sess := r.session(src)
	if sess == nil {
		return true
	}
	if dst.Port() != dnsPort {
		sess.dnsOnly.Store(false)
	}
	sess.lastActive.Store(time.Now().UnixNano())
	sess.enqueue(udpDatagram{pkt: pkt.IncRef(), size: int(hdr.Length()) - header.UDPMinimumSize, dst: dst})
	return true
}

// enqueue 把数据报交给 sendLoop，队列满时丢弃。映射关闭后 sendLoop 排空队列即退出，赶在关闭
// 前取到映射、关闭后才入队的数据报由这里释放。
func (s *udpSession) enqueue(d udpDatagram) {
	select {
	case s.sendQueue <- d:
	default:
		d.pkt.DecRef()
		return
	}
	select {
	case <-s.closed:
		s.drainQueue()
	default:
	}
}

func (s *udpSession) drainQueue() {
	for {
		select {
		case d := <-s.sendQueue:
			d.pkt.DecRef()
		default:
			return
		}
	}
}

func (s *udpSession) sendLoop() {
	for {
		select {
		case <-s.closed:
			s.drainQueue()
			return
		case d := <-s.sendQueue:
			// 出站失败（目标不可达等）与真实网络丢包对应用来说没有区别，不另作处理。
			if payload, ok := d.pkt.Data().PullUp(d.size); ok {
				s.conn.WriteToUDPAddrPort(payload, d.dst)
			}
			d.pkt.DecRef()
		}
	}
}

func (r *udpRelay) session(src netip.AddrPort) *udpSession {
	r.mu.Lock()
	defer r.mu.Unlock()
	if sess, ok := r.sessions[src]; ok {
		return sess
	}
	if r.closed {
		return nil
	}
	if len(r.sessions) >= maxUDPSessions {
		r.evictOldestLocked()
	}
	network := "udp4"
	if src.Addr().Is6() {
		network = "udp6"
	}
	conn, err := r.listen(network)
	if err != nil {
		return nil
	}
	sess := &udpSession{
		src:       src,
		conn:      conn,
		sendQueue: make(chan udpDatagram, udpSendQueueLen),
		closed:    make(chan struct{}),
	}
	sess.dnsOnly.Store(true)
	sess.lastActive.Store(time.Now().UnixNano())
	r.sessions[src] = sess
	go r.readLoop(sess)
	go sess.sendLoop()
	return sess
}

// evictOldestLocked 关闭空闲最久的映射，它的读循环随后退出。
func (r *udpRelay) evictOldestLocked() {
	var oldest *udpSession
	for _, sess := range r.sessions {
		if oldest == nil || sess.lastActive.Load() < oldest.lastActive.Load() {
			oldest = sess
		}
	}
	if oldest != nil {
		delete(r.sessions, oldest.src)
		oldest.close()
	}
}

func (r *udpRelay) readLoop(sess *udpSession) {
	defer r.remove(sess)
	buf := make([]byte, maxUDPPayload)
	for {
		n, from, err := sess.conn.ReadFromUDPAddrPort(buf)
		if err != nil {
			return
		}
		sess.lastActive.Store(time.Now().UnixNano())
		// 绑在未指定地址上的 socket 读到的 IPv4 源地址可能是 IPv4 映射的 IPv6 形式。
		from = netip.AddrPortFrom(from.Addr().Unmap(), from.Port())
		r.writeBack(from, sess.src, buf[:n])
	}
}

func (r *udpRelay) remove(sess *udpSession) {
	sess.close()
	r.mu.Lock()
	if r.sessions[sess.src] == sess {
		delete(r.sessions, sess.src)
	}
	r.mu.Unlock()
}

// writeBack 把一个回包以 from 为源地址经虚拟网卡送回应用。
func (r *udpRelay) writeBack(from, to netip.AddrPort, payload []byte) {
	if from.Addr().Is4() != to.Addr().Is4() {
		return
	}
	proto := header.IPv4ProtocolNumber
	if to.Addr().Is6() {
		proto = header.IPv6ProtocolNumber
	}
	route, err := r.stack.FindRoute(nicID, toTCPIP(from.Addr()), toTCPIP(to.Addr()), proto, false)
	if err != nil {
		return
	}
	defer route.Release()

	pkt := stack.NewPacketBuffer(stack.PacketBufferOptions{
		ReserveHeaderBytes: header.UDPMinimumSize + int(route.MaxHeaderLength()),
		Payload:            buffer.MakeWithData(payload),
	})
	defer pkt.DecRef()

	length := uint16(header.UDPMinimumSize + len(payload))
	hdr := header.UDP(pkt.TransportHeader().Push(header.UDPMinimumSize))
	pkt.TransportProtocolNumber = udp.ProtocolNumber
	hdr.Encode(&header.UDPFields{SrcPort: from.Port(), DstPort: to.Port(), Length: length})
	xsum := checksum.Combine(route.PseudoHeaderChecksum(udp.ProtocolNumber, length), pkt.Data().Checksum())
	// 算出的校验和为 0 时按 RFC 768 以全 1 发送，0 在线上表示「未计算」。
	if xsum = ^hdr.CalculateChecksum(xsum); xsum == 0 {
		xsum = 0xffff
	}
	hdr.SetChecksum(xsum)

	route.WritePacket(stack.NetworkHeaderParams{
		Protocol: udp.ProtocolNumber,
		TTL:      route.DefaultTTL(),
		TOS:      stack.DefaultTOS,
	}, pkt)
}

func (r *udpRelay) sweepLoop() {
	ticker := time.NewTicker(udpSweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-r.stop:
			return
		case now := <-ticker.C:
			r.sweep(now)
		}
	}
}

// sweep 关闭空闲超时的映射。
func (r *udpRelay) sweep(now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for src, sess := range r.sessions {
		timeout := udpIdleTimeout
		if sess.dnsOnly.Load() {
			timeout = udpDNSIdleTimeout
		}
		if now.Sub(time.Unix(0, sess.lastActive.Load())) >= timeout {
			delete(r.sessions, src)
			sess.close()
		}
	}
}

// reset 关闭源地址属于 IPv6（ipv6 为 true）或 IPv4 的全部映射，它们的读循环随后退出。
func (r *udpRelay) reset(ipv6 bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for src, sess := range r.sessions {
		if src.Addr().Is6() == ipv6 {
			delete(r.sessions, src)
			sess.close()
		}
	}
}

func (r *udpRelay) close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.closed = true
	close(r.stop)
	for src, sess := range r.sessions {
		delete(r.sessions, src)
		sess.close()
	}
}

func toNetip(a tcpip.Address) netip.Addr {
	addr, _ := netip.AddrFromSlice(a.AsSlice())
	return addr
}

func toTCPIP(a netip.Addr) tcpip.Address {
	return tcpip.AddrFromSlice(a.AsSlice())
}
