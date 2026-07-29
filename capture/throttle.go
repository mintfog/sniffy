// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package capture

import (
	"io"
	"net"
	"sync/atomic"
	"time"
)

const (
	defaultThrottleBytesPerSecond int64 = 128 * 1024
	throttleMinChunk                    = 1024
	throttleMaxChunk                    = 16 * 1024
)

var throttleBytesPerSecond atomic.Int64

// SetThrottle 设置连接级读写限速。bytesPerSecond <= 0 时沿用默认速率。
func SetThrottle(enabled bool, bytesPerSecond int64) {
	if !enabled {
		throttleBytesPerSecond.Store(0)
		return
	}
	if bytesPerSecond <= 0 {
		bytesPerSecond = defaultThrottleBytesPerSecond
	}
	throttleBytesPerSecond.Store(bytesPerSecond)
}

func wrapThrottleConn(conn net.Conn) net.Conn {
	return &throttleConn{Conn: conn}
}

type throttleConn struct {
	net.Conn
}

func (c *throttleConn) Read(p []byte) (int, error) {
	rate := throttleBytesPerSecond.Load()
	if rate > 0 {
		if chunk := throttleChunkSize(rate); len(p) > chunk {
			p = p[:chunk]
		}
	}
	n, err := c.Conn.Read(p)
	if rate > 0 && n > 0 {
		throttleSleep(n, rate)
	}
	return n, err
}

func (c *throttleConn) Write(p []byte) (int, error) {
	total := 0
	for len(p) > 0 {
		rate := throttleBytesPerSecond.Load()
		if rate <= 0 {
			n, err := c.Conn.Write(p)
			return total + n, err
		}

		chunk := throttleChunkSize(rate)
		if len(p) < chunk {
			chunk = len(p)
		}
		n, err := c.Conn.Write(p[:chunk])
		total += n
		if n > 0 {
			throttleSleep(n, rate)
		}
		if err != nil {
			return total, err
		}
		if n == 0 {
			return total, io.ErrShortWrite
		}
		p = p[n:]
	}
	return total, nil
}

func throttleChunkSize(rate int64) int {
	chunk := rate / 20
	if chunk < throttleMinChunk {
		chunk = throttleMinChunk
	}
	if chunk > throttleMaxChunk {
		chunk = throttleMaxChunk
	}
	return int(chunk)
}

func throttleSleep(bytes int, rate int64) {
	if bytes <= 0 || rate <= 0 {
		return
	}
	time.Sleep(time.Duration(int64(bytes)) * time.Second / time.Duration(rate))
}
