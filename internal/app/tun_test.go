// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package app

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"net"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mintfog/sniffy/internal/netbind"
	"github.com/mintfog/sniffy/internal/service"
	"github.com/mintfog/sniffy/internal/tunhost"
)

// recordingBinder 记录出站绑定的变化，bound 表示当前是否绑定着；events 不为 nil 时按顺序记下每次绑定。
type recordingBinder struct {
	bound  atomic.Bool
	events *eventLog
}

func (b *recordingBinder) BindOutbound(e netbind.Egress) {
	b.bound.Store(e.IPv4 != nil || e.IPv6 != nil)
	if b.events != nil {
		b.events.add("绑定 " + e.String())
	}
}

// fakeHost 是本进程里的虚拟网卡。closeErr 不为 nil 时 Close 返回它、网卡不消失；events 不为 nil 时
// 按顺序记下 SetEgress 收到的出口。
type fakeHost struct {
	closeErr error
	events   *eventLog
	once     sync.Once
	done     chan struct{}
}

func (h *fakeHost) Name() string          { return "sniffy0" }
func (h *fakeHost) Done() <-chan struct{} { return h.done }
func (h *fakeHost) vanish()               { h.once.Do(func() { close(h.done) }) }

func (h *fakeHost) SetEgress(e netbind.Egress) error {
	if h.events != nil {
		h.events.add("网卡 " + e.String())
	}
	return nil
}

// eventLog 是按发生顺序记下的事件，可并发追加。
type eventLog struct {
	mu     sync.Mutex
	events []string
}

func (l *eventLog) add(e string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, e)
}

func (l *eventLog) list() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.events)
}

// recordingLogger 记下 tunhost.Logger 收到的日志。
type recordingLogger struct{ eventLog }

func (l *recordingLogger) Info(msg string, args ...any) { l.add("INFO " + fmt.Sprintf(msg, args...)) }
func (l *recordingLogger) Warn(msg string, args ...any) { l.add("WARN " + fmt.Sprintf(msg, args...)) }

func (h *fakeHost) Close() error {
	if h.closeErr != nil {
		return h.closeErr
	}
	h.vanish()
	return nil
}

func startFakeDevice(host *fakeHost) (*tunDevice, *recordingBinder) {
	binder := &recordingBinder{}
	binder.BindOutbound(netbind.Egress{IPv4: &net.Interface{Index: 1, Name: "eth0"}})
	dev := &tunDevice{host: host, binder: binder, log: &recordingLogger{}, done: make(chan struct{})}
	go dev.watch()
	return dev, binder
}

var (
	eth0  = &net.Interface{Index: 2, Name: "eth0"}
	wlan0 = &net.Interface{Index: 3, Name: "wlan0"}
)

// routeFeed 代替系统路由表：followEgress 每轮依次读到 routes 里的各项（IPv4、IPv6 默认路由所在网卡
// 的索引），读完后一直是最后一项；gateways 里对应一轮的项是这一轮的网关，没有的为无效地址。err 里
// 对应一轮的错误不为 nil 时，这一轮读路由表失败。
type routeFeed struct {
	mu       sync.Mutex
	routes   [][2]int
	gateways [][2]netip.Addr
	err      []error
	reads    int
	lookups  int
}

func (f *routeFeed) routesFunc() defaultRoutes {
	return defaultRoutes{route: f.route, lookup: f.lookup}
}

// route 在每轮第一次读（IPv4）时前进到下一项。
func (f *routeFeed) route(ipv6 bool) (netbind.Route, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !ipv6 {
		f.reads++
	}
	i := min(f.reads, len(f.routes)) - 1
	if i < len(f.err) && f.err[i] != nil {
		return netbind.Route{}, f.err[i]
	}
	family := 0
	if ipv6 {
		family = 1
	}
	r := netbind.Route{Index: f.routes[i][family]}
	if i < len(f.gateways) {
		r.Gateway = f.gateways[i][family]
	}
	return r, nil
}

