// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package capture

import (
	"bytes"
	"io"
	"net"
	"testing"
	"time"
)

func TestThrottleConnWriteLimitsThroughput(t *testing.T) {
	setThrottleBytesPerSecond(128 * 1024)
	defer setThrottleBytesPerSecond(0)

	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	payload := bytes.Repeat([]byte("x"), 32*1024)
	readDone := make(chan error, 1)
	go func() {
		_, err := io.CopyN(io.Discard, client, int64(len(payload)))
		readDone <- err
	}()

	start := time.Now()
	n, err := wrapThrottleConn(server).Write(payload)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Write returned error: %v", err)
	}
	if n != len(payload) {
		t.Fatalf("Write length = %d, want %d", n, len(payload))
	}
	if err := <-readDone; err != nil {
		t.Fatalf("reader returned error: %v", err)
	}
	if elapsed < 180*time.Millisecond {
		t.Fatalf("限速写入耗时 %s, 低于预期", elapsed)
	}
}
