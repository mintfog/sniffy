// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build desktop && darwin

package desktop

import "os/exec"

func (b *Bridge) runInstaller(path string) error {
	return exec.Command("open", path).Run()
}