func (f *routeFeed) lookup(index int) (*net.Interface, error) {
	f.mu.Lock()
	f.lookups++
	f.mu.Unlock()
	for _, iface := range []*net.Interface{eth0, wlan0} {
		if iface.Index == index {
			return iface, nil
		}
	}
	return nil, fmt.Errorf("没有索引为 %d 的网卡", index)
}

// drained 在 followEgress 读完全部各项、又多读了几轮之后返回。
func (f *routeFeed) drained(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		f.mu.Lock()
		done := f.reads >= len(f.routes)+3
		f.mu.Unlock()
		if done {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("followEgress 没有按周期读路由表")
		}
		time.Sleep(time.Millisecond)
	}
}

// startFollowing 以 start 为开启时的出口运行 followEgress，出口的变化记进返回的 eventLog。
func startFollowing(start netbind.Egress, ipv6 bool, feed *routeFeed) (*tunDevice, *fakeHost, *recordingLogger, *eventLog) {
	events := &eventLog{}
	host := &fakeHost{done: make(chan struct{}), events: events}
	binder := &recordingBinder{}
	binder.BindOutbound(start)
	binder.events = events
	logger := &recordingLogger{}
	dev := &tunDevice{host: host, binder: binder, log: logger, done: make(chan struct{})}
	go dev.watch()
	go dev.followEgress(start, ipv6, feed.routesFunc(), time.Millisecond)
	return dev, host, logger, events
}

// TestFollowEgress：默认路由所在的网卡变了，先改绑 sniffy 自己的出站、再更新网卡一侧；某个地址族的
// 默认路由消失时出站绑定保留在原网卡上，IPv6 停止接管；IPv4 默认路由回到原网卡时重绑出站以丢弃
// 空闲连接，网卡一侧以原来的出口重写绕过列表的路由。
func TestFollowEgress(t *testing.T) {
	t.Parallel()
	egress := func(v4, v6 *net.Interface) netbind.Egress { return netbind.Egress{IPv4: v4, IPv6: v6} }
	const none, e, w = 0, 2, 3 // 没有默认路由、eth0、wlan0 的索引
	for _, tc := range []struct {
		name    string
		ipv6    bool
		start   netbind.Egress
		routes  [][2]int
		want    []string
		lookups int
	}{
		{"没有变化", true, egress(eth0, eth0), [][2]int{{e, e}}, nil, 0},
		{"IPv4 换了网卡", true, egress(eth0, nil), [][2]int{{w, none}},
			[]string{"绑定 IPv4 wlan0，IPv6 无", "网卡 IPv4 wlan0，IPv6 无"}, 1},
		{"IPv4 默认路由消失", true, egress(eth0, nil), [][2]int{{none, none}}, nil, 0},
		{"IPv4 默认路由消失后回到原网卡", true, egress(eth0, nil), [][2]int{{none, none}, {e, none}},
			[]string{"绑定 IPv4 eth0，IPv6 无", "网卡 IPv4 eth0，IPv6 无"}, 0},
		{"IPv4 默认路由消失后出现在别的网卡上", true, egress(eth0, nil), [][2]int{{none, none}, {w, none}},
			[]string{"绑定 IPv4 wlan0，IPv6 无", "网卡 IPv4 wlan0，IPv6 无"}, 1},
		{"IPv6 默认路由消失", true, egress(eth0, eth0), [][2]int{{e, none}},
			[]string{"绑定 IPv4 eth0，IPv6 eth0", "网卡 IPv4 eth0，IPv6 无"}, 0},
		{"出现 IPv6 默认路由", true, egress(eth0, nil), [][2]int{{e, w}},
			[]string{"绑定 IPv4 eth0，IPv6 wlan0", "网卡 IPv4 eth0，IPv6 wlan0"}, 1},
		{"IPv6 默认路由消失后出现在别的网卡上", true, egress(eth0, eth0), [][2]int{{e, none}, {e, w}},
			[]string{"绑定 IPv4 eth0，IPv6 eth0", "网卡 IPv4 eth0，IPv6 无", "绑定 IPv4 eth0，IPv6 wlan0", "网卡 IPv4 eth0，IPv6 wlan0"}, 1},
		{"IPv6 默认路由消失后回到原网卡", true, egress(eth0, eth0), [][2]int{{e, none}, {e, e}},
			[]string{"绑定 IPv4 eth0，IPv6 eth0", "网卡 IPv4 eth0，IPv6 无", "绑定 IPv4 eth0，IPv6 eth0", "网卡 IPv4 eth0，IPv6 eth0"}, 1},
		{"不接管 IPv6", false, egress(eth0, nil), [][2]int{{e, w}, {w, w}},
			[]string{"绑定 IPv4 wlan0，IPv6 无", "网卡 IPv4 wlan0，IPv6 无"}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			feed := &routeFeed{routes: tc.routes}
			_, host, _, events := startFollowing(tc.start, tc.ipv6, feed)
			defer host.vanish()
			feed.drained(t)
			if got := events.list(); !slices.Equal(got, tc.want) {
				t.Errorf("出口变化 = %q，期望 %q", got, tc.want)
			}
			// 网卡没变时只读路由表，不查网卡信息：后者在 Windows 上每次要几毫秒。
			feed.mu.Lock()
			lookups := feed.lookups
			feed.mu.Unlock()
			if lookups != tc.lookups {
				t.Errorf("查了 %d 次网卡信息，期望 %d 次", lookups, tc.lookups)
			}
		})
	}
}

