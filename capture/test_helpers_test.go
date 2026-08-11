// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0

package capture

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mintfog/sniffy/capture/types"
	"github.com/mintfog/sniffy/plugins"
)

type testConfig struct {
	address      string
	port         int
	bufferSize   int
	readTimeout  time.Duration
	writeTimeout time.Duration
	logging      bool
	threads      int
}

func (c testConfig) GetAddress() string             { return c.address }
func (c testConfig) GetPort() int                   { return c.port }
func (c testConfig) GetBufferSize() int             { return c.bufferSize }
func (c testConfig) GetReadTimeout() time.Duration  { return c.readTimeout }
func (c testConfig) GetWriteTimeout() time.Duration { return c.writeTimeout }
func (c testConfig) IsLoggingEnabled() bool         { return c.logging }
func (c testConfig) GetThreads() int                { return c.threads }

func defaultTestConfig() testConfig {
	return testConfig{
		address:      "127.0.0.1",
		bufferSize:   4096,
		readTimeout:  time.Second,
		writeTimeout: time.Second,
		threads:      1,
	}
}

type recordingLogger struct {
	mu      sync.Mutex
	entries []string
}

func (l *recordingLogger) add(level, msg string, args ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, level+": "+fmt.Sprintf(msg, args...))
}

func (l *recordingLogger) Info(msg string, args ...interface{})  { l.add("info", msg, args...) }
func (l *recordingLogger) Error(msg string, args ...interface{}) { l.add("error", msg, args...) }
func (l *recordingLogger) Debug(msg string, args ...interface{}) { l.add("debug", msg, args...) }
func (l *recordingLogger) Warn(msg string, args ...interface{})  { l.add("warn", msg, args...) }

func (l *recordingLogger) contains(fragment string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, entry := range l.entries {
		if strings.Contains(entry, fragment) {
			return true
		}
	}
	return false
}

type memoryConn struct {
	mu       sync.Mutex
	reader   *bytes.Reader
	written  bytes.Buffer
	closed   bool
	closeErr error
}

func newMemoryConn(data []byte) *memoryConn {
	return &memoryConn{reader: bytes.NewReader(data)}
}

func (c *memoryConn) Read(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return 0, net.ErrClosed
	}
	return c.reader.Read(p)
}

func (c *memoryConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return 0, net.ErrClosed
	}
	return c.written.Write(p)
}

func (c *memoryConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return c.closeErr
}

func (c *memoryConn) LocalAddr() net.Addr              { return testAddr("local") }
func (c *memoryConn) RemoteAddr() net.Addr             { return testAddr("remote") }
func (c *memoryConn) SetDeadline(time.Time) error      { return nil }
func (c *memoryConn) SetReadDeadline(time.Time) error  { return nil }
func (c *memoryConn) SetWriteDeadline(time.Time) error { return nil }
func (c *memoryConn) output() string                   { c.mu.Lock(); defer c.mu.Unlock(); return c.written.String() }
func (c *memoryConn) isClosed() bool                   { c.mu.Lock(); defer c.mu.Unlock(); return c.closed }

// captureStandardLog 接管标准库 logger 的输出,用于断言未注入 Logger 时的兜底日志。
func captureStandardLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prevOutput, prevFlags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(prevOutput)
		log.SetFlags(prevFlags)
	})
	return &buf
}

type testAddr string

func (a testAddr) Network() string { return "test" }
func (a testAddr) String() string  { return string(a) }

type stubProcessor struct {
	protocol string
	err      error
	called   bool
}

func (p *stubProcessor) Process() error {
	p.called = true
	return p.err
}

func (p *stubProcessor) GetProtocolName() string { return p.protocol }

type stubPacketHandler struct {
	errors []string
}

func (h *stubPacketHandler) HandleConnection(net.Conn, *types.ConnectionInfo) {}
func (h *stubPacketHandler) HandleError(err error, context string) {
	h.errors = append(h.errors, context+": "+err.Error())
}
func (h *stubPacketHandler) OnConnectionStart(net.Conn) error        { return nil }
func (h *stubPacketHandler) OnConnectionEnd(net.Conn, time.Duration) {}

type zeroWriteConn struct{ *memoryConn }

func (c *zeroWriteConn) Write([]byte) (int, error) { return 0, nil }

// errorWriteConn 模拟部分写成功后报错的连接,用于覆盖 throttleConn.Write 的
// "已写入字节数需随错误一并返回"路径。
type errorWriteConn struct {
	*memoryConn
	err error
}

func (c *errorWriteConn) Write([]byte) (int, error) { return 1, c.err }

// recordingConnectionPlugin 是最小可用的连接拦截器插件,用于验证监听器确实在
// 连接前后调用了插件钩子。
type recordingConnectionPlugin struct {
	mu       sync.Mutex
	started  int
	ended    int
	duration time.Duration
}

func (*recordingConnectionPlugin) GetInfo() plugins.PluginInfo {
	return plugins.PluginInfo{Name: "recording-connection", Version: "0.0.1"}
}
func (*recordingConnectionPlugin) Initialize(context.Context, plugins.PluginConfig) error { return nil }
func (*recordingConnectionPlugin) Start(context.Context) error                            { return nil }
func (*recordingConnectionPlugin) Stop(context.Context) error                             { return nil }
func (*recordingConnectionPlugin) IsEnabled() bool                                        { return true }
func (*recordingConnectionPlugin) GetPriority() int                                       { return 0 }

func (p *recordingConnectionPlugin) OnConnectionStart(_ context.Context, conn types.Connection) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if conn != nil {
		p.started++
	}
	return nil
}

func (p *recordingConnectionPlugin) OnConnectionEnd(_ context.Context, conn types.Connection, duration time.Duration) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if conn != nil {
		p.ended++
		p.duration = duration
	}
	return nil
}

func (p *recordingConnectionPlugin) calls() (int, int, time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.started, p.ended, p.duration
}

var _ net.Conn = (*memoryConn)(nil)
var _ types.ProtocolProcessor = (*stubProcessor)(nil)
var _ PacketHandler = (*stubPacketHandler)(nil)
var _ plugins.ConnectionInterceptor = (*recordingConnectionPlugin)(nil)
