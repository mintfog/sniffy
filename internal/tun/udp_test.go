// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package tun

import (
	"bytes"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

// loopbackUDP 是测试用的出站 socket：把收到的数据报原样作为「回包」送回，回包的源地址是
// 数据报的目标地址；replyFrom 非零时改用它作源地址。
type loopbackUDP struct {
	replyFrom netip.AddrPort
	packets   chan udpPacket
	closeOnce sync.Once
	closed    chan struct{}
}

type udpPacket struct {
	payload []byte
	addr    netip.AddrPort
}

func newLoopbackUDP() *loopbackUDP {
	return &loopbackUDP{packets: make(chan udpPacket, 64), closed: make(chan struct{})}
}

func (c *loopbackUDP) WriteToUDPAddrPort(b []byte, addr netip.AddrPort) (int, error) {
	if c.replyFrom.IsValid() {
		addr = c.replyFrom
	}
	select {
	case c.packets <- udpPacket{bytes.Clone(b), addr}:
	case <-c.closed:
		return 0, net.ErrClosed
	}
	return len(b), nil
}

func (c *loopbackUDP) ReadFromUDPAddrPort(b []byte) (int, netip.AddrPort, error) {
	select {
	case p := <-c.packets:
		return copy(b, p.payload), p.addr, nil
	case <-c.closed:
		return 0, netip.AddrPort{}, net.ErrClosed
	}
}

func (c *loopbackUDP) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return nil
}

func (c *loopbackUDP) isClosed() bool {
	select {
	case <-c.closed:
		return true
	default:
		return false
	}
}

// newTestRelay 返回一个未挂到协议栈上的中继，以及按创建顺序记录出站 socket 的切片。
func newTestRelay(t *testing.T) (*udpRelay, *[]*loopbackUDP) {
	t.Helper()
	var conns []*loopbackUDP
	relay := newUDPRelay(func(string) (PacketConn, error) {
		conn := newLoopbackUDP()
		conns = append(conns, conn)
		return conn, nil
	}, false)
	t.Cleanup(relay.close)
	return relay, &conns
}

func TestUDPSessionReusedPerSource(t *testing.T) {
	relay, conns := newTestRelay(t)
	a := netip.MustParseAddrPort("172.29.113.1:40000")
	b := netip.MustParseAddrPort("172.29.113.1:40001")
	if relay.session(a) != relay.session(a) {
		t.Error("同一源地址应复用同一条映射")
	}
	if relay.session(a) == relay.session(b) {
		t.Error("不同源端口应各有一条映射")
	}
	if len(*conns) != 2 {
		t.Errorf("出站 socket 数 = %d，应为 2", len(*conns))
	}
}

func TestUDPSweepIdleSessions(t *testing.T) {
	relay, conns := newTestRelay(t)
	now := time.Now()
	idleFor := func(sess *udpSession, d time.Duration) { sess.lastActive.Store(now.Add(-d).UnixNano()) }

	dns := relay.session(netip.MustParseAddrPort("172.29.113.1:40000"))
	idleFor(dns, udpDNSIdleTimeout+time.Second)

	other := relay.session(netip.MustParseAddrPort("172.29.113.1:40001"))
	other.dnsOnly.Store(false)
	idleFor(other, udpDNSIdleTimeout+time.Second)

	expired := relay.session(netip.MustParseAddrPort("172.29.113.1:40002"))
	expired.dnsOnly.Store(false)
	idleFor(expired, udpIdleTimeout+time.Second)

	relay.sweep(now)

	if !(*conns)[0].isClosed() {
		t.Error("只发过 DNS 的映射应按较短的空闲时间回收")
	}
	if (*conns)[1].isClosed() {
		t.Error("发过其它数据报的映射在 2 分钟内不应回收")
	}
	if !(*conns)[2].isClosed() {
		t.Error("空闲超过 2 分钟的映射应回收")
	}
	relay.mu.Lock()
	remaining := len(relay.sessions)
	relay.mu.Unlock()
	if remaining != 1 {
		t.Errorf("剩余映射数 = %d，应为 1", remaining)
	}
}

func TestUDPEvictsOldestAtLimit(t *testing.T) {
	relay, conns := newTestRelay(t)
	base := time.Now().Add(-time.Hour)
	for i := range maxUDPSessions {
		sess := relay.session(netip.AddrPortFrom(addr4.Addr(), uint16(10000+i)))
		sess.lastActive.Store(base.Add(time.Duration(i) * time.Second).UnixNano())
	}
	oldest, second := (*conns)[0], (*conns)[1]

	relay.session(netip.AddrPortFrom(addr4.Addr(), 9999))
	if !oldest.isClosed() {
		t.Error("达到上限后应顶替空闲最久的映射")
	}
	if second.isClosed() {
		t.Error("只应顶替一条映射")
	}
	relay.mu.Lock()
	n := len(relay.sessions)
	relay.mu.Unlock()
	if n != maxUDPSessions {
		t.Errorf("映射数 = %d，应保持在上限 %d", n, maxUDPSessions)
	}
}

func TestUDPCloseReleasesSessions(t *testing.T) {
	relay, conns := newTestRelay(t)
	relay.session(netip.MustParseAddrPort("172.29.113.1:40000"))
	relay.close()
	if !(*conns)[0].isClosed() {
		t.Error("关闭中继应关闭所有出站 socket")
	}
	if relay.session(netip.MustParseAddrPort("172.29.113.1:40001")) != nil {
		t.Error("关闭后不应再建立映射")
	}
}

// stalledUDP 是发送缓冲已满的出站 socket：读写都阻塞到关闭。
type stalledUDP struct {
	closeOnce sync.Once
	closed    chan struct{}
}

func (c *stalledUDP) WriteToUDPAddrPort([]byte, netip.AddrPort) (int, error) {
	<-c.closed
	return 0, net.ErrClosed
}

func (c *stalledUDP) ReadFromUDPAddrPort([]byte) (int, netip.AddrPort, error) {
	<-c.closed
	return 0, netip.AddrPort{}, net.ErrClosed
}

func (c *stalledUDP) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return nil
}

// 映射关闭时，排队中的数据报与关闭后才入队的数据报都要释放对包的引用。
func TestUDPCloseReleasesQueuedPackets(t *testing.T) {
	relay := newUDPRelay(func(string) (PacketConn, error) {
		return &stalledUDP{closed: make(chan struct{})}, nil
	}, false)
	t.Cleanup(relay.close)
	sess := relay.session(netip.MustParseAddrPort("172.29.113.1:40000"))
	dst := netip.MustParseAddrPort("203.0.113.9:5353")

	// 测试自己留一个引用，包释放后仍可读引用计数。
	var pkts []*stack.PacketBuffer
	send := func() {
		pkt := stack.NewPacketBuffer(stack.PacketBufferOptions{Payload: buffer.MakeWithData([]byte("x"))})
		pkts = append(pkts, pkt)
		sess.enqueue(udpDatagram{pkt: pkt.IncRef(), size: 1, dst: dst})
	}
	released := func() bool {
		for _, pkt := range pkts {
			if pkt.ReadRefs() != 1 {
				return false
			}
		}
		return true
	}

	// 一个卡在出站 socket 上，其余填满队列，再多的被丢弃。
	for range udpSendQueueLen + 2 {
		send()
	}
	relay.close()
	for deadline := time.Now().Add(2 * time.Second); !released(); time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("关闭映射后仍有数据报持有包的引用")
		}
	}

	send()
	if !released() {
		t.Fatal("关闭后入队的数据报应立即释放")
	}
	for _, pkt := range pkts {
		pkt.DecRef()
	}
}