// TestFollowEgressGateway：网卡没变而网关变了时重绑出站、以原来的出口通知网卡一侧；第一轮读到的网关
// 不算变化。
func TestFollowEgressGateway(t *testing.T) {
	t.Parallel()
	gw := netip.MustParseAddr
	const e = 2
	for _, tc := range []struct {
		name     string
		routes   [][2]int
		gateways [][2]netip.Addr
		want     []string
	}{
		{"网关不变", [][2]int{{e, e}, {e, e}}, [][2]netip.Addr{{gw("192.168.1.1"), gw("fe80::1")}, {gw("192.168.1.1"), gw("fe80::1")}}, nil},
		{"IPv4 网关变了", [][2]int{{e, e}, {e, e}}, [][2]netip.Addr{{gw("192.168.1.1"), gw("fe80::1")}, {gw("10.0.0.1"), gw("fe80::1")}},
			[]string{"绑定 IPv4 eth0，IPv6 eth0", "网卡 IPv4 eth0，IPv6 eth0"}},
		{"IPv6 网关变了", [][2]int{{e, e}, {e, e}}, [][2]netip.Addr{{gw("192.168.1.1"), gw("fe80::1")}, {gw("192.168.1.1"), gw("fe80::2")}},
			[]string{"绑定 IPv4 eth0，IPv6 eth0", "网卡 IPv4 eth0，IPv6 eth0"}},
		{"IPv4 默认路由消失不算网关变化", [][2]int{{e, e}, {0, e}}, [][2]netip.Addr{{gw("192.168.1.1"), gw("fe80::1")}, {{}, gw("fe80::1")}}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			feed := &routeFeed{routes: tc.routes, gateways: tc.gateways}
			_, host, logger, events := startFollowing(netbind.Egress{IPv4: eth0, IPv6: eth0}, true, feed)
			defer host.vanish()
			feed.drained(t)
			if got := events.list(); !slices.Equal(got, tc.want) {
				t.Errorf("出口变化 = %q，期望 %q", got, tc.want)
			}
			logged := slices.ContainsFunc(logger.list(), func(l string) bool { return strings.Contains(l, "网关变了") })
			if logged != (tc.want != nil) {
				t.Errorf("网关变化的日志 = %v，期望 %v: %q", logged, tc.want != nil, logger.list())
			}
		})
	}
}

