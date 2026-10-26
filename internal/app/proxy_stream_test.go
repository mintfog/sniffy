// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package app

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/mintfog/sniffy/internal/core"
	"github.com/mintfog/sniffy/internal/service"
)

func waitProxyResponseHeaders(t *testing.T, events <-chan core.Event) service.HTTPSessionDTO {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case event := <-events:
			dto, ok := event.Payload.(service.HTTPSessionDTO)
			if event.Type == core.EventFlowUpdated && ok && dto.Response != nil {
				return dto
			}
		case <-deadline:
			t.Fatal("等待代理响应头快照超时")
			return service.HTTPSessionDTO{}
		}
	}
}

func TestProxyStreamPublishesResponseHeaders(t *testing.T) {
	for _, tc := range []struct {
		name        string
		contentType string
		wire        string
		version     string
		grpcRequest bool
	}{
		{"SSE_HTTP1", "text/event-stream", "data: one\n\n", "HTTP/1.1", false},
		{"SSE_HTTP2", "text/event-stream", "data: one\n\n", "HTTP/2.0", false},
		{"NDJSON_HTTP1", "application/x-ndjson", "{\"data\":\"one\"}\n", "HTTP/1.1", false},
		{"gRPC响应_HTTP1", "application/grpc", "\x00\x00\x00\x00\x03one", "HTTP/1.1", false},
		{"gRPC请求_HTTP2", "application/grpc", "\x00\x00\x00\x00\x03one", "HTTP/2.0", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !appScenarioProcess(t) {
				return
			}
			firstMessage, finished := make(chan struct{}), make(chan struct{})
			emit := sync.OnceFunc(func() { close(firstMessage) })
			finish := sync.OnceFunc(func() { close(finished) })
			origin := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.grpcRequest {
					_, _ = io.Copy(io.Discard, r.Body)
				}
				w.Header().Set("Content-Type", tc.contentType)
				w.WriteHeader(http.StatusOK)
				w.(http.Flusher).Flush()
				select {
				case <-firstMessage:
				case <-r.Context().Done():
					return
				}
				_, _ = io.WriteString(w, tc.wire)
				w.(http.Flusher).Flush()
				select {
				case <-finished:
				case <-r.Context().Done():
				}
			}))
			h2 := tc.version == "HTTP/2.0"
			origin.EnableHTTP2 = h2
			if h2 {
				origin.StartTLS()
			} else {
				origin.Start()
			}
			t.Cleanup(origin.Close)
			t.Cleanup(finish)
			t.Cleanup(emit)

			a := buildRunningApp(t)
			a.Service.UpdateConfig(map[string]any{
				"enableHTTPS":      true,
				"tlsInsecureHosts": []string{"127.0.0.1"},
			})
			client := proxyTestClient(t, a)
			client.Transport.(*http.Transport).ForceAttemptHTTP2 = h2
			events, unsubscribe := a.Engine.Bus().Subscribe()
			t.Cleanup(unsubscribe)
			method := http.MethodGet
			var body io.Reader
			if tc.grpcRequest {
				method, body = http.MethodPost, bytes.NewBufferString(tc.wire)
			}
			request, err := http.NewRequest(method, origin.URL+"/events", body)
			if err != nil {
				t.Fatal(err)
			}
			if tc.grpcRequest {
				request.Header.Set("Content-Type", "application/grpc")
			}
			response, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = response.Body.Close() })
			if response.Proto != tc.version {
				t.Fatalf("客户端响应版本 = %q，期望 %q", response.Proto, tc.version)
			}

			headers := waitProxyResponseHeaders(t, events)
			if headers.Status != "pending" || headers.Response.HTTPVersion != tc.version || headers.Response.Status != http.StatusOK {
				t.Fatalf("进行中的响应头快照 = %+v，响应 = %+v", headers, headers.Response)
			}
			if headers.Response.Headers["Content-Type"] != tc.contentType {
				t.Fatalf("响应头 = %v，期望 Content-Type %q", headers.Response.Headers, tc.contentType)
			}
			if got := a.Service.Statistics().TotalRequests; got != 0 {
				t.Fatalf("流尚未结束时已结算 %d 次请求", got)
			}
			emit()
			first := make([]byte, len(tc.wire))
			if _, err := io.ReadFull(response.Body, first); err != nil {
				t.Fatal(err)
			}
			if string(first) != tc.wire {
				t.Fatalf("首条消息 = %q，期望 %q", first, tc.wire)
			}
			waitStreamSession(t, a, events, headers.ID, "流消息", func(dto service.StreamSessionDTOType) bool {
				return dto.Status == "open" && dto.StatusCode == http.StatusOK && dto.MessageCount > 0
			})
			finish()
			if _, err := io.Copy(io.Discard, response.Body); err != nil {
				t.Fatal(err)
			}
			final := waitFlowSettled(t, events, headers.ID)
			if final.Status != "completed" || final.Response == nil || final.Response.HTTPVersion != tc.version {
				t.Fatalf("流结束快照 = %+v", final)
			}
			if got := a.Service.Statistics().TotalRequests; got != 1 {
				t.Fatalf("流结束后结算了 %d 次请求，期望 1 次", got)
			}
		})
	}
}
