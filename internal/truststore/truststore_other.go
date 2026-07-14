// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build !darwin && !windows && !linux

package truststore

// Install 在不支持的平台上直接返回 ErrUnsupported。
func Install(pem []byte) error { return ErrUnsupported }
