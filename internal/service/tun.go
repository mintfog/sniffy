// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package service

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/mintfog/sniffy/internal/core"
)

// 虚拟网卡的状态。
const (
	TunStateOff      = "off"
	TunStateStarting = "starting"
	TunStateRunning  = "running"
	TunStateStopping = "stopping"
	TunStateFailed   = "failed"
)

var (
	// ErrTunUnavailable 表示当前形态没有接入虚拟网卡。
	ErrTunUnavailable = errors.New("当前环境不支持虚拟网卡")
	// ErrTunStopping 表示上一次开启的虚拟网卡还没有消失，此时不能再开启。
	ErrTunStopping = errors.New("虚拟网卡正在关闭")
)

// tunTrustPollInterval 是开启期间复查根证书信任状态的周期，用于发现在 sniffy 之外装入或移除的
// 根证书。在 sniffy 里安装时由 RecheckTunCATrust 立即复查。
const tunTrustPollInterval = 30 * time.Second

// TunStatus 是虚拟网卡的状态快照，查询与 tun_state 事件传递的都是它。
type TunStatus struct {
	// Revision 每次状态变化时递增，供调用方在查询结果与 tun_state 推送之间丢弃迟到的旧快照。
	Revision uint64 `json:"revision"`
	State    string `json:"state"`
	// Name 是虚拟网卡在系统里的名字，运行中才有。
	Name string `json:"name,omitempty"`
	// CAUntrusted 表示开启期间根证书未被系统信任：透明连接上的 TLS 一律盲转发、不解密，只抓
	// 明文 HTTP。根证书变为被信任后，此后的新连接恢复解密。
	CAUntrusted bool `json:"caUntrusted,omitempty"`
	// Error 在 failed 时是失败原因；在 stopping 时是没能及时关掉网卡的原因，此时网卡可能还在。
	Error string `json:"error,omitempty"`
}

// TunOptions 是开启虚拟网卡的参数，取自配置。
type TunOptions struct {
	// IPv6 为 false 时不接管 IPv6。为 true 时也只在有 IPv6 默认路由时接管。
	IPv6      bool
	BlockQUIC bool
}

// TunDevice 是一块开启了的虚拟网卡。
type TunDevice interface {
	// Name 返回虚拟网卡在系统里的名字。
	Name() string
	// Done 在网卡消失、sniffy 自己的出站恢复原有路由之后关闭。
	Done() <-chan struct{}
	// Err 返回网卡自己停止的原因，在 Done 关闭后调用。经 Close 关闭的、收到退出信号后正常停止的
	// 为 nil。
	Err() error
	// Close 关闭网卡并等它消失。返回错误时网卡可能还在，Done 关闭才说明它已经消失。
	Close() error
}

// DecryptBypassEntry 是直通名单里的一条记录：到这个主机的透明连接不解密、直接盲转发。
type DecryptBypassEntry struct {
	// Host 是小写的 host:port。
	Host string `json:"host"`
	// Reason 取值 cert_rejected（应用在握手时拒绝了证书）、closed_after_handshake（握手后一个
	// 字节不发就关闭，握手后才校验证书的程序拒绝证书时如此）、not_http（TLS 里跑的不是 HTTP）、
	// origin_rejected（源站不接受 sniffy 代为握手）、https_proxy（原始目标是 HTTPS 代理）。
	Reason string `json:"reason"`
	// Since 是记录生效的时间（RFC 3339）。
	Since string `json:"since"`
}

// TunBackend 是虚拟网卡与透明连接解密策略的运行时实现，由装配层提供。
type TunBackend interface {
	// Start 开启虚拟网卡，开启完成或失败后返回，ctx 结束即放弃。返回错误时设备通常为 nil；网卡
	// 可能已经装好、又没能确认它已消失时，设备与错误一起返回，Done 关闭才说明它已经消失。
	Start(ctx context.Context, opts TunOptions) (TunDevice, error)
	// SetCATrusted 下发根证书是否被系统信任：未信任时透明连接上的 TLS 一律盲转发，只影响此后的新
	// 连接。每次检查都调用，结论没变时什么都不做。
	SetCATrusted(trusted bool)
	// TunStatusChanged 在状态每次变化之后调用，调用时持有状态锁，不能再调用 Service 的虚拟网卡方法。
	TunStatusChanged(prev, next TunStatus)
	// DecryptBypass 返回直通名单里已生效的记录，按主机排序。
	DecryptBypass() []DecryptBypassEntry
	// ForgetDecryptBypass 删掉 host 的记录。
	ForgetDecryptBypass(host string)
	// ClearDecryptBypass 清空直通名单。
	ClearDecryptBypass()
}

