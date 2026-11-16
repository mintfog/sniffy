// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package tun

import (
	"encoding/binary"
	"os"
	"sync"

	"golang.org/x/sys/unix"
	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/rawfile"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

const (
	// struct virtio_net_hdr（linux/virtio_net.h）的长度。网卡以 IFF_VNET_HDR 打开，每次读写
	// 的包前面都带这个头。
	virtioHdrLen = 10

	virtioFlagNeedsCsum = 1 // VIRTIO_NET_HDR_F_NEEDS_CSUM
	virtioGSOTCPv4      = 1 // VIRTIO_NET_HDR_GSO_TCPV4
	virtioGSOTCPv6      = 4 // VIRTIO_NET_HDR_GSO_TCPV6

	// 读缓冲取协议栈缓冲池能提供的最大块，装得下 virtio 头加一个最大的包（maxPacketSize，
	// 内核分段卸载的大段也不超过它）。
	readBufSize = buffer.MaxChunkSize

	// 读到的包不小于这个长度时把读缓冲直接交给协议栈、另取一块新的，省掉一次拷贝；更小的
	// 包拷进大小合适的缓冲，免得一个 ACK 占住 64KB。
	zeroCopyThreshold = 16 << 10
)

// vnetEndpoint 是 Linux 上的链路层，直接读写带 virtio 头的 TUN 设备。
//
// 网卡开启了 TSO 与校验和卸载，内核与协议栈之间交换的 TCP 数据是未经分段、未算校验和的
// 大段（最大 64KB）：内核发来的大段原样交给协议栈，协议栈发出的大段带着分段参数写回内核。
// 两个方向都省去按 MTU 拆包与逐包处理，这是 Linux 上吞吐的主要来源。
type vnetEndpoint struct {
	file   *os.File
	fd     int
	mtu    uint32
	onDown func()

	// 写路径直接对 fd 发系统调用，mu 保证关闭之后 fd 号不再被使用。
	mu     sync.RWMutex
	closed bool

	dispatcher stack.NetworkDispatcher // 受 mu 保护
	readers    sync.WaitGroup
}

var (
	_ stack.LinkEndpoint = (*vnetEndpoint)(nil)
	_ stack.GSOEndpoint  = (*vnetEndpoint)(nil)
)

// newVnetEndpoint 接管 fd。fd 必须是非阻塞的、以 IFF_VNET_HDR 打开并已开启 TSO 与校验和卸载的
// TUN 设备。
func newVnetEndpoint(fd int, mtu int, onDown func()) *vnetEndpoint {
	return &vnetEndpoint{
		file:   os.NewFile(uintptr(fd), "/dev/net/tun"),
		fd:     fd,
		mtu:    uint32(mtu),
		onDown: onDown,
	}
}

func (e *vnetEndpoint) shutdown() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil
	}
	e.closed = true
	return e.file.Close()
}

func (e *vnetEndpoint) Attach(dispatcher stack.NetworkDispatcher) {
	e.mu.Lock()
	e.dispatcher = dispatcher
	e.mu.Unlock()
	if dispatcher == nil {
		e.shutdown()
		e.readers.Wait()
		return
	}
	e.readers.Add(1)
	go e.readLoop(dispatcher)
}

func (e *vnetEndpoint) readLoop(dispatcher stack.NetworkDispatcher) {
	defer e.readers.Done()
	defer e.onDown()
	view := buffer.NewViewSize(readBufSize)
	defer func() { view.Release() }()
	for {
		// 这块缓冲刚从池里取出、只有这里持有，可以直接写入。
		n, err := e.file.Read(view.AsSlice())
		if err != nil {
			return
		}
		if n <= virtioHdrLen {
			continue
		}
		if n < zeroCopyThreshold {
			deliver(dispatcher, buffer.NewViewWithData(view.AsSlice()[virtioHdrLen:n]))
			continue
		}
		view.CapLength(n)
		view.TrimFront(virtioHdrLen)
		deliver(dispatcher, view)
		view = buffer.NewViewSize(readBufSize)
	}
}

