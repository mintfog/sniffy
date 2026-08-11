// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0

package http

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	wsprocessor "github.com/mintfog/sniffy/capture/processors/http/websocket"
	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/internal/pipeline"
)

type flowDecisionHook struct {
	onRequest  func(*flow.Flow) flow.Decision
	onResponse func(*flow.Flow) flow.Decision
}

func (*flowDecisionHook) Name() string      { return "flow-decision" }
func (*flowDecisionHook) Priority() int     { return 0 }
func (*flowDecisionHook) Enabled() bool     { return true }
func (*flowDecisionHook) Match(string) bool { return true }
func (h *flowDecisionHook) OnRequest(_ context.Context, f *flow.Flow) flow.Decision {
	if h.onRequest != nil {
		return h.onRequest(f)
	}
	return flow.ContinueDecision()
}
func (h *flowDecisionHook) OnResponse(_ context.Context, f *flow.Flow) flow.Decision {
	if h.onResponse != nil {
		return h.onResponse(f)
	}
	return flow.ContinueDecision()
}

type branchResponder struct {
	stream          streamWriter
	streamOK        bool
	body            bodyStreamer
	bodyOK          bool
	written         *flow.Flow
	aborted         *flow.Decision
	badGatewayCalls int
	reuseDisabled   bool
}

func (r *branchResponder) writeFlowResponse(f *flow.Flow, _ *http.Request) error {
	r.written = f
	return nil
}
func (r *branchResponder) writeAbort(d flow.Decision) error {
	r.aborted = &d
	return nil
}
func (r *branchResponder) writeBadGateway() error {
	r.badGatewayCalls++
	return nil
}
func (r *branchResponder) streamWriter() (streamWriter, bool) { return r.stream, r.streamOK }
func (r *branchResponder) bodyStreamer() (bodyStreamer, bool) { return r.body, r.bodyOK }
func (r *branchResponder) disableReuse()                      { r.reuseDisabled = true }

type failingStreamWriter struct {
	headErr  error
	chunkErr error
}

func (w *failingStreamWriter) writeHead(string, int, http.Header, [][2]string) error {
	return w.headErr
}
func (w *failingStreamWriter) writeChunk([]byte) error { return w.chunkErr }
func (*failingStreamWriter) setTrailer(http.Header)    {}
func (*failingStreamWriter) close() error              { return nil }

type branchBodyStreamer struct {
	headErr       error
	writeErr      error
	closeErr      error
	written       bytes.Buffer
	trailer       http.Header
	writeHeadCall bool
}

func (w *branchBodyStreamer) writeHead(string, int, http.Header, [][2]string, int64) error {
	w.writeHeadCall = true
	return w.headErr
}
func (w *branchBodyStreamer) Write(p []byte) (int, error) {
	if w.writeErr != nil {
		return 0, w.writeErr
	}
	return w.written.Write(p)
}
func (w *branchBodyStreamer) setTrailer(h http.Header) { w.trailer = h.Clone() }
func (w *branchBodyStreamer) close() error             { return w.closeErr }

type errorResponseWriter struct {
	header http.Header
	err    error
}

func (w *errorResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}
func (w *errorResponseWriter) Write([]byte) (int, error) { return 0, w.err }
func (*errorResponseWriter) WriteHeader(int)             {}

type errorWriter struct{ err error }

func (w errorWriter) Write([]byte) (int, error) { return 0, w.err }

// preserveHTTPGlobals 保存并在用例结束时恢复本包的全局注入点。
// SetPipeline / SetFlowSink / SetProcessResolver 会一并下发给 websocket 子包,
// 恢复必须走同一组 setter,否则子包会留着上一个用例的状态。
func preserveHTTPGlobals(t *testing.T) {
	t.Helper()
	previousPipeline, previousFlowSink, previousResolver := activePipeline, flowSink, processResolver
	previousClient, previousStreamClient, previousStreamSink := sharedHttpClient, sharedStreamClient, streamSink
	previousCache := bodyCache.Load()
	t.Cleanup(func() {
		SetPipeline(previousPipeline)
		SetProcessResolver(previousResolver)
		// SetFlowSink 只在实参实现了 WSSink 时才下发,无法靠它清掉子包的旧 sink。
		SetFlowSink(previousFlowSink)
		wsSink, _ := previousFlowSink.(wsprocessor.WSSink)
		wsprocessor.SetWSSink(wsSink)
		sharedHttpClient, sharedStreamClient, streamSink = previousClient, previousStreamClient, previousStreamSink
		SetBodyCache(previousCache)
	})
}

