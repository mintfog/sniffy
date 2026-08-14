// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package service

import (
	"cmp"
	"slices"
	"sync"

	"github.com/mintfog/sniffy/internal/flow"
)

// topHostLimit 是 TopHosts 的条目上限:主机数随抓包量无上限增长,全量返回会让轮询载荷持续变大。
const topHostLimit = 10

// StatisticsDTO 对应前端 Statistics。
type StatisticsDTO struct {
	TotalRequests          int64            `json:"totalRequests"`
	TotalSessions          int64            `json:"totalSessions"`
	TotalBytes             int64            `json:"totalBytes"`
	RequestsPerSecond      float64          `json:"requestsPerSecond"`
	AverageResponseTime    float64          `json:"averageResponseTime"`
	StatusCodeDistribution map[int]int64    `json:"statusCodeDistribution"`
	MethodDistribution     map[string]int64 `json:"methodDistribution"`
	TopHosts               []HostCount      `json:"topHosts"`
}

// HostCount 是 topHosts 的元素。
type HostCount struct {
	Host  string `json:"host"`
	Count int64  `json:"count"`
}

type statsCollector struct {
	mu            sync.RWMutex
	totalRequests int64
	totalBytes    int64
	totalRespTime int64
	respCount     int64
	statusCodes   map[int]int64
	methods       map[string]int64
	hosts         map[string]int64
}

func newStatsCollector() *statsCollector {
	return &statsCollector{
		statusCodes: make(map[int]int64),
		methods:     make(map[string]int64),
		hosts:       make(map[string]int64),
	}
}

// record 在一个 flow 完成时累加统计。
func (s *statsCollector) record(f *flow.Flow) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.totalRequests++
	if f.Request != nil {
		s.methods[f.Request.Method]++
		s.hosts[f.Request.Host]++
	}
	if f.Response != nil {
		s.statusCodes[f.Response.Status]++
		// 走透传旁路的响应体不在内存里(Body 为空),长度取旁路记录值,见 flow.Response.BodyLen。
		s.totalBytes += f.Response.BodyLen()
	}
	if f.Timing.DurationMs > 0 {
		s.totalRespTime += f.Timing.DurationMs
		s.respCount++
	}
}

func (s *statsCollector) snapshot() StatisticsDTO {
	s.mu.RLock()
	defer s.mu.RUnlock()

	statusCp := make(map[int]int64, len(s.statusCodes))
	for k, v := range s.statusCodes {
		statusCp[k] = v
	}
	methodCp := make(map[string]int64, len(s.methods))
	for k, v := range s.methods {
		methodCp[k] = v
	}
	top := make([]HostCount, 0, len(s.hosts))
	for h, c := range s.hosts {
		top = append(top, HostCount{Host: h, Count: c})
	}
	// 按次数降序取前 N;同票按主机名排,使快照顺序稳定(map 遍历顺序不固定)。
	slices.SortFunc(top, func(a, b HostCount) int {
		if c := cmp.Compare(b.Count, a.Count); c != 0 {
			return c
		}
		return cmp.Compare(a.Host, b.Host)
	})
	if len(top) > topHostLimit {
		top = top[:topHostLimit]
	}

	var avg float64
	if s.respCount > 0 {
		avg = float64(s.totalRespTime) / float64(s.respCount)
	}

	return StatisticsDTO{
		TotalRequests:          s.totalRequests,
		TotalSessions:          s.totalRequests,
		TotalBytes:             s.totalBytes,
		AverageResponseTime:    avg,
		StatusCodeDistribution: statusCp,
		MethodDistribution:     methodCp,
		TopHosts:               top,
	}
}

func (s *statsCollector) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.totalRequests = 0
	s.totalBytes = 0
	s.totalRespTime = 0
	s.respCount = 0
	s.statusCodes = make(map[int]int64)
	s.methods = make(map[string]int64)
	s.hosts = make(map[string]int64)
}
