// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package tun

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"io"
	"math/rand/v2"
	"net"
	"net/netip"
	"os"
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
	if err := configureNIC(ifaceName, cfg); err != nil {
		dev.Close()
		return nil, err
	}
	return &nic{name: ifaceName, link: newDeviceEndpoint(dev, cfg.mtu, virtioHdrLen, cfg.onDown)}, nil
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
	n, err := open(nicConfig{mtu: opts.MTU, ipv6: opts.IPv6, onDown: func() { close(down) }})
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
