// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build linux

package process

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"os"

	"golang.org/x/sys/unix"
)

// 以下布局来自 linux/netlink.h、linux/inet_diag.h。
const (
	nlmsgHeaderLen      = 16
	inetDiagReqLen      = 56
	inetDiagMsgLen      = 72
	inetDiagInodeOffset = 68
	inetDiagNoCookie    = ^uint32(0)
)

// sockDiagInode 经 sock_diag 按四元组取 TCP socket 的 inode。内核在连接哈希表里直接查找，不必像
// /proc/net/tcp 那样把全部连接格式化成文本再逐行比对。IPv4 的查询也能查到以 IPv4 映射地址工作的
// IPv6 socket：两者在哈希表里按同样的 IPv4 地址登记。
//
// 找不到时返回的错误满足 errors.Is(err, unix.ENOENT)。内核没有 inet_diag 模块时也是这个错误。
func sockDiagInode(local, remote netip.AddrPort) (uint64, error) {
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, unix.NETLINK_SOCK_DIAG)
	if err != nil {
		return 0, os.NewSyscallError("socket", err)
	}
	defer unix.Close(fd)
	// 内核在 sendto 返回之前就排好了应答，期限只防意外。
	if err := unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &unix.Timeval{Sec: 1}); err != nil {
		return 0, os.NewSyscallError("setsockopt", err)
	}
	if err := unix.Sendto(fd, sockDiagRequest(local, remote), 0, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return 0, os.NewSyscallError("sendto", err)
	}
	var buf [512]byte
	n, _, err := unix.Recvfrom(fd, buf[:], 0)
	if err != nil {
		return 0, os.NewSyscallError("recvfrom", err)
	}
	return parseSockDiagReply(buf[:n])
}

func sockDiagRequest(local, remote netip.AddrPort) []byte {
	req := make([]byte, nlmsgHeaderLen+inetDiagReqLen)
	binary.NativeEndian.PutUint32(req[0:], uint32(len(req)))
	binary.NativeEndian.PutUint16(req[4:], unix.SOCK_DIAG_BY_FAMILY)
	binary.NativeEndian.PutUint16(req[6:], unix.NLM_F_REQUEST)

	r := req[nlmsgHeaderLen:]
	r[0] = unix.AF_INET
	if local.Addr().Is6() {
		r[0] = unix.AF_INET6
	}
	r[1] = unix.IPPROTO_TCP
	binary.NativeEndian.PutUint32(r[4:], ^uint32(0)) // 全部状态
	// inet_diag_sockid：端口与地址按网络字节序，IPv4 地址占 16 字节槽位的前 4 字节。
	id := r[8:]
	binary.BigEndian.PutUint16(id[0:], local.Port())
	binary.BigEndian.PutUint16(id[2:], remote.Port())
	copy(id[4:20], local.Addr().AsSlice())
	copy(id[20:36], remote.Addr().AsSlice())
	binary.NativeEndian.PutUint32(id[40:], inetDiagNoCookie)
	binary.NativeEndian.PutUint32(id[44:], inetDiagNoCookie)
	return req
}

func parseSockDiagReply(b []byte) (uint64, error) {
	if len(b) < nlmsgHeaderLen {
		return 0, errors.New("sock_diag 应答不完整")
	}
	switch typ := binary.NativeEndian.Uint16(b[4:]); typ {
	case unix.NLMSG_ERROR:
		if len(b) < nlmsgHeaderLen+4 {
			return 0, errors.New("sock_diag 错误应答不完整")
		}
		if errno := -int32(binary.NativeEndian.Uint32(b[nlmsgHeaderLen:])); errno > 0 {
			return 0, fmt.Errorf("sock_diag: %w", unix.Errno(errno))
		}
		return 0, errors.New("sock_diag 没有返回连接")
	case unix.SOCK_DIAG_BY_FAMILY:
		if len(b) < nlmsgHeaderLen+inetDiagMsgLen {
			return 0, errors.New("sock_diag 应答不完整")
		}
		return uint64(binary.NativeEndian.Uint32(b[nlmsgHeaderLen+inetDiagInodeOffset:])), nil
	default:
		return 0, fmt.Errorf("sock_diag 应答类型 %d 无法识别", typ)
	}
}