// tunStore 是虚拟网卡的状态机：
//
//	off / failed --StartTun--> starting --开启成功--> running --StopTun--> stopping --网卡消失--> off
//	starting --开启失败--> failed；starting --StopTun--> off
//	running --网卡自己停止--> failed（helper 收到退出信号后正常停止的为 off）
//	starting --开启失败、网卡可能还在--> stopping --网卡消失--> failed
//
// 开关状态不持久化：启动时自动弹出提权确认不合适。
type tunStore struct {
	mu      sync.Mutex
	backend TunBackend
	status  TunStatus
	// run 是当前这次开启，从 StartTun 到网卡消失；off、failed 时为 nil。
	run *tunRun
	// changed 在每次状态变化时关闭并换新，供 waitTun 等待。
	changed chan struct{}

	// trustMu 串行化根证书信任状态的检查与下发：两次检查之间根证书可能已更换，结果交错写入
	// 会让开关停在旧的结论上。锁顺序为 trustMu → mu。
	trustMu sync.Mutex
	// isTrusted 与 trustPoll 是信任检查与复查周期，测试替换它们。
	isTrusted func() (bool, error)
	trustPoll time.Duration
}

type tunRun struct {
	// cancel 放弃开启。
	cancel context.CancelFunc
	// settled 在开启有了结果（运行或失败）后关闭。
	settled chan struct{}
	// done 在这次开启结束（off 或 failed）后关闭，停止信任状态的复查。
	done chan struct{}
	// stopRequested 表示已调用 StopTun。
	stopRequested bool
	// dev 是开启了的网卡，网卡可能还在时不为 nil。
	dev TunDevice
	// startErr 是开启失败、网卡却可能还在时的失败原因。
	startErr error
}

// SetTunBackend 接入虚拟网卡（装配层在启动 transport 之前调用）。
func (s *Service) SetTunBackend(b TunBackend) {
	s.tun.mu.Lock()
	defer s.tun.mu.Unlock()
	s.tun.backend = b
}

// TunStatus 返回虚拟网卡的当前状态。
func (s *Service) TunStatus() TunStatus {
	s.tun.mu.Lock()
	defer s.tun.mu.Unlock()
	return s.tun.status
}

// StartTun 开始开启虚拟网卡并立即返回，开启在后台进行；结果经 tun_state 事件推送，也可以用
// WaitTunSettled 等待。参数取自配置，配置在开启之后的改动下次开启时生效。开启中或运行中时什么都
// 不做；上一次开启的网卡还没有消失时返回 ErrTunStopping。
func (s *Service) StartTun() (TunStatus, error) {
	t := &s.tun
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.backend == nil {
		return t.status, ErrTunUnavailable
	}
	switch t.status.State {
	case TunStateStarting, TunStateRunning:
		return t.status, nil
	case TunStateStopping:
		return t.status, ErrTunStopping
	}
	c := s.cfg.get()
	opts := TunOptions{IPv6: c.TunIPv6, BlockQUIC: c.TunBlockQUIC}
	ctx, cancel := context.WithCancel(context.Background())
	r := &tunRun{cancel: cancel, settled: make(chan struct{}), done: make(chan struct{})}
	t.run = r
	s.setTunStatusLocked(TunStatus{State: TunStateStarting})
	go s.runTun(ctx, r, t.backend, opts)
	return t.status, nil
}

// runTun 检查根证书的信任状态后开启网卡。信任状态要在网卡开启之前下发：此后进来的第一批连接
// 就要按它决定是否解密。
func (s *Service) runTun(ctx context.Context, r *tunRun, backend TunBackend, opts TunOptions) {
	defer close(r.settled)
	s.checkTunTrust(r)
	go s.pollTunTrust(r)
	dev, err := backend.Start(ctx, opts)

	t := &s.tun
	t.mu.Lock()
	defer t.mu.Unlock()
	switch {
	case err == nil:
		r.dev = dev
		// StopTun 在开启快要完成时才到达，没能取消开启：由它在 settled 之后关闭网卡。
		st := t.status
		st.State, st.Name = TunStateRunning, dev.Name()
		s.setTunStatusLocked(st)
		go s.watchTun(r)
	case dev != nil:
		r.dev, r.startErr = dev, err
		s.setTunStatusLocked(TunStatus{State: TunStateStopping, Error: err.Error()})
		go s.watchTun(r)
	case r.stopRequested:
		s.endTunLocked(r, nil)
	default:
		s.endTunLocked(r, err)
	}
}

// watchTun 在网卡消失后收尾，StopTun 已经收尾的除外。
func (s *Service) watchTun(r *tunRun) {
	<-r.dev.Done()
	t := &s.tun
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.run != r {
		return
	}
	switch {
	case r.stopRequested:
		s.endTunLocked(r, nil)
	case r.startErr != nil:
		s.endTunLocked(r, r.startErr)
	default:
		s.endTunLocked(r, r.dev.Err())
	}
}

