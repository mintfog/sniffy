// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package service

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mintfog/sniffy/ca"
	"github.com/mintfog/sniffy/internal/core"
)

// fakeTunBackend 记录 service 对运行时的调用与状态变化。start 决定每次开启的结果，为 nil 时开启成功。
type fakeTunBackend struct {
	mu    sync.Mutex
	calls []string
	// changes 是 TunStatusChanged 收到的状态变化，形如 starting>running。
	changes []string
	opts    []TunOptions
	start   func(ctx context.Context) (TunDevice, error)
	bypass  []DecryptBypassEntry
}

func (b *fakeTunBackend) record(call string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls = append(b.calls, call)
}

func (b *fakeTunBackend) takeCalls() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	calls := b.calls
	b.calls = nil
	return calls
}

func (b *fakeTunBackend) Start(ctx context.Context, opts TunOptions) (TunDevice, error) {
	b.record("start")
	b.mu.Lock()
	b.opts = append(b.opts, opts)
	start := b.start
	b.mu.Unlock()
	if start == nil {
		return newFakeTunDevice(), nil
	}
	return start(ctx)
}

func (b *fakeTunBackend) SetCATrusted(trusted bool) {
	if trusted {
		b.record("trusted")
	} else {
		b.record("untrusted")
	}
}

func (b *fakeTunBackend) TunStatusChanged(prev, next TunStatus) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.changes = append(b.changes, prev.State+">"+next.State)
}

func (b *fakeTunBackend) DecryptBypass() []DecryptBypassEntry {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.bypass)
}

func (b *fakeTunBackend) ForgetDecryptBypass(host string) { b.record("forget-bypass " + host) }

func (b *fakeTunBackend) ClearDecryptBypass() { b.record("clear-bypass") }

type fakeTunDevice struct {
	// closeErr 不为 nil 时 Close 返回它、网卡不消失，模拟 helper 失去响应。
	closeErr error
	closes   atomic.Int32
	once     sync.Once
	err      error
	done     chan struct{}
}

func newFakeTunDevice() *fakeTunDevice { return &fakeTunDevice{done: make(chan struct{})} }

func (d *fakeTunDevice) Name() string          { return "sniffy0" }
func (d *fakeTunDevice) Done() <-chan struct{} { return d.done }
func (d *fakeTunDevice) Err() error            { return d.err }

func (d *fakeTunDevice) Close() error {
	d.closes.Add(1)
	if d.closeErr != nil {
		return d.closeErr
	}
	d.vanish(nil)
	return nil
}

// vanish 让网卡消失，err 是它自己停止的原因。
func (d *fakeTunDevice) vanish(err error) {
	d.once.Do(func() {
		d.err = err
		close(d.done)
	})
}

// newTunService 返回接入了 backend 的服务，根证书的信任状态由 trusted 决定。
func newTunService(t *testing.T, backend *fakeTunBackend, trusted *atomic.Bool) *Service {
	t.Helper()
	s := newTestService(t)
	s.SetTunBackend(backend)
	s.tun.isTrusted = func() (bool, error) { return trusted.Load(), nil }
	t.Cleanup(func() { _, _ = s.StopTun() })
	return s
}

func trustedCA(v bool) *atomic.Bool {
	var b atomic.Bool
	b.Store(v)
	return &b
}

// waitTunState 等到状态变为 want 并返回此时的状态。
func waitTunState(t *testing.T, s *Service, want string) TunStatus {
	t.Helper()
	return waitTunUntil(t, s, "state="+want, func(st TunStatus) bool { return st.State == want })
}

func waitTunUntil(t *testing.T, s *Service, what string, until func(TunStatus) bool) TunStatus {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	st, err := s.waitTun(ctx, until)
	if err != nil {
		t.Fatalf("状态停在 %+v，等不到 %s", st, what)
	}
	return st
}