func TestRunFlowPipelineDecisionAndFailureBranches(t *testing.T) {
	preserveHTTPGlobals(t)
	flowSink = nil

	t.Run("mock request", func(t *testing.T) {
		responseHooks := 0
		p := pipeline.New(nil, nil)
		p.Register(&flowDecisionHook{
			onRequest: func(f *flow.Flow) flow.Decision {
				f.Response = &flow.Response{Status: http.StatusCreated, Header: map[string][]string{}, Body: []byte("mocked")}
				return flow.MockDecision("fixture")
			},
			onResponse: func(*flow.Flow) flow.Decision {
				responseHooks++
				return flow.ContinueDecision()
			},
		})
		activePipeline = p
		r := &branchResponder{}
		req, _ := http.NewRequest(http.MethodGet, "http://example.test/mock", nil)

		if err := runFlowPipeline(silentServer{}, req, flow.ProtoHTTP, nil, nil, r); err != nil {
			t.Fatalf("mock pipeline: %v", err)
		}
		if r.written == nil || r.written.State != flow.StateMocked || responseHooks != 1 {
			t.Fatalf("mock result = flow %v, response hooks %d", r.written, responseHooks)
		}
	})

	t.Run("response abort", func(t *testing.T) {
		sharedHttpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("upstream")),
				Request:    req,
			}, nil
		})}
		p := pipeline.New(nil, nil)
		p.Register(&flowDecisionHook{onResponse: func(*flow.Flow) flow.Decision {
			return flow.AbortDecision(http.StatusForbidden, "response blocked")
		}})
		activePipeline = p
		r := &branchResponder{}
		req, _ := http.NewRequest(http.MethodGet, "http://example.test/data", nil)

		if err := runFlowPipeline(silentServer{}, req, flow.ProtoHTTP, nil, nil, r); err != nil {
			t.Fatalf("response abort pipeline: %v", err)
		}
		if r.aborted == nil || r.aborted.StatusOnAbort != http.StatusForbidden {
			t.Fatalf("abort decision = %+v", r.aborted)
		}
	})

	t.Run("unsupported streaming response", func(t *testing.T) {
		activePipeline = nil
		sharedStreamClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     http.Header{"Content-Type": {"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader("data: event\n\n")),
				Request:    req,
			}, nil
		})}
		r := &branchResponder{}
		req, _ := http.NewRequest(http.MethodGet, "http://example.test/events", nil)
		req.Header.Set("Accept", "text/event-stream")

		if err := runFlowPipeline(silentServer{}, req, flow.ProtoHTTP, nil, nil, r); err != nil {
			t.Fatalf("unsupported stream pipeline: %v", err)
		}
		if r.badGatewayCalls != 1 {
			t.Fatalf("bad gateway calls = %d", r.badGatewayCalls)
		}
	})

	t.Run("ordinary upstream failure", func(t *testing.T) {
		activePipeline = nil
		wantErr := errors.New("ordinary upstream unavailable")
		sharedHttpClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, wantErr
		})}
		r := &branchResponder{}
		req, _ := http.NewRequest(http.MethodGet, "http://example.test/data", nil)

		if err := runFlowPipeline(silentServer{}, req, flow.ProtoHTTP, nil, nil, r); err != nil {
			t.Fatalf("ordinary upstream failure: %v", err)
		}
		if r.badGatewayCalls != 1 {
			t.Fatalf("bad gateway calls = %d", r.badGatewayCalls)
		}
	})

	t.Run("grpc upstream failure", func(t *testing.T) {
		activePipeline = nil
		wantErr := errors.New("grpc upstream unavailable")
		sharedStreamClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, wantErr
		})}
		r := &branchResponder{stream: &captureStreamWriter{}, streamOK: true}
		req, _ := http.NewRequest(http.MethodPost, "https://example.test/service/Call", io.NopCloser(strings.NewReader("")))
		req.Header.Set("Content-Type", "application/grpc")
		req.Proto = "HTTP/2.0"
		req.ProtoMajor = 2
		req.ProtoMinor = 0

		if err := runFlowPipeline(silentServer{}, req, flow.ProtoHTTP, nil, nil, r); err != nil {
			t.Fatalf("grpc failure pipeline: %v", err)
		}
		if r.badGatewayCalls != 1 {
			t.Fatalf("bad gateway calls = %d", r.badGatewayCalls)
		}
	})
}

