// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package websocket

import (
	"sync"
	"time"

	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/internal/pipeline"
	"github.com/mintfog/sniffy/internal/procinfo"
)

// 与 HTTP 处理器一致,WebSocket 也走 flow + pipeline。
// 这些包级变量由上层(internal/core.Engine)经 http 处理器的 setter 下发。

var (
	activePipeline  *pipeline.Pipeline
	wsSink          WSSink
	processResolver *procinfo.Resolver
)

// WSSink 由 service 实现,处理器经其记录/更新 WebSocket 会话。
// added 是本次新增的消息,仅元数据变化(建会话 / 补进程 / 关闭)时为 nil。
type WSSink interface {
	RecordWSSession(ws *flow.WSSession, added *flow.WSMessage)
}

// SetPipeline 注入插件管道(承载 OnWebSocketMessage)。
func SetPipeline(p *pipeline.Pipeline) { activePipeline = p }

// SetWSSink 注入 WebSocket 会话接收器。
func SetWSSink(s WSSink) { wsSink = s }

// SetProcessResolver 注入进程解析器。
func SetProcessResolver(r *procinfo.Resolver) { processResolver = r }

// wsRecorder 维护一条 WebSocket 会话并在每次变化时向 sink 推送快照。
// 两个方向的转发 goroutine 共享同一 recorder,故所有访问以 mu 串行化,
// 且向 sink 传出的是深拷贝快照,避免与 sink/序列化侧产生数据竞争。
type wsRecorder struct {
	mu      sync.Mutex
	session *flow.WSSession
}

// newWSRecorder 创建并登记一条处于 open 状态的会话。sink 未注入时返回 nil。
func newWSRecorder(url string) *wsRecorder {
	if wsSink == nil {
		return nil
	}
	r := &wsRecorder{
		session: &flow.WSSession{
			ID:        flow.NewID(),
			URL:       url,
			Status:    "open",
			StartTime: time.Now(),
			Messages:  make([]flow.WSMessage, 0, 16),
		},
	}
	r.push()
	return r
}

// id 返回会话 ID(供构造 WSMessage 时引用)。
func (r *wsRecorder) id() string {
	if r == nil {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.session.ID
}

// record 追加一条消息并推送更新。
func (r *wsRecorder) record(direction, msgType string, data []byte) {
	if r == nil {
		return
	}
	r.mu.Lock()
	s := r.session
	payload, size := flow.RetainPayload(data)
	s.MessageCount++
	s.TotalSize += size
	m := flow.WSMessage{
		ID:        flow.NewID(),
		FlowID:    s.ID,
		URL:       s.URL,
		Direction: direction,
		Type:      msgType,
		Data:      payload,
		Timestamp: time.Now(),
		Size:      size,
	}
	s.Messages = flow.TrimWSMessages(append(s.Messages, m))
	snap := r.snapshotLocked()
	r.mu.Unlock()
	wsSink.RecordWSSession(snap, &m)
}

// setProcess 挂上异步解析出的进程信息并推送更新。
func (r *wsRecorder) setProcess(p *flow.ProcessInfo) {
	if r == nil || p == nil {
		return
	}
	r.mu.Lock()
	r.session.Process = p
	snap := r.snapshotLocked()
	r.mu.Unlock()
	wsSink.RecordWSSession(snap, nil)
}

// close 标记会话关闭并推送最终状态。
func (r *wsRecorder) close() {
	if r == nil {
		return
	}
	r.mu.Lock()
	now := time.Now()
	r.session.EndTime = &now
	r.session.Status = "closed"
	snap := r.snapshotLocked()
	r.mu.Unlock()
	wsSink.RecordWSSession(snap, nil)
}

func (r *wsRecorder) push() {
	if r == nil || wsSink == nil {
		return
	}
	r.mu.Lock()
	snap := r.snapshotLocked()
	r.mu.Unlock()
	wsSink.RecordWSSession(snap, nil)
}

// snapshotLocked 在持有 mu 时构造会话的深拷贝。
func (r *wsRecorder) snapshotLocked() *flow.WSSession {
	s := r.session
	cp := *s
	cp.Messages = make([]flow.WSMessage, len(s.Messages))
	copy(cp.Messages, s.Messages)
	if s.Process != nil {
		p := *s.Process
		cp.Process = &p
	}
	if s.EndTime != nil {
		t := *s.EndTime
		cp.EndTime = &t
	}
	return &cp
}