func TestTunStartRunStop(t *testing.T) {
	t.Parallel()
	backend := &fakeTunBackend{}
	dev := newFakeTunDevice()
	backend.start = func(context.Context) (TunDevice, error) { return dev, nil }
	s := newTunService(t, backend, trustedCA(true))
	events := newEventRecorder(t, s.Bus())

	st, err := s.StartTun()
	if err != nil || st.State != TunStateStarting {
		t.Fatalf("StartTun = %+v, %v，期望 starting", st, err)
	}
	st = waitTunState(t, s, TunStateRunning)
	if st.Name != "sniffy0" || st.CAUntrusted || st.Error != "" {
		t.Errorf("运行中的状态 = %+v", st)
	}
	if again, err := s.StartTun(); err != nil || again != st {
		t.Errorf("运行中再次开启 = %+v, %v，期望原样返回 %+v", again, err, st)
	}
	if got := backend.takeCalls(); !slices.Equal(got, []string{"trusted", "start"}) {
		t.Errorf("调用 = %v，期望先下发信任结论再开启网卡、且只开启一次", got)
	}

	st, err = s.StopTun()
	if err != nil || st.State != TunStateOff {
		t.Fatalf("StopTun = %+v, %v，期望 off", st, err)
	}
	if n := dev.closes.Load(); n != 1 {
		t.Errorf("网卡关闭了 %d 次", n)
	}

	var states []string
	var revision uint64
	for _, e := range events.drain() {
		if e.Type != core.EventTunState {
			continue
		}
		got := e.Payload.(TunStatus)
		if got.Revision <= revision {
			t.Errorf("Revision %d 没有递增（上一个 %d）", got.Revision, revision)
		}
		revision = got.Revision
		states = append(states, got.State)
	}
	if want := []string{TunStateStarting, TunStateRunning, TunStateStopping, TunStateOff}; !slices.Equal(states, want) {
		t.Errorf("推送的状态 = %v，期望 %v", states, want)
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if want := []string{"off>starting", "starting>running", "running>stopping", "stopping>off"}; !slices.Equal(backend.changes, want) {
		t.Errorf("TunStatusChanged 收到 %v，期望 %v", backend.changes, want)
	}
}

func TestTunStartFailure(t *testing.T) {
	t.Parallel()
	backend := &fakeTunBackend{}
	backend.start = func(context.Context) (TunDevice, error) { return nil, errors.New("没有 IPv4 默认路由") }
	s := newTunService(t, backend, trustedCA(true))

	if _, err := s.StartTun(); err != nil {
		t.Fatal(err)
	}
	st := waitTunState(t, s, TunStateFailed)
	if st.Error != "没有 IPv4 默认路由" {
		t.Errorf("失败原因 = %q", st.Error)
	}
	// 失败后可以重新开启。
	backend.start = nil
	if _, err := s.StartTun(); err != nil {
		t.Fatal(err)
	}
	st = waitTunState(t, s, TunStateRunning)
	if st.Error != "" {
		t.Errorf("重新开启后仍带着上次的失败原因 %q", st.Error)
	}
}

func TestTunStopClearsFailure(t *testing.T) {
	t.Parallel()
	backend := &fakeTunBackend{start: func(context.Context) (TunDevice, error) { return nil, errors.New("x") }}
	s := newTunService(t, backend, trustedCA(true))
	if _, err := s.StartTun(); err != nil {
		t.Fatal(err)
	}
	waitTunState(t, s, TunStateFailed)
	st, err := s.StopTun()
	if err != nil || st.State != TunStateOff || st.Error != "" {
		t.Errorf("StopTun = %+v, %v，期望清除失败原因、变为 off", st, err)
	}
}

// TestTunStopWhileStarting：开启中（如等用户确认提权）关闭即放弃开启，状态回到 off 而不是 failed。
func TestTunStopWhileStarting(t *testing.T) {
	t.Parallel()
	backend := &fakeTunBackend{}
	entered := make(chan struct{})
	backend.start = func(ctx context.Context) (TunDevice, error) {
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	s := newTunService(t, backend, trustedCA(true))
	if _, err := s.StartTun(); err != nil {
		t.Fatal(err)
	}
	<-entered
	st, err := s.StopTun()
	if err != nil || st.State != TunStateOff || st.Error != "" {
		t.Errorf("StopTun = %+v, %v，期望 off", st, err)
	}
	if st, err := s.WaitTunSettled(context.Background()); err != nil || st.State != TunStateOff {
		t.Errorf("WaitTunSettled = %+v, %v", st, err)
	}
}

// TestTunStopRacesStartCompletion：关闭请求到达时开启恰好完成、没能取消，关闭要把开好的网卡关掉。
func TestTunStopRacesStartCompletion(t *testing.T) {
	t.Parallel()
	backend := &fakeTunBackend{}
	dev := newFakeTunDevice()
	entered := make(chan struct{})
	backend.start = func(ctx context.Context) (TunDevice, error) {
		close(entered)
		<-ctx.Done()
		return dev, nil
	}
	s := newTunService(t, backend, trustedCA(true))
	if _, err := s.StartTun(); err != nil {
		t.Fatal(err)
	}
	<-entered
	st, err := s.StopTun()
	if err != nil || st.State != TunStateOff {
		t.Errorf("StopTun = %+v, %v，期望 off", st, err)
	}
	if n := dev.closes.Load(); n != 1 {
		t.Errorf("开启完成的网卡关闭了 %d 次，期望 1 次", n)
	}
}

func TestTunDeviceStopsOnItsOwn(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		cause error
		state string
	}{
		{"失效", errors.New("虚拟网卡已失效（helper 已退出）"), TunStateFailed},
		{"helper 收到退出信号后正常停止", nil, TunStateOff},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dev := newFakeTunDevice()
			backend := &fakeTunBackend{start: func(context.Context) (TunDevice, error) { return dev, nil }}
			s := newTunService(t, backend, trustedCA(true))
			if _, err := s.StartTun(); err != nil {
				t.Fatal(err)
			}
			waitTunState(t, s, TunStateRunning)
			dev.vanish(tc.cause)
			st := waitTunState(t, s, tc.state)
			if tc.cause != nil && st.Error != tc.cause.Error() {
				t.Errorf("失败原因 = %q", st.Error)
			}
		})
	}
}