func TestRunResponseStreamControlBranches(t *testing.T) {
	preserveHTTPGlobals(t)
	flowSink = nil

	newFixture := func() (*flow.Flow, *http.Request, *http.Response) {
		req, _ := http.NewRequest(http.MethodGet, "http://example.test/events", nil)
		f := flow.New(flow.ProtoHTTP)
		f.Request = &flow.Request{URL: req.URL.String(), Method: req.Method}
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": {"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader("data: one\n\n")),
			Request:    req,
		}
		return f, req, resp
	}

	t.Run("response hook abort", func(t *testing.T) {
		p := pipeline.New(nil, nil)
		p.Register(&flowDecisionHook{onResponse: func(*flow.Flow) flow.Decision {
			return flow.AbortDecision(http.StatusUnavailableForLegalReasons, "stream blocked")
		}})
		activePipeline = p
		f, req, resp := newFixture()
		r := &branchResponder{}

		if err := runResponseStream(silentServer{}, f, flow.StreamSSE, resp, req, r, &captureStreamWriter{}); err != nil {
			t.Fatalf("stream abort: %v", err)
		}
		if r.aborted == nil || f.State != flow.StateBlocked {
			t.Fatalf("abort result = decision %+v, state %s", r.aborted, f.State)
		}
	})

	t.Run("response head write failure", func(t *testing.T) {
		activePipeline = nil
		wantErr := errors.New("head write failed")
		f, req, resp := newFixture()
		r := &branchResponder{}
		err := runResponseStream(silentServer{}, f, flow.StreamSSE, resp, req, r, &failingStreamWriter{headErr: wantErr})
		if !errors.Is(err, wantErr) || f.State != flow.StateErrored || !r.reuseDisabled {
			t.Fatalf("head failure = err %v, state %s, disabled %v", err, f.State, r.reuseDisabled)
		}
	})

	t.Run("message hook abort", func(t *testing.T) {
		p := pipeline.New(nil, nil)
		p.Register(&testStreamHook{fn: func(*flow.StreamMessage) flow.Decision {
			return flow.AbortDecision(0, "stop stream")
		}})
		activePipeline = p
		f, req, resp := newFixture()
		r := &branchResponder{}
		sw := &captureStreamWriter{}

		err := runResponseStream(silentServer{}, f, flow.StreamSSE, resp, req, r, sw)
		if !errors.Is(err, errStreamAbort) || f.State != flow.StateBlocked || !r.reuseDisabled || sw.closed {
			t.Fatalf("message abort = err %v, state %s, disabled %v, closed %v", err, f.State, r.reuseDisabled, sw.closed)
		}
	})

	t.Run("captured head and trailer", func(t *testing.T) {
		activePipeline = nil
		f, req, resp := newFixture()
		capture := &flow.ResponseCapture{
			StatusLine: "HTTP/1.1 200 Custom",
			Headers:    [][2]string{{"content-type", "text/event-stream"}},
		}
		req = req.WithContext(flow.WithResponseCapture(req.Context(), capture))
		resp.Request = req
		resp.Trailer = http.Header{"X-Stream-End": {"done"}}
		sw := &captureStreamWriter{}

		if err := runResponseStream(silentServer{}, f, flow.StreamSSE, resp, req, &branchResponder{}, sw); err != nil {
			t.Fatalf("captured stream: %v", err)
		}
		if len(f.Response.RawHeaders) != 1 || sw.trailer.Get("X-Stream-End") != "done" {
			t.Fatalf("captured response = raw %v, trailer %v", f.Response.RawHeaders, sw.trailer)
		}
	})
}

