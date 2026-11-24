// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build !linux

package tunhost

import "errors"

// AppImage 只在 Linux 上运行。
var launchedAppImage *appImage

func checkPeerAppImage(int, *appImage) error { return errors.ErrUnsupported }
