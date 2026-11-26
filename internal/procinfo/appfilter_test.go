// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package procinfo

import (
	"net"
	"os"
	"testing"

	"github.com/mintfog/sniffy/pkg/process"
)

// pidDetector 只实现按 PID 查询，按连接查询会触发 panic。
type pidDetector struct {
	detectorFunc
	byPID func(uint32) (*process.ProcessInfo, error)
}

func (d pidDetector) GetProcessByPID(pid uint32) (*process.ProcessInfo, error) { return d.byPID(pid) }

// selfConnection 返回本进程持有的一条回环连接在应用一侧的 RemoteAddr、LocalAddr 约定下的地址：
// client 是应用 socket 的本端，target 是它的对端。
func selfConnection(t *testing.T) (client, target net.Addr) {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	accepted := make(chan net.Conn, 1)
	go func() {
		c, _ := listener.Accept()
		accepted <- c
	}()
	conn, err := net.Dial("tcp4", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if server := <-accepted; server != nil {
		t.Cleanup(func() { _ = server.Close() })
	}
	return conn.LocalAddr(), conn.RemoteAddr()
}

func TestAppFilterCapture(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	f, err := NewAppFilter(nil)
	if err != nil {
		t.Fatal(err)
	}
	client, target := selfConnection(t)
	for _, tc := range []struct {
		name    string
		mode    string
		entries []string
		capture bool
	}{
		{name: "全部抓取", mode: AppScopeAll, entries: []string{"no-such-app"}, capture: true},
		{name: "未知模式按全部抓取", mode: "bogus", entries: []string{"no-such-app"}, capture: true},
		{name: "只抓名单里的应用，在名单里", mode: AppScopeInclude, entries: []string{self}, capture: true},
		{name: "只抓名单里的应用，不在名单里", mode: AppScopeInclude, entries: []string{"no-such-app"}},
		{name: "只抓名单里的应用，名单为空", mode: AppScopeInclude},
		{name: "名单以外的应用，在名单里", mode: AppScopeExclude, entries: []string{self}},
		{name: "名单以外的应用，不在名单里", mode: AppScopeExclude, entries: []string{"no-such-app"}, capture: true},
		{name: "名单以外的应用，名单为空", mode: AppScopeExclude, capture: true},
	} {
		f.SetScope(tc.mode, tc.entries)
		if got, err := f.Capture(client, target); got != tc.capture || err != nil {
			t.Errorf("%s: Capture = (%v, %v)，期望 %v", tc.name, got, err, tc.capture)
		}
	}
}

// TestAppFilterUnknownOwner：查不到所属进程的连接按不在名单里处理。
func TestAppFilterUnknownOwner(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	f, err := NewAppFilter(nil)
	if err != nil {
		t.Fatal(err)
	}
	gone := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1}
	target := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 2}
	for mode, want := range map[string]bool{AppScopeInclude: false, AppScopeExclude: true} {
		f.SetScope(mode, []string{self})
		if got, _ := f.Capture(gone, target); got != want {
			t.Errorf("%s: Capture = %v，期望 %v", mode, got, want)
		}
	}

	unavailable := &AppFilter{}
	for mode, want := range map[string]bool{AppScopeInclude: false, AppScopeExclude: true} {
		unavailable.SetScope(mode, []string{self})
		if got, err := unavailable.Capture(gone, target); got != want || err == nil {
			t.Errorf("识别不了所属进程时 %s: Capture = (%v, %v)，期望 %v 并带错误", mode, got, err, want)
		}
	}
}

// TestAppFilterHandsOwnerToResolver：只抓名单里的应用时，查到的所属进程转交给解析器，补全时按 PID
// 直接查询，不按连接查找持有者。
func TestAppFilterHandsOwnerToResolver(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var queried uint32
	r := newTestResolver(func(net.Addr, net.Addr) (*process.ProcessInfo, error) {
		t.Error("已知所属进程时不应按连接查找")
		return nil, nil
	})
	r.detector = pidDetector{detectorFunc: r.detector.(detectorFunc), byPID: func(pid uint32) (*process.ProcessInfo, error) {
		queried = pid
		return &process.ProcessInfo{PID: pid, Name: "self"}, nil
	}}
	f, err := NewAppFilter(r)
	if err != nil {
		t.Fatal(err)
	}
	f.SetScope(AppScopeInclude, []string{self})
	client, target := selfConnection(t)
	if ok, err := f.Capture(client, target); !ok || err != nil {
		t.Fatalf("Capture = (%v, %v)", ok, err)
	}
	// 同一源地址连往别处的连接不取这条线索。
	other := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1}
	if _, known := r.takeOwner(connKey(client, other)); known {
		t.Fatal("连往别处的连接取走了线索")
	}
	info := r.Resolve(client, target)
	if info == nil || info.PID != uint32(os.Getpid()) || queried != uint32(os.Getpid()) {
		t.Fatalf("补全结果 %+v，按 PID %d 查询，期望本进程 %d", info, queried, os.Getpid())
	}
}

// BenchmarkAppFilterCaptureAll 量默认的全部抓取时每条透明连接多出的开销。
func BenchmarkAppFilterCaptureAll(b *testing.B) {
	f := &AppFilter{}
	f.SetScope(AppScopeAll, nil)
	client := &net.TCPAddr{IP: net.IPv4(172, 29, 113, 1), Port: 50000}
	target := &net.TCPAddr{IP: net.IPv4(203, 0, 113, 80), Port: 443}
	b.ReportAllocs()
	for b.Loop() {
		if ok, _ := f.Capture(client, target); !ok {
			b.Fatal("全部抓取时不应放过连接")
		}
	}
}
