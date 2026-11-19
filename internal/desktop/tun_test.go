// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build desktop

package desktop

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/mintfog/sniffy/internal/service"
)

func TestBridgeDecryptBypassReturnsListAfterChange(t *testing.T) {
	b := newTestBridge()
	backend := &fakeBypassBackend{entries: []service.DecryptBypassEntry{
		{Host: "a.example:443", Reason: "cert_rejected", Since: "2026-11-19T10:00:00Z"},
		{Host: "b.example:443", Reason: "not_http", Since: "2026-11-19T10:01:00Z"},
	}}
	b.app.Service.SetTunBackend(backend)

	if _, err := b.ForgetDecryptBypass("a.example"); err == nil {
		t.Fatal("不是 host:port 的主机应返回错误")
	}
	list, err := b.ForgetDecryptBypass("a.example:443")
	if err != nil || len(list) != 1 || list[0].Host != "b.example:443" {
		t.Fatalf("删除一条后应返回剩下的名单: %+v, %v", list, err)
	}
	if list := b.ClearDecryptBypass(); len(list) != 0 {
		t.Fatalf("清空后应返回空名单: %+v", list)
	}
}

// fakeBypassBackend 只实现直通名单，开启一律失败。
type fakeBypassBackend struct {
	entries []service.DecryptBypassEntry
}

func (f *fakeBypassBackend) Start(context.Context, service.TunOptions) (service.TunDevice, error) {
	return nil, errors.New("测试中不开启")
}
func (f *fakeBypassBackend) SetCATrusted(bool)                       {}
func (f *fakeBypassBackend) TunStatusChanged(_, _ service.TunStatus) {}
func (f *fakeBypassBackend) DecryptBypass() []service.DecryptBypassEntry {
	return slices.Clone(f.entries)
}
func (f *fakeBypassBackend) ClearDecryptBypass() { f.entries = f.entries[:0] }
func (f *fakeBypassBackend) ForgetDecryptBypass(host string) {
	f.entries = slices.DeleteFunc(f.entries, func(e service.DecryptBypassEntry) bool { return e.Host == host })
}
