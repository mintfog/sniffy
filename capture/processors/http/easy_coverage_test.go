// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0

package http

import (
	"bufio"
	"bytes"
	"compress/flate"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"

	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/internal/pipeline"
)

type flowAndWSSink struct{}

func (*flowAndWSSink) RecordFlowStarted(*flow.Flow)    {}
func (*flowAndWSSink) RecordFlowCompleted(*flow.Flow)  {}
func (*flowAndWSSink) RecordFlowUpdated(*flow.Flow)    {}
func (*flowAndWSSink) RecordWSSession(*flow.WSSession) {}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

type failWriteConn struct {
	*mockConn
	err error
}

func (c *failWriteConn) Write([]byte) (int, error) { return 0, c.err }

const proxyFixtureTimeout = 3 * time.Second

func armProxyFixture(t *testing.T, conn net.Conn) {
	t.Helper()
	if err := conn.SetDeadline(time.Now().Add(proxyFixtureTimeout)); err != nil {
		t.Fatalf("set proxy fixture deadline: %v", err)
	}
}

func waitProxyAuthorization(t *testing.T, auth <-chan string) string {
	t.Helper()
	timer := time.NewTimer(proxyFixtureTimeout)
	defer timer.Stop()
	select {
	case value := <-auth:
		return value
	case <-timer.C:
		t.Fatal("proxy fixture did not receive a request")
		return ""
	}
}

func TestConfigurationSetters(t *testing.T) {
	preserveHTTPGlobals(t)

	p := pipeline.New(nil, nil)
	SetPipeline(p)
	if activePipeline != p {
		t.Fatal("SetPipeline did not retain the pipeline")
	}
	sink := &flowAndWSSink{}
	SetFlowSink(sink)
	if flowSink != sink {
		t.Fatal("SetFlowSink did not retain the sink")
	}
	SetProcessResolver(nil)
	if processResolver != nil {
		t.Fatal("SetProcessResolver(nil) did not clear the resolver")
	}
	stream := &fakeStreamSink{}
	SetStreamSink(stream)
	if streamSink != stream {
		t.Fatal("SetStreamSink did not retain the sink")
	}

	if streamClientFrom(nil) != nil {
		t.Fatal("streamClientFrom(nil) should return nil")
	}
}

func TestHTTPRespondersAndCertificateProfiles(t *testing.T) {
	server := newMockServer()

	recorder := httptest.NewRecorder()
	h2 := &h2Responder{w: recorder}
	if err := h2.writeBadGateway(); err != nil {
		t.Fatalf("h2 writeBadGateway: %v", err)
	}
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("h2 bad gateway status = %d", recorder.Code)
	}
	h2.disableReuse()
	if _, ok := h2.bodyStreamer(); !ok {
		t.Fatal("h2 responder should provide a body streamer")
	}

	recorder = httptest.NewRecorder()
	h2 = &h2Responder{w: recorder}
	if err := h2.writeAbort(flow.AbortDecision(http.StatusForbidden, "denied")); err != nil {
		t.Fatalf("h2 writeAbort: %v", err)
	}
	if recorder.Code != http.StatusForbidden || !strings.Contains(recorder.Body.String(), "denied") {
		t.Fatalf("h2 abort response = %d %q", recorder.Code, recorder.Body.String())
	}

	func() {
		defer func() {
			if recovered := recover(); recovered != http.ErrAbortHandler {
				t.Fatalf("zero-status h2 abort panic = %#v", recovered)
			}
		}()
		_ = (&h2Responder{w: httptest.NewRecorder()}).writeAbort(flow.AbortDecision(0, "drop"))
	}()

	recorder = httptest.NewRecorder()
	serveIOSProfileH2(server, recorder)
	if recorder.Code != http.StatusOK || recorder.Header().Get("Content-Type") != "application/x-apple-aspen-config" || recorder.Body.Len() == 0 {
		t.Fatalf("h2 profile response = %d, type=%q, bytes=%d", recorder.Code, recorder.Header().Get("Content-Type"), recorder.Body.Len())
	}

	raw := newMockConn("")
	conn := newMockConnection(raw, server)
	processor := New(conn).(*Processor)
	if err := processor.serveIOSProfile(server); err != nil {
		t.Fatalf("serveIOSProfile: %v", err)
	}
	if !processor.closeAfterResponse || !strings.Contains(raw.WrittenData(), "application/x-apple-aspen-config") {
		t.Fatalf("h1 profile response was not written: %q", raw.WrittenData())
	}

	raw = newMockConn("")
	processor = New(newMockConnection(raw, server)).(*Processor)
	if err := processor.writeRawResponse("HTTP/1.1 204 No Content\r\n\r\n"); err != nil {
		t.Fatalf("writeRawResponse: %v", err)
	}
	if raw.WrittenData() != "HTTP/1.1 204 No Content\r\n\r\n" {
		t.Fatalf("raw response = %q", raw.WrittenData())
	}
}

