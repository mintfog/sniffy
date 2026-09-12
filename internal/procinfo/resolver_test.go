// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package procinfo

import (
	"errors"
	"fmt"
	"net"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/pkg/process"
)

// 嵌入的 nil 接口使 GetProcessByConnection 之外的调用直接触发 panic。
type detectorFunc struct {
	process.Detector
	lookup func(net.Addr, net.Addr) (*process.ProcessInfo, error)
}

func (d detectorFunc) GetProcessByConnection(client, proxy net.Addr) (*process.ProcessInfo, error) {
	return d.lookup(client, proxy)
}

func newTestResolver(lookup func(net.Addr, net.Addr) (*process.ProcessInfo, error)) *Resolver {
	return &Resolver{
		detector: detectorFunc{lookup: lookup},
		ttl:      defaultTTL,
		timeout:  defaultTimeout,
		cache:    make(map[string]cacheEntry),
	}
}

func TestNewResolver(t *testing.T) {
	t.Parallel()
	r := NewResolver()
	if r == nil || r.detector == nil {
		t.Fatal("创建解析器失败")
	}
	t.Cleanup(func() {
		if err := r.detector.Stop(); err != nil {
			t.Errorf("停止检测器: %v", err)
		}
	})
	if r.icons == nil || r.cache == nil || len(r.cache) != 0 {
		t.Fatal("图标提取器与空缓存应已初始化")
	}
	if r.ttl != 30*time.Second || r.timeout != 2*time.Second {
		t.Fatalf("默认 TTL/超时 = %v/%v，期望 30s/2s", r.ttl, r.timeout)
	}
}

func TestResolveUnavailable(t *testing.T) {
	t.Parallel()
	client := &net.TCPAddr{Port: 50000}
	for _, tc := range []struct {
		name   string
		r      *Resolver
		client net.Addr
	}{
		{name: "nil解析器", client: client},
		{name: "缺少检测器", r: &Resolver{}, client: client},
		{name: "缺少客户端地址", r: newTestResolver(func(net.Addr, net.Addr) (*process.ProcessInfo, error) {
			panic("缺少地址时不应调用检测器")
		})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.r.Resolve(tc.client, nil); got != nil {
				t.Fatalf("Resolve() = %+v，期望 nil", got)
			}
		})
	}
}

func TestResolveConnectionAddresses(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		client net.Addr
		proxy  net.Addr
	}{
		{name: "IPv4", client: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 50000}, proxy: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8080}},
		{name: "IPv6带zone", client: &net.TCPAddr{IP: net.ParseIP("fe80::1"), Port: 50000, Zone: "eth0"}, proxy: &net.TCPAddr{IP: net.ParseIP("fe80::2"), Port: 8080, Zone: "eth0"}},
		{name: "缺少代理地址", client: &net.TCPAddr{Port: 50000}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			r := newTestResolver(func(client, proxy net.Addr) (*process.ProcessInfo, error) {
				calls.Add(1)
				if client != tc.client || proxy != tc.proxy {
					t.Errorf("检测器收到 (%v, %v)，期望客户端在前、代理在后 (%v, %v)", client, proxy, tc.client, tc.proxy)
				}
				return &process.ProcessInfo{PID: 42}, nil
			})
			for range 2 {
				if got := r.Resolve(tc.client, tc.proxy); got == nil || got.PID != 42 {
					t.Fatalf("Resolve() = %+v，期望 PID 42", got)
				}
			}
			if got := calls.Load(); got != 1 {
				t.Fatalf("检测器调用 %d 次，期望缓存使其只调用一次", got)
			}
		})
	}
}

func TestResolveCacheTTL(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		info *process.ProcessInfo
		err  error
		want *flow.ProcessInfo
	}{
		{name: "正缓存", info: &process.ProcessInfo{PID: 1, Name: "旧进程"}, want: &flow.ProcessInfo{PID: 1, Name: "旧进程"}},
		{name: "未找到进程"},
		{name: "检测失败", err: errors.New("权限不足")},
		{name: "错误伴随部分结果", info: &process.ProcessInfo{PID: 1}, err: errors.New("扫描中断")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var calls atomic.Int32
				r := newTestResolver(func(net.Addr, net.Addr) (*process.ProcessInfo, error) {
					n := calls.Add(1)
					time.Sleep(defaultTimeout / 2)
					if n == 1 {
						return tc.info, tc.err
					}
					return &process.ProcessInfo{PID: uint32(n), Name: "新进程"}, nil
				})
				client := &net.TCPAddr{Port: 50000}
				if got := r.Resolve(client, nil); !reflect.DeepEqual(got, tc.want) {
					t.Fatalf("首次解析 = %+v，期望 %+v", got, tc.want)
				}
				// 缓存读取沿用结果写入时确定的过期时间。
				for _, elapsed := range []time.Duration{r.ttl / 2, r.ttl/2 - time.Nanosecond} {
					time.Sleep(elapsed)
					if got := r.Resolve(client, nil); !reflect.DeepEqual(got, tc.want) {
						t.Fatalf("TTL 内结果 = %+v，期望 %+v", got, tc.want)
					}
					if got := calls.Load(); got != 1 {
						t.Fatalf("TTL 内检测器调用 %d 次，期望 1", got)
					}
				}
				time.Sleep(time.Nanosecond)
				want := &flow.ProcessInfo{PID: 2, Name: "新进程"}
				if got := r.Resolve(client, nil); !reflect.DeepEqual(got, want) {
					t.Fatalf("TTL 边界重新解析 = %+v，期望 %+v", got, want)
				}
				time.Sleep(r.ttl - time.Nanosecond)
				if got := r.Resolve(client, nil); !reflect.DeepEqual(got, want) || calls.Load() != 2 {
					t.Fatalf("刷新后缓存 = %+v，调用 %d 次，期望 %+v、2 次", got, calls.Load(), want)
				}
			})
		})
	}
}