// TestFollowEgressReadError：读不出路由表时不动出口，连续失败只记一次日志。
func TestFollowEgressReadError(t *testing.T) {
	t.Parallel()
	readErr := errors.New("netlink 出错")
	feed := &routeFeed{routes: [][2]int{{}, {}, {wlan0.Index, 0}}, err: []error{readErr, readErr}}
	_, host, logger, events := startFollowing(netbind.Egress{IPv4: eth0}, true, feed)
	defer host.vanish()
	feed.drained(t)
	if got, want := events.list(), []string{"绑定 IPv4 wlan0，IPv6 无", "网卡 IPv4 wlan0，IPv6 无"}; !slices.Equal(got, want) {
		t.Errorf("出口变化 = %q，期望 %q", got, want)
	}
	warns := 0
	for _, l := range logger.list() {
		if strings.Contains(l, "netlink 出错") {
			warns++
		}
	}
	if warns != 1 {
		t.Errorf("读取失败记了 %d 次日志，期望 1 次: %q", warns, logger.list())
	}
}

// TestFollowEgressStopsWithDevice：网卡消失、出站绑定撤销之后，默认路由再变也不能把绑定恢复：
// 此后已没有路由指向虚拟网卡。
func TestFollowEgressStopsWithDevice(t *testing.T) {
	t.Parallel()
	feed := &routeFeed{routes: [][2]int{{eth0.Index, 0}}}
	dev, host, _, events := startFollowing(netbind.Egress{IPv4: eth0}, true, feed)
	binder := dev.binder.(*recordingBinder)
	host.vanish()
	<-dev.Done()
	feed.mu.Lock()
	feed.routes = append(feed.routes, [2]int{wlan0.Index, 0})
	feed.mu.Unlock()
	time.Sleep(20 * time.Millisecond)
	if dev.bind(netbind.Egress{IPv4: wlan0}) || binder.bound.Load() {
		t.Errorf("网卡消失后出站又被绑定: %q", events.list())
	}
}

func TestTunDeviceClose(t *testing.T) {
	t.Parallel()
	dev, binder := startFakeDevice(&fakeHost{done: make(chan struct{})})
	if err := dev.Close(); err != nil {
		t.Fatal(err)
	}
	if binder.bound.Load() {
		t.Error("Close 返回时出站绑定应已撤销")
	}
	if err := dev.Err(); err != nil {
		t.Errorf("经 Close 关闭的网卡 Err = %v，期望 nil", err)
	}
}

// TestTunDeviceCloseTimeoutKeepsBinding：网卡没能及时关掉时保持出站绑定，等它真正消失后才撤销，
// 也不按失效报告。
func TestTunDeviceCloseTimeoutKeepsBinding(t *testing.T) {
	t.Parallel()
	host := &fakeHost{closeErr: errors.New("helper 没有在 10s 内拆掉虚拟网卡"), done: make(chan struct{})}
	dev, binder := startFakeDevice(host)
	if err := dev.Close(); err == nil {
		t.Fatal("网卡关不掉时 Close 应返回错误")
	}
	select {
	case <-dev.Done():
		t.Fatal("网卡还在时 Done 不应关闭")
	case <-time.After(50 * time.Millisecond):
	}
	if !binder.bound.Load() {
		t.Fatal("网卡还在时撤销了出站绑定，sniffy 自己的出站会进虚拟网卡")
	}
	host.vanish()
	<-dev.Done()
	if binder.bound.Load() {
		t.Error("网卡消失后应撤销出站绑定")
	}
	if err := dev.Err(); err != nil {
		t.Errorf("被要求关闭的网卡 Err = %v，期望 nil", err)
	}
}

func TestTunDeviceFailsOnItsOwn(t *testing.T) {
	t.Parallel()
	host := &fakeHost{done: make(chan struct{})}
	dev, binder := startFakeDevice(host)
	host.vanish()
	<-dev.Done()
	if binder.bound.Load() {
		t.Error("网卡消失后应撤销出站绑定")
	}
	if err := dev.Err(); err == nil {
		t.Error("网卡自己停止时 Err 应报告失效")
	}
}

