// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build !darwin && !windows && !linux

package sysproxy

import "errors"

var errUnsupported = errors.New("当前平台不支持设置系统代理")

func Set(host string, port int) error     { return errUnsupported }
func Clear() error                        { return errUnsupported }
func PointsTo(host string, port int) bool { return false }