func TestRunGRPCStreamControlFailures(t *testing.T) {
	preserveHTTPGlobals(t)
	flowSink = nil
	streamSink = nil

	newRequest := func() *http.Request {
		req, _ := http.NewRequest(http.MethodPost, "https://example.test/service/Call", io.NopCloser(strings.NewReader("")))
		req.Header.Set("Content-Type", "application/grpc")
		req.Proto = "HTTP/2.0"
		req.ProtoMajor = 2
		return req
	}
	setResponse := func(body []byte) {
		sharedStreamClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     http.Header{"Content-Type": {"application/grpc"}},
				Body:       io.NopCloser(bytes.NewReader(body)),
				Request:    req,
			}, nil
		})}
	}

	t.Run("request hook abort", func(t *testing.T) {
		p := pipeline.New(nil, nil)
		p.Register(&flowDecisionHook{onRequest: func(*flow.Flow) flow.Decision {
			return flow.AbortDecision(http.StatusForbidden, "grpc request blocked")
		}})
		activePipeline = p
		r := &branchResponder{}

		if err := runGRPCStream(silentServer{}, newRequest(), flow.ProtoHTTP, nil, nil, r, &captureStreamWriter{}); err != nil {
			t.Fatalf("grpc request abort: %v", err)
		}
		if r.aborted == nil || r.aborted.StatusOnAbort != http.StatusForbidden {
			t.Fatalf("grpc request abort decision = %+v", r.aborted)
		}
	})

	t.Run("response hook abort", func(t *testing.T) {
		setResponse(nil)
		p := pipeline.New(nil, nil)
		p.Register(&flowDecisionHook{onResponse: func(*flow.Flow) flow.Decision {
			return flow.AbortDecision(http.StatusForbidden, "grpc response blocked")
		}})
		activePipeline = p
		r := &branchResponder{}

		if err := runGRPCStream(silentServer{}, newRequest(), flow.ProtoHTTP, nil, nil, r, &captureStreamWriter{}); err != nil {
			t.Fatalf("grpc response abort: %v", err)
		}
		if r.aborted == nil || r.aborted.StatusOnAbort != http.StatusForbidden {
			t.Fatalf("grpc response abort decision = %+v", r.aborted)
		}
	})

	t.Run("response head write failure", func(t *testing.T) {
		setResponse(nil)
		activePipeline = nil
		wantErr := errors.New("grpc response head failed")
		r := &branchResponder{}
		err := runGRPCStream(silentServer{}, newRequest(), flow.ProtoHTTP, nil, nil, r, &failingStreamWriter{headErr: wantErr})
		if !errors.Is(err, wantErr) || !r.reuseDisabled {
			t.Fatalf("grpc head failure = err %v, disabled %v", err, r.reuseDisabled)
		}
	})

	t.Run("message hook abort", func(t *testing.T) {
		setResponse(grpcFrameBytes([]byte("response"), false))
		p := pipeline.New(nil, nil)
		p.Register(&testStreamHook{fn: func(*flow.StreamMessage) flow.Decision {
			return flow.AbortDecision(0, "grpc message blocked")
		}})
		activePipeline = p
		r := &branchResponder{}
		err := runGRPCStream(silentServer{}, newRequest(), flow.ProtoHTTP, nil, nil, r, &captureStreamWriter{})
		if !errors.Is(err, errStreamAbort) || !r.reuseDisabled {
			t.Fatalf("grpc message abort = err %v, disabled %v", err, r.reuseDisabled)
		}
	})

	t.Run("client body write failure", func(t *testing.T) {
		setResponse(grpcFrameBytes([]byte("response"), false))
		activePipeline = nil
		wantErr := errors.New("grpc client stopped reading")
		r := &branchResponder{}
		err := runGRPCStream(silentServer{}, newRequest(), flow.ProtoHTTP, nil, nil, r, &failingStreamWriter{chunkErr: wantErr})
		if !errors.Is(err, wantErr) || !r.reuseDisabled {
			t.Fatalf("grpc body failure = err %v, disabled %v", err, r.reuseDisabled)
		}
	})
}

