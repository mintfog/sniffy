// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package tun

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"math/rand/v2"
	"net"
	"net/netip"
	"os"
	"slices"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	wgtun "golang.zx2c4.com/wireguard/tun"
)

// 这里的测试要创建网卡、把公网路由指向它，只在独立的网络命名空间里以 root 运行，不碰宿主的
// 网络。无需真正的 root：
//
//	go test -c -o /tmp/tun.test ./internal/tun && unshare -rn /tmp/tun.test -test.v

// 链路层的两种实现都要过同一组用例：Linux 实际使用的 vnetEndpoint，以及 macOS / Windows 使用的
// deviceEndpoint（这里架在 wireguard-go 的 Linux 网卡封装上）。
var linkVariants = []struct {
	name string
	open func(cfg nicConfig) (*nic, error)
}{
	{"vnet", openNIC},
	{"device", openDeviceNIC},
}

func openDeviceNIC(cfg nicConfig) (*nic, error) {
	dev, err := wgtun.CreateTUN(ifaceName, cfg.mtu)
	if err != nil {
		return nil, err
	}
	n, err := configureNIC(ifaceName, cfg)
	if err != nil {
		dev.Close()
		return nil, err
	}
	n.link = newDeviceEndpoint(dev, cfg.mtu, virtioHdrLen, cfg.onDown)
	return n, nil
}

// requireIsolatedRoot 在不满足条件时跳过测试。新建的网络命名空间里只有 lo；还有别的网卡说明
// 身处宿主或容器的网络里，改路由会切走它们的流量。
func requireIsolatedRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("需要 root：unshare -rn 下运行")
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		t.Skipf("列出网卡: %v", err)
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback == 0 && iface.Name != ifaceName {
			t.Skipf("存在网卡 %s，不是独立的网络命名空间：unshare -rn 下运行", iface.Name)
		}
	}
}

