// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build desktop

package desktop

import "github.com/mintfog/sniffy/internal/service"

// ---- 虚拟网卡（状态变化另经 tun_state 事件推送） ----

func (b *Bridge) GetTunStatus() service.TunStatus      { return b.app.Service.TunStatus() }
func (b *Bridge) StartTun() (service.TunStatus, error) { return b.app.Service.StartTun() }
func (b *Bridge) StopTun() (service.TunStatus, error)  { return b.app.Service.StopTun() }
func (b *Bridge) GetDecryptBypass() []service.DecryptBypassEntry {
	return b.app.Service.DecryptBypass()
}

// ForgetDecryptBypass 删掉直通名单里 host（host:port）的记录，此后到它的透明连接重新尝试解密，
// 返回删除后的名单。
func (b *Bridge) ForgetDecryptBypass(host string) ([]service.DecryptBypassEntry, error) {
	if err := b.app.Service.ForgetDecryptBypass(host); err != nil {
		return nil, err
	}
	return b.app.Service.DecryptBypass(), nil
}

// ClearDecryptBypass 清空直通名单，此后所有主机的透明连接都重新尝试解密，返回清空后的名单。
func (b *Bridge) ClearDecryptBypass() []service.DecryptBypassEntry {
	b.app.Service.ClearDecryptBypass()
	return b.app.Service.DecryptBypass()
}
