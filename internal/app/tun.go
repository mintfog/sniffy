// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"sync/atomic"
	"time"

	"github.com/mintfog/sniffy/internal/core"
	"github.com/mintfog/sniffy/internal/netbind"
	"github.com/mintfog/sniffy/internal/service"
	"github.com/mintfog/sniffy/internal/tun"
	"github.com/mintfog/sniffy/internal/tunhost"
)

// EnableTun 接入虚拟网卡，此后经 Service.StartTun 开启。本进程没有 root / 管理员权限时改由
// helper 子进程开启网卡，launch 负责以管理员身份运行它（自动提权用 tunhost.Elevate）。
func (a *App) EnableTun(launch tunhost.Launch) {
	a.Service.SetTunBackend(&tunBackend{engine: a.Engine, logger: a.Logger, launch: launch})
}

// tunBackend 实现 service.TunBackend：开启网卡时全机 TCP 连接交给引擎的透明入口，UDP 由网卡在
// 包内中继。
type tunBackend struct {
	engine *core.Engine
	logger *Logger
	launch tunhost.Launch
}

// Start 确定物理网卡后开启虚拟网卡，有权限时在本进程里开启，否则经 helper 子进程。
//
// sniffy 自己的出站中本来会进虚拟网卡的，绑定到默认路由所在的物理网卡，其余照原有路由走。
// 绑定要先于添加指向虚拟网卡的路由：顺序反过来，其间代应用发出的连接会再进虚拟网卡形成回环。
func (b *tunBackend) Start(ctx context.Context, opts service.TunOptions) (service.TunDevice, error) {
	egress, err := tunEgress(opts.IPv6)
	if err != nil {
		return nil, err
	}
	listener := b.engine.Listener()
	listener.BindOutbound(egress)
	host, err := tunhost.Start(ctx, egress, tunhost.Options{BlockQUIC: opts.BlockQUIC}, listener.ServeTransparent, b.launch)
	if host == nil {
		listener.BindOutbound(netbind.Egress{})
		return nil, err
	}
	// 开启失败却仍返回了网卡时，网卡可能还在：保持出站绑定，等它消失。
	dev := &tunDevice{host: host, listener: listener, done: make(chan struct{})}
	go dev.watch()
	if err == nil {
		b.logger.Info("虚拟网卡 %s 已开启，sniffy 的出站经物理网卡（%s）", host.Name(), egress)
	}
	return dev, err
}

// tunEgress 确定开启虚拟网卡时 sniffy 自己的出站要绑定的物理网卡。
func tunEgress(ipv6 bool) (netbind.Egress, error) {
	egress, err := netbind.DefaultEgress()
	if err != nil {
		return egress, fmt.Errorf("确定物理网卡: %w", err)
	}
	// 虚拟网卡总会接管 IPv4，没有出口可绑的话 sniffy 自己的 IPv4 出站会再进虚拟网卡。IPv6 没有
	// 默认路由时不接管：否则应用的 IPv6 连接会先被虚拟网卡接下、再因出不去而断开，不如照旧
	// 立即报不可达，让应用回退到 IPv4。
	if egress.IPv4 == nil {
		return egress, errors.New("没有 IPv4 默认路由")
	}
	if !ipv6 {
		egress.IPv6 = nil
	}
	// 绑定不了的话，开启后 sniffy 代发的连接全部拨号失败，等于全机断网，要在添加路由之前发现。
	if err := egress.CheckBind(); err != nil {
		if errors.Is(err, fs.ErrPermission) {
			return egress, fmt.Errorf("当前权限下不能把出站绑定到物理网卡（Linux 内核低于 5.7 时需以 root 运行 sniffy）: %w", err)
		}
		return egress, fmt.Errorf("出站绑定物理网卡: %w", err)
	}
	egress.Tunnel = tun.Prefixes()
	return egress, nil
}

func (b *tunBackend) SetCATrusted(trusted bool) { b.engine.SetTransparentCATrusted(trusted) }