func TestPassthroughResponseFailureAndTrailerBranches(t *testing.T) {
	preserveHTTPGlobals(t)
	flowSink = nil
	SetBodyCache(nil)

	newFixture := func() (*flow.Flow, *http.Request, *http.Response) {
		req, _ := http.NewRequest(http.MethodGet, "http://example.test/video", nil)
		f := flow.New(flow.ProtoHTTP)
		f.Request = &flow.Request{URL: req.URL.String(), Method: req.Method}
		resp := &http.Response{
			StatusCode:    http.StatusOK,
			Status:        "200 OK",
			Header:        http.Header{"Content-Type": {"video/mp4"}},
			Body:          io.NopCloser(strings.NewReader("video-data")),
			ContentLength: int64(len("video-data")),
			Request:       req,
		}
		return f, req, resp
	}

	t.Run("response hook abort", func(t *testing.T) {
		p := pipeline.New(nil, nil)
		p.Register(&flowDecisionHook{onResponse: func(*flow.Flow) flow.Decision {
			return flow.AbortDecision(http.StatusForbidden, "media blocked")
		}})
		activePipeline = p
		f, req, resp := newFixture()
		r := &branchResponder{}
		if err := runPassthroughResponse(silentServer{}, f, resp, req, r, &branchBodyStreamer{}); err != nil {
			t.Fatalf("passthrough abort: %v", err)
		}
		if r.aborted == nil || f.State != flow.StateBlocked {
			t.Fatalf("abort result = decision %+v, state %s", r.aborted, f.State)
		}
	})

	t.Run("head write failure", func(t *testing.T) {
		activePipeline = nil
		wantErr := errors.New("media head failed")
		f, req, resp := newFixture()
		r := &branchResponder{}
		err := runPassthroughResponse(silentServer{}, f, resp, req, r, &branchBodyStreamer{headErr: wantErr})
		if !errors.Is(err, wantErr) || f.State != flow.StateErrored || !r.reuseDisabled {
			t.Fatalf("head failure = err %v, state %s, disabled %v", err, f.State, r.reuseDisabled)
		}
	})

	t.Run("body write failure", func(t *testing.T) {
		activePipeline = nil
		wantErr := errors.New("client disconnected")
		f, req, resp := newFixture()
		r := &branchResponder{}
		err := runPassthroughResponse(silentServer{}, f, resp, req, r, &branchBodyStreamer{writeErr: wantErr})
		if !errors.Is(err, wantErr) || f.State != flow.StateErrored || !r.reuseDisabled {
			t.Fatalf("body failure = err %v, state %s, disabled %v", err, f.State, r.reuseDisabled)
		}
	})

	t.Run("captured head and trailer", func(t *testing.T) {
		activePipeline = nil
		f, req, resp := newFixture()
		req = req.WithContext(flow.WithResponseCapture(req.Context(), &flow.ResponseCapture{
			StatusLine: "HTTP/1.1 200 Custom",
			Headers:    [][2]string{{"content-type", "video/mp4"}},
		}))
		resp.Request = req
		resp.Trailer = http.Header{"X-Checksum": {"ok"}}
		w := &branchBodyStreamer{}
		if err := runPassthroughResponse(silentServer{}, f, resp, req, &branchResponder{}, w); err != nil {
			t.Fatalf("passthrough success: %v", err)
		}
		if w.written.String() != "video-data" || w.trailer.Get("X-Checksum") != "ok" || len(f.Response.RawHeaders) != 1 {
			t.Fatalf("passthrough result = body %q, trailer %v, raw %v", w.written.String(), w.trailer, f.Response.RawHeaders)
		}
	})
}