// TestTunStatusChangedLogs：按状态的变化记日志。开启失败、运行中失效、关闭各记一条；被放弃的开启、
// 状态里只有信任结论变化的开启中，什么都不记；根证书未被信任时每次开启成功都警告。
func TestTunStatusChangedLogs(t *testing.T) {
	preserveAppLogging(t)
	var logs bytes.Buffer
	log.SetOutput(&logs)
	b := &tunBackend{logger: NewLogger(false)}
	st := func(state, name, err string, untrusted bool) service.TunStatus {
		return service.TunStatus{State: state, Name: name, Error: err, CAUntrusted: untrusted}
	}
	const (
		off, starting, running = service.TunStateOff, service.TunStateStarting, service.TunStateRunning
		stopping, failed       = service.TunStateStopping, service.TunStateFailed
	)
	for _, tc := range []struct {
		name       string
		prev, next service.TunStatus
		want       string // 为空表示不记日志
	}{
		{"开启", st(off, "", "", false), st(starting, "", "", false), ""},
		{"开启中得出未信任", st(starting, "", "", false), st(starting, "", "", true), ""},
		{"开启成功且根证书已被信任", st(starting, "", "", false), st(running, "sniffy0", "", false), ""},
		{"开启成功但根证书未被信任", st(starting, "", "", true), st(running, "sniffy0", "", true), "根证书未被系统信任"},
		{"运行中变为信任", st(running, "sniffy0", "", true), st(running, "sniffy0", "", false), "恢复解密"},
		{"运行中变为未信任", st(running, "sniffy0", "", false), st(running, "sniffy0", "", true), "根证书未被系统信任"},
		{"开启失败", st(starting, "", "", false), st(failed, "", "用户取消了提权", false), "开启虚拟网卡失败: 用户取消了提权"},
		{"开启中被放弃", st(starting, "", "", false), st(off, "", "", false), ""},
		{"开启失败但网卡可能还在", st(starting, "", "", false), st(stopping, "", "i/o timeout", false), "网卡可能还在"},
		{"其后网卡消失", st(stopping, "", "i/o timeout", false), st(failed, "", "i/o timeout", false), "（开启未完成） 已关闭"},
		{"开始关闭", st(running, "sniffy0", "", false), st(stopping, "sniffy0", "", false), ""},
		{"没能及时关闭", st(stopping, "sniffy0", "", false), st(stopping, "sniffy0", "超时", false), "关闭虚拟网卡 sniffy0: 超时"},
		{"关闭完成", st(stopping, "sniffy0", "", false), st(off, "", "", false), "虚拟网卡 sniffy0 已关闭"},
		{"运行中失效", st(running, "sniffy0", "", false), st(failed, "", "虚拟网卡已失效（helper 已退出）", false), "sniffy0: 虚拟网卡已失效（helper 已退出），流量恢复直连"},
		{"清除失败原因", st(failed, "", "x", false), st(off, "", "", false), ""},
	} {
		logs.Reset()
		b.TunStatusChanged(tc.prev, tc.next)
		got := logs.String()
		if tc.want == "" && got != "" || !strings.Contains(got, tc.want) {
			t.Errorf("%s：日志 = %q，期望 %q", tc.name, got, tc.want)
		}
	}
}

func TestClassifyStartError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		err      error
		canceled bool
		code     string
	}{
		{name: "取消提权", err: fmt.Errorf("等待 helper 连入: helper 没有连入就结束了: %w", tunhost.ErrElevationCanceled), canceled: true},
		{name: "无法自动提权", err: fmt.Errorf("启动 helper: %w: 找不到 pkexec", tunhost.ErrElevationUnavailable),
			code: service.TunErrorElevationUnavailable},
		{name: "其它失败", err: errors.New("没有 IPv4 默认路由")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyStartError(tt.err)
			if !errors.Is(got, tt.err) {
				t.Errorf("分类后丢了原来的错误链: %v", got)
			}
			if errors.Is(got, service.ErrTunStartCanceled) != tt.canceled {
				t.Errorf("按放弃处理 = %v，期望 %v", !tt.canceled, tt.canceled)
			}
			var code string
			var te *service.TunError
			if errors.As(got, &te) {
				code = te.Code
			}
			if code != tt.code {
				t.Errorf("分类 = %q，期望 %q", code, tt.code)
			}
			if !tt.canceled && got.Error() != tt.err.Error() {
				t.Errorf("原因 = %q，应保持 %q", got, tt.err)
			}
		})
	}
}
