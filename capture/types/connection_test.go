// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0

package types

import (
	"bytes"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

type memoryConn struct {
	input    *bytes.Reader
	output   bytes.Buffer
	closed   bool
	closeErr error
}

func newMemoryConn(input string) *memoryConn {
	return &memoryConn{input: bytes.NewReader([]byte(input))}
}

func (c *memoryConn) Read(p []byte) (int, error)       { return c.input.Read(p) }
func (c *memoryConn) Write(p []byte) (int, error)      { return c.output.Write(p) }
func (c *memoryConn) Close() error                     { c.closed = true; return c.closeErr }
func (c *memoryConn) LocalAddr() net.Addr              { return addr("local") }
func (c *memoryConn) RemoteAddr() net.Addr             { return addr("remote") }
func (c *memoryConn) SetDeadline(time.Time) error      { return nil }
func (c *memoryConn) SetReadDeadline(time.Time) error  { return nil }
func (c *memoryConn) SetWriteDeadline(time.Time) error { return nil }

type addr string

func (a addr) Network() string { return "test" }
func (a addr) String() string  { return string(a) }

func TestDefaultConnectionAccessorsAndReplacement(t *testing.T) {
	first := newMemoryConn("first")
	var server Server
	conn := NewConnection(first, server).(*DefaultConnection)
	if conn.GetConn() != first || conn.GetServer() != server {
		t.Fatal("NewConnection did not retain its dependencies")
	}
	data, err := io.ReadAll(conn.GetReader())
	if err != nil || string(data) != "first" {
		t.Fatalf("initial reader returned %q, %v", data, err)
	}

	second := newMemoryConn("second")
	conn.SetConn(second)
	if conn.GetConn() != second {
		t.Fatal("SetConn did not replace the raw connection")
	}
	data, err = io.ReadAll(conn.GetReader())
	if err != nil || string(data) != "second" {
		t.Fatalf("replacement reader returned %q, %v", data, err)
	}
	if _, err := conn.GetWriter().WriteString("response"); err != nil {
		t.Fatalf("buffered write: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("Close returned %v", err)
	}
	if !second.closed || second.output.String() != "response" {
		t.Fatalf("Close state: closed=%v output=%q", second.closed, second.output.String())
	}
}

func TestDefaultConnectionCloseCases(t *testing.T) {
	if err := (&DefaultConnection{}).Close(); err != nil {
		t.Fatalf("zero-value Close returned %v", err)
	}

	wantErr := errors.New("close failed")
	raw := newMemoryConn("")
	raw.closeErr = wantErr
	conn := NewConnection(raw, nil).(*DefaultConnection)
	if err := conn.Close(); !errors.Is(err, wantErr) {
		t.Fatalf("Close error = %v, want %v", err, wantErr)
	}
}

func TestPacketDirectionString(t *testing.T) {
	tests := []struct {
		direction PacketDirection
		want      string
	}{
		{DirectionInbound, "inbound"},
		{DirectionOutbound, "outbound"},
		{PacketDirection(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.direction.String(); got != tt.want {
			t.Errorf("PacketDirection(%d).String() = %q, want %q", tt.direction, got, tt.want)
		}
	}
}