// TunStatusChanged 按状态的变化记日志：开启失败、没能及时关闭、运行中失效、关闭，以及运行期间根证书
// 的信任状态。
func (b *tunBackend) TunStatusChanged(prev, next service.TunStatus) {
	// 开启中途失败时 helper 没来得及回报网卡名。
	name := cmp.Or(prev.Name, "（开启未完成）")
	switch next.State {
	case service.TunStateRunning:
		started := prev.State != service.TunStateRunning
		switch {
		case next.CAUntrusted && (started || !prev.CAUntrusted):
			b.logger.Warn("根证书未被系统信任：经虚拟网卡的 HTTPS 一律盲转发、不解密，只抓明文 HTTP；信任根证书后自动恢复解密")
		case !started && prev.CAUntrusted && !next.CAUntrusted:
			b.logger.Info("根证书已被系统信任，经虚拟网卡的 HTTPS 恢复解密")
		}
	case service.TunStateStopping:
		switch {
		case prev.State == service.TunStateStarting:
			b.logger.Error("开启虚拟网卡失败，网卡可能还在，等它消失: %s", next.Error)
		case next.Error != "" && prev.Error == "":
			b.logger.Error("关闭虚拟网卡 %s: %s", name, next.Error)
		}
	case service.TunStateFailed:
		switch prev.State {
		case service.TunStateStarting:
			b.logger.Error("开启虚拟网卡失败: %s", next.Error)
		case service.TunStateRunning:
			b.logger.Error("%s: %s，流量恢复直连", name, next.Error)
		case service.TunStateStopping:
			b.logger.Info("虚拟网卡 %s 已关闭", name)
		}
	case service.TunStateOff:
		if prev.State == service.TunStateRunning || prev.State == service.TunStateStopping {
			b.logger.Info("虚拟网卡 %s 已关闭", name)
		}
	}
}

func (b *tunBackend) DecryptBypass() []service.DecryptBypassEntry {
	hosts := b.engine.BypassHosts()
	list := make([]service.DecryptBypassEntry, len(hosts))
	for i, h := range hosts {
		list[i] = service.DecryptBypassEntry{Host: h.Host, Reason: h.Reason, Since: h.Since.UTC().Format(time.RFC3339)}
	}
	return list
}

func (b *tunBackend) ForgetDecryptBypass(host string) { b.engine.ForgetBypass(host) }

func (b *tunBackend) ClearDecryptBypass() { b.engine.ClearBypass() }

// outboundBinder 是 sniffy 自己出站的绑定，即 *capture.TCPListener。
type outboundBinder interface {
	BindOutbound(netbind.Egress)
}

// tunDevice 是开启了的虚拟网卡连同 sniffy 自己出站的绑定。网卡消失、路由随之消失之后才撤销
// 绑定，否则其间 sniffy 自己的出站会进虚拟网卡。
type tunDevice struct {
	host     tunhost.Host
	listener outboundBinder
	// closing 表示网卡是经 Close 关闭的，消失时不按失效报告。
	closing atomic.Bool
	err     error
	done    chan struct{}
}

func (d *tunDevice) Name() string          { return d.host.Name() }
func (d *tunDevice) Done() <-chan struct{} { return d.done }
func (d *tunDevice) Err() error            { return d.err }

// Close 关闭网卡，等它消失、撤销出站绑定后返回，经它建立的连接全部中断。网卡没能及时关掉
// （helper 失去响应）时返回错误并保持绑定，等网卡真正消失时由 watch 撤销。
func (d *tunDevice) Close() error {
	d.closing.Store(true)
	if err := d.host.Close(); err != nil {
		return err
	}
	<-d.done
	return nil
}

// watch 在网卡消失后撤销出站绑定，并记下网卡自己停止的原因（被外部删除、驱动出错、helper 退出）。
// helper 收到退出信号后自行拆掉网卡的，Session.Err 为 nil，与关闭同样处理。
func (d *tunDevice) watch() {
	<-d.host.Done()
	d.listener.BindOutbound(netbind.Egress{})
	s, isSession := d.host.(*tunhost.Session)
	switch {
	case d.closing.Load():
	case !isSession:
		d.err = errors.New("虚拟网卡已失效")
	case s.Err() != nil:
		d.err = fmt.Errorf("虚拟网卡已失效（%w）", s.Err())
	}
	close(d.done)
}
