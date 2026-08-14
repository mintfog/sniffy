// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package service

import (
	"fmt"
	"maps"
	"net/http"
	"reflect"
	"sync"
	"testing"
)

// hostCounts 把无序的 TopHosts 规整为可比较的映射(snapshot 按 map 遍历取值,顺序不保证)。
func hostCounts(list []HostCount) map[string]int64 {
	out := make(map[string]int64, len(list))
	for _, h := range list {
		out[h.Host] = h.Count
	}
	return out
}

func TestStatsCollectorAccumulates(t *testing.T) {
	t.Parallel()
	s := newStatsCollector()
	s.record(newFlow("a",
		withRequest(http.MethodGet, "https://a.example.com/x"),
		withResponse(http.StatusOK, "text/plain", []byte("12345")),
		withDuration(100),
	))
	s.record(newFlow("b",
		withRequest(http.MethodPost, "https://a.example.com/y"),
		withResponse(http.StatusNotFound, "text/plain", []byte("no")),
		withDuration(300),
	))
	s.record(newFlow("c",
		withRequest(http.MethodGet, "https://b.example.com/z"),
		withResponse(http.StatusOK, "text/plain", nil),
	))

	got := s.snapshot()
	if got.TotalRequests != 3 || got.TotalSessions != 3 {
		t.Errorf("请求数 = %d/%d, want 3/3", got.TotalRequests, got.TotalSessions)
	}
	if got.TotalBytes != 7 {
		t.Errorf("总字节 = %d, want 7", got.TotalBytes)
	}
	// 只有耗时 > 0 的会话计入平均值。
	if got.AverageResponseTime != 200 {
		t.Errorf("平均耗时 = %v, want 200", got.AverageResponseTime)
	}
	if want := map[int]int64{http.StatusOK: 2, http.StatusNotFound: 1}; !maps.Equal(got.StatusCodeDistribution, want) {
		t.Errorf("状态码分布 = %v, want %v", got.StatusCodeDistribution, want)
	}
	if want := map[string]int64{http.MethodGet: 2, http.MethodPost: 1}; !maps.Equal(got.MethodDistribution, want) {
		t.Errorf("方法分布 = %v, want %v", got.MethodDistribution, want)
	}
	if want := map[string]int64{"a.example.com": 2, "b.example.com": 1}; !maps.Equal(hostCounts(got.TopHosts), want) {
		t.Errorf("主机分布 = %v, want %v", hostCounts(got.TopHosts), want)
	}
}

// TestStatsCountsSpilledResponseBytes 走透传旁路的响应体不在内存里(Body 为空),
// 总流量须按旁路记账大小累加,不能用 len(Body)。
func TestStatsCountsSpilledResponseBytes(t *testing.T) {
	t.Parallel()
	svc, c := newSpillService(t)
	const size = 10 << 20
	svc.RecordFlowCompleted(spilledFlow(t, c, "big", make([]byte, size)))
	svc.RecordFlowCompleted(newFlow("small", withResponse(http.StatusOK, "text/plain", []byte("hello"))))

	if got := svc.Statistics().TotalBytes; got != size+5 {
		t.Fatalf("总字节 = %d, want %d(旁路响应必须按记账大小计入)", got, size+5)
	}
}

// TestStatsTopHostsSortedAndCapped TopHosts 是"前 N 名":按次数降序、条目数有上限。
func TestStatsTopHostsSortedAndCapped(t *testing.T) {
	t.Parallel()
	s := newStatsCollector()
	// h00 命中 1 次、h01 两次……次数越大的主机名字典序越靠后,可同时验证排序键与稳定性。
	for i := range topHostLimit + 5 {
		for range i + 1 {
			s.record(newFlow("f", withRequest(http.MethodGet, fmt.Sprintf("https://h%02d.example.com/x", i))))
		}
	}

	got := s.snapshot().TopHosts
	if len(got) != topHostLimit {
		t.Fatalf("条目数 = %d, want %d", len(got), topHostLimit)
	}
	if got[0].Host != "h14.example.com" || got[0].Count != topHostLimit+5 {
		t.Errorf("首位应是命中最多的主机: %+v", got[0])
	}
	for i := 1; i < len(got); i++ {
		if got[i].Count > got[i-1].Count {
			t.Fatalf("应按次数降序: %+v", got)
		}
	}

	// 次数相同时按主机名排,保证同一份数据多次快照顺序一致。
	tie := newStatsCollector()
	for _, host := range []string{"z.example.com", "a.example.com", "m.example.com"} {
		tie.record(newFlow("f", withRequest(http.MethodGet, "https://"+host+"/x")))
	}
	first := tie.snapshot().TopHosts
	if first[0].Host != "a.example.com" || first[2].Host != "z.example.com" {
		t.Fatalf("同票应按主机名升序: %+v", first)
	}
	if second := tie.snapshot().TopHosts; !reflect.DeepEqual(first, second) {
		t.Fatalf("同一份数据的两次快照应完全一致: %+v vs %+v", first, second)
	}
}