func TestResolveCacheAddressIsolation(t *testing.T) {
	t.Parallel()
	clients := []*net.TCPAddr{
		{IP: net.ParseIP("127.0.0.1"), Port: 50000},
		{IP: net.ParseIP("127.0.0.1"), Port: 50001},
		{IP: net.ParseIP("127.0.0.2"), Port: 50000},
		{IP: net.ParseIP("fe80::1"), Port: 50000, Zone: "eth0"},
		{IP: net.ParseIP("fe80::1"), Port: 50000, Zone: "eth1"},
	}
	var calls atomic.Int32
	r := newTestResolver(func(client, _ net.Addr) (*process.ProcessInfo, error) {
		calls.Add(1)
		return &process.ProcessInfo{Name: client.String()}, nil
	})
	for range 2 {
		for _, client := range clients {
			copyAddr := *client
			if got := r.Resolve(&copyAddr, nil); got == nil || got.Name != client.String() {
				t.Fatalf("地址 %v 的缓存结果 = %+v", client, got)
			}
		}
	}
	if got := calls.Load(); got != int32(len(clients)) {
		t.Fatalf("检测器调用 %d 次，期望 %d", got, len(clients))
	}
}

func TestResolveTimeout(t *testing.T) {
	t.Parallel()
	for _, lateErr := range []error{nil, errors.New("迟到的扫描错误")} {
		name := "迟到的成功结果"
		if lateErr != nil {
			name = "迟到的错误"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				release := make(chan struct{})
				defer close(release)
				var calls atomic.Int32
				r := newTestResolver(func(net.Addr, net.Addr) (*process.ProcessInfo, error) {
					if calls.Add(1) == 1 {
						<-release
						return &process.ProcessInfo{PID: 1}, lateErr
					}
					return &process.ProcessInfo{PID: 2}, nil
				})
				client := &net.TCPAddr{Port: 50000}
				done := make(chan *flow.ProcessInfo, 1)
				go func() { done <- r.Resolve(client, nil) }()
				synctest.Wait()
				time.Sleep(r.timeout - time.Nanosecond)
				synctest.Wait()
				select {
				case got := <-done:
					t.Fatalf("超时前提前返回: %+v", got)
				default:
				}
				time.Sleep(time.Nanosecond)
				synctest.Wait()
				select {
				case got := <-done:
					if got != nil {
						t.Fatalf("超时结果 = %+v，期望 nil", got)
					}
				default:
					t.Fatal("达到超时期限仍未返回")
				}
				if got := r.Resolve(client, nil); got != nil || calls.Load() != 1 {
					t.Fatalf("超时负缓存 = %+v，调用 %d 次，期望 nil、1 次", got, calls.Load())
				}
				time.Sleep(r.ttl)
				if got := r.Resolve(client, nil); got == nil || got.PID != 2 || calls.Load() != 2 {
					t.Fatalf("超时负缓存过期后 = %+v，调用 %d 次，期望 PID 2、2 次", got, calls.Load())
				}
				// 倒序完成的两次查询应保留较新查询的缓存结果。
				// synctest.Test 会等待全部 goroutine 退出，迟到发送若阻塞会使测试失败。
				release <- struct{}{}
				synctest.Wait()
				if got := r.Resolve(client, nil); got == nil || got.PID != 2 || calls.Load() != 2 {
					t.Fatalf("迟到结果返回后 = %+v，调用 %d 次，期望 PID 2、2 次", got, calls.Load())
				}
			})
		})
	}
}

