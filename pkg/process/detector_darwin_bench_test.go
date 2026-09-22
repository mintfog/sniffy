// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build darwin

package process

import (
	"os"
	"testing"
)

func BenchmarkDarwinConnectionLookup(b *testing.B) {
	benchmarkDarwinConnection(b, false)
}

func BenchmarkDarwinConnectionScan(b *testing.B) {
	benchmarkDarwinConnection(b, true)
}

func benchmarkDarwinConnection(b *testing.B, forceScan bool) {
	client, _ := darwinTCPPair(b, "tcp4", "127.0.0.1:0")
	d, err := NewDarwinDetector()
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if forceScan {
			_ = d.Stop()
		}
		info, err := d.GetProcessByConnection(client.LocalAddr(), client.RemoteAddr())
		if err != nil || info.PID != uint32(os.Getpid()) {
			b.Fatalf("识别当前进程: %v, %v", info, err)
		}
	}
}
