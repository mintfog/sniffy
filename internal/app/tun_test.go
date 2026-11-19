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
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mintfog/sniffy/internal/netbind"
	"github.com/mintfog/sniffy/internal/service"
	"github.com/mintfog/sniffy/internal/tunhost"
)

// recordingBinder 记录出站绑定的变化，bound 表示当前是否绑定着。
type recordingBinder struct{ bound atomic.Bool }

func (b *recordingBinder) BindOutbound(e netbind.Egress) {
	b.bound.Store(e.IPv4 != nil || e.IPv6 != nil)
}

// fakeHost 是本进程里的虚拟网卡。closeErr 不为 nil 时 Close 返回它、网卡不消失。
type fakeHost struct {
	closeErr error
	once     sync.Once
	done     chan struct{}
}

func (h *fakeHost) Name() string          { return "sniffy0" }
func (h *fakeHost) Done() <-chan struct{} { return h.done }
func (h *fakeHost) vanish()               { h.once.Do(func() { close(h.done) }) }

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
	dev := &tunDevice{host: host, listener: binder, done: make(chan struct{})}
	go dev.watch()
	return dev, binder
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
