// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package tun

import (
	"encoding/binary"
	"fmt"
	"net/netip"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// netlinkConn 是一条 rtnetlink 连接，只实现配置虚拟网卡用到的三种请求。
type netlinkConn struct {
	fd  int
	seq uint32
}

func dialNetlink() (*netlinkConn, error) {
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.NETLINK_ROUTE)
	if err != nil {
		return nil, fmt.Errorf("打开 netlink: %w", err)
	}
	if err := unix.Bind(fd, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("打开 netlink: %w", err)
	}
	return &netlinkConn{fd: fd}, nil
}

func (c *netlinkConn) close() { unix.Close(c.fd) }

// setLinkUp 设置 MTU 并启用网卡。
func (c *netlinkConn) setLinkUp(index, mtu int) error {
	msg := unix.IfInfomsg{Family: unix.AF_UNSPEC, Index: int32(index), Flags: unix.IFF_UP, Change: unix.IFF_UP}
	body := appendStruct(nil, &msg)
	body = appendAttr(body, unix.IFLA_MTU, binary.NativeEndian.AppendUint32(nil, uint32(mtu)))
	return c.request(unix.RTM_NEWLINK, 0, body)
}

func (c *netlinkConn) addAddr(index int, prefix netip.Prefix) error {
	msg := unix.IfAddrmsg{Family: family(prefix.Addr()), Prefixlen: uint8(prefix.Bits()), Index: uint32(index)}
	if prefix.Addr().Is6() {
		// 点对点的虚拟链路上没有别的节点，跳过重复地址检测，地址立即可用。
		msg.Flags = unix.IFA_F_NODAD
	}
	body := appendStruct(nil, &msg)
	body = appendAttr(body, unix.IFA_LOCAL, prefix.Addr().AsSlice())
	body = appendAttr(body, unix.IFA_ADDRESS, prefix.Addr().AsSlice())
	return c.request(unix.RTM_NEWADDR, unix.NLM_F_CREATE|unix.NLM_F_EXCL, body)
}

// addRoute 添加一条经网卡直接可达的路由。
func (c *netlinkConn) addRoute(index int, dst netip.Prefix) error {
	msg := unix.RtMsg{
		Family:   family(dst.Addr()),
		Dst_len:  uint8(dst.Bits()),
		Table:    unix.RT_TABLE_MAIN,
		Protocol: unix.RTPROT_STATIC,
		Scope:    unix.RT_SCOPE_LINK,
		Type:     unix.RTN_UNICAST,
	}
	body := appendStruct(nil, &msg)
	body = appendAttr(body, unix.RTA_DST, dst.Addr().AsSlice())
	body = appendAttr(body, unix.RTA_OIF, binary.NativeEndian.AppendUint32(nil, uint32(index)))
	return c.request(unix.RTM_NEWROUTE, unix.NLM_F_CREATE|unix.NLM_F_EXCL, body)
}

// request 发送一条请求并等待内核的确认。
func (c *netlinkConn) request(msgType uint16, flags uint16, body []byte) error {
	c.seq++
	hdr := unix.NlMsghdr{
		Len:   uint32(unix.SizeofNlMsghdr + len(body)),
		Type:  msgType,
		Flags: unix.NLM_F_REQUEST | unix.NLM_F_ACK | flags,
		Seq:   c.seq,
	}
	req := append(appendStruct(nil, &hdr), body...)
	if err := unix.Sendto(c.fd, req, 0, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return err
	}
	buf := make([]byte, 4096)
	for {
		n, _, err := unix.Recvfrom(c.fd, buf, 0)
		if err != nil {
			return err
		}
		msgs, err := syscall.ParseNetlinkMessage(buf[:n])
		if err != nil {
			return err
		}
		for _, m := range msgs {
			if m.Header.Seq != c.seq || m.Header.Type != unix.NLMSG_ERROR || len(m.Data) < 4 {
				continue
			}
			if errno := int32(binary.NativeEndian.Uint32(m.Data)); errno != 0 {
				return unix.Errno(-errno)
			}
			return nil
		}
	}
}

func family(addr netip.Addr) uint8 {
	if addr.Is4() {
		return unix.AF_INET
	}
	return unix.AF_INET6
}

func appendStruct[T any](b []byte, v *T) []byte {
	return append(b, unsafe.Slice((*byte)(unsafe.Pointer(v)), unsafe.Sizeof(*v))...)
}

// appendAttr 追加一个 rtattr，按 4 字节对齐。
func appendAttr(b []byte, attrType uint16, value []byte) []byte {
	attr := unix.RtAttr{Len: uint16(unix.SizeofRtAttr + len(value)), Type: attrType}
	b = appendStruct(b, &attr)
	b = append(b, value...)
	for len(b)%unix.NLMSG_ALIGNTO != 0 {
		b = append(b, 0)
	}
	return b
}
