// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build !windows

package platform

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRestrictToOwner(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "d")
	file := filepath.Join(t.TempDir(), "f")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]os.FileMode{dir: 0o700, file: 0o600} {
		if err := RestrictToOwner(path); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s 权限 %o，应为 %o", path, got, want)
		}
	}
}
