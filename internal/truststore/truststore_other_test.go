// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build !darwin && !windows && !linux

package truststore

import (
	"errors"
	"testing"
)

// 未支持平台必须返回 ErrUnsupported 哨兵,其文案即给用户的手动安装引导。
func TestInstallUnsupported(t *testing.T) {
	if err := Install(nil); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Install() = %v, want ErrUnsupported", err)
	}
}
