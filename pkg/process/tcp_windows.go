// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build windows

package process

import (
	"encoding/binary"
	"fmt"
	"net/netip"
	"strconv"
	"unsafe"

	"golang.org/x/sys/windows"
)

var getExtendedTCPTable = windows.NewLazySystemDLL("iphlpapi.dll").NewProc("GetExtendedTcpTable")

type windowsTCPConnection struct {
	local  netip.AddrPort
	remote netip.AddrPort
	pid    uint32
}

func readWindowsTCPTable(family uint32) ([]windowsTCPConnection, error) {
	const tcpTableOwnerPIDConnections = 4
	buffer := make([]byte, 16*1024)
	// 连接表可能在两次系统调用之间增长。
	for range 10 {
		size := uint32(len(buffer))
		// GetExtendedTcpTable 的 Win32 错误码由返回值给出。
		status, _, _ := getExtendedTCPTable.Call(
			uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&size)),
			0, uintptr(family), tcpTableOwnerPIDConnections, 0,
		)
		switch windows.Errno(status) {
		case windows.ERROR_SUCCESS:
			if uint64(size) > uint64(len(buffer)) {
				return nil, fmt.Errorf("TCP 连接表长度超出缓冲区")
			}
			return parseWindowsTCPTable(buffer[:size], family)
		case windows.ERROR_INSUFFICIENT_BUFFER:
			if uint64(size) <= uint64(len(buffer)) {
				return nil, fmt.Errorf("TCP 连接表返回了无效的缓冲区大小")
			}
			buffer = make([]byte, size)
		default:
			return nil, fmt.Errorf("读取 TCP 连接表: %w", windows.Errno(status))
		}
	}
	return nil, fmt.Errorf("TCP 连接表持续增长，重试次数已达上限")
}

func parseWindowsTCPTable(buffer []byte, family uint32) ([]windowsTCPConnection, error) {
	// 表头为 4 字节行数，随后是定长的 MIB_TCPROW_OWNER_PID（24 字节）
	// 或 MIB_TCP6ROW_OWNER_PID（56 字节）；端口占 4 字节槽位，仅前 2 字节有效。
	rowSize := 24
	switch family {
	case windows.AF_INET:
	case windows.AF_INET6:
		rowSize = 56
	default:
		return nil, fmt.Errorf("不支持的 TCP 地址族: %d", family)
	}
	if len(buffer) < 4 {
		return nil, fmt.Errorf("TCP 连接表缺少行数")
	}
	count := binary.LittleEndian.Uint32(buffer)
	if uint64(count) > uint64((len(buffer)-4)/rowSize) {
		return nil, fmt.Errorf("TCP 连接表数据不完整")
	}
	connections := make([]windowsTCPConnection, 0, int(count))
	for i := 0; i < int(count); i++ {
		row := buffer[4+i*rowSize : 4+(i+1)*rowSize]
		var localIP, remoteIP netip.Addr
		var localPort, remotePort uint16
		var state, pid uint32
		// 状态、PID 和 scope 按小端解码；地址及端口按网络字节序解码。
		if family == windows.AF_INET {
			state = binary.LittleEndian.Uint32(row[0:4])
			localIP = netip.AddrFrom4([4]byte(row[4:8]))
			localPort = binary.BigEndian.Uint16(row[8:10])
			remoteIP = netip.AddrFrom4([4]byte(row[12:16]))
			remotePort = binary.BigEndian.Uint16(row[16:18])
			pid = binary.LittleEndian.Uint32(row[20:24])
		} else {
			localIP = netip.AddrFrom16([16]byte(row[0:16]))
			if scope := binary.LittleEndian.Uint32(row[16:20]); scope != 0 {
				localIP = localIP.WithZone(strconv.FormatUint(uint64(scope), 10))
			}
			localPort = binary.BigEndian.Uint16(row[20:22])
			remoteIP = netip.AddrFrom16([16]byte(row[24:40]))
			if scope := binary.LittleEndian.Uint32(row[40:44]); scope != 0 {
				remoteIP = remoteIP.WithZone(strconv.FormatUint(uint64(scope), 10))
			}
			remotePort = binary.BigEndian.Uint16(row[44:46])
			state = binary.LittleEndian.Uint32(row[48:52])
			pid = binary.LittleEndian.Uint32(row[52:56])
		}
		if state != 5 { // MIB_TCP_STATE_ESTAB
			continue
		}
		connections = append(connections, windowsTCPConnection{
			local:  netip.AddrPortFrom(localIP, localPort),
			remote: netip.AddrPortFrom(remoteIP, remotePort),
			pid:    pid,
		})
	}
	return connections, nil
}
