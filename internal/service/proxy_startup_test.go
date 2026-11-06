// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package service

import (
	"errors"
	"fmt"
	"sync"
	"syscall"
	"testing"
)

func TestProxyStartupRecovery(t *testing.T) {
	dir := t.TempDir()
	s := New(nil, nil, dir, "")
	var starts, applied int
	available := false
	s.SetProxyLifecycle(8080, func(port int) error {
		starts++
		if !available {
			return fmt.Errorf("监听失败: %w", syscall.EADDRINUSE)
		}
		return nil
	}, nil)
	s.SetSystemProxyApplier(func(on bool) error {
		if on {
			applied++
		}
		return nil
	})
	if err := s.StartProxy(); !errors.Is(err, syscall.EADDRINUSE) {
		t.Fatalf("应保留端口占用错误: %v", err)
	}
	status := s.ProxyStatus()
	if status.Running || status.ErrorCode != ProxyErrorAddressInUse || status.Port != 8080 {
		t.Fatalf("端口冲突状态错误: %+v", status)
	}
	if cfg := s.UpdateConfig(map[string]any{"systemProxy": true}); cfg.SystemProxy || applied != 0 {
		t.Fatalf("监听失败后接管了系统代理: enabled=%v applied=%d", cfg.SystemProxy, applied)
	}
	status, err := s.RetryProxy(9090)
	if err != nil || status.Running || s.Config().Port != 8080 || applied != 0 {
		t.Fatalf("重试失败不应保存端口或接管系统代理: %+v err=%v cfg=%d applied=%d", status, err, s.Config().Port, applied)
	}
	available = true
	status, err = s.RetryProxy(9090)
	if err != nil || !status.Running || status.Error != "" || status.ErrorCode != "" || applied != 1 {
		t.Fatalf("重试恢复失败: %+v err=%v applied=%d", status, err, applied)
	}
	cfg, ok := LoadSaved(dir)
	if !ok || cfg.Port != 9090 || !cfg.SystemProxy {
		t.Fatalf("恢复后的配置未保存: %+v", cfg)
	}
	status, err = s.RetryProxy(9091)
	if err != nil || status.Port != 9090 || starts != 3 || applied != 1 {
		t.Fatalf("重复重试影响了运行中的代理: %+v err=%v starts=%d applied=%d", status, err, starts, applied)
	}
}

func TestRetryProxyRejectsInvalidPort(t *testing.T) {
	s := New(nil, nil, "", "")
	starts := 0
	s.SetProxyLifecycle(8080, func(int) error { starts++; return nil }, nil)
	for _, port := range []int{-1, 0, 65536} {
		if _, err := s.RetryProxy(port); err == nil {
			t.Errorf("非法端口 %d 未被拒绝", port)
		}
	}
	if starts != 0 {
		t.Fatalf("非法端口触发了 %d 次监听", starts)
	}
}

func TestRetryProxyLeavesSystemProxyOff(t *testing.T) {
	for _, tc := range []struct {
		name      string
		auto      bool
		applyErr  error
		wantCalls int
	}{
		{name: "未开自动启用", auto: false},
		{name: "系统代理应用失败", auto: true, applyErr: errors.New("应用失败"), wantCalls: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := New(nil, nil, "", "")
			s.UpdateConfig(map[string]any{"autoSystemProxy": tc.auto})
			applied := 0
			s.SetProxyLifecycle(8080, func(int) error { return nil }, nil)
			s.SetSystemProxyApplier(func(bool) error { applied++; return tc.applyErr })
			status, err := s.RetryProxy(9090)
			if err != nil || !status.Running || s.Config().SystemProxy || applied != tc.wantCalls {
				t.Fatalf("恢复状态错误: %+v err=%v systemProxy=%v applied=%d，期望 applied=%d",
					status, err, s.Config().SystemProxy, applied, tc.wantCalls)
			}
		})
	}
}

