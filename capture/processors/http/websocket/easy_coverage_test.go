// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0

package websocket

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/internal/pipeline"
)

type recordingWSSink struct {
	mu      sync.Mutex
	session *flow.WSSession
}

type testWSHook struct {
	decision flow.Decision
	rewrite  []byte
}

func (*testWSHook) Name() string      { return "test-ws-hook" }
func (*testWSHook) Priority() int     { return 0 }
func (*testWSHook) Enabled() bool     { return true }
func (*testWSHook) Match(string) bool { return true }
func (h *testWSHook) OnWebSocketMessage(_ context.Context, message *flow.WSMessage) flow.Decision {
	if h.rewrite != nil {
		message.Data = append([]byte(nil), h.rewrite...)
	}
	return h.decision
}

type stagedErrorWriter struct {
	calls  int
	failAt int
	err    error
}

func (w *stagedErrorWriter) Write(p []byte) (int, error) {
	call := w.calls
	w.calls++
	if call == w.failAt {
		return 0, w.err
	}
	return len(p), nil
}

func (s *recordingWSSink) RecordWSSession(session *flow.WSSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.session = session
}

func (s *recordingWSSink) last() *flow.WSSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.session
}

func TestWSRecorderLifecycleAndSnapshotLimit(t *testing.T) {
	prevPipeline, prevSink, prevResolver := activePipeline, wsSink, processResolver
	t.Cleanup(func() {
		activePipeline, wsSink, processResolver = prevPipeline, prevSink, prevResolver
	})

	p := pipeline.New(nil, nil)
	SetPipeline(p)
	if activePipeline != p {
		t.Fatal("SetPipeline did not retain the pipeline")
	}
	SetProcessResolver(nil)
	if processResolver != nil {
		t.Fatal("SetProcessResolver(nil) did not clear the resolver")
	}

	SetWSSink(nil)
	if newWSRecorder("ws://example.test") != nil {
		t.Fatal("newWSRecorder should return nil without a sink")
	}
	var nilRecorder *wsRecorder
	if nilRecorder.id() != "" {
		t.Fatal("nil recorder ID should be empty")
	}
	nilRecorder.record("direction", "type", nil)
	nilRecorder.setProcess(nil)
	nilRecorder.close()
	nilRecorder.push()

	sink := &recordingWSSink{}
	SetWSSink(sink)
	recorder := newWSRecorder("ws://example.test/socket")
	if recorder == nil || recorder.id() == "" {
		t.Fatal("newWSRecorder did not create an open session")
	}
	payload := []byte("first")
	recorder.record(flow.WSClientToServer, flow.WSText, payload)
	payload[0] = 'X'
	if got := string(sink.last().Messages[0].Data); got != "first" {
		t.Fatalf("recorded data aliases caller buffer: %q", got)
	}
	for i := 0; i < maxWSMessages; i++ {
		recorder.record(flow.WSServerToClient, flow.WSBinary, []byte{byte(i)})
	}

	process := &flow.ProcessInfo{PID: 42, Name: "test-process"}
	recorder.setProcess(process)
	process.Name = "mutated"
	if got := sink.last().Process.Name; got != "test-process" {
		t.Fatalf("process snapshot aliases caller: %q", got)
	}
	recorder.close()
	last := sink.last()
	if last.Status != "closed" || last.EndTime == nil {
		t.Fatalf("closed session = %+v", last)
	}
	if last.MessageCount != maxWSMessages+1 || len(last.Messages) != maxWSMessages {
		t.Fatalf("message totals = count %d retained %d", last.MessageCount, len(last.Messages))
	}
}

