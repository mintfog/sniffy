// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package service

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"testing"

	"github.com/mintfog/sniffy/ca"
	"github.com/mintfog/sniffy/internal/bodycache"
	"github.com/mintfog/sniffy/internal/core"
	"github.com/mintfog/sniffy/internal/flow"
)

// 本文件集中放 service 包各测试共用的构造器:测试只声明自己关心的字段,其余取稳定缺省值。

const (
	fixtureURL  = "https://api.example.com/v1/ping"
	fixtureHost = "api.example.com"
	fixturePath = "/v1/ping"
)

// newTestService 造一个纯内存 Service(真实 CA + 事件总线,不落盘)。
func newTestService(t *testing.T) *Service {
	t.Helper()
	c, err := ca.NewInMemorySelfSignedCA()
	if err != nil {
		t.Fatalf("创建 CA 失败: %v", err)
	}
	return New(c, core.NewEventBus(), "", "")
}

// newSpillService 造一个接了响应体落盘缓存的 Service,缓存目录随测试自动清理。
func newSpillService(t *testing.T) (*Service, *bodycache.Cache) {
	t.Helper()
	svc := New(nil, nil, "", "")
	c, err := bodycache.New(t.TempDir(), 0)
	if err != nil {
		t.Fatalf("bodycache.New: %v", err)
	}
	svc.SetBodyCache(c)
	return svc, c
}

// flowOpt 定制 newFlow 造出的 Flow,按传入顺序生效(后者可覆盖前者)。
type flowOpt func(*flow.Flow)

// newFlow 造一条测试用 Flow:默认 GET fixtureURL、pending 态、无响应。
func newFlow(id string, opts ...flowOpt) *flow.Flow {
	f := flow.New(flow.ProtoHTTPS)
	f.ID = id
	f.Request = &flow.Request{
		Method: http.MethodGet,
		URL:    fixtureURL,
		Host:   fixtureHost,
		Path:   fixturePath,
		Header: map[string][]string{},
	}
	for _, opt := range opts {
		opt(f)
	}
	return f
}

// withRequest 改写请求行,Host / Path 从 URL 解析。
func withRequest(method, rawURL string) flowOpt {
	return func(f *flow.Flow) {
		f.Request.Method = method
		f.Request.URL = rawURL
		if u, err := url.Parse(rawURL); err == nil {
			f.Request.Host = u.Host
			f.Request.Path = u.Path
		}
	}
}

func withRequestHeader(key string, values ...string) flowOpt {
	return func(f *flow.Flow) { f.Request.Header[key] = values }
}

func withRequestBody(body []byte) flowOpt {
	return func(f *flow.Flow) { f.Request.Body = body }
}

// withResponse 挂上内存响应体,并把状态置为 completed(需要别的状态时随后再加 withState)。
func withResponse(status int, mime string, body []byte) flowOpt {
	return func(f *flow.Flow) {
		header := map[string][]string{}
		if mime != "" {
			header["Content-Type"] = []string{mime}
		}
		f.Response = &flow.Response{Status: status, Header: header, Body: body}
		f.State = flow.StateCompleted
	}
}

func withResponseHeader(key string, values ...string) flowOpt {
	return func(f *flow.Flow) {
		if f.Response == nil {
			f.Response = &flow.Response{Status: http.StatusOK, Header: map[string][]string{}}
		}
		f.Response.Header[key] = values
	}
}

func withState(state flow.FlowState) flowOpt {
	return func(f *flow.Flow) { f.State = state }
}

func withDuration(ms int64) flowOpt {
	return func(f *flow.Flow) { f.Timing.DurationMs = ms }
}

func withProcess(p *flow.ProcessInfo) flowOpt {
	return func(f *flow.Flow) { f.SetProcess(p) }
}

func withError(msg string) flowOpt {
	return func(f *flow.Flow) {
		f.Error = msg
		f.State = flow.StateErrored
	}
}

func withModified() flowOpt {
	return func(f *flow.Flow) { f.Modified = true }
}

// spilledFlow 造一条「体已落盘」的 Flow:Body 为空,大小与路径由透传旁路记录。
func spilledFlow(t *testing.T, c *bodycache.Cache, id string, data []byte) *flow.Flow {
	t.Helper()
	e := c.Create(id)
	if _, err := e.Write(data); err != nil {
		t.Fatalf("写副本失败: %v", err)
	}
	path, size := e.Commit()
	if path == "" {
		t.Fatal("副本未提交")
	}
	f := newFlow(id,
		withRequest(http.MethodGet, "https://media.example.com/v.mp4"),
		withResponse(http.StatusOK, "video/mp4", nil),
	)
	f.Response.SetPassthroughBody(path, size)
	return f
}

// eventRecorder 订阅事件总线并收集事件。EventBus.Emit 为同步投递,断言前无需等待。
type eventRecorder struct {
	ch <-chan core.Event
}

func newEventRecorder(t *testing.T, bus *core.EventBus) *eventRecorder {
	t.Helper()
	ch, cancel := bus.Subscribe()
	t.Cleanup(cancel)
	return &eventRecorder{ch: ch}
}

// drain 取出当前已入队的全部事件。
func (r *eventRecorder) drain() []core.Event {
	var out []core.Event
	for {
		select {
		case e := <-r.ch:
			out = append(out, e)
		default:
			return out
		}
	}
}

// types 取出当前已入队事件的类型序列,便于直接比对广播顺序。
func (r *eventRecorder) types() []core.EventType {
	events := r.drain()
	out := make([]core.EventType, 0, len(events))
	for _, e := range events {
		out = append(out, e.Type)
	}
	return out
}

// elide 压短过长的展示串再打进失败信息(预览截断类用例的期望值有 1MB)。
func elide(s string) string {
	const keep = 48
	if len(s) <= keep {
		return strconv.Quote(s)
	}
	return fmt.Sprintf("%s…(共 %d 字节)", strconv.Quote(s[:keep]), len(s))
}

// sessionIDs 把分页结果压成 ID 列表,便于比对顺序。
func sessionIDs(list []HTTPSessionDTO) []string {
	out := make([]string, 0, len(list))
	for _, s := range list {
		out = append(out, s.ID)
	}
	return out
}
