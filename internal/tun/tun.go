// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

// Package tun 用虚拟网卡把全机流量收进来：TCP 逐条连接交给回调，UDP 在包内中继。
//
// 路由不替换默认路由，而是添加覆盖全部地址空间的两条 /1（IPv4、IPv6 各两条）指向虚拟网卡。
// 它们绑定在网卡上，进程退出（包括被强杀）时随网卡一起消失。Linux 上它们放在单独的路由表里，
// 由策略规则只让应用新建的连接查它，见 policyRules。
package tun

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"

	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

// DefaultMTU 是 Options.MTU 为 0 时使用的虚拟网卡 MTU。
//
// 虚拟网卡与协议栈之间不经过真实链路，MTU 只决定两者交换数据的包大小：TCP 在协议栈终结后
// 由宿主另行出站，UDP 由中继的出站 socket 按物理网卡的 MTU 分片，取多大都不影响实际的路径
// MTU。逐包收发的平台上吞吐随 MTU 近似线性增长；Linux 上内核与协议栈以分段卸载的大段交换，
// 吞吐基本不随 MTU 变化。
const DefaultMTU = 9000

const (
	minMTU = 1280 // IPv6 要求的最小链路 MTU

	// 网卡与协议栈之间单个 IP 包的最大长度，同时是 MTU 的上限。比 65535 略小，留出各平台
	// 读写网卡时附加在包前面的头。
	maxPacketSize = 65520
)

var (
	// 网卡自身的地址。应用发往虚拟网卡的连接以它为源地址，取私有段里不常用的一小段以减少
	// 与现有网络冲突的可能。
	addr4 = netip.MustParsePrefix("172.29.113.1/30")
	addr6 = netip.MustParsePrefix("fd73:6e69:6666::1/126")

	routes4 = []netip.Prefix{netip.MustParsePrefix("0.0.0.0/1"), netip.MustParsePrefix("128.0.0.0/1")}
	routes6 = []netip.Prefix{netip.MustParsePrefix("::/1"), netip.MustParsePrefix("8000::/1")}
)

// MaxBypass 是绕过列表的条目上限。macOS / Windows 上每个网段对应一条路由，网关变了时逐条重写。
const MaxBypass = 256

// CheckBypass 检查绕过列表：每项都是规范形式的网段（主机位为零），前缀长度至少为 2，即比指向
// 虚拟网卡的 /1 路由更具体，条目不超过 MaxBypass。
func CheckBypass(prefixes []netip.Prefix) error {
	if len(prefixes) > MaxBypass {
		return fmt.Errorf("绕过列表有 %d 项，超过上限 %d", len(prefixes), MaxBypass)
	}
	for _, p := range prefixes {
		switch {
		case !p.IsValid() || p.Addr().Is4In6():
			return fmt.Errorf("绕过列表里的 %s 不是有效的网段", p)
		case p != p.Masked():
			return fmt.Errorf("绕过列表里的 %s 主机位不为零，应写作 %s", p, p.Masked())
		case p.Bits() < 2:
			return fmt.Errorf("绕过列表里的 %s 范围过大，前缀长度至少为 2", p)
		}
	}
	return nil
}

// Prefixes 返回虚拟网卡自身的地址及其网段，如 172.29.113.1/30。应用经虚拟网卡发出的连接以
// 其中的地址为源地址。
func Prefixes() []netip.Prefix {
	return []netip.Prefix{addr4, addr6}
}

// PacketConn 是 UDP 中继使用的出站 socket，*net.UDPConn 满足它。
type PacketConn interface {
	ReadFromUDPAddrPort(b []byte) (int, netip.AddrPort, error)
	WriteToUDPAddrPort(b []byte, addr netip.AddrPort) (int, error)
	Close() error
}

