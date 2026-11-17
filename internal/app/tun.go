// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package app

import (
	"errors"
	"fmt"

	"github.com/mintfog/sniffy/internal/netbind"
	"github.com/mintfog/sniffy/internal/tun"
)

// StartTun 在本进程里开启虚拟网卡：全机 TCP 连接交给引擎的透明入口，UDP 由网卡在包内中继。
// 需要 root / 管理员权限。
//
// sniffy 自己的出站中本来会进虚拟网卡的，绑定到默认路由所在的物理网卡，其余照原有路由走。
// 绑定要先于添加指向虚拟网卡的路由：顺序反过来，其间代应用发出的连接会再进虚拟网卡形成回环。
func (a *App) StartTun() error {
	a.tunMu.Lock()
	defer a.tunMu.Unlock()
	if a.tunDev != nil {
		return errors.New("虚拟网卡已在运行")
	}

	egress, err := netbind.DefaultEgress()
	if err != nil {
		return fmt.Errorf("确定物理网卡: %w", err)
	}
	// 虚拟网卡总会接管 IPv4，没有出口可绑的话 sniffy 自己的 IPv4 出站会再进虚拟网卡。IPv6 没有
	// 默认路由时不接管：否则应用的 IPv6 连接会先被虚拟网卡接下、再因出不去而断开，不如照旧
	// 立即报不可达，让应用回退到 IPv4。
	if egress.IPv4 == nil {
		return errors.New("没有 IPv4 默认路由")
	}
	egress.Tunnel = tun.Prefixes()
	listener := a.Engine.Listener()
	listener.BindOutbound(egress)
	dev, err := tun.Start(tun.Options{
		IPv6:      egress.IPv6 != nil,
		BlockQUIC: true,
		Handler:   listener.ServeTransparent,
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
		listener.BindOutbound(netbind.Egress{})
		return err
	}
	a.tunDev = dev
	a.Logger.Info("虚拟网卡 %s 已开启，sniffy 的出站经物理网卡（%s）", dev.Name(), egress)
	go a.watchTun(dev)
	return nil
}

// StopTun 关闭虚拟网卡，路由随之消失，经它建立的连接全部中断。未开启时什么都不做。
func (a *App) StopTun() error {
	a.tunMu.Lock()
	defer a.tunMu.Unlock()
	dev := a.tunDev
	if dev == nil {
		return nil
	}
	a.tunDev = nil
	err := dev.Close()
	// 网卡关闭、路由消失之后才能取消绑定，否则其间的出站会进虚拟网卡。
	a.Engine.Listener().BindOutbound(netbind.Egress{})
	return err
}

// watchTun 在网卡自己失效（被外部删除、驱动出错）时收尾，StopTun 关掉的不归它管。
func (a *App) watchTun(dev *tun.Device) {
	<-dev.Done()
	a.tunMu.Lock()
	defer a.tunMu.Unlock()
	if a.tunDev != dev {
		return
	}
	a.tunDev = nil
	a.Engine.Listener().BindOutbound(netbind.Egress{})
	a.Logger.Error("虚拟网卡 %s 已失效，流量恢复直连", dev.Name())
}
