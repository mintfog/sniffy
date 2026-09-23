// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package app

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/internal/pipeline"
)

type composeDecisionHook struct {
	request  func(*flow.Flow) flow.Decision
	response func(*flow.Flow) flow.Decision
	message  func(*flow.WSMessage) flow.Decision
}

func (*composeDecisionHook) Name() string      { return "测试处置钩子" }
func (*composeDecisionHook) Priority() int     { return 0 }
func (*composeDecisionHook) Enabled() bool     { return true }
func (*composeDecisionHook) Match(string) bool { return true }
func (h *composeDecisionHook) OnRequest(_ context.Context, f *flow.Flow) flow.Decision {
	if h.request != nil {
		return h.request(f)
	}
	return flow.ContinueDecision()
}
func (h *composeDecisionHook) OnResponse(_ context.Context, f *flow.Flow) flow.Decision {
	if h.response != nil {
		return h.response(f)
	}
	return flow.ContinueDecision()
}
func (h *composeDecisionHook) OnWebSocketMessage(_ context.Context, m *flow.WSMessage) flow.Decision {
	if h.message != nil {
		return h.message(m)
	}
	return flow.ContinueDecision()
}

type composeRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn composeRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }

type composeFailingReader struct{}

func (composeFailingReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestComposePipelineTerminalStates(t *testing.T) {
	for _, sse := range []bool{false, true} {
		name := "HTTP"
		if sse {
			name = "SSE"
		}
		t.Run(name, func(t *testing.T) {
			for _, tt := range []struct {
				name, url                string
				request, response        flow.Decision
				transportErr             error
				truncated                bool
				wantState                flow.FlowState
				wantError, wantBody      string
				wantStatus               int
				wantCalls, wantResponses int
			}{
				{
					name: "请求阻断", request: flow.AbortDecision(403, "request-denied"),
					wantState: flow.StateBlocked, wantError: "request-denied",
				},
				{
					name: "请求模拟", request: flow.MockDecision("mock"),
					wantState: flow.StateMocked, wantStatus: 201, wantBody: "mock-body", wantResponses: 1,
				},
				{
					name: "请求格式错误", url: "http://%invalid",
					wantState: flow.StateErrored, wantError: "invalid",
				},
				{
					name: "上游连接失败", transportErr: errors.New("dial-sentinel"),
					wantState: flow.StateErrored, wantError: "dial-sentinel", wantCalls: 1,
				},
				{
					name: "响应读取失败", truncated: true,
					wantState: flow.StateErrored, wantError: "不完整", wantCalls: 1, wantResponses: 1,
				},
				{
					name: "响应阻断", response: flow.AbortDecision(403, "response-denied"),
					wantState: flow.StateBlocked, wantError: "response-denied", wantCalls: 1, wantResponses: 1,
				},
				{
					name: "响应放行", wantState: flow.StateCompleted,
					wantStatus: 200, wantBody: "upstream-body", wantCalls: 1, wantResponses: 1,
				},
			} {
				t.Run(tt.name, func(t *testing.T) {
					a := newComposeApp(t)
					calls, responses := 0, 0
					a.Pipeline.Register(&composeDecisionHook{
						request: func(f *flow.Flow) flow.Decision {
							if tt.request.Kind == flow.Mock {
								f.Response = &flow.Response{Status: 201, Body: []byte("mock-body")}
							}
							return tt.request
						},
						response: func(*flow.Flow) flow.Decision {
							responses++
							return tt.response
						},
					})
					transport := composeRoundTripFunc(func(r *http.Request) (*http.Response, error) {
						calls++
						if tt.transportErr != nil {
							return nil, tt.transportErr
						}
						var body io.Reader = strings.NewReader("upstream-body")
						if tt.truncated {
							body = io.MultiReader(strings.NewReader("partial"), composeFailingReader{})
						}
						return &http.Response{
							StatusCode: 200,
							Status:     "200 OK",
							Header:     http.Header{"Content-Type": {"text/plain"}},
							Body:       io.NopCloser(body),
							Request:    r,
						}, nil
					})
					a.Engine.UpstreamClient().Transport = transport
					a.Engine.StreamUpstreamClient().Transport = transport
					f := flow.New("http")
					f.Request = &flow.Request{Method: "GET", URL: "http://example.test/path"}
					if tt.url != "" {
						f.Request.URL = tt.url
					}
					if sse {
						ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
						defer cancel()
						a.runComposeRequest(ctx, cancel, f, true)
					} else {
						a.runResend(f, true)
					}
					if calls != tt.wantCalls || responses != tt.wantResponses {
						t.Fatalf("上游调用=%d，响应钩子=%d，期望 %d/%d", calls, responses, tt.wantCalls, tt.wantResponses)
					}
					stored, ok := a.Service.RawFlow(f.ID)
					if !ok {
						t.Fatal("终态未提交到服务层")
					}
					if stored.State != tt.wantState {
						t.Fatalf("终态 = %q，期望 %q", stored.State, tt.wantState)
					}
					if tt.wantError == "" {
						if stored.Error != "" {
							t.Fatalf("成功请求带有错误: %q", stored.Error)
						}
					} else if !strings.Contains(stored.Error, tt.wantError) {
						t.Fatalf("错误 = %q，期望包含 %q", stored.Error, tt.wantError)
					}
					if stored.Timing.CompletedAt.IsZero() {
						t.Fatal("终态缺少完成时间")
					}
					if tt.wantStatus != 0 {
						if stored.Response == nil {
							t.Fatal("响应未提交到服务层")
						}
						if stored.Response.Status != tt.wantStatus || string(stored.Response.Body) != tt.wantBody {
							t.Fatalf("响应 = %d %q，期望 %d %q", stored.Response.Status, stored.Response.Body, tt.wantStatus, tt.wantBody)
						}
					}
				})
			}
		})
	}
}

func TestComposeWSPipelinePreservesMessageContract(t *testing.T) {
	t.Parallel()
	for _, direction := range []string{flow.WSClientToServer, flow.WSServerToClient} {
		for _, abort := range []bool{false, true} {
			pipe := pipeline.New(nil, nil)
			calls := 0
			pipe.Register(&composeDecisionHook{message: func(m *flow.WSMessage) flow.Decision {
				calls++
				if m.FlowID != "session" || m.URL != "ws://example.test/socket" || m.Direction != direction || m.Type != flow.WSBinary || m.ID == "" || m.Timestamp.IsZero() {
					t.Errorf("消息上下文不完整: %+v", m)
				}
				m.Data[0] = 'X'
				if abort {
					return flow.AbortDecision(0, "frame-denied")
				}
				return flow.ContinueDecision()
			}})
			c := &composeWSConn{pipe: pipe, session: &flow.WSSession{ID: "session", URL: "ws://example.test/socket"}}
			input := []byte("original")
			got, allowed := c.applyPipeline(direction, flow.WSBinary, input)
			if calls != 1 || allowed == abort {
				t.Fatalf("消息处置错误: calls=%d, allowed=%v, abort=%v", calls, allowed, abort)
			}
			if string(input) != "original" {
				t.Fatal("插件修改污染了原始载荷")
			}
			if abort && got != nil {
				t.Fatal("阻断消息仍返回了载荷")
			}
			if !abort && string(got) != "Xriginal" {
				t.Fatalf("改写结果未返回: %q", got)
			}
		}
	}
}