// StopTun 关闭虚拟网卡，等到网卡消失、sniffy 自己的出站恢复原有路由后返回。开启中时放弃开启。
// 网卡没能及时关掉时返回错误，状态停在 stopping，网卡消失后才变为 off。failed 时清除失败原因。
func (s *Service) StopTun() (TunStatus, error) {
	t := &s.tun
	t.mu.Lock()
	r := t.run
	if r == nil {
		defer t.mu.Unlock()
		if t.status.State == TunStateFailed {
			s.setTunStatusLocked(TunStatus{State: TunStateOff})
		}
		return t.status, nil
	}
	r.stopRequested = true
	r.cancel()
	t.mu.Unlock()

	<-r.settled
	t.mu.Lock()
	if t.run != r {
		defer t.mu.Unlock()
		return t.status, nil
	}
	dev := r.dev
	if t.status.State == TunStateRunning {
		st := t.status
		st.State = TunStateStopping
		s.setTunStatusLocked(st)
	}
	t.mu.Unlock()

	err := dev.Close()
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.run != r {
		return t.status, err
	}
	if err != nil {
		st := t.status
		st.Error = err.Error()
		s.setTunStatusLocked(st)
		return t.status, err
	}
	s.endTunLocked(r, nil)
	return t.status, nil
}

// WaitTunSettled 等到虚拟网卡不在开启中，返回此时的状态。ctx 先结束时返回 ctx 的错误。
func (s *Service) WaitTunSettled(ctx context.Context) (TunStatus, error) {
	return s.waitTun(ctx, func(st TunStatus) bool { return st.State != TunStateStarting })
}

// waitTun 等到状态满足 until，返回此时的状态。ctx 先结束时返回 ctx 的错误。
func (s *Service) waitTun(ctx context.Context, until func(TunStatus) bool) (TunStatus, error) {
	t := &s.tun
	for {
		t.mu.Lock()
		st, changed := t.status, t.changed
		t.mu.Unlock()
		if until(st) {
			return st, nil
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return st, ctx.Err()
		}
	}
}

// endTunLocked 结束 r：cause 为 nil 时状态变为 off，否则为 failed。调用方持有 mu。
func (s *Service) endTunLocked(r *tunRun, cause error) {
	t := &s.tun
	t.run = nil
	r.cancel()
	close(r.done)
	st := TunStatus{State: TunStateOff}
	if cause != nil {
		st = TunStatus{State: TunStateFailed, Error: cause.Error()}
	}
	s.setTunStatusLocked(st)
}

// setTunStatusLocked 记下新状态并推送。调用方持有 mu。
func (s *Service) setTunStatusLocked(st TunStatus) {
	t := &s.tun
	prev := t.status
	st.Revision = prev.Revision + 1
	t.status = st
	close(t.changed)
	t.changed = make(chan struct{})
	t.backend.TunStatusChanged(prev, st)
	s.emit(core.EventTunState, st)
}

// RecheckTunCATrust 在虚拟网卡开启期间立即复查根证书的信任状态，供在 sniffy 里安装、更换根证书
// 之后调用。未开启时什么都不做。
func (s *Service) RecheckTunCATrust() {
	s.tun.mu.Lock()
	r := s.tun.run
	s.tun.mu.Unlock()
	if r != nil {
		s.checkTunTrust(r)
	}
}

func (s *Service) pollTunTrust(r *tunRun) {
	ticker := time.NewTicker(s.tun.trustPoll)
	defer ticker.Stop()
	for {
		select {
		case <-r.done:
			return
		case <-ticker.C:
			s.checkTunTrust(r)
		}
	}
}

// checkTunTrust 检查根证书是否被系统信任，下发结论并在状态里标记。检查出错时按未信任处理：盲转发
// 只是不解密，不会让连接失败。
func (s *Service) checkTunTrust(r *tunRun) {
	t := &s.tun
	t.trustMu.Lock()
	defer t.trustMu.Unlock()
	trusted, err := t.isTrusted()
	untrusted := !trusted || err != nil

	t.mu.Lock()
	defer t.mu.Unlock()
	if t.run != r {
		return
	}
	t.backend.SetCATrusted(!untrusted)
	if t.status.CAUntrusted != untrusted {
		st := t.status
		st.CAUntrusted = untrusted
		s.setTunStatusLocked(st)
	}
}

// DecryptBypass 返回直通名单里已生效的记录：到这些主机的透明连接不解密、直接盲转发。
func (s *Service) DecryptBypass() []DecryptBypassEntry {
	b := s.tunBackend()
	if b == nil {
		return []DecryptBypassEntry{}
	}
	return b.DecryptBypass()
}

// ForgetDecryptBypass 删掉直通名单里 host 的记录，此后到它的透明连接重新尝试解密；不在名单里的
// 同样成功。只在 host 不是 host:port 形式时返回错误。
func (s *Service) ForgetDecryptBypass(host string) error {
	if _, _, err := net.SplitHostPort(host); err != nil {
		return fmt.Errorf("直通名单的主机须为 host:port: %w", err)
	}
	if b := s.tunBackend(); b != nil {
		b.ForgetDecryptBypass(host)
	}
	return nil
}

// ClearDecryptBypass 清空直通名单，此后所有主机的透明连接都重新尝试解密。
func (s *Service) ClearDecryptBypass() {
	if b := s.tunBackend(); b != nil {
		b.ClearDecryptBypass()
	}
}

func (s *Service) tunBackend() TunBackend {
	s.tun.mu.Lock()
	defer s.tun.mu.Unlock()
	return s.tun.backend
}