func startTest(t *testing.T, open func(nicConfig) (*nic, error), opts Options) *Device {
	t.Helper()
	requireIsolatedRoot(t)
	if opts.MTU == 0 {
		opts.MTU = DefaultMTU
	}
	if opts.Handler == nil {
		opts.Handler = func(conn net.Conn) { conn.Close() }
	}
	if opts.ListenUDP == nil {
		opts.ListenUDP = func(string) (PacketConn, error) { return nil, errors.New("未提供") }
	}
	down := make(chan struct{})
	n, err := open(nicConfig{mtu: opts.MTU, ipv6: opts.IPv6, bypass: opts.Bypass, onDown: func() { close(down) }})
	if err != nil {
		t.Fatalf("打开网卡: %v", err)
	}
	d, err := start(n, opts, down)
	if err != nil {
		t.Fatalf("启动: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestHandlerSeesOriginalAddresses(t *testing.T) {
	for _, v := range linkVariants {
		t.Run(v.name, func(t *testing.T) {
			type seen struct{ local, remote string }
			got := make(chan seen, 4)
			startTest(t, v.open, Options{IPv6: true, Handler: func(conn net.Conn) {
				defer conn.Close()
				got <- seen{conn.LocalAddr().String(), conn.RemoteAddr().String()}
				io.Copy(conn, conn)
			}})

			for _, target := range []string{"203.0.113.7:8080", "10.9.8.7:443", "[2001:db8::7]:8080"} {
				conn, err := net.DialTimeout("tcp", target, 3*time.Second)
				if err != nil {
					t.Fatalf("拨 %s: %v", target, err)
				}
				s := <-got
				if s.local != target {
					t.Errorf("LocalAddr = %s，应为原始目标 %s", s.local, target)
				}
				if want := conn.LocalAddr().String(); s.remote != want {
					t.Errorf("RemoteAddr = %s，应为应用的源地址 %s", s.remote, want)
				}
				src := netip.MustParseAddrPort(conn.LocalAddr().String()).Addr()
				if src != addr4.Addr() && src != addr6.Addr() {
					t.Errorf("连接的源地址 %s 不是虚拟网卡的地址", src)
				}

				msg := []byte("ping " + target)
				conn.Write(msg)
				echo := make([]byte, len(msg))
				conn.SetReadDeadline(time.Now().Add(3 * time.Second))
				if _, err := io.ReadFull(conn, echo); err != nil || !bytes.Equal(echo, msg) {
					t.Errorf("回显 = %q, %v", echo, err)
				}
				conn.Close()
			}
		})
	}
}

// 大块数据双向传输后内容一致：覆盖分段卸载的大段、校验和卸载、IPv4 与 IPv6。
func TestBulkTransferIntact(t *testing.T) {
	const size = 48 << 20
	for _, v := range linkVariants {
		for _, mtu := range []int{1500, DefaultMTU, maxPacketSize} {
			for _, target := range []string{"203.0.113.7:9000", "[2001:db8::7]:9000"} {
				t.Run(v.name+"/"+target+"/mtu="+strconv.Itoa(mtu), func(t *testing.T) {
					downSum := make(chan [32]byte, 1)
					startTest(t, v.open, Options{MTU: mtu, IPv6: true, Handler: func(conn net.Conn) {
						defer conn.Close()
						// 上行：读完应用发来的数据；下行：发回同样大小的另一份随机数据。
						h := sha256.New()
						if _, err := io.CopyN(h, conn, size); err != nil {
							t.Errorf("协议栈侧读: %v", err)
							return
						}
						conn.Write(h.Sum(nil))
						down := sha256.New()
						io.CopyN(io.MultiWriter(conn, down), randReader(2), size)
						downSum <- [32]byte(down.Sum(nil))
					}})

					conn, err := net.DialTimeout("tcp", target, 3*time.Second)
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					conn.SetDeadline(time.Now().Add(60 * time.Second))

					up := sha256.New()
					if _, err := io.CopyN(io.MultiWriter(conn, up), randReader(1), size); err != nil {
						t.Fatalf("上行写: %v", err)
					}
					var gotUp [32]byte
					if _, err := io.ReadFull(conn, gotUp[:]); err != nil {
						t.Fatalf("读上行摘要: %v", err)
					}
					if gotUp != [32]byte(up.Sum(nil)) {
						t.Error("上行数据在途中被改动")
					}
					down := sha256.New()
					if _, err := io.CopyN(down, conn, size); err != nil {
						t.Fatalf("下行读: %v", err)
					}
					if [32]byte(down.Sum(nil)) != <-downSum {
						t.Error("下行数据在途中被改动")
					}
				})
			}
		}
	}
}

func TestIPv6Disabled(t *testing.T) {
	startTest(t, openNIC, Options{IPv6: false})
	if _, err := net.DialTimeout("tcp", "203.0.113.7:80", 3*time.Second); err != nil {
		t.Errorf("IPv4 应可达: %v", err)
	}
	if _, err := net.DialTimeout("tcp", "[2001:db8::7]:80", time.Second); !errors.Is(err, syscall.ENETUNREACH) {
		t.Errorf("未启用 IPv6 时不应有 IPv6 路由，拨号结果: %v", err)
	}
}

// 运行中开始、停止接管 IPv6：地址、路由与策略规则随之配上、撤掉，IPv4 不受影响。
func TestSetIPv6(t *testing.T) {
	accepted := make(chan string, 4)
	d := startTest(t, openNIC, Options{IPv6: false, Handler: func(conn net.Conn) {
		accepted <- conn.LocalAddr().String()
		conn.Close()
	}})
	expectIPv6 := func(on bool) {
		t.Helper()
		conn, err := net.DialTimeout("tcp", "[2001:db8::7]:80", time.Second)
		if !on {
			// 网卡上没有了 IPv6 地址，没有任何可用源地址时报的是「无法分配地址」。
			if !errors.Is(err, syscall.ENETUNREACH) && !errors.Is(err, syscall.EADDRNOTAVAIL) {
				t.Errorf("不接管 IPv6 时应无 IPv6 路由，拨号结果: %v", err)
			}
		} else if err != nil {
			t.Errorf("接管 IPv6 时应进虚拟网卡: %v", err)
		} else {
			conn.Close()
			if got := <-accepted; got != "[2001:db8::7]:80" {
				t.Errorf("虚拟网卡收到的连接目标 = %s", got)
			}
		}
		if got := hasAddr(t, d.Name(), addr6.Addr()); got != on {
			t.Errorf("网卡上有 IPv6 地址 = %v，期望 %v", got, on)
		}
		if n, want := countTableRoutes(t, unix.AF_INET6), map[bool]int{true: len(routes6)}[on]; n != want {
			t.Errorf("路由表 %d 里有 %d 条 IPv6 路由，期望 %d", routeTable, n, want)
		}
		want := len(policyRules(addr4))
		if on {
			want += len(policyRules(addr6))
		}
		if n := countOwnRules(t); n != want {
			t.Errorf("策略规则 %d 条，期望 %d", n, want)
		}
		conn, err = net.DialTimeout("tcp", "203.0.113.7:80", time.Second)
		if err != nil {
			t.Fatalf("IPv4 应照旧进虚拟网卡: %v", err)
		}
		conn.Close()
		<-accepted
	}

	expectIPv6(false)
	for _, on := range []bool{true, true, false, false, true} {
		if err := d.SetIPv6(on); err != nil {
			t.Fatalf("SetIPv6(%v): %v", on, err)
		}
		expectIPv6(on)
	}
	d.Close()
	if n := countOwnRules(t); n != 0 {
		t.Errorf("Close 后还剩 %d 条规则", n)
	}
	if err := d.SetIPv6(false); err != nil {
		t.Errorf("网卡关闭后 SetIPv6 应什么都不做，返回 %v", err)
	}
}

// countTableRoutes 数出 routeTable 里地址族 family 的路由。
func countTableRoutes(t *testing.T, family int) int {
	t.Helper()
	rib, err := syscall.NetlinkRIB(syscall.RTM_GETROUTE, family)
	if err != nil {
		t.Fatal(err)
	}
	msgs, err := syscall.ParseNetlinkMessage(rib)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for i := range msgs {
		if msgs[i].Header.Type != syscall.RTM_NEWROUTE {
			continue
		}
		attrs, err := syscall.ParseNetlinkRouteAttr(&msgs[i])
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range attrs {
			if a.Attr.Type == unix.RTA_TABLE && binary.NativeEndian.Uint32(a.Value) == routeTable {
				count++
			}
		}
	}
	return count
}

func TestCloseRemovesRoutesAndAbortsConns(t *testing.T) {
	d := startTest(t, openNIC, Options{IPv6: true, Handler: func(conn net.Conn) {
		defer conn.Close()
		io.Copy(io.Discard, conn)
	}})
	conn, err := net.DialTimeout("tcp", "203.0.113.7:80", 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case <-d.Done():
	default:
		t.Error("Close 返回后 Done 应已关闭")
	}
	if _, err := net.InterfaceByName(d.Name()); err == nil {
		t.Error("网卡仍然存在")
	}
	for _, target := range []string{"1.1.1.1:80", "203.0.113.7:80", "[2001:db8::7]:80", "[2400::1]:80"} {
		// IPv6 在没有任何可用源地址时报的是「无法分配地址」。
		_, err := net.DialTimeout("tcp", target, time.Second)
		if !errors.Is(err, syscall.ENETUNREACH) && !errors.Is(err, syscall.EADDRNOTAVAIL) {
			t.Errorf("销毁网卡后 %s 应无路由，拨号结果: %v", target, err)
		}
	}
}

// 源地址属于别的网卡的包不进虚拟网卡：本机作为服务端的回包、开启前已建立的连接都是这样，
// 它们照旧按 main 表选路。这里 main 表没有默认路由，所以拨号报不可达。
func TestOtherSourceAddressBypasses(t *testing.T) {
	accepted := make(chan string, 4)
	startTest(t, openNIC, Options{IPv6: true, Handler: func(conn net.Conn) {
		accepted <- conn.LocalAddr().String()
		conn.Close()
	}})
	for _, tc := range []struct{ local, target string }{
		{"198.51.100.1", "203.0.113.7:80"},
		{"2001:db8:ffff::1", "[2001:db8::7]:80"},
	} {
		addLoopbackAddr(t, netip.MustParseAddr(tc.local))
		d := net.Dialer{Timeout: time.Second, LocalAddr: &net.TCPAddr{IP: net.ParseIP(tc.local)}}
		if _, err := d.Dial("tcp", tc.target); !errors.Is(err, syscall.ENETUNREACH) {
			t.Errorf("从 %s 拨 %s 应按 main 表报不可达，拨号结果: %v", tc.local, tc.target, err)
		}
		conn, err := net.DialTimeout("tcp", tc.target, time.Second)
		if err != nil {
			t.Fatalf("不指定源地址拨 %s: %v", tc.target, err)
		}
		conn.Close()
		if got := <-accepted; got != tc.target {
			t.Errorf("虚拟网卡收到的连接目标 = %s，期望 %s", got, tc.target)
		}
	}
	select {
	case got := <-accepted:
		t.Errorf("虚拟网卡收到了不该收的连接 %s", got)
	default:
	}
}

// main 表里有默认路由（物理网卡）时，应用新建的连接照样进虚拟网卡。
func TestCapturesDespiteMainDefaultRoute(t *testing.T) {
	accepted := make(chan string, 2)
	startTest(t, openNIC, Options{IPv6: true, Handler: func(conn net.Conn) {
		accepted <- conn.LocalAddr().String()
		conn.Close()
	}})
	addLoopbackDefaultRoutes(t)
	for _, target := range []string{"203.0.113.7:80", "[2001:db8::7]:80"} {
		conn, err := net.DialTimeout("tcp", target, time.Second)
		if err != nil {
			t.Fatalf("拨 %s 没进虚拟网卡: %v", target, err)
		}
		conn.Close()
		if got := <-accepted; got != target {
			t.Errorf("虚拟网卡收到的连接目标 = %s，期望 %s", got, target)
		}
	}
}

// addLoopbackDefaultRoutes 启用 lo 并在 main 表里添加两个地址族经 lo 的默认路由，代替物理网卡的
// 默认路由，返回 lo 的索引。测试结束时撤销。
func addLoopbackDefaultRoutes(t *testing.T) int {
	t.Helper()
	lo, err := net.InterfaceByName("lo")
	if err != nil {
		t.Fatal(err)
	}
	up := unix.IfInfomsg{Family: unix.AF_UNSPEC, Index: int32(lo.Index), Flags: unix.IFF_UP, Change: unix.IFF_UP}
	netlinkRequest(t, unix.RTM_NEWLINK, 0, appendStruct(nil, &up))
	t.Cleanup(func() {
		down := up
		down.Flags = 0
		netlinkRequest(t, unix.RTM_NEWLINK, 0, appendStruct(nil, &down))
	})
	// 默认路由要用普通的单播路由：unreachable 类型的路由一命中就报错结束选路，轮不到规则 1
	// 「不用默认路由」这一层。
	for _, dst := range []string{"0.0.0.0/0", "::/0"} {
		addMainRoute(t, netip.MustParsePrefix(dst), unix.RTN_UNICAST, lo.Index)
	}
	return lo.Index
}

// main 表里比默认路由更具体的路由（如局域网）照旧优先，不进虚拟网卡。
func TestSpecificMainRoutesWin(t *testing.T) {
	startTest(t, openNIC, Options{Handler: func(conn net.Conn) { conn.Close() }})
	addMainRoute(t, netip.MustParsePrefix("10.200.0.0/16"), unix.RTN_UNREACHABLE, 0)

	_, err := net.DialTimeout("tcp", "10.200.0.1:80", time.Second)
	if !errors.Is(err, syscall.EHOSTUNREACH) && !errors.Is(err, syscall.ENETUNREACH) {
		t.Errorf("10.200.0.1 应按 main 表的 unreachable 路由报不可达，拨号结果: %v", err)
	}
	conn, err := net.DialTimeout("tcp", "10.201.0.1:80", time.Second)
	if err != nil {
		t.Fatalf("main 表路由之外的 10.201.0.1 应进虚拟网卡: %v", err)
	}
	conn.Close()
}

// 策略规则在 Close 时撤销；进程被强杀后残留的规则在下次开启时清掉，不会越积越多。
func TestPolicyRulesLifecycle(t *testing.T) {
	requireIsolatedRoot(t)
	nl, err := dialNetlink()
	if err != nil {
		t.Fatal(err)
	}
	defer nl.close()
	stale := append(policyRules(addr4), policyRules(addr6)...)
	for range 2 {
		for _, r := range stale {
			if err := nl.request(unix.RTM_NEWRULE, unix.NLM_F_CREATE, ruleBody(r)); err != nil {
				t.Fatalf("预置残留规则: %v", err)
			}
		}
	}
	if n := countOwnRules(t); n != 2*len(stale) {
		t.Fatalf("预置残留规则 %d 条，期望 %d", n, 2*len(stale))
	}

	d := startTest(t, openNIC, Options{IPv6: true})
	if n := countOwnRules(t); n != len(stale) {
		t.Errorf("开启后有 %d 条规则，期望 %d", n, len(stale))
	}
	d.Close()
	if n := countOwnRules(t); n != 0 {
		t.Errorf("Close 后还剩 %d 条规则", n)
	}
}

// 绕过列表里的网段照常按 main 表选路（含默认路由），其余照旧进虚拟网卡；IPv6 网段的规则随 IPv6 的
// 接管装上、撤掉，Close 后不剩规则，上次残留的绕过规则不论网段都在开启时清掉。
func TestBypass(t *testing.T) {
	requireIsolatedRoot(t)
	nl, err := dialNetlink()
	if err != nil {
		t.Fatal(err)
	}
	defer nl.close()
	stale := bypassRules([]netip.Prefix{netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("2001:db8:9::/48")}, false)
	stale = append(stale, bypassRules([]netip.Prefix{netip.MustParsePrefix("2001:db8:9::/48")}, true)...)
	for _, r := range stale {
		if err := nl.request(unix.RTM_NEWRULE, unix.NLM_F_CREATE, ruleBody(r)); err != nil {
			t.Fatalf("预置残留规则: %v", err)
		}
	}

	bypass := []netip.Prefix{netip.MustParsePrefix("10.200.0.0/16"), netip.MustParsePrefix("2001:db8:1::/48"),
		netip.MustParsePrefix("198.51.100.7/32")}
	d := startTest(t, openNIC, Options{IPv6: true, Bypass: bypass})
	lo := addLoopbackDefaultRoutes(t)
	tunIndex := mustIndex(t, d.Name())
	expect := func(dst string, oif int) {
		t.Helper()
		if got := routeOIF(t, netip.MustParseAddr(dst)); got != oif {
			t.Errorf("%s 选路到网卡 %d，期望 %d", dst, got, oif)
		}
	}
	for dst, oif := range map[string]int{"10.200.0.1": lo, "10.201.0.1": tunIndex, "198.51.100.7": lo, "198.51.100.8": tunIndex,
		"2001:db8:1::1": lo, "2001:db8:2::1": tunIndex, "192.0.2.1": tunIndex, "2001:db8:9::1": tunIndex} {
		expect(dst, oif)
	}
	if n := countRules(t, unix.AF_INET, bypassPriority); n != 2 {
		t.Errorf("IPv4 绕过规则 %d 条，期望 2", n)
	}

	if err := d.SetIPv6(false); err != nil {
		t.Fatal(err)
	}
	if n := countRules(t, unix.AF_INET6, bypassPriority); n != 0 {
		t.Errorf("停止接管 IPv6 后还剩 %d 条 IPv6 绕过规则", n)
	}
	if err := d.SetIPv6(true); err != nil {
		t.Fatal(err)
	}
	expect("2001:db8:1::1", lo)
	expect("2001:db8:2::1", tunIndex)
	if err := d.RefreshBypass(); err != nil {
		t.Errorf("RefreshBypass: %v", err)
	}

	d.Close()
	if n := countRules(t, unix.AF_UNSPEC, bypassPriority, rulePriority, rulePriority+1); n != 0 {
		t.Errorf("Close 后还剩 %d 条规则", n)
	}
}

func mustIndex(t *testing.T, name string) int {
	t.Helper()
	iface, err := net.InterfaceByName(name)
	if err != nil {
		t.Fatal(err)
	}
	return iface.Index
}

// routeOIF 像 ip route get 一样让内核为发往 dst 的包选路（经过策略规则），返回选中的网卡索引。
func routeOIF(t *testing.T, dst netip.Addr) int {
	t.Helper()
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.NETLINK_ROUTE)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	msg := unix.RtMsg{Family: family(dst), Dst_len: uint8(dst.BitLen())}
	body := appendAttr(appendStruct(nil, &msg), unix.RTA_DST, dst.AsSlice())
	hdr := unix.NlMsghdr{Len: uint32(unix.SizeofNlMsghdr + len(body)), Type: unix.RTM_GETROUTE, Flags: unix.NLM_F_REQUEST, Seq: 1}
	if err := unix.Sendto(fd, append(appendStruct(nil, &hdr), body...), 0, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1<<16)
	n, _, err := unix.Recvfrom(fd, buf, 0)
	if err != nil {
		t.Fatal(err)
	}
	msgs, err := syscall.ParseNetlinkMessage(buf[:n])
	if err != nil {
		t.Fatal(err)
	}
	for i := range msgs {
		if msgs[i].Header.Type == unix.NLMSG_ERROR {
			t.Fatalf("为 %s 选路: %v", dst, unix.Errno(-int32(binary.NativeEndian.Uint32(msgs[i].Data))))
		}
		attrs, err := syscall.ParseNetlinkRouteAttr(&msgs[i])
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range attrs {
			if a.Attr.Type == unix.RTA_OIF {
				return int(binary.NativeEndian.Uint32(a.Value))
			}
		}
	}
	t.Fatalf("为 %s 选路的结果里没有网卡", dst)
	return 0
}

// addMainRoute 在 main 表里添加一条路由，测试结束时删除。routeType 为 RTN_UNICAST 时经网卡
// oif 直达；为 RTN_UNREACHABLE 时命中它的拨号立即报不可达。
func addMainRoute(t *testing.T, dst netip.Prefix, routeType uint8, oif int) {
	t.Helper()
	msg := unix.RtMsg{Family: family(dst.Addr()), Dst_len: uint8(dst.Bits()), Table: unix.RT_TABLE_MAIN,
		Protocol: unix.RTPROT_STATIC, Scope: unix.RT_SCOPE_UNIVERSE, Type: routeType}
	if routeType == unix.RTN_UNICAST {
		msg.Scope = unix.RT_SCOPE_LINK
	}
	body := appendAttr(appendStruct(nil, &msg), unix.RTA_DST, dst.Addr().AsSlice())
	if oif != 0 {
		body = appendAttr(body, unix.RTA_OIF, binary.NativeEndian.AppendUint32(nil, uint32(oif)))
	}
	netlinkRequest(t, unix.RTM_NEWROUTE, unix.NLM_F_CREATE|unix.NLM_F_EXCL, body)
	t.Cleanup(func() { netlinkRequest(t, unix.RTM_DELROUTE, 0, body) })
}

// addLoopbackAddr 给 lo 添加地址，测试结束时删除。
func addLoopbackAddr(t *testing.T, addr netip.Addr) {
	t.Helper()
	lo, err := net.InterfaceByName("lo")
	if err != nil {
		t.Fatal(err)
	}
	msg := unix.IfAddrmsg{Family: family(addr), Prefixlen: uint8(addr.BitLen()), Index: uint32(lo.Index)}
	body := appendStruct(nil, &msg)
	body = appendAttr(body, unix.IFA_LOCAL, addr.AsSlice())
	body = appendAttr(body, unix.IFA_ADDRESS, addr.AsSlice())
	netlinkRequest(t, unix.RTM_NEWADDR, unix.NLM_F_CREATE|unix.NLM_F_EXCL, body)
	t.Cleanup(func() { netlinkRequest(t, unix.RTM_DELADDR, 0, body) })
}

func netlinkRequest(t *testing.T, msgType, flags uint16, body []byte) {
	t.Helper()
	nl, err := dialNetlink()
	if err != nil {
		t.Fatal(err)
	}
	defer nl.close()
	if err := nl.request(msgType, flags, body); err != nil {
		t.Fatalf("netlink 请求 %d: %v", msgType, err)
	}
}

// countOwnRules 数出引用 routeTable 的策略规则及其前面那条查 main 表的规则，即 policyRules 装上的。
func countOwnRules(t *testing.T) int {
	t.Helper()
	return countRules(t, unix.AF_UNSPEC, rulePriority, rulePriority+1)
}

// countRules 数出地址族 family（AF_UNSPEC 为全部）里优先级是 priorities 之一的策略规则。
func countRules(t *testing.T, family uint8, priorities ...uint32) int {
	t.Helper()
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.NETLINK_ROUTE)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	hdr := unix.NlMsghdr{Len: unix.SizeofNlMsghdr + 12, Type: unix.RTM_GETRULE, Flags: unix.NLM_F_REQUEST | unix.NLM_F_DUMP, Seq: 1}
	req := append(appendStruct(nil, &hdr), appendStruct(nil, &fibRuleHdr{Family: family})...)
	if err := unix.Sendto(fd, req, 0, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		t.Fatal(err)
	}
	count := 0
	buf := make([]byte, 1<<16)
	for {
		n, _, err := unix.Recvfrom(fd, buf, 0)
		if err != nil {
			t.Fatal(err)
		}
		msgs, err := syscall.ParseNetlinkMessage(buf[:n])
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range msgs {
			if m.Header.Type == unix.NLMSG_DONE {
				return count
			}
			if m.Header.Type != unix.RTM_NEWRULE || len(m.Data) < 12 {
				continue
			}
			for attrs := m.Data[12:]; len(attrs) >= unix.SizeofRtAttr; {
				l := int(binary.NativeEndian.Uint16(attrs))
				if l < unix.SizeofRtAttr || l > len(attrs) {
					break
				}
				if binary.NativeEndian.Uint16(attrs[2:]) == unix.FRA_PRIORITY {
					if slices.Contains(priorities, binary.NativeEndian.Uint32(attrs[4:])) {
						count++
					}
				}
				attrs = attrs[min((l+unix.NLMSG_ALIGNTO-1)&^(unix.NLMSG_ALIGNTO-1), len(attrs)):]
			}
		}
	}
}