func TestStreamRecorderAndDispatchEdgeBranches(t *testing.T) {
	preserveHTTPGlobals(t)
	sink := &fakeStreamSink{}
	withStreamSink(t, sink)
	activePipeline = nil

	f := flow.New(flow.ProtoHTTP)
	f.Request = &flow.Request{URL: "http://example.test/stream", Method: http.MethodGet}
	process := &flow.ProcessInfo{PID: 42, Name: "stream-client"}
	f.SetProcess(process)
	recorder := newStreamRecorder(f, flow.StreamChunk)
	for i := 0; i <= maxStreamMessages; i++ {
		recorder.add(&flow.StreamMessage{Data: []byte{byte(i)}})
	}
	process.Name = "mutated"
	snapshot := sink.snapshot()
	if snapshot.Process == nil || snapshot.Process.Name != "stream-client" || len(snapshot.Messages) != maxStreamMessages || snapshot.MessageCount != maxStreamMessages+1 {
		t.Fatalf("stream snapshot = %+v", snapshot)
	}

	var nilRecorder *streamRecorder
	nilRecorder.push()

	scanner := &grpcScanner{}
	header := make([]byte, 5)
	header[4] = 3
	if frames := scanner.push(append(header, 'x')); len(frames) != 0 {
		t.Fatalf("partial grpc frame parsed unexpectedly: %v", frames)
	}
	overflow := &grpcScanner{overflow: true}
	if frames := overflow.push([]byte("raw")); len(frames) != 0 {
		t.Fatalf("overflow scanner parsed frames: %v", frames)
	}

	modify := pipeline.New(nil, nil)
	modify.Register(&testStreamHook{fn: func(m *flow.StreamMessage) flow.Decision {
		m.Data = []byte("changed")
		return flow.ContinueDecision()
	}})
	activePipeline = modify
	out, err := emitStreamMessage(nil, "url", flow.WSServerToClient, flow.StreamChunk, "", []byte("old"), []byte("old"))
	if err != nil || string(out) != "changed" {
		t.Fatalf("chunk modification = %q, %v", out, err)
	}

	abort := pipeline.New(nil, nil)
	abort.Register(&testStreamHook{fn: func(*flow.StreamMessage) flow.Decision {
		return flow.AbortDecision(0, "stop compressed grpc")
	}})
	activePipeline = abort
	compressed := grpcFrame{Raw: grpcFrameBytes([]byte("compressed"), true), Payload: []byte("compressed"), Compressed: true}
	if _, err := emitStreamMessageGRPC(nil, "url", flow.WSServerToClient, compressed, compressed.Payload, compressed.Raw); !errors.Is(err, errStreamAbort) {
		t.Fatalf("compressed grpc abort = %v", err)
	}

	wantErr := errors.New("stream client write failed")
	activePipeline = nil
	if err := dispatchChunk(nil, "url", flow.WSServerToClient, flow.StreamChunk, &sseScanner{}, &grpcScanner{}, []byte("chunk"), &failingStreamWriter{chunkErr: wantErr}); !errors.Is(err, wantErr) {
		t.Fatalf("chunk dispatch error = %v", err)
	}
	if err := dispatchChunk(nil, "url", flow.WSServerToClient, flow.StreamSSE, &sseScanner{}, &grpcScanner{}, []byte("data: event\n\n"), &failingStreamWriter{chunkErr: wantErr}); !errors.Is(err, wantErr) {
		t.Fatalf("SSE dispatch error = %v", err)
	}
	if err := dispatchChunk(nil, "url", flow.WSServerToClient, flow.StreamGRPC, &sseScanner{}, &grpcScanner{}, grpcFrameBytes([]byte("frame"), false), &failingStreamWriter{chunkErr: wantErr}); !errors.Is(err, wantErr) {
		t.Fatalf("grpc dispatch error = %v", err)
	}
	overflowHeader := make([]byte, 5)
	overflowHeader[0] = 1
	overflowHeader[1] = 1
	overflowHeader[4] = 1
	if err := dispatchChunk(nil, "url", flow.WSServerToClient, flow.StreamGRPC, &sseScanner{}, &grpcScanner{}, overflowHeader, &failingStreamWriter{chunkErr: wantErr}); !errors.Is(err, wantErr) {
		t.Fatalf("grpc overflow dispatch error = %v", err)
	}
	if err := pumpResponseStream(silentServer{}, nil, "url", flow.StreamSSE, strings.NewReader("data: partial"), &failingStreamWriter{chunkErr: wantErr}); !errors.Is(err, wantErr) {
		t.Fatalf("stream leftover error = %v", err)
	}
	if err := pumpGRPCFrames(nil, "url", flow.WSClientToServer, &grpcScanner{}, grpcFrameBytes([]byte("frame"), false), errorWriter{err: wantErr}); !errors.Is(err, wantErr) {
		t.Fatalf("grpc request pump error = %v", err)
	}
	activePipeline = abort
	if err := pumpGRPCFrames(nil, "url", flow.WSClientToServer, &grpcScanner{}, grpcFrameBytes([]byte("frame"), false), io.Discard); !errors.Is(err, errStreamAbort) {
		t.Fatalf("grpc request abort = %v", err)
	}
	activePipeline = nil
	if err := pumpGRPCFrames(nil, "url", flow.WSClientToServer, &grpcScanner{}, overflowHeader, errorWriter{err: wantErr}); !errors.Is(err, wantErr) {
		t.Fatalf("grpc request overflow error = %v", err)
	}

	h2 := newH2StreamWriter(&errorResponseWriter{err: wantErr})
	if err := h2.writeChunk(nil); err != nil {
		t.Fatalf("empty h2 chunk: %v", err)
	}
	if err := h2.writeChunk([]byte("chunk")); !errors.Is(err, wantErr) {
		t.Fatalf("h2 chunk error = %v", err)
	}
}

