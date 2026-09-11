// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package app

import (
	"slices"
	"testing"

	gws "github.com/gorilla/websocket"

	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/internal/service"
)

func TestWebSocketPipelineOnConnection(t *testing.T) {
	for _, tt := range []struct {
		name                   string
		viaPipeline            bool
		wantSent, wantReceived []string
	}{
		{"启用插件", true, []string{"out:marker"}, []string{"in:marker"}},
		{"关闭插件", false, []string{"blocked", "marker"}, []string{"blocked", "marker"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			app := newComposeApp(t)
			app.Pipeline.Register(&composeDecisionHook{message: func(m *flow.WSMessage) flow.Decision {
				if string(m.Data) == "blocked" {
					return flow.AbortDecision(0, "frame-denied")
				}
				prefix := "in:"
				if m.Direction == flow.WSClientToServer {
					prefix = "out:"
				}
				m.Data = []byte(prefix + string(m.Data))
				return flow.ContinueDecision()
			}})
			srv := newWSTestServer(t, false)
			events, unsubscribe := app.Engine.Bus().Subscribe()
			t.Cleanup(unsubscribe)
			id, err := app.OpenWebSocket(flow.RequestSpec{URL: srv.url, ViaPipeline: tt.viaPipeline})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = app.CloseWebSocket(id) })
			// 同一连接上的帧有序；marker 到达后即可检查之前的阻断结果。
			for _, data := range []string{"blocked", "marker"} {
				if err := app.SendWSMessage(id, flow.WSText, data); err != nil {
					t.Fatal(err)
				}
			}
			for _, want := range tt.wantSent {
				got := srv.nextFrame(t, srv.received, "客户端消息")
				if got.mt != gws.TextMessage || string(got.data) != want {
					t.Fatalf("服务端收到 %d %q，期望 %q", got.mt, got.data, want)
				}
			}
			for _, data := range []string{"blocked", "marker"} {
				srv.push(t, gws.TextMessage, []byte(data))
			}
			last := tt.wantReceived[len(tt.wantReceived)-1]
			session := waitWSSession(t, app, events, id, "服务端 marker", func(s service.WSSessionDTOType) bool {
				return slices.ContainsFunc(s.Messages, func(m service.WSMessageDTO) bool {
					return m.Direction == "inbound" && m.Data == last
				})
			})
			var sent, received []string
			for _, m := range session.Messages {
				if m.Direction == "outbound" {
					sent = append(sent, m.Data)
				} else {
					received = append(received, m.Data)
				}
			}
			if !slices.Equal(sent, tt.wantSent) || !slices.Equal(received, tt.wantReceived) {
				t.Fatalf("会话消息 sent=%q received=%q，期望 sent=%q received=%q", sent, received, tt.wantSent, tt.wantReceived)
			}
		})
	}
}
