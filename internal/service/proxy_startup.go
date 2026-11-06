// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package service

import (
	"errors"
	"runtime"
	"syscall"
)

// ProxyStatus.ErrorCode 的取值，与前端 proxyStartup.<code> 文案键一一对应。
const (
	ProxyErrorAddressInUse     = "address_in_use"
	ProxyErrorPermissionDenied = "permission_denied"
	ProxyErrorListenFailed     = "listen_failed"
)

var errProxyClosed = errors.New("代理已关闭")

// Windows 套接字返回 Winsock 错误码，errors.Is 不会把它们映射到 syscall 的 POSIX 常量。
const (
	wsaEACCES     = syscall.Errno(10013)
	wsaEADDRINUSE = syscall.Errno(10048)
)

// ProxyStatus 是代理监听的当前状态。Port 为最近一次尝试监听的端口，监听失败时
// ErrorCode 给出分类、Error 保留原始错误文本。
type ProxyStatus struct {
	Port      int    `json:"port"`
	Running   bool   `json:"running"`
	ErrorCode string `json:"errorCode,omitempty"`
	Error     string `json:"error,omitempty"`
}

// proxyStartup 与配置共用 applyMu，防止重试、系统代理切换与退出交错。
type proxyStartup struct {
	status ProxyStatus
	start  func(int) error
	stop   func() error
	closed bool
}

// SetProxyLifecycle 在启动前注入监听回调；回调在 applyMu 内执行，不得重入服务的配置方法。
func (s *Service) SetProxyLifecycle(port int, start func(int) error, stop func() error) {
	s.proxy = proxyStartup{status: ProxyStatus{Port: port}, start: start, stop: stop}
}

// ProxyStatus 返回代理监听的当前状态。
func (s *Service) ProxyStatus() ProxyStatus {
	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	return s.proxy.status
}

// StartProxy 按注入时的端口启动监听，失败原因同时记入 ProxyStatus。
func (s *Service) StartProxy() error {
	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	if s.proxy.closed || s.proxy.start == nil {
		return errProxyClosed
	}
	if s.proxy.status.Running {
		return errors.New("代理已启动")
	}
	return s.listenOn(s.proxy.status.Port)
}

// RetryProxy 在监听未运行时改用 port 重新监听；成功后保存端口，并与正常启动一样对齐系统代理。
// 监听失败不作为 error 返回，原因在返回的状态里；error 只表示请求被拒绝（端口非法或已退出）。
// 监听已在运行时原样返回当前状态。
func (s *Service) RetryProxy(port int) (ProxyStatus, error) {
	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	if port < 1 || port > 65535 {
		return s.proxy.status, errors.New("代理端口必须为 1 到 65535 的整数")
	}
	if s.proxy.closed || s.proxy.start == nil {
		return s.proxy.status, errProxyClosed
	}
	if s.proxy.status.Running {
		return s.proxy.status, nil
	}
	if s.listenOn(port) == nil {
		s.cfg.update(map[string]any{"port": float64(port)})
		s.alignSystemProxy()
	}
	return s.proxy.status, nil
}

// listenOn 监听 port 并把结果记入状态；调用方已确认代理未关闭、未在运行。
func (s *Service) listenOn(port int) error {
	err := s.proxy.start(port)
	s.proxy.status = ProxyStatus{Port: port, Running: err == nil}
	if err != nil {
		s.proxy.status.ErrorCode = proxyListenErrorCode(err)
		s.proxy.status.Error = err.Error()
		// 开关默认开启且会持久化;没有监听时系统代理不可能生效,不能让配置继续报告开启。
		s.cfg.setSystemProxy(false)
	}
	return err
}

// AlignSystemProxy 供桌面层启动时调用，把系统代理与当前开关对齐到实际状态。
func (s *Service) AlignSystemProxy() {
	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	s.alignSystemProxy()
}

// alignSystemProxy 按「自动启用」决定接管与否；不接管时清掉上次异常退出残留的指向本端口的设置。
// 监听未就绪时不碰系统设置：此时指向该端口的代理可能属于占用端口的其他程序。
func (s *Service) alignSystemProxy() {
	on := false
	if s.proxyListening() && s.applySystemProxy != nil {
		if s.cfg.get().AutoProxy {
			on = s.enableSystemProxy()
		} else if s.clearStaleSystemProxy != nil {
			s.clearStaleSystemProxy()
		}
	}
	s.cfg.setSystemProxy(on)
}

// switchSystemProxy 按用户开关接管或释放系统代理，报告是否达成；监听未就绪时接管不会成功。
func (s *Service) switchSystemProxy(on bool) bool {
	if on {
		return s.proxyListening() && s.enableSystemProxy()
	}
	if s.applySystemProxy(false) != nil {
		return false
	}
	s.systemProxyResidual = false
	return true
}

// enableSystemProxy 接管系统代理并报告是否成功，调用方已确认监听就绪。失败时设置可能已部分
// 写入，立即撤销；撤销也失败则记为残留，让后续的关闭请求即使开关值未变也会再次清理。
func (s *Service) enableSystemProxy() bool {
	if s.applySystemProxy(true) == nil {
		return true
	}
	s.systemProxyResidual = s.applySystemProxy(false) != nil
	return false
}

// proxyListening 报告系统代理此刻能否指向本程序。未注入生命周期（只验证配置的单测）视为
// 监听就绪，让系统代理开关照常下发。
func (s *Service) proxyListening() bool {
	return !s.proxy.closed && (s.proxy.start == nil || s.proxy.status.Running)
}

// CloseProxy 拒绝此后的重试与系统代理接管，并返回监听是否在运行。
// 退出时先调用它、释放系统代理，再 StopProxy，避免其他应用在释放前连向已关闭的端口。
func (s *Service) CloseProxy() bool {
	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	s.proxy.closed = true
	return s.proxy.status.Running
}

// StopProxy 置位关闭后即释放 applyMu 再停监听：停监听会有界等待连接收尾，
// 置位后不会再有启动与之交错，没必要让查询与配置更新陪着阻塞。
func (s *Service) StopProxy() error {
	s.applyMu.Lock()
	s.proxy.closed = true
	s.proxy.status.Running = false
	stop := s.proxy.stop
	s.applyMu.Unlock()
	if stop == nil {
		return nil
	}
	return stop()
}

func proxyListenErrorCode(err error) string {
	windows := runtime.GOOS == "windows"
	switch {
	case errors.Is(err, syscall.EADDRINUSE), windows && errors.Is(err, wsaEADDRINUSE):
		return ProxyErrorAddressInUse
	case errors.Is(err, syscall.EACCES), windows && errors.Is(err, wsaEACCES):
		return ProxyErrorPermissionDenied
	default:
		return ProxyErrorListenFailed
	}
}
