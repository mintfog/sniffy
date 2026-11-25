// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build !windows && !darwin

package process

import (
	"errors"
	"fmt"
)

func installedApps() ([]InstalledApp, error) {
	return nil, fmt.Errorf("当前平台不能列出已安装的应用: %w", errors.ErrUnsupported)
}