// Options 是虚拟网卡的启动参数。
type Options struct {
	// MTU 为 0 时取 DefaultMTU。
	MTU int

	// IPv6 为 false 时网卡不配置 IPv6 地址与路由，IPv6 流量照常走物理网卡、不被收进来。
	// 宿主系统未启用 IPv6 时该项不生效。
	IPv6 bool

	// BlockQUIC 拒绝发往 UDP 443 的数据报（回 ICMP 端口不可达），使客户端回退到 TCP。
	BlockQUIC bool

	// Bypass 是不进虚拟网卡的目标网段，发往其中的流量照常经物理网卡收发，要求见 CheckBypass。
	// Linux 上由策略规则让它们照常查 main 表；macOS / Windows 上给它们加经默认路由网关的更具体的
	// 路由，默认路由变了要调用 Device.RefreshBypass。
	Bypass []netip.Prefix

	// Handler 在每条 TCP 连接完成握手后调用一次，各在独立的 goroutine 里，由它负责关闭 conn。
	// conn 的 RemoteAddr 是应用的原始源地址，LocalAddr 是原始目标地址。
	Handler func(conn net.Conn)

	// ListenUDP 为 UDP 中继创建出站 socket，network 是 "udp4" 或 "udp6"。返回的 socket 必须
	// 绑定到物理网卡，否则它发出的数据报会按路由表再次进入虚拟网卡。
	ListenUDP func(network string) (PacketConn, error)
}

// Device 是一块运行中的虚拟网卡及其协议栈。
type Device struct {
	nic   *nic
	stack *stack.Stack
	udp   *udpRelay

	// mu 串行化 SetIPv6 与 Close 对网卡系统配置的改动。
	mu     sync.Mutex
	closed bool

	closeOnce sync.Once
	closeErr  error
	done      chan struct{}
}

// linkEndpoint 是协议栈的链路层。shutdown 销毁底层网卡并使读循环退出，可重复调用。
type linkEndpoint interface {
	stack.LinkEndpoint
	shutdown() error
}

// nic 是各平台打开网卡的结果：网卡已配置好地址与路由，link 尚未挂到协议栈上。
type nic struct {
	name string
	link linkEndpoint
	// ipv6 表示网卡配着 IPv6 的地址与路由，即正在接管 IPv6。
	ipv6 bool
	// setIPv6 给网卡配上（on 为 true）或撤掉 IPv6 的地址与路由，返回此后是否接管着 IPv6。宿主系统
	// 未启用 IPv6 时配不上，返回 false 与 nil。配上失败时撤回已配的部分；撤掉失败时可能还剩一部分，
	// 返回 true。
	setIPv6 func(on bool) (bool, error)
	// refreshBypass 让绕过列表的路由跟上当前的默认路由，可为 nil（Linux 上绕过列表不依赖网关）。
	refreshBypass func() error
	// cleanup 撤销不随网卡销毁而消失的系统配置（Linux 的策略路由规则，macOS / Windows 绕过列表的
	// 路由），可为 nil。
	cleanup func()
}

// close 销毁网卡并撤销相关的系统配置。
func (n *nic) close() error {
	err := n.link.shutdown()
	if n.cleanup != nil {
		n.cleanup()
	}
	return err
}

type nicConfig struct {
	mtu    int
	ipv6   bool
	bypass []netip.Prefix
	// onDown 在链路层的读循环退出时调用一次：网卡被 shutdown，或自己失效（被外部删除、
	// 驱动出错）。
	onDown func()
}

// Start 创建虚拟网卡、配置地址与路由并启动协议栈。需要管理员权限。
func Start(opts Options) (*Device, error) {
	if opts.Handler == nil || opts.ListenUDP == nil {
		return nil, errors.New("tun: Handler 与 ListenUDP 不能为空")
	}
	mtu := opts.MTU
	if mtu == 0 {
		mtu = DefaultMTU
	}
	if mtu < minMTU || mtu > maxPacketSize {
		return nil, fmt.Errorf("tun: MTU %d 超出范围 [%d, %d]", mtu, minMTU, maxPacketSize)
	}
	if err := CheckBypass(opts.Bypass); err != nil {
		return nil, fmt.Errorf("tun: %w", err)
	}

	down := make(chan struct{})
	n, err := openNIC(nicConfig{mtu: mtu, ipv6: opts.IPv6, bypass: opts.Bypass, onDown: func() { close(down) }})
	if err != nil {
		return nil, fmt.Errorf("tun: 创建虚拟网卡: %w", err)
	}
	return start(n, opts, down)
}

