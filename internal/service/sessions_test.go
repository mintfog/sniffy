// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package service

import (
	"fmt"
	"math"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/mintfog/sniffy/internal/flow"
)

// storeIDs 把分页结果压成 ID 列表。
func storeIDs(flows []*flow.Flow) []string {
	out := make([]string, 0, len(flows))
	for _, f := range flows {
		out = append(out, f.ID)
	}
	return out
}

func TestStoreCapacityDefaults(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		got  int
		want int
	}{
		{"会话存储", newSessionStore(0).cap, 5000},
		{"会话存储负值", newSessionStore(-1).cap, 5000},
		{"会话存储显式容量", newSessionStore(7).cap, 7},
		{"WebSocket 存储", newWSStore(0).cap, 2000},
		{"流式存储", newStreamStore(0).cap, 2000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if tt.got != tt.want {
				t.Errorf("cap = %d, want %d", tt.got, tt.want)
			}
		})
	}
}

// TestSessionStorePutIsUpsert 同一条会话在请求/响应两个阶段各写一次,第二次不应再占环位置。
func TestSessionStorePutIsUpsert(t *testing.T) {
	t.Parallel()
	s := newSessionStore(10)
	s.put(newFlow("a"))
	s.put(newFlow("b"))
	s.put(newFlow("a", withResponse(200, "text/plain", []byte("done"))))

	list, total := s.list(1, 10)
	if total != 2 {
		t.Fatalf("总数 = %d, want 2", total)
	}
	if got := storeIDs(list); !slices.Equal(got, []string{"b", "a"}) {
		t.Fatalf("重复 put 不应改变入库顺序,实际 %v", got)
	}
	f, ok := s.get("a")
	if !ok || f.Response == nil {
		t.Fatal("重复 put 应覆盖为最新内容")
	}
}

// TestSessionStoreEvictsOldest 环满时淘汰最旧的,并把被淘汰的对象交给回调回收落盘副本。
func TestSessionStoreEvictsOldest(t *testing.T) {
	t.Parallel()
	s := newSessionStore(3)
	var evicted []string
	s.setOnEvict(func(f *flow.Flow) { evicted = append(evicted, f.ID) })

	for _, id := range []string{"a", "b", "c", "d", "e"} {
		s.put(newFlow(id))
	}

	list, total := s.list(1, 10)
	if total != 3 {
		t.Fatalf("总数 = %d, want 3", total)
	}
	if got := storeIDs(list); !slices.Equal(got, []string{"e", "d", "c"}) {
		t.Fatalf("留存会话 = %v, want [e d c]", got)
	}
	if !slices.Equal(evicted, []string{"a", "b"}) {
		t.Fatalf("淘汰回调 = %v, want [a b]", evicted)
	}
}

func TestSessionStoreSetCap(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		newCap    int
		wantIDs   []string
		wantEvict []string
	}{
		{"缩容立即淘汰最旧", 2, []string{"d", "c"}, []string{"a", "b"}},
		{"扩容不动已有记录", 10, []string{"d", "c", "b", "a"}, nil},
		{"零值忽略", 0, []string{"d", "c", "b", "a"}, nil},
		{"负值忽略", -5, []string{"d", "c", "b", "a"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := newSessionStore(4)
			var evicted []string
			s.setOnEvict(func(f *flow.Flow) { evicted = append(evicted, f.ID) })
			for _, id := range []string{"a", "b", "c", "d"} {
				s.put(newFlow(id))
			}

			s.setCap(tt.newCap)

			list, _ := s.list(1, 10)
			if got := storeIDs(list); !slices.Equal(got, tt.wantIDs) {
				t.Errorf("留存会话 = %v, want %v", got, tt.wantIDs)
			}
			if !slices.Equal(evicted, tt.wantEvict) {
				t.Errorf("淘汰回调 = %v, want %v", evicted, tt.wantEvict)
			}
		})
	}
}

