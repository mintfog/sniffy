// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"

	"github.com/mintfog/sniffy/internal/netbind"
	"github.com/mintfog/sniffy/internal/tun"
	"github.com/mintfog/sniffy/internal/tunhost"
)

// StartTun 在本进程里开启虚拟网卡：全机 TCP 连接交给引擎的透明入口，UDP 由网卡在包内中继。
// 需要 root / 管理员权限，没有时返回的错误满足 errors.Is(err, fs.ErrPermission)，可改用
// StartTunHelper。
func (a *App) StartTun() error {
	return a.startTun(func(egress netbind.Egress) (tunhost.Host, error) {
		return tunhost.StartLocal(egress, a.Engine.Listener().ServeTransparent)
	})
}

// StartTunHelper 经以管理员身份运行的 helper 子进程开启虚拟网卡，本进程不需要特权。launch
// 拿到 helper 的命令行后负责以管理员身份运行它；ctx 结束前 helper 没有连入即放弃。
func (a *App) StartTunHelper(ctx context.Context, launch func(cmd []string) error) error {
	return a.startTun(func(egress netbind.Egress) (tunhost.Host, error) {
		s, err := tunhost.StartHelper(ctx, egress, a.Engine.Listener().ServeTransparent, launch)
		if s == nil {
			return nil, err
		}
		return s, err
	})
}

// startTun 确定物理网卡后用 open 开启虚拟网卡。open 失败时若仍返回了 Host，说明网卡可能还在
// （见 tunhost.StartHelper），按 StopTun 没能及时关掉处理：保持出站绑定，等它消失。
//
// sniffy 自己的出站中本来会进虚拟网卡的，绑定到默认路由所在的物理网卡，其余照原有路由走。
// 绑定要先于添加指向虚拟网卡的路由：顺序反过来，其间代应用发出的连接会再进虚拟网卡形成回环。
func (a *App) startTun(open func(netbind.Egress) (tunhost.Host, error)) error {
	a.tunMu.Lock()
	defer a.tunMu.Unlock()
	if a.tunHost != nil {
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
	// 绑定不了的话，开启后 sniffy 代发的连接全部拨号失败，等于全机断网，要在添加路由之前发现。
	if err := egress.CheckBind(); err != nil {
		if errors.Is(err, fs.ErrPermission) {
			return fmt.Errorf("当前权限下不能把出站绑定到物理网卡（Linux 内核低于 5.7 时需以 root 运行 sniffy）: %w", err)
		}
		return fmt.Errorf("出站绑定物理网卡: %w", err)
	}
	egress.Tunnel = tun.Prefixes()
	listener := a.Engine.Listener()
	listener.BindOutbound(egress)
	host, err := open(egress)
	if err != nil && host != nil {
		a.tunHost = host
		a.tunStopping = true
		go a.watchTun(host)
		return err
	}
	if err != nil {
		listener.BindOutbound(netbind.Egress{})
		return err
	}
	a.tunHost = host
	a.Logger.Info("虚拟网卡 %s 已开启，sniffy 的出站经物理网卡（%s）", host.Name(), egress)
	go a.watchTun(host)
	return nil
}

// StopTun 关闭虚拟网卡，路由随之消失，经它建立的连接全部中断。未开启时什么都不做。
//
// 网卡关闭、路由消失之后才能取消出站绑定，否则其间 sniffy 自己的出站会进虚拟网卡。Close 出错
// 时网卡可能还在（helper 失去响应），保持绑定并返回错误，等网卡真正消失时由 watchTun 收尾。
func (a *App) StopTun() error {
	a.tunMu.Lock()
	defer a.tunMu.Unlock()
	host := a.tunHost
	if host == nil {
		return nil
	}
	if err := host.Close(); err != nil {
		a.tunStopping = true
		return err
	}
	a.tunHost = nil
	a.Engine.Listener().BindOutbound(netbind.Egress{})
	return nil
}

// watchTun 在网卡停止后收尾：网卡自己停止（被外部删除、驱动出错、helper 退出），或 StopTun
// 没能及时关掉、之后才消失。StopTun 已经关掉的不归它管。
func (a *App) watchTun(host tunhost.Host) {
	<-host.Done()
	a.tunMu.Lock()
	defer a.tunMu.Unlock()
	if a.tunHost != host {
		return
	}
	a.tunHost = nil
	a.Engine.Listener().BindOutbound(netbind.Egress{})
	if a.tunStopping {
		a.tunStopping = false
		name := host.Name()
		if name == "" { // 开启中途失败，helper 没来得及回报网卡名
			name = "（开启未完成）"
		}
		a.Logger.Info("虚拟网卡 %s 已关闭", name)
		return
	}
	reason := "已失效"
	if s, ok := host.(*tunhost.Session); ok && s.Err() != nil {
		reason = "已失效（" + s.Err().Error() + "）"
	}
	a.Logger.Error("虚拟网卡 %s %s，流量恢复直连", host.Name(), reason)
}