// TestTunStopTimeout：网卡没能及时关掉时状态停在 stopping 并带上原因，此时不能再开启；网卡消失后
// 才变为 off。
func TestTunStopTimeout(t *testing.T) {
	t.Parallel()
	dev := newFakeTunDevice()
	dev.closeErr = errors.New("helper 没有在 10s 内拆掉虚拟网卡 sniffy0")
	backend := &fakeTunBackend{start: func(context.Context) (TunDevice, error) { return dev, nil }}
	s := newTunService(t, backend, trustedCA(true))
	if _, err := s.StartTun(); err != nil {
		t.Fatal(err)
	}
	waitTunState(t, s, TunStateRunning)

	st, err := s.StopTun()
	if err == nil || st.State != TunStateStopping || st.Error != dev.closeErr.Error() {
		t.Fatalf("StopTun = %+v, %v，期望 stopping 并带上原因", st, err)
	}
	if _, err := s.StartTun(); !errors.Is(err, ErrTunStopping) {
		t.Errorf("网卡还在时开启 = %v，期望 ErrTunStopping", err)
	}
	dev.vanish(nil)
	if st := waitTunState(t, s, TunStateOff); st.Error != "" {
		t.Errorf("网卡消失后仍带着原因 %q", st.Error)
	}
}

// TestTunStartFailsWithDeviceLingering：开启失败、网卡却可能还在时停在 stopping，网卡消失后以开启
// 失败的原因变为 failed。
func TestTunStartFailsWithDeviceLingering(t *testing.T) {
	t.Parallel()
	dev := newFakeTunDevice()
	startErr := errors.New("等待 helper 开启网卡: i/o timeout")
	backend := &fakeTunBackend{start: func(context.Context) (TunDevice, error) { return dev, startErr }}
	s := newTunService(t, backend, trustedCA(true))
	if _, err := s.StartTun(); err != nil {
		t.Fatal(err)
	}
	st := waitTunState(t, s, TunStateStopping)
	if st.Error != startErr.Error() {
		t.Errorf("stopping 的原因 = %q", st.Error)
	}
	if _, err := s.StartTun(); !errors.Is(err, ErrTunStopping) {
		t.Errorf("网卡还在时开启 = %v，期望 ErrTunStopping", err)
	}
	dev.vanish(nil)
	if st := waitTunState(t, s, TunStateFailed); st.Error != startErr.Error() {
		t.Errorf("失败原因 = %q", st.Error)
	}
}

func TestTunOptionsFromConfig(t *testing.T) {
	t.Parallel()
	backend := &fakeTunBackend{}
	s := newTunService(t, backend, trustedCA(true))
	if _, err := s.StartTun(); err != nil {
		t.Fatal(err)
	}
	waitTunState(t, s, TunStateRunning)
	if _, err := s.StopTun(); err != nil {
		t.Fatal(err)
	}
	s.UpdateConfig(map[string]any{"tunIPv6": false, "tunBlockQUIC": false})
	if _, err := s.StartTun(); err != nil {
		t.Fatal(err)
	}
	waitTunState(t, s, TunStateRunning)
	want := []TunOptions{{IPv6: true, BlockQUIC: true}, {}}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if !slices.Equal(backend.opts, want) {
		t.Errorf("开启参数 = %+v，期望 %+v", backend.opts, want)
	}
}

// TestTunUntrustedCA：开启网卡之前先下发信任结论，此后每次复查都下发；状态里的 caUntrusted 随结论
// 变化。
func TestTunUntrustedCA(t *testing.T) {
	t.Parallel()
	trusted := trustedCA(false)
	backend := &fakeTunBackend{}
	s := newTunService(t, backend, trusted)
	if _, err := s.StartTun(); err != nil {
		t.Fatal(err)
	}
	if st := waitTunState(t, s, TunStateRunning); !st.CAUntrusted {
		t.Errorf("根证书未信任时状态 = %+v，期望带 caUntrusted", st)
	}
	if got := backend.takeCalls(); !slices.Equal(got, []string{"untrusted", "start"}) {
		t.Errorf("调用 = %v，期望开启网卡之前下发未信任", got)
	}

	for _, v := range []bool{false, true, false} {
		trusted.Store(v)
		s.RecheckTunCATrust()
		if st := s.TunStatus(); st.CAUntrusted == v || st.State != TunStateRunning {
			t.Errorf("信任=%v 时状态 = %+v", v, st)
		}
		want := "untrusted"
		if v {
			want = "trusted"
		}
		if got := backend.takeCalls(); !slices.Equal(got, []string{want}) {
			t.Errorf("信任=%v 时调用 = %v，期望 %s", v, got, want)
		}
	}
}

