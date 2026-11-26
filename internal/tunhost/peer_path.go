// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build darwin || windows

package tunhost

import (
	"os"

	"github.com/mintfog/sniffy/pkg/process"
)

func executableOf(pid int) (os.FileInfo, error) {
	path, err := process.ExecutablePath(uint32(pid))
	if err != nil {
		return nil, err
	}
	return os.Stat(path)
}
