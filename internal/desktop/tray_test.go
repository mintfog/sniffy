// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build desktop

package desktop

import "testing"

func TestBuildTrayMenuWithoutApplication(t *testing.T) {
	if got := buildTrayMenu(nil, labelsFor("en")); got == nil {
		t.Fatal("托盘菜单不应为空")
	}
	showWindow(nil)
}
