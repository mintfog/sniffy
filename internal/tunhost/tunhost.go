// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

// Package tunhost 运行虚拟网卡：有权限时在本进程里直接开启（StartLocal），否则交给以管理员
// 身份运行的 helper 子进程（StartHelper），Start 按权限选择其一。
//
// helper 是同一个可执行文件的 tun-helper 子命令，只负责网卡、路由与协议栈。它把每条 TCP 连接
// 经一条单独的交接连接交给主进程，抓包、插件与界面都留在普通权限的主进程里。通道协议见 proto.go。
package tunhost

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/netip"
	"sync/atomic"

	"github.com/mintfog/sniffy/internal/netbind"
	"github.com/mintfog/sniffy/internal/tun"
)

// Host 是一块运行中的虚拟网卡：本进程里开启的，或 helper 子进程里的 *Session。
type Host interface {
	// Name 返回虚拟网卡在系统里的名字。
	Name() string
	// Done 在网卡停止后关闭：调用了 Close，或网卡自己失效。
	Done() <-chan struct{}
	// SetEgress 换用 egress 指定的出口网卡（默认路由所在的网卡或网关变了时调用），egress.IPv4 不能为
	// nil。经默认路由网关装上的路由随之跟上当前的默认路由，见 tun.Device.RefreshRoutes。
	// UDP 中继此后新建的出站 socket 绑定到新网卡，换了网卡的地址族的现有映射随之关闭；egress.IPv6
	// 为 nil 时停止接管 IPv6，不为 nil 时开始接管。sniffy 自己的出站要先绑定到新的 IPv6 网卡，再
	// 开始接管 IPv6，否则其间代应用发出的 IPv6 连接会再进虚拟网卡。
	SetEgress(egress netbind.Egress) error
	// Close 销毁虚拟网卡，路由随之消失。
	Close() error
}

// Logger 记录虚拟网卡运行中出现、不影响它继续运行的问题。
type Logger interface {
	Warn(msg string, args ...any)
}

// Options 是虚拟网卡可由用户配置的参数，其余固定。
type Options struct {
	// BlockQUIC 拒绝发往 UDP 443 的数据报，使客户端回退到 TCP。
	BlockQUIC bool
	// Bypass 是不进虚拟网卡的目标网段，要求见 tun.CheckBypass。
	Bypass []netip.Prefix
}

// Start 开启虚拟网卡：有权限时在本进程里开启，否则经 helper 子进程开启，由 launch 以管理员身份
// 运行 helper。参数的要求同 StartLocal 与 StartHelper。ctx 已经结束时什么都不做。
//
// 返回错误时 Host 通常为 nil；不为 nil 时网卡可能还在，见 StartHelper。
func Start(ctx context.Context, egress netbind.Egress, opts Options, handler func(net.Conn), launch Launch, log Logger) (Host, error) {
	// 本进程里开启一开始就会建好网卡、装上路由，中途放弃不了：已经放弃的不再开启，否则要等装好
	// 再拆掉，全机流量白白中断一下。
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	host, err := StartLocal(egress, opts, handler)
	if !errors.Is(err, fs.ErrPermission) {
		return host, err
	}
	s, err := StartHelper(ctx, egress, opts, handler, launch, log)
	// nil 的 *Session 不能放进接口返回，否则调用方会以为网卡可能还在。
	if s == nil {
		return nil, err
	}
	return s, err
}

// StartLocal 在本进程里开启虚拟网卡，需要 root / 管理员权限，没有时返回的错误满足
// errors.Is(err, fs.ErrPermission)。每条 TCP 连接交给 handler；UDP 在网卡内中继，出站经
// egress 指定的物理网卡。egress.IPv4 不能为 nil，egress.IPv6 为 nil 时不接管 IPv6。
func StartLocal(egress netbind.Egress, opts Options, handler func(net.Conn)) (Host, error) {
	h := &localHost{}
	h.egress.Store(&egress)
	dev, err := tun.Start(tun.Options{
		IPv6:      egress.IPv6 != nil,
		BlockQUIC: opts.BlockQUIC,
		Bypass:    opts.Bypass,
		Handler:   handler,
		ListenUDP: func(network string) (tun.PacketConn, error) {
			iface := h.egress.Load().For(network)
			if iface == nil {
				return nil, fmt.Errorf("%s 没有出口网卡", network)
			}
			conn, err := netbind.ListenUDP(iface, network)
			if err != nil {
				return nil, err
			}
			return conn, nil
		},
	})
	if err != nil {
		return nil, err
	}
	h.Device = dev
	return h, nil
}

// localHost 是本进程里开启的虚拟网卡。
type localHost struct {
	*tun.Device
	// egress 是 UDP 中继的出站 socket 绑定的网卡。
	egress atomic.Pointer[netbind.Egress]
}

func (h *localHost) SetEgress(egress netbind.Egress) error {
	old := h.egress.Swap(&egress)
	if !netbind.SameInterface(old.IPv4, egress.IPv4) {
		h.ResetUDP(false)
	}
	if !netbind.SameInterface(old.IPv6, egress.IPv6) {
		h.ResetUDP(true)
	}
	return errors.Join(h.SetIPv6(egress.IPv6 != nil), h.RefreshRoutes())
}

// nopLogger 在调用方没有给出 Logger 时使用。
type nopLogger struct{}

func (nopLogger) Warn(string, ...any) {}
