// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build desktop && !windows

package desktop

import "github.com/wailsapp/wails/v3/pkg/application"

func preserveMaximisedOnRestore(application.Window) {}

func restoreMinimisedWindow(win application.Window) {
	if win != nil && win.IsMinimised() {
		win.Restore()
	}
}
