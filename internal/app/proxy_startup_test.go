// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package app

import (
	"net"
	"testing"

	"github.com/mintfog/sniffy/internal/service"
)

func TestBuildProxyStartupRecovery(t *testing.T) {
	if !inAppTestSubprocess(t) {
		if out, err := appTestSubprocess(t); err != nil {
			t.Fatalf("启动恢复子进程失败: %v\n%s", err, out)
		}
		return
	}
	t.Run("释放原端口", func(t *testing.T) {
		a, occupied := startOnOccupiedPort(t)
		port := occupied.Addr().(*net.TCPAddr).Port
		occupied.Close()
		status, err := a.Service.RetryProxy(port)
		if err != nil {
			t.Fatal(err)
		}
		assertProxyRecovered(t, a, status, port)
	})
	t.Run("更换端口", func(t *testing.T) {
		a, _ := startOnOccupiedPort(t)
		status, port := retryOnFreePort(t, a)
		assertProxyRecovered(t, a, status, port)
	})
}

// startOnOccupiedPort 在已被占用的端口上装配并启动应用，确认监听失败被识别为端口冲突。
func startOnOccupiedPort(t *testing.T) (*App, net.Listener) {
	t.Helper()
	isolateAppDirs(t)
	preserveAppLogging(t)
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { occupied.Close() })
	cfg := DefaultConfig()
	cfg.Address = "127.0.0.1"
	cfg.Port = occupied.Addr().(*net.TCPAddr).Port
	a, err := Build(cfg, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Stop() })
	if err := a.Start(); err == nil {
		t.Fatal("被占用的端口应启动失败")
	}
	if status := a.Service.ProxyStatus(); status.Running || status.ErrorCode != service.ProxyErrorAddressInUse {
		t.Fatalf("实际端口冲突未被识别: %+v", status)
	}
	return a, occupied
}

// retryOnFreePort 换到新探测的空闲端口重试。探测到的端口关闭后可能被并行测试抢走，允许换几个候选。
func retryOnFreePort(t *testing.T, a *App) (service.ProxyStatus, int) {
	t.Helper()
	for range 5 {
		probe, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := probe.Addr().(*net.TCPAddr).Port
		probe.Close()
		status, err := a.Service.RetryProxy(port)
		if err != nil {
			t.Fatal(err)
		}
		if status.Running {
			return status, port
		}
	}
	t.Fatal("连续 5 个空闲端口都未能监听")
	return service.ProxyStatus{}, 0
}

// assertProxyRecovered 确认监听已切到 port、能实际抓包，且端口已保存供下次启动使用。
func assertProxyRecovered(t *testing.T, a *App, status service.ProxyStatus, port int) {
	t.Helper()
	if !status.Running || status.Port != port || a.Engine.Config().GetPort() != port {
		t.Fatalf("监听恢复失败: %+v 引擎端口=%d，期望 %d", status, a.Engine.Config().GetPort(), port)
	}
	assertBuiltAppCapturesHTTP(t, a, a.Engine.Listener().GetAddress())
	if _, savedPort := ResolveListen("", 8080); savedPort != port {
		t.Fatalf("下次启动端口=%d，期望 %d", savedPort, port)
	}
}
