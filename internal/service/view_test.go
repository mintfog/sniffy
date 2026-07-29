// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package service

import (
	"testing"
	"time"
)

func TestRFC3339PreservesFractionalSeconds(t *testing.T) {
	got := rfc3339(time.Date(2026, time.July, 29, 12, 34, 56, 123456789, time.UTC))
	want := "2026-07-29T12:34:56.123456789Z"
	if got != want {
		t.Fatalf("rfc3339() = %q, want %q", got, want)
	}
}