func TestSessionStoreListPagination(t *testing.T) {
	t.Parallel()
	s := newSessionStore(10)
	for i := 1; i <= 5; i++ {
		s.put(newFlow(fmt.Sprintf("f%d", i)))
	}

	tests := []struct {
		name     string
		page     int
		pageSize int
		want     []string
	}{
		{"最新优先", 1, 3, []string{"f5", "f4", "f3"}},
		{"末页不足", 2, 3, []string{"f2", "f1"}},
		{"越界页", 3, 3, []string{}},
		{"远越界页", 1000, 3, []string{}},
		{"页码归一到 1", -1, 2, []string{"f5", "f4"}},
		{"页大小归一到默认值", 1, -1, []string{"f5", "f4", "f3", "f2", "f1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			list, total := s.list(tt.page, tt.pageSize)
			if total != 5 {
				t.Errorf("总数 = %d, want 5", total)
			}
			if got := storeIDs(list); !slices.Equal(got, tt.want) {
				t.Errorf("分页 = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestStoreListSurvivesOverflowingPage page/pageSize 来自 URL query(上游只校验 > 0),
// (page-1)*pageSize 溢出后会把切片下标带成负数。
func TestStoreListSurvivesOverflowingPage(t *testing.T) {
	t.Parallel()
	// 2^62+1:与 pageSize=2 相乘恰好回绕成 int64 最小值。
	const overflowPage = 4611686018427387905

	tests := []struct {
		name string
		list func(page, pageSize int) (int, int)
	}{
		{"HTTP 会话", func(page, pageSize int) (int, int) {
			s := newSessionStore(10)
			s.put(newFlow("a"))
			l, total := s.list(page, pageSize)
			return len(l), total
		}},
		{"WebSocket 会话", func(page, pageSize int) (int, int) {
			s := newWSStore(10)
			s.put(&flow.WSSession{ID: "a"})
			l, total := s.list(page, pageSize)
			return len(l), total
		}},
		{"流式会话", func(page, pageSize int) (int, int) {
			s := newStreamStore(10)
			s.put(&flow.StreamSession{ID: "a"})
			l, total := s.list(page, pageSize)
			return len(l), total
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			for _, c := range []struct {
				page, pageSize int
				want           int
			}{
				{overflowPage, 2, 0},           // 起点乘法回绕成 int64 最小值
				{overflowPage, math.MaxInt, 0}, // 起点与终点同时溢出
				{math.MaxInt, math.MaxInt, 0},  // 极大页码
				{200000000000000000, 100, 0},   // 回绕成一个很大的正数,仍是越界页
				{1, math.MaxInt, 1},            // 首页 + 极大页长:合法,应给出全部记录
			} {
				got, total := tt.list(c.page, c.pageSize)
				if got != c.want {
					t.Errorf("page=%d pageSize=%d 返回 %d 条, want %d", c.page, c.pageSize, got, c.want)
				}
				if total != 1 {
					t.Errorf("page=%d pageSize=%d 的总数 = %d, want 1", c.page, c.pageSize, total)
				}
			}
		})
	}
}

func TestSessionStoreDeleteAndClearNotifyEvict(t *testing.T) {
	t.Parallel()
	s := newSessionStore(10)
	var evicted []string
	s.setOnEvict(func(f *flow.Flow) { evicted = append(evicted, f.ID) })
	for _, id := range []string{"a", "b", "c"} {
		s.put(newFlow(id))
	}

	s.delete("missing") // 不存在的 ID 不应触发回收
	s.delete("b")
	if got := evicted; !slices.Equal(got, []string{"b"}) {
		t.Fatalf("删除回调 = %v, want [b]", got)
	}
	list, total := s.list(1, 10)
	if total != 2 {
		t.Fatalf("删除后总数 = %d, want 2", total)
	}
	if got := storeIDs(list); !slices.Equal(got, []string{"c", "a"}) {
		t.Fatalf("删除中间元素后顺序 = %v, want [c a]", got)
	}

	s.clear()
	slices.Sort(evicted)
	if !slices.Equal(evicted, []string{"a", "b", "c"}) {
		t.Fatalf("清空应回收全部剩余会话,实际 %v", evicted)
	}
	if _, total := s.list(1, 10); total != 0 {
		t.Fatalf("清空后总数 = %d, want 0", total)
	}
}

// TestSessionStoreConcurrentAccess 存储被代理热路径(put)与 UI 查询(list/get)并发访问,
// 在 -race 下压竞态;断言只要求不丢不炸,顺序由上面的确定性用例保证。
func TestSessionStoreConcurrentAccess(t *testing.T) {
	t.Parallel()
	s := newSessionStore(64)
	s.setOnEvict(func(*flow.Flow) {})

	const workers = 8
	const perWorker = 100
	var wg sync.WaitGroup
	for w := range workers {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := range perWorker {
				id := fmt.Sprintf("w%d-%d", w, i)
				s.put(newFlow(id))
				s.get(id)
				s.list(1, 10)
				if i%10 == 0 {
					s.delete(id)
				}
				if i%50 == 0 {
					s.setCap(32 + i)
				}
			}
		}(w)
	}
	wg.Wait()

	if _, total := s.list(1, 10); total > s.cap {
		t.Fatalf("并发写入后总数 %d 超过容量 %d", total, s.cap)
	}
}

func TestWSStoreEvictsOldestAndPaginates(t *testing.T) {
	t.Parallel()
	s := newWSStore(2)
	for _, id := range []string{"a", "b", "c"} {
		s.put(&flow.WSSession{ID: id, StartTime: time.Now()})
	}
	if _, ok := s.get("a"); ok {
		t.Error("超出容量后最旧的 WebSocket 会话应被淘汰")
	}

	list, total := s.list(1, 10)
	if total != 2 || len(list) != 2 || list[0].ID != "c" {
		t.Fatalf("分页应最新优先: total=%d list=%+v", total, list)
	}
	// 非法分页参数与 HTTP 会话一致地归一,而不是越界 panic。
	if got, _ := s.list(0, 0); len(got) != 2 {
		t.Errorf("非法分页参数应归一到默认值,得到 %d 条", len(got))
	}
	if got, _ := s.list(9, 1); len(got) != 0 {
		t.Errorf("越界页应返回空,得到 %d 条", len(got))
	}

	// 同 ID 复写(消息追加)不应再占环位置。
	s.put(&flow.WSSession{ID: "c", MessageCount: 3})
	if _, total := s.list(1, 10); total != 2 {
		t.Fatalf("复写后总数 = %d, want 2", total)
	}
	if ws, ok := s.get("c"); !ok || ws.MessageCount != 3 {
		t.Fatal("复写应覆盖为最新内容")
	}

	s.clear()
	if _, total := s.list(1, 10); total != 0 {
		t.Fatal("清空后不应还有 WebSocket 会话")
	}
}

func TestStreamStoreEvictsOldestAndPaginates(t *testing.T) {
	t.Parallel()
	s := newStreamStore(2)
	for _, id := range []string{"a", "b", "c"} {
		s.put(&flow.StreamSession{ID: id, Kind: flow.StreamSSE, StartTime: time.Now()})
	}
	if _, ok := s.get("a"); ok {
		t.Error("超出容量后最旧的流式会话应被淘汰")
	}

	list, total := s.list(1, 10)
	if total != 2 || len(list) != 2 || list[0].ID != "c" {
		t.Fatalf("分页应最新优先: total=%d list=%+v", total, list)
	}
	if got, _ := s.list(0, 0); len(got) != 2 {
		t.Errorf("非法分页参数应归一到默认值,得到 %d 条", len(got))
	}
	if got, _ := s.list(9, 1); len(got) != 0 {
		t.Errorf("越界页应返回空,得到 %d 条", len(got))
	}

	s.put(&flow.StreamSession{ID: "c", MessageCount: 5})
	if ss, ok := s.get("c"); !ok || ss.MessageCount != 5 {
		t.Fatal("复写应覆盖为最新内容")
	}

	s.clear()
	if _, total := s.list(1, 10); total != 0 {
		t.Fatal("清空后不应还有流式会话")
	}
}

func BenchmarkSessionStorePut(b *testing.B) {
	s := newSessionStore(5000)
	flows := make([]*flow.Flow, 1000)
	for i := range flows {
		flows[i] = newFlow(fmt.Sprintf("bench-%d", i))
	}
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		s.put(flows[i%len(flows)])
	}
}

func BenchmarkSessionStoreList(b *testing.B) {
	s := newSessionStore(5000)
	for i := range 5000 {
		s.put(newFlow(fmt.Sprintf("bench-%d", i)))
	}
	b.ReportAllocs()
	for b.Loop() {
		s.list(1, 50)
	}
}