func (e *vnetEndpoint) WritePackets(pkts stack.PacketBufferList) (int, tcpip.Error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.closed {
		return 0, &tcpip.ErrClosedForSend{}
	}
	// 包与包之间互不相干，一个写失败（内核一时没有缓冲等）不耽误其余的。
	sent := 0
	var firstErr tcpip.Error
	for _, pkt := range pkts.AsSlice() {
		if err := e.writePacket(pkt); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		sent++
	}
	return sent, firstErr
}

func (e *vnetEndpoint) writePacket(pkt *stack.PacketBuffer) tcpip.Error {
	var hdr [virtioHdrLen]byte
	if gso := pkt.GSOOptions; gso.Type != stack.GSONone {
		if gso.NeedsCsum {
			hdr[0] = virtioFlagNeedsCsum
			binary.LittleEndian.PutUint16(hdr[6:], gso.L3HdrLen)   // csum_start
			binary.LittleEndian.PutUint16(hdr[8:], gso.CsumOffset) // csum_offset
		}
		if pkt.Data().Size() > int(gso.MSS) {
			if gso.Type == stack.GSOTCPv6 {
				hdr[1] = virtioGSOTCPv6
			} else {
				hdr[1] = virtioGSOTCPv4
			}
			binary.LittleEndian.PutUint16(hdr[2:], uint16(pkt.HeaderSize())) // hdr_len
			binary.LittleEndian.PutUint16(hdr[4:], gso.MSS)                  // gso_size
		}
	}

	views, offset := pkt.AsViewList()
	var iovecArr [8]unix.Iovec
	iovecs := iovecArr[:0]
	if n := views.Len() + 1; n > len(iovecArr) {
		iovecs = make([]unix.Iovec, 0, n)
	}
	iovecs = append(iovecs, rawfile.IovecFromBytes(hdr[:]))
	for view := views.Front(); view != nil; view = view.Next() {
		b := view.AsSlice()
		if offset >= len(b) {
			offset -= len(b)
			continue
		}
		iovecs = append(iovecs, rawfile.IovecFromBytes(b[offset:]))
		offset = 0
	}
	if errno := rawfile.NonBlockingWriteIovec(e.fd, iovecs); errno != 0 {
		return tcpip.TranslateErrno(errno)
	}
	return nil
}

func (e *vnetEndpoint) GSOMaxSize() uint32                   { return maxPacketSize }
func (e *vnetEndpoint) SupportedGSO() stack.SupportedGSO     { return stack.HostGSOSupported }
func (e *vnetEndpoint) MTU() uint32                          { return e.mtu }
func (e *vnetEndpoint) SetMTU(uint32)                        {}
func (e *vnetEndpoint) MaxHeaderLength() uint16              { return 0 }
func (e *vnetEndpoint) LinkAddress() tcpip.LinkAddress       { return "" }
func (e *vnetEndpoint) SetLinkAddress(tcpip.LinkAddress)     {}
func (e *vnetEndpoint) Wait()                                { e.readers.Wait() }
func (e *vnetEndpoint) AddHeader(*stack.PacketBuffer)        {}
func (e *vnetEndpoint) ParseHeader(*stack.PacketBuffer) bool { return true }
func (e *vnetEndpoint) Close()                               { e.shutdown() }
func (e *vnetEndpoint) SetOnCloseAction(func())              {}

func (e *vnetEndpoint) ARPHardwareType() header.ARPHardwareType { return header.ARPHardwareNone }

func (e *vnetEndpoint) IsAttached() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.dispatcher != nil
}

// 发来的包校验和尚未计算（由内核卸载给了网卡），协议栈不能校验。
func (e *vnetEndpoint) Capabilities() stack.LinkEndpointCapabilities {
	return stack.CapabilityRXChecksumOffload
}