func TestResolveConcurrentFailurePreservesSuccess(t *testing.T) {
	t.Parallel()
	for _, timeout := range []bool{false, true} {
		t.Run(fmt.Sprintf("超时=%t", timeout), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				release := make(chan struct{})
				defer close(release)
				var calls atomic.Int32
				r := newTestResolver(func(net.Addr, net.Addr) (*process.ProcessInfo, error) {
					if calls.Add(1) == 1 {
						<-release
						return nil, errors.New("连接已关闭")
					}
					return &process.ProcessInfo{PID: 42}, nil
				})
				client := &net.TCPAddr{Port: 50000}
				done := make(chan struct{})
				go func() {
					r.Resolve(client, nil)
					close(done)
				}()
				synctest.Wait()
				if got := r.Resolve(client, nil); got == nil || got.PID != 42 {
					t.Fatalf("并发成功结果 = %+v，期望 PID 42", got)
				}
				if timeout {
					time.Sleep(r.timeout)
				} else {
					release <- struct{}{}
				}
				<-done
				if got := r.Resolve(client, nil); got == nil || got.PID != 42 || calls.Load() != 2 {
					t.Fatalf("并发失败后的缓存 = %+v，调用 %d 次，期望 PID 42、2 次", got, calls.Load())
				}
				// 正缓存过期后，失败仍应正常写入负缓存。
				time.Sleep(r.ttl)
				r.detector = detectorFunc{lookup: func(net.Addr, net.Addr) (*process.ProcessInfo, error) {
					return nil, errors.New("进程已退出")
				}}
				if got := r.Resolve(client, nil); got != nil {
					t.Fatalf("过期后解析失败 = %+v，期望 nil", got)
				}
				if got, ok := r.fromCache(client.String()); !ok || got != nil {
					t.Fatalf("过期后的负缓存 = %+v，命中=%t", got, ok)
				}
			})
		})
	}
}

func TestResolveSlowLookupDoesNotBlockOtherClients(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		defer close(release)
		slow := &net.TCPAddr{Port: 50000}
		fast := &net.TCPAddr{Port: 50001}
		r := newTestResolver(func(client, _ net.Addr) (*process.ProcessInfo, error) {
			if client == slow {
				<-release
			}
			return &process.ProcessInfo{Name: client.String()}, nil
		})
		done := make(chan *flow.ProcessInfo, 1)
		go func() { done <- r.Resolve(slow, nil) }()
		synctest.Wait()
		start := time.Now()
		for range 2 {
			if got := r.Resolve(fast, nil); got == nil || got.Name != fast.String() {
				t.Fatalf("其他客户端解析 = %+v，期望 %s", got, fast)
			}
		}
		if elapsed := time.Since(start); elapsed != 0 {
			t.Fatalf("其他客户端被慢扫描阻塞了 %v", elapsed)
		}
		release <- struct{}{}
		if got := <-done; got == nil || got.Name != slow.String() {
			t.Fatalf("慢扫描释放后结果 = %+v，期望 %s", got, slow)
		}
	})
}

func TestResolveCacheCapacity(t *testing.T) {
	t.Parallel()
	for _, negative := range []bool{false, true} {
		t.Run(fmt.Sprintf("负缓存=%t", negative), func(t *testing.T) {
			var calls atomic.Int32
			r := newTestResolver(func(client, _ net.Addr) (*process.ProcessInfo, error) {
				calls.Add(1)
				if negative {
					return nil, nil
				}
				return &process.ProcessInfo{Name: client.String()}, nil
			})
			for i := range 2*maxCacheEntries + 1 {
				client := &net.TCPAddr{Port: 10000 + i}
				var want *flow.ProcessInfo
				if !negative {
					want = &flow.ProcessInfo{Name: client.String()}
				}
				for range 2 {
					got := r.Resolve(client, nil)
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("地址 %v 的结果 = %+v，期望 %+v", client, got, want)
					}
				}
				if got := calls.Load(); got != int32(i+1) {
					t.Fatalf("第 %d 个地址的检测器调用累计 %d 次，期望 %d", i, got, i+1)
				}
				if got := len(r.cache); got > maxCacheEntries {
					t.Fatalf("缓存容量 %d 超过上限 %d", got, maxCacheEntries)
				}
			}
		})
	}
}

func TestResolveConcurrent(t *testing.T) {
	t.Parallel()
	for _, churn := range []bool{false, true} {
		t.Run(fmt.Sprintf("持续新地址=%t", churn), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				r := newTestResolver(func(client, _ net.Addr) (*process.ProcessInfo, error) {
					port := client.(*net.TCPAddr).Port
					if port%2 == 0 {
						return nil, nil
					}
					return &process.ProcessInfo{PID: uint32(port)}, nil
				})
				const workers, iterations = 16, 128
				start := make(chan struct{})
				var wg sync.WaitGroup
				for worker := range workers {
					wg.Go(func() {
						<-start
						for i := range iterations {
							port := 10000 + i%2
							if churn {
								port = 10000 + worker*iterations + i
							}
							var want *flow.ProcessInfo
							if port%2 != 0 {
								want = &flow.ProcessInfo{PID: uint32(port)}
							}
							got := r.Resolve(&net.TCPAddr{Port: port}, nil)
							if !reflect.DeepEqual(got, want) {
								t.Errorf("端口 %d 的并发解析结果 = %+v，期望 %+v", port, got, want)
								return
							}
						}
					})
				}
				close(start)
				wg.Wait()
				if got := len(r.cache); got > maxCacheEntries {
					t.Fatalf("并发写入后缓存容量 %d 超过上限 %d", got, maxCacheEntries)
				}
			})
		})
	}
}
