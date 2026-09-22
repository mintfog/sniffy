// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build darwin

package process

import (
	"encoding/binary"
	"fmt"
	"net/netip"
	"strconv"
	"sync"

	"github.com/ebitengine/purego"
	"golang.org/x/sys/unix"
)

// 以下布局来自 macOS SDK 的 sys/proc_info.h，arm64 与 amd64 相同。
// TestDarwinLibprocABI 使用本机 SDK 核对实际访问的大小及偏移。
const (
	darwinSocketSize = 792
	darwinBSDSize    = 136
	darwinFDSize     = 8

	darwinProtocolOffset = 180
	darwinFamilyOffset   = 184
	darwinKindOffset     = 256
	darwinInetOffset     = 264
	darwinStateOffset    = 344

	darwinForeignPortOffset = 0
	darwinLocalPortOffset   = 4
	darwinVersionOffset     = 24
	darwinForeignAddrOffset = 32
	darwinLocalAddrOffset   = 48
	darwinInterfaceOffset   = 76

	darwinUIDOffset  = 20
	darwinCommOffset = 48
	darwinNameOffset = 64
)

const (
	procPIDListFDs         = 1
	procPIDTBSDInfo        = 3
	procPIDFDSocketInfo    = 3
	procFDSocket           = 2
	socketInfoTCP          = 2
	darwinTCPEstablished   = 4
	darwinTCPLastConnected = 9
)

type darwinLibproc struct {
	listPIDs func(buffer *int32, size int32) int32
	pidInfo  func(pid int32, flavor int32, arg uint64, buffer *byte, size int32) int32
	fdInfo   func(pid int32, fd int32, flavor int32, buffer *byte, size int32) int32
	pidPath  func(pid int32, buffer *byte, size uint32) int32
}

var loadDarwinLibproc = sync.OnceValues(func() (*darwinLibproc, error) {
	handle, err := purego.Dlopen("/usr/lib/libproc.dylib", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		return nil, fmt.Errorf("加载 libproc: %w", err)
	}
	api := &darwinLibproc{}
	for _, symbol := range []struct {
		name   string
		target any
	}{
		{"proc_listallpids", &api.listPIDs},
		{"proc_pidinfo", &api.pidInfo},
		{"proc_pidfdinfo", &api.fdInfo},
		{"proc_pidpath", &api.pidPath},
	} {
		address, err := purego.Dlsym(handle, symbol.name)
		if err != nil {
			_ = purego.Dlclose(handle)
			return nil, fmt.Errorf("加载 %s: %w", symbol.name, err)
		}
		purego.RegisterFunc(symbol.target, address)
	}
	// 函数指针在整个进程生命周期内使用，库句柄必须保持打开。
	return api, nil
})

type darwinConnectionKey struct {
	local  netip.AddrPort
	remote netip.AddrPort
}

type darwinConnection struct {
	darwinConnectionKey
	pid   uint32
	fd    int32
	state uint32
}

func (api *darwinLibproc) connections() ([]darwinConnection, error) {
	pids, err := api.readPIDs()
	if err != nil {
		return nil, err
	}

	var connections []darwinConnection
	fdBuffer := make([]byte, 256*darwinFDSize)
	var socketBuffer [darwinSocketSize]byte
	for _, pid := range pids {
		if pid <= 0 {
			continue
		}
		fdBuffer = api.readFDs(pid, fdBuffer)
		for offset := 0; offset < len(fdBuffer); offset += darwinFDSize {
			if binary.LittleEndian.Uint32(fdBuffer[offset+4:]) != procFDSocket {
				continue
			}
			fd := int32(binary.LittleEndian.Uint32(fdBuffer[offset:]))
			if api.fdInfo(pid, fd, procPIDFDSocketInfo, &socketBuffer[0], darwinSocketSize) != darwinSocketSize {
				continue
			}
			conn, ok := parseDarwinSocket(socketBuffer[:])
			if !ok {
				continue
			}
			conn.pid, conn.fd = uint32(pid), fd
			connections = append(connections, conn)
		}
	}
	return connections, nil
}

func (api *darwinLibproc) readPIDs() ([]int32, error) {
	count := api.listPIDs(nil, 0)
	if count <= 0 {
		return nil, fmt.Errorf("读取进程列表失败")
	}
	for range 8 {
		pids := make([]int32, int(count)+128)
		count = api.listPIDs(&pids[0], int32(len(pids)*4))
		if count < 0 {
			return nil, fmt.Errorf("读取进程列表失败")
		}
		if int(count) < len(pids) {
			return pids[:count], nil
		}
	}
	return nil, fmt.Errorf("进程列表持续增长")
}

