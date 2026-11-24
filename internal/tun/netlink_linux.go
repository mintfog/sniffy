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

// netlinkConn 是一条 rtnetlink 连接，只实现配置虚拟网卡用到的几种请求。
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
	return c.request(unix.RTM_NEWADDR, unix.NLM_F_CREATE|unix.NLM_F_EXCL, addrBody(index, prefix))
}

func (c *netlinkConn) delAddr(index int, prefix netip.Prefix) error {
	return c.request(unix.RTM_DELADDR, 0, addrBody(index, prefix))
}

func addrBody(index int, prefix netip.Prefix) []byte {
	msg := unix.IfAddrmsg{Family: family(prefix.Addr()), Prefixlen: uint8(prefix.Bits()), Index: uint32(index)}
	if prefix.Addr().Is6() {
		// 点对点的虚拟链路上没有别的节点，跳过重复地址检测，地址立即可用。
		msg.Flags = unix.IFA_F_NODAD
	}
	body := appendStruct(nil, &msg)
	body = appendAttr(body, unix.IFA_LOCAL, prefix.Addr().AsSlice())
	return appendAttr(body, unix.IFA_ADDRESS, prefix.Addr().AsSlice())
}

// addRoute 在路由表 table 里添加一条经网卡直接可达的路由。
func (c *netlinkConn) addRoute(index int, dst netip.Prefix, table uint32) error {
	return c.request(unix.RTM_NEWROUTE, unix.NLM_F_CREATE|unix.NLM_F_EXCL, routeBody(index, dst, table))
}

func (c *netlinkConn) delRoute(index int, dst netip.Prefix, table uint32) error {
	return c.request(unix.RTM_DELROUTE, 0, routeBody(index, dst, table))
}

func routeBody(index int, dst netip.Prefix, table uint32) []byte {
	msg := unix.RtMsg{
		Family:   family(dst.Addr()),
		Dst_len:  uint8(dst.Bits()),
		Table:    unix.RT_TABLE_UNSPEC, // 表号大于 255，由 RTA_TABLE 给出
		Protocol: unix.RTPROT_STATIC,
		Scope:    unix.RT_SCOPE_LINK,
		Type:     unix.RTN_UNICAST,
	}
	body := appendStruct(nil, &msg)
	body = appendAttr(body, unix.RTA_DST, dst.Addr().AsSlice())
	body = appendAttr(body, unix.RTA_OIF, binary.NativeEndian.AppendUint32(nil, uint32(index)))
	return appendAttr(body, unix.RTA_TABLE, binary.NativeEndian.AppendUint32(nil, table))
}

// fibRuleHdr 对应内核的 struct fib_rule_hdr，x/sys/unix 没有导出它。
type fibRuleHdr struct {
	Family uint8
	DstLen uint8
	SrcLen uint8
	Tos    uint8
	Table  uint8
	Res1   uint8
	Res2   uint8
	Action uint8
	Flags  uint32
}

// policyRule 是一条策略路由规则：src、dst 有效时只匹配源地址、目标地址落在其中的选路，命中后查
// 路由表 table。
// suppressDefault 让这次查表忽略默认路由，只有更具体的路由命中才算数，否则交给后面的规则。
// findSrc 让不带源地址的选路也能命中：先查表，再按查到的路由选出源地址，落在 src 里才算数。
// unmarked 让规则只匹配不带防火墙标记（fwmark 为 0）的包。
type policyRule struct {
	family          uint8
	priority        uint32
	table           uint32
	src             netip.Prefix
	dst             netip.Prefix
	suppressDefault bool
	findSrc         bool
	unmarked        bool
}

func (c *netlinkConn) addRule(r policyRule) error {
	return c.request(unix.RTM_NEWRULE, unix.NLM_F_CREATE|unix.NLM_F_EXCL, ruleBody(r))
}

// deleteRules 删除与 rules 相同的规则，每条删到不存在为止，同一条规则可能残留了不止一份。
func (c *netlinkConn) deleteRules(rules []policyRule) {
	for _, r := range rules {
		for c.request(unix.RTM_DELRULE, 0, ruleBody(r)) == nil {
		}
	}
}

func ruleBody(r policyRule) []byte {
	hdr := fibRuleHdr{Family: r.family, Table: unix.RT_TABLE_UNSPEC, Action: unix.FR_ACT_TO_TBL}
	if r.src.IsValid() {
		hdr.SrcLen = uint8(r.src.Bits())
	}
	if r.dst.IsValid() {
		hdr.DstLen = uint8(r.dst.Bits())
	}
	if r.findSrc {
		hdr.Flags = unix.FIB_RULE_FIND_SADDR
	}
	body := appendStruct(nil, &hdr)
	body = appendAttr(body, unix.FRA_PRIORITY, binary.NativeEndian.AppendUint32(nil, r.priority))
	body = appendAttr(body, unix.FRA_TABLE, binary.NativeEndian.AppendUint32(nil, r.table))
	if r.src.IsValid() {
		body = appendAttr(body, unix.FRA_SRC, r.src.Addr().AsSlice())
	}
	if r.dst.IsValid() {
		body = appendAttr(body, unix.FRA_DST, r.dst.Addr().AsSlice())
	}
	if r.suppressDefault {
		body = appendAttr(body, unix.FRA_SUPPRESS_PREFIXLEN, binary.NativeEndian.AppendUint32(nil, 0))
	}
	if r.unmarked {
		body = appendAttr(body, unix.FRA_FWMARK, binary.NativeEndian.AppendUint32(nil, 0))
		body = appendAttr(body, unix.FRA_FWMASK, binary.NativeEndian.AppendUint32(nil, 0xffffffff))
	}
	return body
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
