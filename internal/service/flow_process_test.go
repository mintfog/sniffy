// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package service

import (
	"reflect"
	"sync"
	"testing"

	"github.com/mintfog/sniffy/internal/core"
	"github.com/mintfog/sniffy/internal/flow"
)

func TestProcessUpdateDuringResponse(t *testing.T) {
	svc := newTestService(t)
	f := newFlow("并发进程补全")
	svc.RecordFlowStarted(f)
	events, unsubscribe := svc.Bus().Subscribe()
	defer unsubscribe()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			f.Response = &flow.Response{Status: 200}
			f.State = flow.StateCompleted
			f.Timing.DurationMs = int64(i)
		}
	}()
	process := &flow.ProcessInfo{
		PID:          42,
		Name:         "测试进程",
		Path:         "/usr/bin/test",
		User:         "用户",
		IconData:     "aW1n",
		IconType:     "png",
		HasIcon:      true,
		IconCategory: "cli",
	}
	f.SetProcess(process)
	svc.RecordFlowUpdated(f)
	wg.Wait()
	select {
	case e := <-events:
		dto := e.Payload.(HTTPSessionDTO)
		if dto.Status != "pending" || dto.Response != nil || dto.ProcessID != 42 {
			t.Fatalf("进程补全未使用已发布快照: %+v", dto)
		}
		got := flow.ProcessInfo{
			PID:          dto.ProcessID,
			Name:         dto.ProcessName,
			Path:         dto.ProcessPath,
			User:         dto.ProcessUser,
			IconData:     dto.IconData,
			IconType:     dto.IconType,
			HasIcon:      dto.HasIcon,
			IconCategory: dto.IconCategory,
		}
		if !reflect.DeepEqual(got, *process) {
			t.Fatalf("进程信息不完整: %+v", got)
		}
	default:
		t.Fatal("缺少进程补全事件")
	}
	svc.RecordFlowCompleted(f)
	svc.RecordFlowUpdated(f)
	var final HTTPSessionDTO
	for len(events) > 0 {
		e := <-events
		if e.Type == core.EventFlowUpdated {
			final = e.Payload.(HTTPSessionDTO)
		}
	}
	if final.Status != "completed" || final.Response == nil || final.ProcessID != 42 {
		t.Fatalf("完成后进程更新丢失响应: %+v", final)
	}
	svc.DeleteSession(f.ID)
	svc.RecordFlowUpdated(f)
	if _, ok := svc.RawFlow(f.ID); ok {
		t.Fatal("迟到的进程补全恢复了已删除会话")
	}
}

func BenchmarkRecordFlowLifecycle(b *testing.B) {
	svc := New(nil, core.NewEventBus(), "", "")
	f := newFlow("生命周期基准")
	b.ReportAllocs()
	for b.Loop() {
		f.Response = nil
		f.State = flow.StatePending
		svc.RecordFlowStarted(f)
		f.SetProcess(&flow.ProcessInfo{PID: 42})
		svc.RecordFlowUpdated(f)
		f.Response = &flow.Response{Status: 200}
		f.State = flow.StateCompleted
		svc.RecordFlowCompleted(f)
	}
}

func BenchmarkRecordFlowLifecycleParallel(b *testing.B) {
	svc := New(nil, core.NewEventBus(), "", "")
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		f := newFlow(flow.NewID())
		for pb.Next() {
			f.Response = nil
			f.State = flow.StatePending
			svc.RecordFlowStarted(f)
			f.SetProcess(&flow.ProcessInfo{PID: 42})
			svc.RecordFlowUpdated(f)
			f.Response = &flow.Response{Status: 200}
			f.State = flow.StateCompleted
			svc.RecordFlowCompleted(f)
		}
	})
}

func TestPendingSnapshotRemoval(t *testing.T) {
	for _, action := range []string{"删除", "清空", "容量淘汰", "缩减容量", "完成"} {
		t.Run(action, func(t *testing.T) {
			svc := newTestService(t)
			svc.sessions.setCap(2)
			f := newFlow("待移除会话")
			f.Request.Body = []byte("需要释放的正文预览")
			svc.RecordFlowStarted(f)
			survivor := newFlow("保留会话")
			svc.RecordFlowStarted(survivor)
			if _, pending, exists := svc.sessions.pendingSnapshot(f.ID); !pending || !exists {
				t.Fatal("缺少处理中的快照")
			}
			events, unsubscribe := svc.Bus().Subscribe()
			defer unsubscribe()
			switch action {
			case "删除":
				svc.DeleteSession(f.ID)
			case "清空":
				svc.ClearSessions()
			case "容量淘汰":
				svc.RecordFlowStarted(newFlow("新会话"))
			case "缩减容量":
				svc.sessions.setCap(1)
			case "完成":
				svc.RecordFlowCompleted(f)
			}
			if _, pending, _ := svc.sessions.pendingSnapshot(f.ID); pending {
				t.Fatal("会话快照未释放")
			}
			if action != "清空" {
				if _, pending, exists := svc.sessions.pendingSnapshot(survivor.ID); !pending || !exists {
					t.Fatal("保留会话的快照被误删")
				}
			}
			if action != "完成" {
				for len(events) > 0 {
					<-events
				}
				f.SetProcess(&flow.ProcessInfo{PID: 42})
				svc.RecordFlowUpdated(f)
				if len(events) != 0 {
					t.Fatal("已移除会话仍发布进程补全事件")
				}
			}
		})
	}
}

func TestConcurrentPendingSnapshotRemoval(t *testing.T) {
	svc := newTestService(t)
	svc.sessions.setCap(16)
	var wg sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				f := newFlow(flow.NewID())
				svc.RecordFlowStarted(f)
				svc.RecordFlowUpdated(f)
				svc.DeleteSession(f.ID)
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			svc.ClearSessions()
			svc.sessions.setCap(8 + i%8)
		}
	}()
	wg.Wait()
	svc.ClearSessions()
	if len(svc.sessions.pending) != 0 {
		t.Fatalf("残留 %d 个快照", len(svc.sessions.pending))
	}
}