func TestConnResponderCapabilities(t *testing.T) {
	server := newMockServer()
	raw := newMockConn("")
	processor := New(newMockConnection(raw, server)).(*Processor)
	responder := &connResponder{p: processor, server: server}
	if err := responder.writeBadGateway(); err != nil {
		t.Fatalf("writeBadGateway: %v", err)
	}
	if !strings.Contains(raw.WrittenData(), "502 Bad Gateway") {
		t.Fatalf("bad gateway response = %q", raw.WrittenData())
	}
	if _, ok := responder.streamWriter(); !ok {
		t.Fatal("responder with a connection should support streaming")
	}
	if _, ok := responder.bodyStreamer(); !ok {
		t.Fatal("responder with a connection should support body passthrough")
	}

	nilConn := &mockConnection{
		reader: bufio.NewReader(strings.NewReader("")),
		writer: bufio.NewWriter(io.Discard),
		server: server,
	}
	nilResponder := &connResponder{p: New(nilConn).(*Processor), server: server}
	if _, ok := nilResponder.streamWriter(); ok {
		t.Fatal("responder without a raw connection should not support streaming")
	}
	if _, ok := nilResponder.bodyStreamer(); ok {
		t.Fatal("responder without a raw connection should not support body passthrough")
	}
}

func TestStreamAndPassthroughWriters(t *testing.T) {
	raw := newMockConn("")
	stream := newConnStreamWriter(raw, 0)
	header := http.Header{"Content-Type": {"text/event-stream"}, "Connection": {"keep-alive"}}
	rawHead := [][2]string{{"content-type", "ignored"}, {"Connection", "ignored"}}
	if err := stream.writeHead("", http.StatusOK, header, rawHead); err != nil {
		t.Fatalf("stream writeHead: %v", err)
	}
	if err := stream.writeChunk(nil); err != nil {
		t.Fatalf("empty stream chunk: %v", err)
	}
	if err := stream.writeChunk([]byte("hello")); err != nil {
		t.Fatalf("stream writeChunk: %v", err)
	}
	stream.setTrailer(http.Header{"X-Trailer": {"done"}})
	if err := stream.close(); err != nil {
		t.Fatalf("stream close: %v", err)
	}
	written := raw.WrittenData()
	if !strings.Contains(strings.ToLower(written), "transfer-encoding: chunked") || !strings.Contains(written, "5\r\nhello\r\n0\r\n\r\n") {
		t.Fatalf("chunked stream output = %q", written)
	}

	reconciled := reconcileStreamHead(
		[][2]string{{"x-first", "old"}, {"Drop-Me", "old"}},
		http.Header{"X-First": {"new"}, "X-Added": {"value"}},
	)
	if len(reconciled) != 2 || reconciled[0] != [2]string{"x-first", "new"} {
		t.Fatalf("reconciled headers = %#v", reconciled)
	}

	recorder := httptest.NewRecorder()
	h2Stream := newH2StreamWriter(recorder)
	if err := h2Stream.writeHead("", http.StatusOK, http.Header{"Content-Type": {"text/event-stream"}}, nil); err != nil {
		t.Fatalf("h2 stream writeHead: %v", err)
	}
	if err := h2Stream.writeChunk([]byte("event")); err != nil {
		t.Fatalf("h2 stream writeChunk: %v", err)
	}
	h2Stream.setTrailer(http.Header{"Grpc-Status": {"0"}})
	if err := h2Stream.close(); err != nil {
		t.Fatalf("h2 stream close: %v", err)
	}
	if recorder.Header().Get(http.TrailerPrefix+"Grpc-Status") != "0" {
		t.Fatalf("h2 stream trailer = %q", recorder.Header().Get(http.TrailerPrefix+"Grpc-Status"))
	}

	recorder = httptest.NewRecorder()
	body := newH2BodyStreamer(recorder)
	if err := body.writeHead("", http.StatusPartialContent, http.Header{"Content-Type": {"video/mp4"}}, nil, 4); err != nil {
		t.Fatalf("h2 body writeHead: %v", err)
	}
	if n, err := body.Write([]byte("data")); err != nil || n != 4 {
		t.Fatalf("h2 body Write = (%d, %v)", n, err)
	}
	body.setTrailer(http.Header{"X-Checksum": {"ok"}})
	if err := body.close(); err != nil {
		t.Fatalf("h2 body close: %v", err)
	}
	if recorder.Body.String() != "data" || recorder.Header().Get(http.TrailerPrefix+"X-Checksum") != "ok" {
		t.Fatalf("h2 body response = %q, headers=%v", recorder.Body.String(), recorder.Header())
	}

	newConnBodyStreamer(newMockConn(""), 0).setTrailer(http.Header{"Ignored": {"value"}})
}

