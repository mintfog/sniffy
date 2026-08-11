// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0

package websocket

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/internal/pipeline"
)

type websocketFailWriteConn struct {
	*mockConn
	err error
}

func (c *websocketFailWriteConn) Write([]byte) (int, error) { return 0, c.err }

type websocketErrorWriter struct{ err error }

func (w websocketErrorWriter) Write([]byte) (int, error) { return 0, w.err }

const websocketFixtureTimeout = 3 * time.Second

func armHandshakeListener(t *testing.T, listener net.Listener) time.Time {
	t.Helper()
	deadline := time.Now().Add(websocketFixtureTimeout)
	tcpListener, ok := listener.(*net.TCPListener)
	if !ok {
		t.Fatalf("listener type = %T, want *net.TCPListener", listener)
	}
	if err := tcpListener.SetDeadline(deadline); err != nil {
		t.Fatalf("set listener deadline: %v", err)
	}
	return deadline
}

func waitHandshakeFixture(t *testing.T, done <-chan error) {
	t.Helper()
	timer := time.NewTimer(websocketFixtureTimeout)
	defer timer.Stop()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("upstream fixture: %v", err)
		}
	case <-timer.C:
		t.Fatal("upstream fixture did not finish")
	}
}

func requireWebSocketLog(t *testing.T, server *mockServer, fragments ...string) {
	t.Helper()
	for _, entry := range server.logs {
		matched := true
		for _, fragment := range fragments {
			matched = matched && strings.Contains(entry, fragment)
		}
		if matched {
			return
		}
	}
	t.Fatalf("missing log containing %q: %v", fragments, server.logs)
}

func requirePromptReturn(t *testing.T, run func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		run()
	}()
	timer := time.NewTimer(websocketFixtureTimeout)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		t.Fatal("operation did not return")
	}
}

func startHandshakeFixture(t *testing.T, response string) (string, <-chan error) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	deadline := armHandshakeListener(t, listener)
	done := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		if err := conn.SetDeadline(deadline); err != nil {
			done <- err
			return
		}
		reader := bufio.NewReader(conn)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				done <- err
				return
			}
			if line == "\r\n" || line == "\n" {
				break
			}
		}
		if response != "" {
			_, err = io.WriteString(conn, response)
		}
		done <- err
	}()
	return listener.Addr().String(), done
}

func websocketRequest(t *testing.T, addr string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "http://"+addr+"/socket?room=1", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	return req
}

func TestProcessForwardsRejectedUpgradeResponse(t *testing.T) {
	const response = "HTTP/1.1 403 Forbidden\r\nContent-Length: 6\r\nX-Rejected: yes\r\n\r\ndenied"
	addr, serverDone := startHandshakeFixture(t, response)
	server := newMockServer()
	client := newMockConn("")
	req := websocketRequest(t, addr)
	req.Host = "" // dialUpstreamFaithful must fall back to URL.Host.
	processor := New(newMockConnection(client, server), req, false)

	if err := processor.Process(server); err != nil {
		t.Fatalf("process rejected upgrade: %v", err)
	}
	if got := client.WrittenData(); got != response {
		t.Fatalf("rejected response = %q, want %q", got, response)
	}
	waitHandshakeFixture(t, serverDone)
}

func TestDialUpstreamFaithfulFailureBranches(t *testing.T) {
	t.Run("response EOF", func(t *testing.T) {
		addr, serverDone := startHandshakeFixture(t, "")
		server := newMockServer()
		processor := New(newMockConnection(newMockConn(""), server), websocketRequest(t, addr), false)
		conn, reader, response, status, err := processor.dialUpstreamFaithful()
		if err == nil || conn != nil || reader != nil || response != nil || status != 0 {
			t.Fatalf("EOF result = conn %v, reader %v, response %q, status %d, err %v", conn, reader, response, status, err)
		}
		waitHandshakeFixture(t, serverDone)
	})

	t.Run("TLS handshake", func(t *testing.T) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		defer listener.Close()
		deadline := armHandshakeListener(t, listener)
		done := make(chan error, 1)
		go func() {
			conn, err := listener.Accept()
			if err != nil {
				done <- err
				return
			}
			defer conn.Close()
			if err := conn.SetDeadline(deadline); err != nil {
				done <- err
				return
			}
			_, err = io.WriteString(conn, "not a TLS record")
			done <- err
		}()

		server := newMockServer()
		processor := New(newMockConnection(newMockConn(""), server), websocketRequest(t, listener.Addr().String()), true)
		conn, reader, response, status, err := processor.dialUpstreamFaithful()
		if err == nil || conn != nil || reader != nil || response != nil || status != 0 {
			t.Fatalf("TLS failure result = conn %v, reader %v, response %q, status %d, err %v", conn, reader, response, status, err)
		}
		waitHandshakeFixture(t, done)
	})
}

