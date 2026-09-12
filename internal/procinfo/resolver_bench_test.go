// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package procinfo

import (
	"net"
	"testing"
	"time"

	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/pkg/process"
)

func BenchmarkResolve(b *testing.B) {
	client := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 50000}
	proxy := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8080}
	for _, tc := range []struct {
		name string
		info *flow.ProcessInfo
	}{
		{name: "正缓存", info: &flow.ProcessInfo{PID: 42, Name: "sniffy"}},
		{name: "负缓存"},
	} {
		for _, parallel := range []bool{false, true} {
			name := tc.name + "/串行"
			if parallel {
				name = tc.name + "/并行"
			}
			b.Run(name, func(b *testing.B) {
				r := newTestResolver(func(net.Addr, net.Addr) (*process.ProcessInfo, error) {
					panic("缓存命中时不应调用检测器")
				})
				r.ttl = time.Hour
				r.store(client.String(), tc.info)
				b.ReportAllocs()
				b.ResetTimer()
				if parallel {
					b.RunParallel(func(pb *testing.PB) {
						for pb.Next() {
							if got := r.Resolve(client, proxy); got != tc.info {
								b.Error("缓存结果不匹配")
								return
							}
						}
					})
				} else {
					for b.Loop() {
						if got := r.Resolve(client, proxy); got != tc.info {
							b.Fatal("缓存结果不匹配")
						}
					}
				}
			})
		}
	}
	// 检测器即时返回，使基准聚焦解析器自身的调度、缓存与映射开销。
	b.Run("缓存未命中", func(b *testing.B) {
		pi := &process.ProcessInfo{PID: 42, Name: "sniffy"}
		r := newTestResolver(func(net.Addr, net.Addr) (*process.ProcessInfo, error) { return pi, nil })
		r.ttl = 0
		b.ReportAllocs()
		for b.Loop() {
			if got := r.Resolve(client, proxy); got == nil || got.PID != pi.PID {
				b.Fatal("进程信息不匹配")
			}
		}
	})
}
