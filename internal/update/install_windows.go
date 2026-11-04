// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build desktop && windows

package update

const (
	installerAction  = ActionRun
	appArchiveAction = ActionReveal
)

// appReplaceable 测试时可替换。
var appReplaceable = func() bool { return false }
