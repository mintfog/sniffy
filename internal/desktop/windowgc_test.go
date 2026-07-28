// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build desktop

package desktop

import "testing"

func TestChildWindowManagerScheduleAndReuse(t *testing.T) {
	m := newChildWindowManager()
	if got := m.reuse("missing"); got != nil {
		t.Fatalf("reuse(missing) = %v，期望 nil", got)
	}

	mw := &managedChildWindow{}
	m.wins["settings"] = mw
	m.scheduleDestroy("settings", &managedChildWindow{})
	if mw.timer != nil || mw.epoch != 0 {
		t.Fatal("过期窗口记录不应改变当前窗口的调度状态")
	}

	m.scheduleDestroy("settings", mw)
	if mw.timer == nil || mw.epoch != 1 {
		t.Fatalf("首次调度后 timer=%v epoch=%d", mw.timer, mw.epoch)
	}

	_ = m.reuse("settings")
	if mw.timer != nil || mw.epoch != 2 {
		t.Fatalf("复用后 timer=%v epoch=%d", mw.timer, mw.epoch)
	}
}
