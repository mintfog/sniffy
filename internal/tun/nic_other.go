// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build !linux && !darwin && !windows

package tun

import (
	"errors"
	"runtime"
)

func openNIC(nicConfig) (*nic, error) {
	return nil, errors.New("不支持的操作系统: " + runtime.GOOS)
}
