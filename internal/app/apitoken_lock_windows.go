// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build windows

package app

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func lockTokenDir(dir string) (func() error, error) {
	lockPath := filepath.Join(dir, apiTokenFileName+".lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	h := windows.Handle(f.Fd())
	if err := windows.LockFileEx(h, windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, new(windows.Overlapped)); err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() error {
		_ = windows.UnlockFileEx(h, 0, 1, 0, new(windows.Overlapped))
		return f.Close()
	}, nil
}