func TestUpstreamProxyConfigurationHelpers(t *testing.T) {
	previous := tunnelUpstream.Load()
	t.Cleanup(func() { tunnelUpstream.Store(previous) })

	SetUpstreamProxyURL(nil)
	if tunnelUpstream.Load() != nil {
		t.Fatal("nil upstream proxy did not clear configuration")
	}
	u, _ := url.Parse("http://user:pass@proxy.example:3128")
	SetUpstreamProxyURL(u)
	u.Host = "mutated.example:9999"
	if got := tunnelUpstream.Load().Host; got != "proxy.example:3128" {
		t.Fatalf("stored proxy URL was not copied: %q", got)
	}

	tests := []struct {
		raw  string
		want string
	}{
		{"http://proxy.example", "proxy.example:80"},
		{"https://proxy.example", "proxy.example:443"},
		{"http://proxy.example:8080", "proxy.example:8080"},
	}
	for _, tt := range tests {
		parsed, _ := url.Parse(tt.raw)
		if got := proxyDialAddr(parsed); got != tt.want {
			t.Errorf("proxyDialAddr(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}

func TestTunnelViaProxy(t *testing.T) {
	t.Run("authenticated success", func(t *testing.T) {
		client, server := net.Pipe()
		defer client.Close()
		armProxyFixture(t, server)
		auth := make(chan string, 1)
		go func() {
			defer server.Close()
			req, err := http.ReadRequest(bufio.NewReader(server))
			if err != nil {
				auth <- "read error: " + err.Error()
				return
			}
			auth <- req.Header.Get("Proxy-Authorization")
			_, _ = io.WriteString(server, "HTTP/1.1 200 Connection Established\r\nContent-Length: 0\r\n\r\n")
		}()

		proxyURL, _ := url.Parse("http://user:pass@proxy.example")
		if err := tunnelViaProxy(client, "origin.example:443", proxyURL, time.Second); err != nil {
			t.Fatalf("tunnelViaProxy: %v", err)
		}
		if got := waitProxyAuthorization(t, auth); got != "Basic dXNlcjpwYXNz" {
			t.Fatalf("Proxy-Authorization = %q", got)
		}
	})

	t.Run("rejected", func(t *testing.T) {
		client, server := net.Pipe()
		defer client.Close()
		armProxyFixture(t, server)
		go func() {
			defer server.Close()
			_, _ = http.ReadRequest(bufio.NewReader(server))
			_, _ = io.WriteString(server, "HTTP/1.1 407 Proxy Authentication Required\r\nContent-Length: 0\r\n\r\n")
		}()

		proxyURL, _ := url.Parse("http://proxy.example")
		err := tunnelViaProxy(client, "origin.example:443", proxyURL, time.Second)
		if err == nil || !strings.Contains(err.Error(), "407") {
			t.Fatalf("rejected tunnel error = %v", err)
		}
	})

	t.Run("malformed response", func(t *testing.T) {
		client, server := net.Pipe()
		defer client.Close()
		armProxyFixture(t, server)
		go func() {
			defer server.Close()
			_, _ = http.ReadRequest(bufio.NewReader(server))
			_, _ = io.WriteString(server, "not an HTTP response\r\n\r\n")
		}()

		proxyURL, _ := url.Parse("http://proxy.example")
		if err := tunnelViaProxy(client, "origin.example:443", proxyURL, time.Second); err == nil {
			t.Fatal("malformed proxy response should fail")
		}
	})
}

func TestProcessorSimpleBranchesAndTLSFailureRecord(t *testing.T) {
	preserveHTTPGlobals(t)
	server := newMockServer()
	if got := readTimeoutOf(server); got != 0 {
		t.Fatalf("readTimeoutOf(nil config) = %v", got)
	}
	if got := writeTimeoutOf(server); got != 0 {
		t.Fatalf("writeTimeoutOf(nil config) = %v", got)
	}

	cached, _ := http.NewRequest(http.MethodGet, "http://example.test/cached", nil)
	processor := New(newMockConnection(newMockConn(""), server)).(*Processor)
	processor.request = cached
	if got, err := processor.readRequest(); err != nil || got != cached {
		t.Fatalf("cached readRequest = (%p, %v)", got, err)
	}

	profileRaw := newMockConn("")
	profileProcessor := New(newMockConnection(profileRaw, server)).(*Processor)
	profileProcessor.request = httptest.NewRequest(http.MethodGet, "http://cert.sniffy/", nil)
	if err := profileProcessor.handleRequest(server); err != nil {
		t.Fatalf("certificate-domain handleRequest: %v", err)
	}
	if !strings.Contains(profileRaw.WrittenData(), "application/x-apple-aspen-config") {
		t.Fatalf("certificate response = %q", profileRaw.WrittenData())
	}

	wantRoundTripErr := errors.New("upstream unavailable")
	sharedHttpClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, wantRoundTripErr
	})}
	badGatewayRaw := newMockConn("")
	badGatewayProcessor := New(newMockConnection(badGatewayRaw, server)).(*Processor)
	request, _ := http.NewRequest(http.MethodGet, "http://example.test/", nil)
	if err := badGatewayProcessor.forwardSimple(server, request); err != nil {
		t.Fatalf("forwardSimple fallback: %v", err)
	}
	if !strings.Contains(badGatewayRaw.WrittenData(), "502 Bad Gateway") {
		t.Fatalf("forwardSimple fallback response = %q", badGatewayRaw.WrittenData())
	}

	wantWriteErr := errors.New("client write failed")
	failingRaw := &failWriteConn{mockConn: newMockConn(""), err: wantWriteErr}
	failingProcessor := New(newMockConnection(failingRaw, server)).(*Processor)
	f := flow.New(flow.ProtoHTTP)
	f.Response = &flow.Response{Status: http.StatusOK, Header: map[string][]string{}, Body: []byte("body")}
	if err := failingProcessor.writeFlowResponse(server, f, request); !errors.Is(err, wantWriteErr) {
		t.Fatalf("writeFlowResponse error = %v, want %v", err, wantWriteErr)
	}

	sink := &collectingSink{}
	SetFlowSink(sink)
	recordRaw := newMockConn("")
	recordProcessor := New(newMockConnection(recordRaw, server)).(*Processor)
	recordProcessor.recordTLSFailure("example.test:443", errors.New("bad handshake"))
	recorded := sink.last()
	if recorded == nil || recorded.State != flow.StateErrored || recorded.Request.Host != "example.test" || recorded.Request.ClientIP == "" {
		t.Fatalf("TLS failure flow = %+v", recorded)
	}

	nilConn := &mockConnection{
		reader: bufio.NewReader(strings.NewReader("")),
		writer: bufio.NewWriter(io.Discard),
		server: server,
	}
	New(nilConn).(*Processor).resolveProcessAsync(flow.New(flow.ProtoHTTP))
}

func TestHTTPProcessorWebSocketDelegationFailure(t *testing.T) {
	server := newMockServer()
	raw := newMockConn("")
	conn := newMockConnection(raw, server)
	request, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:0/ws", nil)
	request.Host = "127.0.0.1:0"
	processor := New(conn).(*Processor)
	processor.request = request

	if err := processor.handleWebSocket(server); err != nil {
		t.Fatalf("failed upstream should be converted to an HTTP response: %v", err)
	}
	if !strings.Contains(raw.WrittenData(), "502 Bad Gateway") {
		t.Fatalf("delegated failure response = %q", raw.WrittenData())
	}
}

func TestH2CertificateRoute(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "https://cert.sniffy/", nil)
	request.Host = certMagicDomain
	handler := &h2Handler{server: newMockServer(), conn: newMockConn("")}
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || recorder.Header().Get("Content-Type") != "application/x-apple-aspen-config" {
		t.Fatalf("certificate route response = %d, headers=%v", recorder.Code, recorder.Header())
	}
}

