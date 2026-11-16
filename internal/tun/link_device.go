// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package tun

import (
	"sync"

	wgtun "golang.zx2c4.com/wireguard/tun"
	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

// deviceEndpoint 是建立在 wireguard-go 网卡封装之上的链路层，逐包收发，用于 macOS（utun）与
// Windows（wintun）。
type deviceEndpoint struct {
	dev wgtun.Device
	mtu uint32
	// offset 是各平台的网卡封装要求在包前面预留的字节数。
	offset int
	onDown func()

	// 网卡封装的 Write 不允许并发调用，写缓冲也只有一份。
	writeMu  sync.Mutex
	writeBuf []byte
	writeVec [1][]byte

	mu         sync.Mutex
	closed     bool
	dispatcher stack.NetworkDispatcher
	readers    sync.WaitGroup
}

var _ stack.LinkEndpoint = (*deviceEndpoint)(nil)

func newDeviceEndpoint(dev wgtun.Device, mtu, offset int, onDown func()) *deviceEndpoint {
	// 网卡封装经 Events 报告启停与 MTU 变化，通道容量有限，没人读时 macOS 上的监听协程
	// 会阻塞在发送上，关闭网卡后也退不出。这些事件用不上，读空即可；网卡关闭时通道随之关闭。
	go func() {
		for range dev.Events() {
		}
	}()
	return &deviceEndpoint{
		dev:      dev,
		mtu:      uint32(mtu),
		offset:   offset,
		onDown:   onDown,
		writeBuf: make([]byte, offset+maxPacketSize),
	}
}

func (e *deviceEndpoint) shutdown() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil
	}
	e.closed = true
	return e.dev.Close()
}

func (e *deviceEndpoint) Attach(dispatcher stack.NetworkDispatcher) {
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

func (e *deviceEndpoint) readLoop(dispatcher stack.NetworkDispatcher) {
	defer e.readers.Done()
	defer e.onDown()
	batch := e.dev.BatchSize()
	bufs := make([][]byte, batch)
	for i := range bufs {
		bufs[i] = make([]byte, e.offset+maxPacketSize)
	}
	sizes := make([]int, batch)
	for {
		n, err := e.dev.Read(bufs, sizes, e.offset)
		for i := range n {
			if sizes[i] > 0 {
				deliver(dispatcher, buffer.NewViewWithData(bufs[i][e.offset:e.offset+sizes[i]]))
			}
		}
		if err != nil {
			return
		}
	}
}

func (e *deviceEndpoint) WritePackets(pkts stack.PacketBufferList) (int, tcpip.Error) {
	e.writeMu.Lock()
	defer e.writeMu.Unlock()
	sent := 0
	for _, pkt := range pkts.AsSlice() {
		n := e.offset
		views, skip := pkt.AsViewList()
		for view := views.Front(); view != nil; view = view.Next() {
			b := view.AsSlice()
			if skip >= len(b) {
				skip -= len(b)
				continue
			}
			n += copy(e.writeBuf[n:], b[skip:])
			skip = 0
		}
		e.writeVec[0] = e.writeBuf[:n]
		if _, err := e.dev.Write(e.writeVec[:], e.offset); err != nil {
			return sent, &tcpip.ErrClosedForSend{}
		}
		sent++
	}
	return sent, nil
}

func (e *deviceEndpoint) MTU() uint32                          { return e.mtu }
func (e *deviceEndpoint) SetMTU(uint32)                        {}
func (e *deviceEndpoint) MaxHeaderLength() uint16              { return 0 }
func (e *deviceEndpoint) LinkAddress() tcpip.LinkAddress       { return "" }
func (e *deviceEndpoint) SetLinkAddress(tcpip.LinkAddress)     {}
func (e *deviceEndpoint) Wait()                                { e.readers.Wait() }
func (e *deviceEndpoint) AddHeader(*stack.PacketBuffer)        {}
func (e *deviceEndpoint) ParseHeader(*stack.PacketBuffer) bool { return true }
func (e *deviceEndpoint) Close()                               { e.shutdown() }
func (e *deviceEndpoint) SetOnCloseAction(func())              {}

func (e *deviceEndpoint) ARPHardwareType() header.ARPHardwareType { return header.ARPHardwareNone }

func (e *deviceEndpoint) IsAttached() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.dispatcher != nil
}

// 包只在本机的内核与协议栈之间传递，没有会损坏数据的链路，不做接收校验。
func (e *deviceEndpoint) Capabilities() stack.LinkEndpointCapabilities {
	return stack.CapabilityRXChecksumOffload
}
