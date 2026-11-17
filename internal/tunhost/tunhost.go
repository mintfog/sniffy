// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

// Package tunhost 运行虚拟网卡：有权限时在本进程里直接开启（StartLocal），否则交给以管理员
// 身份运行的 helper 子进程（StartHelper）。
//
// helper 是同一个可执行文件的 tun-helper 子命令，只负责网卡、路由与协议栈。它把每条 TCP 连接
// 经一条单独的交接连接交给主进程，抓包、插件与界面都留在普通权限的主进程里。通道协议见 proto.go。
package tunhost

import (
	"fmt"
	"net"

	"github.com/mintfog/sniffy/internal/netbind"
	"github.com/mintfog/sniffy/internal/tun"
)

// Host 是一块运行中的虚拟网卡：本进程里的 *tun.Device，或 helper 子进程里的 *Session。
type Host interface {
	// Name 返回虚拟网卡在系统里的名字。
	Name() string
	// Done 在网卡停止后关闭：调用了 Close，或网卡自己失效。
	Done() <-chan struct{}
	// Close 销毁虚拟网卡，路由随之消失。
	Close() error
}

// StartLocal 在本进程里开启虚拟网卡，需要 root / 管理员权限，没有时返回的错误满足
// errors.Is(err, fs.ErrPermission)。每条 TCP 连接交给 handler；UDP 在网卡内中继，出站经
// egress 指定的物理网卡。egress.IPv4 不能为 nil，egress.IPv6 为 nil 时不接管 IPv6。
func StartLocal(egress netbind.Egress, handler func(net.Conn)) (Host, error) {
	dev, err := tun.Start(tun.Options{
		IPv6:      egress.IPv6 != nil,
		BlockQUIC: true,
		Handler:   handler,
		ListenUDP: func(network string) (tun.PacketConn, error) {
			iface := egress.For(network)
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
	return dev, nil
}
