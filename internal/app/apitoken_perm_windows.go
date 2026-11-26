// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build windows

package app

import (
	"fmt"
	"os"

	"github.com/mintfog/sniffy/internal/platform"
)

// Windows 文件模式不能可靠反映 DACL，权限由 secureFilePerm 收紧。
func filePermTooWide(path string) (bool, error) {
	return false, nil
}

// secureFilePerm 使用受保护的 DACL 将访问权限制为当前用户。
func secureFilePerm(path string) error {
	err := platform.RestrictToOwner(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("设置 token 文件 DACL: %w", err)
	}
	return nil
}