// start 在已配置好的网卡上启动协议栈。down 在网卡链路层的读循环退出时关闭。
func start(n *nic, opts Options, down <-chan struct{}) (*Device, error) {
	relay := newUDPRelay(opts.ListenUDP, opts.BlockQUIC)
	s, err := newStack(n.link, opts.Handler, relay)
	if err != nil {
		relay.close()
		n.close()
		return nil, fmt.Errorf("tun: 启动协议栈: %w", err)
	}
	d := &Device{nic: n, stack: s, udp: relay, done: make(chan struct{})}
	go func() {
		<-down
		d.Close()
	}()
	return d, nil
}

// Name 返回虚拟网卡在系统里的名字。
func (d *Device) Name() string { return d.nic.name }

// Done 在网卡停止后关闭：调用了 Close，或网卡自己失效（被外部删除、驱动出错）。
func (d *Device) Done() <-chan struct{} { return d.done }

// SetIPv6 开始或停止接管 IPv6，即给网卡配上或撤掉 IPv6 的地址与路由。宿主系统未启用 IPv6 时
// 开始接管不生效。停止后，应用经虚拟网卡建立的 IPv6 连接随之中断，新的 IPv6 连接照常走物理网卡。
// 网卡关闭后什么都不做。
func (d *Device) SetIPv6(on bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || d.nic.ipv6 == on {
		return nil
	}
	ipv6, err := d.nic.setIPv6(on)
	d.nic.ipv6 = ipv6
	if err != nil {
		return fmt.Errorf("tun: %w", err)
	}
	return nil
}

// RefreshBypass 让绕过列表的路由跟上当前的默认路由：网关变了的地址族改经新网关，没变的补上缺的
// （如随网卡断开被系统删掉的）。默认路由所在的网卡或网关变了、或者默认路由消失后又恢复时调用。
// 某个地址族没有默认路由时保留原有的路由。Linux 上什么都不做：策略规则让绕过的网段照常查 main 表，
// 不依赖网关。网卡关闭后什么都不做。
func (d *Device) RefreshBypass() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || d.nic.refreshBypass == nil {
		return nil
	}
	if err := d.nic.refreshBypass(); err != nil {
		return fmt.Errorf("tun: 更新绕过列表的路由: %w", err)
	}
	return nil
}

// ResetUDP 关闭 IPv4（ipv6 为 false）或 IPv6 的全部 UDP 映射，应用此后发出的数据报经 ListenUDP
// 新建出站 socket。出口网卡换了之后调用：已有映射的 socket 仍绑在原来的网卡上，应用一直在发的话
// 映射也不会因空闲而回收。
func (d *Device) ResetUDP(ipv6 bool) { d.udp.reset(ipv6) }

// Close 销毁虚拟网卡（路由随之消失）并中断所有经它建立的连接。
func (d *Device) Close() error {
	d.closeOnce.Do(func() {
		// 等进行中的 SetIPv6 做完：它装上的策略规则要由 nic.close 撤销。
		d.mu.Lock()
		d.closed = true
		d.mu.Unlock()
		d.closeErr = d.nic.close()
		// 读循环可能还在把最后几个包交给协议栈，处理时要取协议栈的锁；Destroy 却持着这把锁
		// 等读循环退出。先等它退出再销毁协议栈。
		d.nic.link.Wait()
		d.udp.close()
		d.stack.Destroy()
		close(d.done)
	})
	return d.closeErr
}