// readFDs 复用 buffer 的容量；进程退出、不可访问或列表不完整时返回空切片。
func (api *darwinLibproc) readFDs(pid int32, buffer []byte) []byte {
	buffer = buffer[:cap(buffer)]
	required := 0
	for range 8 {
		// 空缓冲查询返回所需字节数及内核预留量。
		sizeHint := int(api.pidInfo(pid, procPIDListFDs, 0, nil, 0))
		if sizeHint <= 0 {
			return buffer[:0]
		}
		required = max(required, sizeHint)
		if required > (1<<31-1)-darwinFDSize {
			return buffer[:0]
		}
		// 多留一个槽位；返回值填满缓冲区时，仍需扩容重读以排除截断。
		if required+darwinFDSize > len(buffer) {
			buffer = make([]byte, required+darwinFDSize)
		}
		size := int(api.pidInfo(pid, procPIDListFDs, 0, &buffer[0], int32(len(buffer))))
		if size <= 0 || size%darwinFDSize != 0 {
			return buffer[:0]
		}
		if size < len(buffer) {
			return buffer[:size]
		}

		required = len(buffer) * 2
	}
	return buffer[:0]
}

func (api *darwinLibproc) ownsConnection(conn darwinConnection) bool {
	var buffer [darwinSocketSize]byte
	if api.fdInfo(int32(conn.pid), conn.fd, procPIDFDSocketInfo, &buffer[0], darwinSocketSize) != darwinSocketSize {
		return false
	}
	current, ok := parseDarwinSocket(buffer[:])
	return ok && current.darwinConnectionKey == conn.darwinConnectionKey
}

func parseDarwinSocket(buffer []byte) (darwinConnection, bool) {
	var conn darwinConnection
	if len(buffer) != darwinSocketSize {
		return conn, false
	}
	if binary.LittleEndian.Uint32(buffer[darwinKindOffset:]) != socketInfoTCP ||
		binary.LittleEndian.Uint32(buffer[darwinProtocolOffset:]) != unix.IPPROTO_TCP {
		return conn, false
	}
	family := binary.LittleEndian.Uint32(buffer[darwinFamilyOffset:])
	if family != unix.AF_INET && family != unix.AF_INET6 {
		return conn, false
	}
	conn.state = binary.LittleEndian.Uint32(buffer[darwinStateOffset:])
	// 已半关闭但仍由进程持有的 socket 也可用于异步补全。
	if conn.state < darwinTCPEstablished || conn.state > darwinTCPLastConnected {
		return conn, false
	}
	inet := buffer[darwinInetOffset:]
	localPort := binary.BigEndian.Uint16(inet[darwinLocalPortOffset:])
	remotePort := binary.BigEndian.Uint16(inet[darwinForeignPortOffset:])
	if localPort == 0 || remotePort == 0 {
		return conn, false
	}
	var localIP, remoteIP netip.Addr
	switch {
	case inet[darwinVersionOffset]&1 != 0:
		// in4in6_addr 将 IPv4 存在 16 字节地址槽的最后 4 字节。
		localIP = netip.AddrFrom4([4]byte(inet[darwinLocalAddrOffset+12 : darwinLocalAddrOffset+16]))
		remoteIP = netip.AddrFrom4([4]byte(inet[darwinForeignAddrOffset+12 : darwinForeignAddrOffset+16]))
	case inet[darwinVersionOffset]&2 != 0:
		scope := binary.LittleEndian.Uint16(inet[darwinInterfaceOffset:])
		localIP = darwinIPv6([16]byte(inet[darwinLocalAddrOffset:darwinLocalAddrOffset+16]), scope)
		remoteIP = darwinIPv6([16]byte(inet[darwinForeignAddrOffset:darwinForeignAddrOffset+16]), scope)
	default:
		return conn, false
	}
	conn.local = netip.AddrPortFrom(localIP, localPort)
	conn.remote = netip.AddrPortFrom(remoteIP, remotePort)
	return conn, true
}

func darwinIPv6(raw [16]byte, scope uint16) netip.Addr {
	ip := netip.AddrFrom16(raw).Unmap()
	if !ip.Is6() || !ip.IsLinkLocalUnicast() {
		return ip
	}
	// Darwin 的内核地址可能将 scope 嵌入第 3、4 字节（KAME 表示）。
	if embedded := binary.BigEndian.Uint16(raw[2:4]); embedded != 0 {
		scope = embedded
	}
	raw[2], raw[3] = 0, 0
	ip = netip.AddrFrom16(raw)
	if scope != 0 {
		ip = ip.WithZone(strconv.Itoa(int(scope)))
	}
	return ip
}
