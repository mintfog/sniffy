// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package service

import (
	"sync"

	"github.com/mintfog/sniffy/internal/flow"
)

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
		s.totalBytes += int64(len(f.Response.Body))
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
