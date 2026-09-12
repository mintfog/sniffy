// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build !darwin && !windows && !linux

package sysproxy

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestUnsupportedPlatform(t *testing.T) {
	t.Parallel()
	assert.ErrorIs(t, Set("localhost", 8080), errUnsupported)
	assert.ErrorIs(t, Clear(), errUnsupported)
	assert.False(t, PointsTo("localhost", 8080))
}
