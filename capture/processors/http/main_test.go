// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package http

import (
	"os"
	"testing"

	"github.com/mintfog/sniffy/ca"
)

func TestMain(m *testing.M) {
	rootCA, err := ca.NewInMemorySelfSignedCA()
	if err != nil {
		panic(err)
	}
	SetCA(rootCA)
	os.Exit(m.Run())
}

func BenchmarkCurrentCALookup(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if currentCA() == nil {
			b.Fatal("根 CA 未注入")
		}
	}
}