// 网卡被外部删除时 Device 自行停止。
func TestStopsWhenInterfaceRemoved(t *testing.T) {
	for _, v := range linkVariants {
		t.Run(v.name, func(t *testing.T) {
			d := startTest(t, v.open, Options{})
			iface, err := net.InterfaceByName(d.Name())
			if err != nil {
				t.Fatal(err)
			}
			nl, err := dialNetlink()
			if err != nil {
				t.Fatal(err)
			}
			defer nl.close()
			msg := unix.IfInfomsg{Family: unix.AF_UNSPEC, Index: int32(iface.Index)}
			if err := nl.request(unix.RTM_DELLINK, 0, appendStruct(nil, &msg)); err != nil {
				t.Fatalf("删除网卡: %v", err)
			}
			select {
			case <-d.Done():
			case <-time.After(5 * time.Second):
				t.Fatal("网卡被删除后 Done 未关闭")
			}
		})
	}
}

func TestUDPRelay(t *testing.T) {
	for _, v := range linkVariants {
		t.Run(v.name, func(t *testing.T) {
			var mu sync.Mutex
			var networks []string
			startTest(t, v.open, Options{IPv6: true, ListenUDP: func(network string) (PacketConn, error) {
				mu.Lock()
				networks = append(networks, network)
				mu.Unlock()
				return newLoopbackUDP(), nil
			}})

			for _, tc := range []struct {
				network string
				targets []string
			}{
				{"udp4", []string{"203.0.113.9:5353", "10.9.8.7:53", "198.51.100.3:40000"}},
				{"udp6", []string{"[2001:db8::9]:5353", "[2400::1]:53"}},
			} {
				// 同一个应用 socket 发往多个目标，回包各自带着对应目标的源地址。
				client, err := net.ListenUDP(tc.network, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer client.Close()
				for _, target := range tc.targets {
					for _, size := range []int{1, 1200, 20000} {
						msg := make([]byte, size)
						randReader(uint64(size)).Read(msg)
						dst := netip.MustParseAddrPort(target)
						if _, err := client.WriteToUDPAddrPort(msg, dst); err != nil {
							t.Fatalf("发往 %s: %v", target, err)
						}
						buf := make([]byte, 65535)
						client.SetReadDeadline(time.Now().Add(3 * time.Second))
						n, from, err := client.ReadFromUDPAddrPort(buf)
						if err != nil {
							t.Fatalf("等 %s 的回包（%d 字节）: %v", target, size, err)
						}
						if from != dst {
							t.Errorf("回包源地址 = %s，应为 %s", from, dst)
						}
						if !bytes.Equal(buf[:n], msg) {
							t.Errorf("%s 的回包内容不一致（%d 字节）", target, size)
						}
					}
				}
			}

			mu.Lock()
			defer mu.Unlock()
			if len(networks) != 2 || networks[0] != "udp4" || networks[1] != "udp6" {
				t.Errorf("出站 socket = %v，应为每个应用 socket 一个", networks)
			}
		})
	}
}

// 回包来自应用没发过数据的地址时照样送达：映射与目标无关。
func TestUDPReplyFromOtherAddress(t *testing.T) {
	other := netip.MustParseAddrPort("198.51.100.77:3478")
	startTest(t, openNIC, Options{ListenUDP: func(string) (PacketConn, error) {
		conn := newLoopbackUDP()
		conn.replyFrom = other
		return conn, nil
	}})
	client, err := net.ListenUDP("udp4", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.WriteToUDPAddrPort([]byte("hello"), netip.MustParseAddrPort("203.0.113.9:3478"))
	buf := make([]byte, 64)
	client.SetReadDeadline(time.Now().Add(3 * time.Second))
	n, from, err := client.ReadFromUDPAddrPort(buf)
	if err != nil || from != other || string(buf[:n]) != "hello" {
		t.Errorf("回包 = %q 来自 %s, %v；应来自 %s", buf[:n], from, err, other)
	}
}

func TestBlockQUIC(t *testing.T) {
	for _, block := range []bool{true, false} {
		t.Run("block="+map[bool]string{true: "on", false: "off"}[block], func(t *testing.T) {
			startTest(t, openNIC, Options{IPv6: true, BlockQUIC: block, ListenUDP: func(string) (PacketConn, error) {
				return newLoopbackUDP(), nil
			}})
			for _, target := range []string{"203.0.113.9:443", "[2001:db8::9]:443"} {
				conn, err := net.Dial("udp", target)
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				conn.Write([]byte("initial"))
				buf := make([]byte, 64)
				conn.SetReadDeadline(time.Now().Add(3 * time.Second))
				n, err := conn.Read(buf)
				switch {
				case block && !errors.Is(err, syscall.ECONNREFUSED):
					t.Errorf("%s: 拦截 QUIC 时应立即得到端口不可达，实际: %q, %v", target, buf[:n], err)
				case !block && (err != nil || string(buf[:n]) != "initial"):
					t.Errorf("%s: 不拦截时应正常中继，实际: %q, %v", target, buf[:n], err)
				}
			}

			// UDP 443 以外的端口不受影响。
			conn, err := net.Dial("udp", "203.0.113.9:8443")
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			conn.Write([]byte("other"))
			buf := make([]byte, 64)
			conn.SetReadDeadline(time.Now().Add(3 * time.Second))
			if n, err := conn.Read(buf); err != nil || string(buf[:n]) != "other" {
				t.Errorf("8443 端口: %q, %v", buf[:n], err)
			}
		})
	}
}

// ping 外网不应得到应答（协议栈不代答 ICMP echo）。超过 MTU 的 IPv6 ping 被内核分片后，
// ICMPv6 头落在分片头之后。
func TestPingGetsNoReply(t *testing.T) {
	startTest(t, openNIC, Options{IPv6: true})
	cases := []struct {
		name          string
		domain, proto int
		to            unix.Sockaddr
		echo          []byte
		isReply       func(msg []byte) bool
	}{
		{
			name: "IPv4", domain: unix.AF_INET, proto: unix.IPPROTO_ICMP,
			to:      &unix.SockaddrInet4{Addr: [4]byte{203, 0, 113, 7}},
			echo:    []byte{8, 0, 0xf7, 0xff, 0, 0, 0, 0}, // echo request，校验和已算好
			isReply: func([]byte) bool { return true },
		},
		{
			// ICMPv6 的校验和由内核填写。协议栈还会发出 MLD 等其它 ICMPv6，只认 echo reply。
			name: "IPv6 分片", domain: unix.AF_INET6, proto: unix.IPPROTO_ICMPV6,
			to:      &unix.SockaddrInet6{Addr: netip.MustParseAddr("2001:db8::7").As16()},
			echo:    append([]byte{128, 0, 0, 0, 0, 0, 0, 0}, make([]byte, 2*DefaultMTU)...),
			isReply: func(msg []byte) bool { return msg[0] == 129 },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fd, err := unix.Socket(tc.domain, unix.SOCK_RAW|unix.SOCK_CLOEXEC, tc.proto)
			if err != nil {
				t.Skipf("无法创建 ICMP socket: %v", err)
			}
			defer unix.Close(fd)
			if err := unix.Sendto(fd, tc.echo, 0, tc.to); err != nil {
				t.Fatal(err)
			}
			unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &unix.Timeval{Usec: 500_000})
			buf := make([]byte, 128)
			for {
				n, _, err := unix.Recvfrom(fd, buf, 0)
				if err != nil {
					return
				}
				if n > 0 && tc.isReply(buf[:n]) {
					t.Fatalf("收到了 ICMP 应答: % x", buf[:n])
				}
			}
		})
	}
}

func randReader(seed uint64) io.Reader {
	return rand.NewChaCha8([32]byte{byte(seed), byte(seed >> 8)})
}
