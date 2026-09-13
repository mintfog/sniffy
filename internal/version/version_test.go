// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package version

import "testing"

func TestGetFallsBackWhenNotInjected(t *testing.T) {
	originalVersion := Version
	t.Cleanup(func() { Version = originalVersion })

	Version = ""
	if got := Get(); got != DevVersion {
		t.Fatalf("Get() = %q,期望 %q", got, DevVersion)
	}
}

func TestGetPrefersInjectedVersion(t *testing.T) {
	originalVersion := Version
	t.Cleanup(func() { Version = originalVersion })

	Version = "1.2.3-test"
	if got := Get(); got != "1.2.3-test" {
		t.Fatalf("Get() = %q,期望注入值 1.2.3-test", got)
	}
}