func TestConcurrentProxyRetriesAndShutdown(t *testing.T) {
	s := New(nil, nil, "", "")
	starts, stops := 0, 0
	s.SetProxyLifecycle(8080, func(int) error { starts++; return nil }, func() error { stops++; return nil })
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if _, err := s.RetryProxy(9090); err != nil {
				t.Errorf("重试: %v", err)
			}
		})
	}
	wg.Wait()
	if err := s.StopProxy(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RetryProxy(9090); err == nil {
		t.Fatal("关闭后不应重新监听")
	}
	if starts != 1 || stops != 1 || s.ProxyStatus().Running {
		t.Fatalf("生命周期调用异常: starts=%d stops=%d status=%+v", starts, stops, s.ProxyStatus())
	}
}

func TestPartialSystemProxyFailureStillCleansUp(t *testing.T) {
	for _, tc := range []struct {
		name         string
		rollbackErr  error
		wantCleanups int
	}{
		{name: "撤销成功", wantCleanups: 1},
		{name: "撤销失败", rollbackErr: errors.New("撤销失败"), wantCleanups: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := New(nil, nil, "", "")
			s.UpdateConfig(map[string]any{"systemProxy": false})
			s.SetProxyLifecycle(8080, func(int) error { return nil }, nil)
			if err := s.StartProxy(); err != nil {
				t.Fatal(err)
			}
			cleanups := 0
			rollbackErr := tc.rollbackErr
			s.SetSystemProxyApplier(func(on bool) error {
				if on {
					return errors.New("部分网络服务设置失败")
				}
				cleanups++
				// 只有接管失败后的那次撤销按用例出错，之后的清理都成功。
				err := rollbackErr
				rollbackErr = nil
				return err
			})
			if cfg := s.UpdateConfig(map[string]any{"systemProxy": true}); cfg.SystemProxy {
				t.Fatal("接管失败后开关应为关闭")
			}
			// 开关值未变的关闭请求：有残留时清理一次，残留清掉后不再执行外部命令。
			s.UpdateConfig(map[string]any{"systemProxy": false})
			s.UpdateConfig(map[string]any{"systemProxy": false})
			if cleanups != tc.wantCleanups {
				t.Fatalf("清理次数=%d，期望 %d", cleanups, tc.wantCleanups)
			}
		})
	}
}

func TestRetryAlignsSystemProxyLikeStartup(t *testing.T) {
	s := New(nil, nil, "", "")
	s.UpdateConfig(map[string]any{"autoSystemProxy": false})
	available := false
	s.SetProxyLifecycle(8080, func(int) error {
		if !available {
			return syscall.EADDRINUSE
		}
		return nil
	}, nil)
	applied, cleaned := 0, 0
	s.SetSystemProxyApplier(func(bool) error { applied++; return nil })
	s.SetStaleSystemProxyCleaner(func() { cleaned++ })
	_ = s.StartProxy()
	s.AlignSystemProxy()
	if cleaned != 0 {
		t.Fatal("监听失败时不应清理可能属于占用端口程序的系统代理")
	}
	available = true
	if _, err := s.RetryProxy(9090); err != nil {
		t.Fatal(err)
	}
	if cleaned != 1 || applied != 0 || s.Config().SystemProxy {
		t.Fatalf("关闭自动启用时重试成功应清理残留: cleaned=%d applied=%d", cleaned, applied)
	}
}

func TestCloseProxyBlocksTakeoverBeforeStop(t *testing.T) {
	s := New(nil, nil, "", "")
	s.UpdateConfig(map[string]any{"systemProxy": false})
	stops := 0
	s.SetProxyLifecycle(8080, func(int) error { return nil }, func() error { stops++; return nil })
	if err := s.StartProxy(); err != nil {
		t.Fatal(err)
	}
	enables := 0
	s.SetSystemProxyApplier(func(on bool) error {
		if on {
			enables++
		}
		return nil
	})
	if !s.CloseProxy() {
		t.Fatal("关闭前监听应在运行")
	}
	if stops != 0 {
		t.Fatal("CloseProxy 不应关闭监听")
	}
	if cfg := s.UpdateConfig(map[string]any{"systemProxy": true}); cfg.SystemProxy || enables != 0 {
		t.Fatalf("退出期间接管了系统代理: enabled=%v calls=%d", cfg.SystemProxy, enables)
	}
	if _, err := s.RetryProxy(9090); err == nil {
		t.Fatal("退出期间不应重新监听")
	}
	if err := s.StopProxy(); err != nil || stops != 1 {
		t.Fatalf("StopProxy: err=%v stops=%d", err, stops)
	}
}
