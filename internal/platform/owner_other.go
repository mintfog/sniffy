// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build !windows

package platform

import "os"

// RestrictToOwner 把 path 的权限收紧为只有属主可访问：目录 0700，文件 0600。
func RestrictToOwner(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	mode := os.FileMode(0o600)
	if info.IsDir() {
		mode = 0o700
	}
	if info.Mode().Perm() == mode {
		return nil
	}
	return os.Chmod(path, mode)
}

// RestrictToOwnerAndAdmins 同 RestrictToOwner：root 不受权限位限制，无须另行授权。
func RestrictToOwnerAndAdmins(path string) error {
	return RestrictToOwner(path)
}
