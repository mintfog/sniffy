// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0

package tcp

import (
	"bufio"
	"bytes"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/mintfog/sniffy/capture/types"
)

type testConnection struct {
	server types.Server
	reader *bufio.Reader
	writer *bufio.Writer
}

func newTestConnection(server types.Server) *testConnection {
	return &testConnection{server: server, reader: bufio.NewReader(strings.NewReader("")), writer: bufio.NewWriter(&bytes.Buffer{})}
}

func (*testConnection) GetConn() net.Conn          { return nil }
func (*testConnection) SetConn(net.Conn)           {}
func (c *testConnection) GetReader() *bufio.Reader { return c.reader }
func (c *testConnection) GetWriter() *bufio.Writer { return c.writer }
func (c *testConnection) GetServer() types.Server  { return c.server }
func (*testConnection) Close() error               { return nil }

type testServer struct{ logs []string }

func (*testServer) GetConfig() types.Config { return nil }
func (s *testServer) LogInfo(msg string, args ...interface{}) {
	s.logs = append(s.logs, fmt.Sprintf(msg, args...))
}
func (*testServer) LogError(string, ...interface{})      {}
func (*testServer) LogDebug(string, ...interface{})      {}
func (*testServer) FormatDataPreview(data []byte) string { return string(data) }

func TestProcessor(t *testing.T) {
	server := &testServer{}
	conn := newTestConnection(server)
	p, ok := New(conn).(*Processor)
	if !ok || p.Conn != conn {
		t.Fatal("New did not construct a TCP processor with its connection")
	}
	if got := p.GetProtocolName(); got != "TCP" {
		t.Fatalf("GetProtocolName = %q", got)
	}
	// TCP 协议处理目前是占位实现，只验证 Process 走通了连接上的 server，
	// 不断言日志内容/条数，避免真正实现协议时测试因无关原因失败。
	if err := p.Process(); err != nil {
		t.Fatalf("Process returned %v", err)
	}
	if len(server.logs) == 0 {
		t.Fatal("Process did not reach the server from the connection")
	}
}
