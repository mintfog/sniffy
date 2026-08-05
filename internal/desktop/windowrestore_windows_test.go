// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build desktop && windows

package desktop

import "testing"

func TestPlacementRestoresMaximised(t *testing.T) {
	tests := []struct {
		name  string
		flags uint32
		want  bool
	}{
		{name: "普通窗口", flags: 0, want: false},
		{name: "恢复到最大化", flags: wpfRestoreToMaximised, want: true},
		{name: "保留其他标志", flags: wpfRestoreToMaximised | 0x0001, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := placementRestoresMaximised(tt.flags); got != tt.want {
				t.Fatalf("placementRestoresMaximised(%#x) = %v, want %v", tt.flags, got, tt.want)
			}
		})
	}
}
