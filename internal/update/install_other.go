// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build !desktop || (!windows && !darwin)

package update

const (
	installerAction  = ActionReveal
	appArchiveAction = ActionReveal
)

// appReplaceable 测试时可替换。
var appReplaceable = func() bool { return false }