func startConnectProxyFixture(t *testing.T, response string) (string, <-chan error) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("proxy listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	done := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		req, err := http.ReadRequest(bufio.NewReader(conn))
		if err == nil {
			_ = req.Body.Close()
			_, err = io.WriteString(conn, response)
		}
		done <- err
	}()
	return listener.Addr().String(), done
}

func waitTunnelFixture(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("tunnel fixture: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("tunnel fixture did not finish")
	}
}

func TestDialTunnelTargetDirectAndProxyBranches(t *testing.T) {
	previous := tunnelUpstream.Load()
	t.Cleanup(func() { SetUpstreamProxyURL(previous) })

	t.Run("direct success", func(t *testing.T) {
		SetUpstreamProxyURL(nil)
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("origin listen: %v", err)
		}
		defer listener.Close()
		done := make(chan error, 1)
		go func() {
			conn, err := listener.Accept()
			if err == nil {
				err = conn.Close()
			}
			done <- err
		}()

		conn, err := dialTunnelTarget(listener.Addr().String())
		if err != nil {
			t.Fatalf("direct tunnel dial: %v", err)
		}
		_ = conn.Close()
		waitTunnelFixture(t, done)
	})

	closedListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("closed-address listen: %v", err)
	}
	closedAddr := closedListener.Addr().String()
	_ = closedListener.Close()

	t.Run("direct failure and tunnel logging", func(t *testing.T) {
		SetUpstreamProxyURL(nil)
		if conn, err := dialTunnelTarget(closedAddr); err == nil || conn != nil {
			t.Fatalf("closed direct target = conn %v, err %v", conn, err)
		}

		server := newMockServer()
		processor := New(newMockConnection(newMockConn(""), server)).(*Processor)
		processor.request = &http.Request{Host: closedAddr}
		if err := processor.tunnel(server, bufio.NewReader(strings.NewReader(""))); err == nil {
			t.Fatal("tunnel to closed target succeeded")
		}
	})

	t.Run("proxy dial failure", func(t *testing.T) {
		proxyURL := &url.URL{Scheme: "http", Host: closedAddr}
		SetUpstreamProxyURL(proxyURL)
		if conn, err := dialTunnelTarget("origin.example:443"); err == nil || conn != nil {
			t.Fatalf("closed proxy = conn %v, err %v", conn, err)
		}
	})

	t.Run("proxy rejection", func(t *testing.T) {
		addr, done := startConnectProxyFixture(t, "HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\n\r\n")
		SetUpstreamProxyURL(&url.URL{Scheme: "http", Host: addr})
		if conn, err := dialTunnelTarget("origin.example:443"); err == nil || conn != nil {
			t.Fatalf("rejected proxy = conn %v, err %v", conn, err)
		}
		waitTunnelFixture(t, done)
	})

	t.Run("proxy success", func(t *testing.T) {
		addr, done := startConnectProxyFixture(t, "HTTP/1.1 200 Connection Established\r\nContent-Length: 0\r\n\r\n")
		SetUpstreamProxyURL(&url.URL{Scheme: "http", Host: addr})
		conn, err := dialTunnelTarget("origin.example:443")
		if err != nil || conn == nil {
			t.Fatalf("proxy tunnel = conn %v, err %v", conn, err)
		}
		_ = conn.Close()
		waitTunnelFixture(t, done)
	})
}