func TestProcessPropagatesClientHandshakeWriteFailure(t *testing.T) {
	const response = "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n"
	addr, serverDone := startHandshakeFixture(t, response)
	wantErr := errors.New("client write failed")
	client := &websocketFailWriteConn{mockConn: newMockConn(""), err: wantErr}
	server := newMockServer()
	processor := New(newMockConnection(client, server), websocketRequest(t, addr), false)

	if err := processor.Process(server); !errors.Is(err, wantErr) {
		t.Fatalf("client handshake write error = %v, want %v", err, wantErr)
	}
	waitHandshakeFixture(t, serverDone)
}

func TestWebSocketClientWriteFailuresAndNilDeadlines(t *testing.T) {
	wantErr := errors.New("client write failed")
	server := newMockServer()
	client := &websocketFailWriteConn{mockConn: newMockConn(""), err: wantErr}
	processor := New(newMockConnection(client, server), &http.Request{}, false)
	if err := processor.sendWebSocketError(); !errors.Is(err, wantErr) {
		t.Fatalf("sendWebSocketError = %v, want %v", err, wantErr)
	}

	processor.armClientWriteDeadline(nil)
	nilConn := &mockConnection{server: server}
	New(nilConn, &http.Request{}, false).armClientWriteDeadline(server)
	clearRawDeadline(nil)
}

func TestPumpFramesDropCloseAndErrorPaths(t *testing.T) {
	previous := activePipeline
	t.Cleanup(func() { activePipeline = previous })
	server := newMockServer()
	request, _ := http.NewRequest(http.MethodGet, "http://example.test/socket", nil)
	processor := New(newMockConnection(newMockConn(""), server), request, false)
	processor.targetURL = "ws://example.test/socket"

	abort := pipeline.New(nil, nil)
	abort.Register(&testWSHook{decision: flow.AbortDecision(0, "drop frame")})
	activePipeline = abort
	var source bytes.Buffer
	if err := writeFrame(&source, wsFrame{fin: true, opcode: opText, payload: []byte("secret")}, false); err != nil {
		t.Fatalf("encode data frame: %v", err)
	}
	if err := writeFrame(&source, wsFrame{fin: true, opcode: opClose}, false); err != nil {
		t.Fatalf("encode close frame: %v", err)
	}
	var destination bytes.Buffer
	processor.pumpFrames(server, bufio.NewReader(&source), &destination, false, flow.WSClientToServer)
	frame, err := readFrame(&destination)
	if err != nil || frame.opcode != opClose {
		t.Fatalf("forwarded frame = %+v, err %v", frame, err)
	}
	if destination.Len() != 0 {
		t.Fatalf("unexpected extra forwarded bytes: %x", destination.Bytes())
	}

	activePipeline = nil
	requirePromptReturn(t, func() {
		processor.pumpFrames(server, bufio.NewReader(strings.NewReader("\x81")), io.Discard, false, flow.WSServerToClient)
	})
	requireWebSocketLog(t, server, flow.WSServerToClient, "读帧结束", "unexpected EOF")
	var closeFrame bytes.Buffer
	if err := writeFrame(&closeFrame, wsFrame{fin: true, opcode: opClose}, false); err != nil {
		t.Fatalf("encode close frame: %v", err)
	}
	wantErr := errors.New("destination closed")
	requirePromptReturn(t, func() {
		processor.pumpFrames(server, bufio.NewReader(&closeFrame), websocketErrorWriter{err: wantErr}, false, flow.WSServerToClient)
	})
	requireWebSocketLog(t, server, flow.WSServerToClient, "写帧失败", wantErr.Error())
}