// TestStatsCollectorHandlesPartialFlows "完成"也包括请求发不出去、响应没读到的情形,
// 累加时 Request / Response 均可能为 nil。
func TestStatsCollectorHandlesPartialFlows(t *testing.T) {
	t.Parallel()
	s := newStatsCollector()

	noResp := newFlow("no-resp")
	s.record(noResp)

	noReq := newFlow("no-req", withResponse(http.StatusBadGateway, "", nil))
	noReq.Request = nil
	s.record(noReq)

	got := s.snapshot()
	if got.TotalRequests != 2 {
		t.Errorf("请求数 = %d, want 2", got.TotalRequests)
	}
	if got.AverageResponseTime != 0 {
		t.Errorf("无计时样本时平均耗时应为 0,实际 %v", got.AverageResponseTime)
	}
	if want := map[string]int64{http.MethodGet: 1}; !maps.Equal(got.MethodDistribution, want) {
		t.Errorf("方法分布 = %v, want %v", got.MethodDistribution, want)
	}
	if want := map[int]int64{http.StatusBadGateway: 1}; !maps.Equal(got.StatusCodeDistribution, want) {
		t.Errorf("状态码分布 = %v, want %v", got.StatusCodeDistribution, want)
	}
}

// TestStatsSnapshotIsDetached 快照交给 transport 序列化,与采集侧并发,不能共享底层 map。
func TestStatsSnapshotIsDetached(t *testing.T) {
	t.Parallel()
	s := newStatsCollector()
	s.record(newFlow("a", withResponse(http.StatusOK, "", []byte("x"))))

	snap := s.snapshot()
	snap.StatusCodeDistribution[http.StatusOK] = 999
	snap.MethodDistribution["HACKED"] = 999

	fresh := s.snapshot()
	if fresh.StatusCodeDistribution[http.StatusOK] != 1 {
		t.Errorf("改动快照不应影响内部状态码分布: %v", fresh.StatusCodeDistribution)
	}
	if _, ok := fresh.MethodDistribution["HACKED"]; ok {
		t.Errorf("改动快照不应影响内部方法分布: %v", fresh.MethodDistribution)
	}
}

func TestStatsCollectorReset(t *testing.T) {
	t.Parallel()
	s := newStatsCollector()
	s.record(newFlow("a", withResponse(http.StatusOK, "", []byte("x")), withDuration(50)))
	s.reset()

	got := s.snapshot()
	if got.TotalRequests != 0 || got.TotalBytes != 0 || got.AverageResponseTime != 0 {
		t.Fatalf("重置后计数应清零: %+v", got)
	}
	if len(got.StatusCodeDistribution) != 0 || len(got.MethodDistribution) != 0 || len(got.TopHosts) != 0 {
		t.Fatalf("重置后分布应为空: %+v", got)
	}
	// 重置后仍可继续累加(map 被重建而不是置 nil)。
	s.record(newFlow("b", withResponse(http.StatusOK, "", []byte("y"))))
	if got := s.snapshot(); got.TotalRequests != 1 {
		t.Fatalf("重置后应能继续累加,实际 %d", got.TotalRequests)
	}
}

// TestStatsCollectorConcurrent 采集在每条会话完成时发生,快照由 UI 轮询;-race 下压出并发问题。
func TestStatsCollectorConcurrent(t *testing.T) {
	t.Parallel()
	s := newStatsCollector()

	const workers = 8
	const perWorker = 200
	var wg sync.WaitGroup
	for w := range workers {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := range perWorker {
				s.record(newFlow(fmt.Sprintf("w%d-%d", w, i),
					withRequest(http.MethodGet, fmt.Sprintf("https://h%d.example.com/x", w)),
					withResponse(http.StatusOK, "", []byte("ab")),
					withDuration(10),
				))
			}
		}(w)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range workers * perWorker {
			_ = s.snapshot()
		}
	}()
	wg.Wait()

	got := s.snapshot()
	if got.TotalRequests != workers*perWorker {
		t.Fatalf("请求数 = %d, want %d", got.TotalRequests, workers*perWorker)
	}
	if got.TotalBytes != int64(workers*perWorker*2) {
		t.Fatalf("总字节 = %d, want %d", got.TotalBytes, workers*perWorker*2)
	}
	if got.AverageResponseTime != 10 {
		t.Fatalf("平均耗时 = %v, want 10", got.AverageResponseTime)
	}
}

func TestServiceStatisticsSnapshot(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	svc.RecordFlowCompleted(newFlow("a", withResponse(http.StatusOK, "", []byte("hello")), withDuration(20)))

	got := svc.Statistics()
	if got.TotalRequests != 1 || got.TotalBytes != 5 || got.AverageResponseTime != 20 {
		t.Fatalf("统计快照 = %+v", got)
	}
}