func TestHandshakeHelpers(t *testing.T) {
	for _, tt := range []struct {
		host string
		want string
	}{
		{"example.test:443", "example.test"},
		{"example.test", "example.test"},
		{"[::1]:8443", "::1"},
	} {
		if got := hostnameOnly(tt.host); got != tt.want {
			t.Errorf("hostnameOnly(%q) = %q, want %q", tt.host, got, tt.want)
		}
	}

	req, _ := http.NewRequest(http.MethodGet, "http://example.test/socket?q=1", nil)
	req.Host = "example.test"
	req.Header.Set("Upgrade", "websocket")
	var out bytes.Buffer
	if err := writeFaithfulHandshake(&out, req); err != nil {
		t.Fatalf("writeFaithfulHandshake: %v", err)
	}
	written := out.String()
	for _, fragment := range []string{"GET /socket?q=1 HTTP/1.1", "Host: example.test", "Upgrade: websocket"} {
		if !strings.Contains(written, fragment) {
			t.Errorf("handshake missing %q: %q", fragment, written)
		}
	}

	if status := parseStatusCode("invalid"); status != 0 {
		t.Fatalf("invalid status code = %d", status)
	}
	if got := wsHostPort("example.test", true); got != "example.test:443" {
		t.Fatalf("secure wsHostPort = %q", got)
	}
	if got := wsHostPort("example.test:8080", false); got != "example.test:8080" {
		t.Fatalf("explicit wsHostPort = %q", got)
	}

	if _, _, err := readHandshakeResponse(bufio.NewReader(strings.NewReader(""))); err == nil {
		t.Fatal("empty handshake response should fail")
	}
	large := strings.Repeat("X", 64*1024) + "\r\n"
	if _, _, err := readHandshakeResponse(bufio.NewReader(strings.NewReader(large))); err == nil || !strings.Contains(err.Error(), "过大") {
		t.Fatalf("oversized handshake error = %v", err)
	}
}

func TestHandleFrameDataPipeline(t *testing.T) {
	previous := activePipeline
	t.Cleanup(func() { activePipeline = previous })
	server := newMockServer()
	conn := newMockConnection(newMockConn(""), server)
	request, _ := http.NewRequest(http.MethodGet, "http://example.test/ws", nil)
	processor := New(conn, request, false)
	processor.targetURL = "ws://example.test/ws"

	rewritePipeline := pipeline.New(nil, nil)
	rewritePipeline.Register(&testWSHook{decision: flow.ContinueDecision(), rewrite: []byte("rewritten")})
	activePipeline = rewritePipeline
	data, drop := processor.handleFrameData([]byte("original"), flow.WSClientToServer, server)
	if drop || string(data) != "rewritten" {
		t.Fatalf("pipeline rewrite = (%q, drop=%v)", data, drop)
	}

	abortPipeline := pipeline.New(nil, nil)
	abortPipeline.Register(&testWSHook{decision: flow.AbortDecision(0, "blocked")})
	activePipeline = abortPipeline
	if data, drop := processor.handleFrameData([]byte("blocked"), flow.WSServerToClient, server); !drop || data != nil {
		t.Fatalf("pipeline abort = (%q, drop=%v)", data, drop)
	}

}

func TestFrameReadAndWriteErrors(t *testing.T) {
	oversized := []byte{0x82, 127}
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], maxFramePayload+1)
	oversized = append(oversized, size[:]...)

	inputs := [][]byte{
		nil,
		{0x81, 126, 0x00},
		{0x82, 127, 0x00},
		oversized,
		{0x81, 0x80},
		{0x81, 0x02, 'x'},
	}
	for i, input := range inputs {
		if _, err := readFrame(bytes.NewReader(input)); err == nil {
			t.Errorf("readFrame error case %d succeeded", i)
		}
	}

	wantErr := errors.New("write failed")
	tests := []struct {
		name   string
		masked bool
		failAt int
	}{
		{name: "unmasked header", masked: false, failAt: 0},
		{name: "unmasked payload", masked: false, failAt: 1},
		{name: "masked header", masked: true, failAt: 0},
		{name: "masked payload", masked: true, failAt: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writer := &stagedErrorWriter{failAt: tt.failAt, err: wantErr}
			err := writeFrame(writer, wsFrame{fin: true, opcode: opText, payload: []byte("data")}, tt.masked)
			if !errors.Is(err, wantErr) {
				t.Fatalf("writeFrame error = %v, want %v", err, wantErr)
			}
		})
	}

	if err := writeFrame(io.Discard, wsFrame{fin: true, opcode: opPing}, false); err != nil {
		t.Fatalf("empty unmasked frame: %v", err)
	}
	if err := writeFrame(io.Discard, wsFrame{fin: true, opcode: opPing}, true); err != nil {
		t.Fatalf("empty masked frame: %v", err)
	}
}

func TestProcessorFailureWritesBadGateway(t *testing.T) {
	server := newMockServer()
	raw := newMockConn("")
	conn := newMockConnection(raw, server)
	request, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:0/ws", nil)
	request.Host = "127.0.0.1:0"
	processor := New(conn, request, false)

	if err := processor.Process(server); err != nil {
		t.Fatalf("failed upstream should be converted to an HTTP response: %v", err)
	}
	if !strings.Contains(raw.WrittenData(), "502 Bad Gateway") {
		t.Fatalf("delegated failure response = %q", raw.WrittenData())
	}
}