func TestTunTrustCheckErrorBlindForwards(t *testing.T) {
	t.Parallel()
	backend := &fakeTunBackend{}
	s := newTunService(t, backend, trustedCA(true))
	s.tun.isTrusted = func() (bool, error) { return false, errors.New("根证书尚未就绪") }
	if _, err := s.StartTun(); err != nil {
		t.Fatal(err)
	}
	if st := waitTunState(t, s, TunStateRunning); !st.CAUntrusted {
		t.Errorf("检查出错时状态 = %+v，期望按未信任处理", st)
	}
	if got := backend.takeCalls(); !slices.Equal(got, []string{"untrusted", "start"}) {
		t.Errorf("调用 = %v", got)
	}
}

func TestTunTrustPolling(t *testing.T) {
	t.Parallel()
	trusted := trustedCA(false)
	backend := &fakeTunBackend{}
	s := newTunService(t, backend, trusted)
	s.tun.trustPoll = 10 * time.Millisecond
	if _, err := s.StartTun(); err != nil {
		t.Fatal(err)
	}
	waitTunState(t, s, TunStateRunning)
	trusted.Store(true)
	waitTunUntil(t, s, "复查发现根证书已被信任", func(st TunStatus) bool { return !st.CAUntrusted })
}

func TestTunSetCARechecksTrust(t *testing.T) {
	t.Parallel()
	trusted := trustedCA(true)
	backend := &fakeTunBackend{}
	s := newTunService(t, backend, trusted)
	if _, err := s.StartTun(); err != nil {
		t.Fatal(err)
	}
	waitTunState(t, s, TunStateRunning)
	regenerated, err := ca.NewInMemorySelfSignedCA()
	if err != nil {
		t.Fatal(err)
	}
	trusted.Store(false)
	s.SetCA(regenerated)
	if st := s.TunStatus(); !st.CAUntrusted {
		t.Errorf("换上未被信任的根证书后状态 = %+v，期望立即改为盲转发", st)
	}
}

func TestTunRecheckWhileOffDoesNothing(t *testing.T) {
	t.Parallel()
	backend := &fakeTunBackend{}
	s := newTunService(t, backend, trustedCA(false))
	s.RecheckTunCATrust()
	if got := backend.takeCalls(); len(got) != 0 {
		t.Errorf("未开启时复查调用了 %v", got)
	}
}

func TestTunUnavailableWithoutBackend(t *testing.T) {
	t.Parallel()
	s := New(nil, nil, "", "")
	if st := s.TunStatus(); st.State != TunStateOff {
		t.Errorf("初始状态 = %+v", st)
	}
	if _, err := s.StartTun(); !errors.Is(err, ErrTunUnavailable) {
		t.Errorf("StartTun = %v，期望 ErrTunUnavailable", err)
	}
	if st, err := s.StopTun(); err != nil || st.State != TunStateOff {
		t.Errorf("StopTun = %+v, %v", st, err)
	}
	if list := s.DecryptBypass(); list == nil || len(list) != 0 {
		t.Errorf("直通名单 = %#v，期望空列表", list)
	}
	if err := s.ForgetDecryptBypass("a.test:443"); err != nil {
		t.Errorf("ForgetDecryptBypass = %v", err)
	}
	s.ClearDecryptBypass()
}

func TestDecryptBypassDelegates(t *testing.T) {
	t.Parallel()
	entry := DecryptBypassEntry{Host: "pinned.test:443", Reason: "cert_rejected", Since: "2026-11-18T00:00:00Z"}
	backend := &fakeTunBackend{bypass: []DecryptBypassEntry{entry}}
	s := newTunService(t, backend, trustedCA(true))
	if got := s.DecryptBypass(); !slices.Equal(got, []DecryptBypassEntry{entry}) {
		t.Errorf("直通名单 = %+v", got)
	}
	if err := s.ForgetDecryptBypass("pinned.test:443"); err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"", "pinned.test", "::1"} {
		if err := s.ForgetDecryptBypass(host); err == nil {
			t.Errorf("ForgetDecryptBypass(%q) 应拒绝不是 host:port 的主机", host)
		}
	}
	s.ClearDecryptBypass()
	if got := backend.takeCalls(); !slices.Equal(got, []string{"forget-bypass pinned.test:443", "clear-bypass"}) {
		t.Errorf("调用 = %v", got)
	}
}