func TestDecodeStreamBodyEncodings(t *testing.T) {
	encode := func(t *testing.T, encoding string, write func(io.Writer) (io.Closer, error)) {
		t.Helper()
		var encoded bytes.Buffer
		closer, err := write(&encoded)
		if err != nil {
			t.Fatalf("create %s writer: %v", encoding, err)
		}
		if _, err := closer.(io.Writer).Write([]byte("payload")); err != nil {
			t.Fatalf("write %s payload: %v", encoding, err)
		}
		if err := closer.Close(); err != nil {
			t.Fatalf("close %s writer: %v", encoding, err)
		}
		resp := &http.Response{
			Header: http.Header{"Content-Encoding": {encoding}},
			Body:   io.NopCloser(bytes.NewReader(encoded.Bytes())),
		}
		reader, consumed := decodeStreamBody(resp)
		decoded, err := io.ReadAll(reader)
		if err != nil || !consumed || string(decoded) != "payload" {
			t.Fatalf("decode %s = %q, consumed=%v, err=%v", encoding, decoded, consumed, err)
		}
	}

	encode(t, "deflate", func(w io.Writer) (io.Closer, error) { return flate.NewWriter(w, flate.DefaultCompression) })
	encode(t, "br", func(w io.Writer) (io.Closer, error) { return brotli.NewWriter(w), nil })
	encode(t, "zstd", func(w io.Writer) (io.Closer, error) { return zstd.NewWriter(w) })

	invalid := &http.Response{
		Header: http.Header{"Content-Encoding": {"gzip"}},
		Body:   io.NopCloser(strings.NewReader("not-gzip")),
	}
	reader, consumed := decodeStreamBody(invalid)
	if consumed {
		t.Fatal("invalid gzip should fall back to the original body")
	}
	if reader != invalid.Body {
		t.Fatal("invalid gzip should return the original body reader")
	}

	request, _ := http.NewRequest(http.MethodGet, "http://example.test/video", nil)
	request.Header.Set("Range", "bytes=0-10")
	if !largeBodyIntent(request) {
		t.Fatal("Range request should signal large-body intent")
	}
}
