// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package process

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestRunningApps(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	entries, err := NormalizeAppEntries([]string{self, filepath.Base(self), "no-such-app", filepath.Join(filepath.Dir(self), "no-such-app")})
	if err != nil {
		t.Fatal(err)
	}
	got, err := RunningApps(entries)
	if err != nil {
		t.Fatal(err)
	}
	if want := []bool{true, true, false, false}; !slices.Equal(got, want) {
		t.Fatalf("RunningApps(%q) = %v，期望 %v", entries, got, want)
	}
}
