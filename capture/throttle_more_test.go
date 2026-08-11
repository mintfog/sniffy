// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0

package capture

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestSetThrottleUsesDefaultRate(t *testing.T) {
	SetThrottle(true, 0)
	defer SetThrottle(false, 0)
	if got := throttleBytesPerSecond.Load(); got != defaultThrottleBytesPerSecond {
		t.Fatalf("default rate = %d, want %d", got, defaultThrottleBytesPerSecond)
	}
}

func TestThrottleChunkSizeBounds(t *testing.T) {
	tests := []struct {
		rate int64
		want int
	}{
		{rate: 1, want: throttleMinChunk},
		{rate: 128 * 1024, want: 128 * 1024 / 20}, // 落在上下限之间时取 1/20 秒的量
		{rate: 1024 * 1024, want: throttleMaxChunk},
	}
	for _, tt := range tests {
		if got := throttleChunkSize(tt.rate); got != tt.want {
			t.Errorf("throttleChunkSize(%d) = %d, want %d", tt.rate, got, tt.want)
		}
	}
	throttleSleep(0, 1)
	throttleSleep(1, 0)
}

func TestThrottleConnReadHonorsChunkAndDisable(t *testing.T) {
	// 限速是全局状态,中途 t.Fatalf 退出时必须复位,否则会泄漏给后续用例。
	defer SetThrottle(false, 0)

	data := bytes.Repeat([]byte("x"), 32*1024)
	SetThrottle(true, 1_000_000_000)
	conn := newMemoryConn(data)
	p := make([]byte, len(data))
	n, err := wrapThrottleConn(conn).Read(p)
	if err != nil {
		t.Fatalf("throttled Read returned %v", err)
	}
	if n != throttleMaxChunk {
		t.Fatalf("throttled Read size = %d, want %d", n, throttleMaxChunk)
	}

	SetThrottle(false, 0)
	conn = newMemoryConn(data)
	n, err = wrapThrottleConn(conn).Read(p)
	if err != nil {
		t.Fatalf("unthrottled Read returned %v", err)
	}
	if n != len(data) {
		t.Fatalf("unthrottled Read size = %d, want %d", n, len(data))
	}
}

func TestThrottleConnWriteErrorPaths(t *testing.T) {
	defer SetThrottle(false, 0)

	SetThrottle(false, 0)
	plain := newMemoryConn(nil)
	n, err := wrapThrottleConn(plain).Write([]byte("plain"))
	if err != nil || n != 5 || plain.output() != "plain" {
		t.Fatalf("unthrottled Write = (%d, %v), output %q", n, err, plain.output())
	}

	SetThrottle(true, 1_000_000_000)
	n, err = wrapThrottleConn(&zeroWriteConn{memoryConn: newMemoryConn(nil)}).Write([]byte("x"))
	if !errors.Is(err, io.ErrShortWrite) || n != 0 {
		t.Fatalf("zero Write = (%d, %v), want (0, io.ErrShortWrite)", n, err)
	}

	wantErr := errors.New("write failed")
	n, err = wrapThrottleConn(&errorWriteConn{memoryConn: newMemoryConn(nil), err: wantErr}).Write([]byte("abc"))
	if !errors.Is(err, wantErr) || n != 1 {
		t.Fatalf("partial Write = (%d, %v), want (1, %v)", n, err, wantErr)
	}
}
