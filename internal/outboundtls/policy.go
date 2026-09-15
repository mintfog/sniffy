// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

// Package outboundtls 将调试证书例外限制到显式配置的精确主机,避免降低其他目标和上游代理的信任要求。
package outboundtls

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"sync/atomic"
)

type hostSet struct {
	hosts map[string]struct{}
}

// Policy 的零值不允许任何证书例外;快照发布后不可修改,可由转发热路径并发读取。
type Policy struct {
	current atomic.Pointer[hostSet]
	roots   *x509.CertPool
}

// NewWithRootCAs 为显式信任的私有 CA 创建策略;复制入参以免调用方后续修改影响并发握手。
func NewWithRootCAs(roots *x509.CertPool) *Policy {
	p := &Policy{}
	if roots != nil {
		p.roots = roots.Clone()
	}
	return p
}

// NormalizeHosts 只接受精确 ASCII 主机,不接受 URL、端口或模式,防止调试配置意外扩展到其他站点。
func NormalizeHosts(hosts []string) ([]string, error) {
	unique := make(map[string]struct{}, len(hosts))
	for _, host := range hosts {
		normalized, err := normalizeHost(host)
		if err != nil {
			return nil, fmt.Errorf("无效的 TLS 证书例外主机 %q: %w", host, err)
		}
		unique[normalized] = struct{}{}
	}
	result := make([]string, 0, len(unique))
	for host := range unique {
		result = append(result, host)
	}
	slices.Sort(result)
	return result, nil
}

func normalizeHost(host string) (string, error) {
	if host == "" {
		return "", fmt.Errorf("主机不能为空")
	}
	for i := 0; i < len(host); i++ {
		if host[i] <= ' ' || host[i] >= 127 {
			return "", fmt.Errorf("只能使用 ASCII 主机名且不能包含空白")
		}
	}
	if strings.ContainsAny(host, "[]%/\\@?#*") {
		return "", fmt.Errorf("不能包含 URL、通配符、IPv6 括号或区域标识")
	}
	if strings.Contains(host, ":") {
		addr, err := netip.ParseAddr(host)
		if err != nil || !addr.Is6() || addr.Zone() != "" {
			return "", fmt.Errorf("不能包含端口,IPv6 必须使用无括号地址")
		}
		return addr.String(), nil
	}
	host = strings.TrimSuffix(host, ".")
	if addr, err := netip.ParseAddr(host); err == nil {
		return addr.String(), nil
	}
	if len(host) == 0 || len(host) > 253 {
		return "", fmt.Errorf("DNS 主机长度必须在 1 到 253 之间")
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", fmt.Errorf("DNS 标签不能为空、超过 63 字符或以连字符开始/结束")
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return "", fmt.Errorf("DNS 标签只能包含字母、数字和连字符")
			}
		}
	}
	return strings.ToLower(host), nil
}

// SetInsecureHosts 不因非法配置部分更新策略;返回是否实际发布了新快照。
func (p *Policy) SetInsecureHosts(hosts []string) (bool, error) {
	normalized, err := NormalizeHosts(hosts)
	if err != nil {
		return false, err
	}
	next := &hostSet{hosts: make(map[string]struct{}, len(normalized))}
	for _, host := range normalized {
		next.hosts[host] = struct{}{}
	}
	for {
		previous := p.current.Load()
		if equalHosts(previous, next) {
			return false, nil
		}
		if p.current.CompareAndSwap(previous, next) {
			return true, nil
		}
	}
}

func equalHosts(a, b *hostSet) bool {
	if a == nil {
		return len(b.hosts) == 0
	}
	if len(a.hosts) != len(b.hosts) {
		return false
	}
	for host := range a.hosts {
		if _, ok := b.hosts[host]; !ok {
			return false
		}
	}
	return true
}

// AllowsInsecure 对常见的已规范化主机只做一次无分配快照查找,不会匹配子域名。
func (p *Policy) AllowsInsecure(host string) bool {
	if p == nil {
		return false
	}
	snapshot := p.current.Load()
	if snapshot == nil || len(snapshot.hosts) == 0 {
		return false
	}
	if _, ok := snapshot.hosts[host]; ok {
		return true
	}
	if strings.Contains(host, ":") {
		addr, err := netip.ParseAddr(host)
		if err != nil || !addr.Is6() || addr.Zone() != "" {
			return false
		}
		var canonical [64]byte
		_, ok := snapshot.hosts[string(addr.AppendTo(canonical[:0]))]
		return ok
	}
	normalized := strings.TrimSuffix(host, ".")
	if !hasUpperASCII(normalized) {
		if normalized == host {
			return false
		}
		_, ok := snapshot.hosts[normalized]
		return ok
	}
	if len(normalized) > 253 {
		return false
	}
	// 只折叠 ASCII,防止 Unicode 大小写规则把非法主机转成已允许的 ASCII 主机。
	var canonical [253]byte
	for i := 0; i < len(normalized); i++ {
		c := normalized[i]
		if c >= 127 {
			return false
		}
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		canonical[i] = c
	}
	_, ok := snapshot.hosts[string(canonical[:len(normalized)])]
	return ok
}

func hasUpperASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 'A' && s[i] <= 'Z' {
			return true
		}
	}
	return false
}

// ConfigForHost 返回新的 TLS 配置;nil 策略使用系统信任库。
// 例外配置在握手时按当前名单复核。
func (p *Policy) ConfigForHost(host string) *tls.Config {
	cfg := &tls.Config{ServerName: host, RootCAs: p.rootCAs()}
	if !p.AllowsInsecure(host) {
		return cfg
	}
	cfg.InsecureSkipVerify = true
	cfg.VerifyConnection = func(state tls.ConnectionState) error {
		// IP 不发送 SNI,ConnectionState.ServerName 为空;这里的 host 来自实际拨号目标。
		return p.verifyConnection(cfg, state, host)
	}
	return cfg
}

func (p *Policy) rootCAs() *x509.CertPool {
	if p == nil {
		return nil
	}
	return p.roots
}

// InsecureTLSConfig 必须只交给按例外目标选中的独立连接池;实际 TLS 对端仍需检查,
// 因为 net/http 会将同一配置用于 HTTPS 上游代理,且配置创建后例外可能被撤销。
func (p *Policy) InsecureTLSConfig() *tls.Config {
	cfg := &tls.Config{InsecureSkipVerify: true, RootCAs: p.rootCAs()}
	cfg.VerifyConnection = func(state tls.ConnectionState) error {
		return p.verifyConnection(cfg, state, state.ServerName)
	}
	return cfg
}

func (p *Policy) verifyConnection(cfg *tls.Config, state tls.ConnectionState, host string) error {
	if host != "" && p.AllowsInsecure(host) {
		return nil
	}
	if len(state.PeerCertificates) == 0 || host == "" {
		return fmt.Errorf("TLS 对端缺少证书或主机名")
	}
	opts := x509.VerifyOptions{
		DNSName:       host,
		Roots:         cfg.RootCAs,
		Intermediates: x509.NewCertPool(),
	}
	if cfg.Time != nil {
		opts.CurrentTime = cfg.Time()
	}
	for _, cert := range state.PeerCertificates[1:] {
		opts.Intermediates.AddCert(cert)
	}
	_, err := state.PeerCertificates[0].Verify(opts)
	return err
}
