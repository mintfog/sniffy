// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package websocket

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/internal/procinfo"
)

// TestHandleFrameDataRecordsWithoutPipeline 未注入 pipeline 时,帧应原样放行并记入会话。
func TestHandleFrameDataRecordsWithoutPipeline(t *testing.T) {
	prevPipeline, prevSink := activePipeline, wsSink
	t.Cleanup(func() { activePipeline, wsSink = prevPipeline, prevSink })
	activePipeline = nil
	sink := &recordingWSSink{}
	wsSink = sink

	server := newMockServer()
	request, _ := http.NewRequest(http.MethodGet, "http://example.test/ws", nil)
	processor := New(newMockConnection(newMockConn(""), server), request, false)
	processor.targetURL = "ws://example.test/ws"
	processor.recorder = newWSRecorder(processor.targetURL)

	data, drop := processor.handleFrameData([]byte("hello"), flow.WSClientToServer, server)
	if drop || string(data) != "hello" {
		t.Fatalf("无 pipeline 时应原样放行 = (%q, drop=%v)", data, drop)
	}
	recorded := sink.last()
	if recorded.MessageCount != 1 || len(recorded.Messages) != 1 {
		t.Fatalf("放行的帧应记录一条: count=%d messages=%d", recorded.MessageCount, len(recorded.Messages))
	}
	message := recorded.Messages[0]
	if string(message.Data) != "hello" || message.Direction != flow.WSClientToServer || message.Type != flow.WSText {
		t.Fatalf("记录的消息 = %+v", message)
	}
}

// TestSendWebSocketErrorWriteStringFailure 用 1 字节缓冲的 bufio.Writer 逼 WriteString
// 直接落到底层连接,从而覆盖 Flush 之前就失败的分支。
func TestSendWebSocketErrorWriteStringFailure(t *testing.T) {
	wantErr := errors.New("底层连接已断开")
	server := newMockServer()
	raw := &websocketFailWriteConn{mockConn: newMockConn(""), err: wantErr}
	conn := &mockConnection{
		conn:   raw,
		reader: bufio.NewReader(raw),
		writer: bufio.NewWriterSize(raw, 1),
		server: server,
	}

	if err := New(conn, &http.Request{}, false).sendWebSocketError(); !errors.Is(err, wantErr) {
		t.Fatalf("sendWebSocketError = %v, want %v", err, wantErr)
	}
}

// gapSignalSink 在每次收到会话快照时投递一份到 updates。
type gapSignalSink struct {
	updates chan *flow.WSSession
}

func newGapSignalSink() *gapSignalSink {
	return &gapSignalSink{updates: make(chan *flow.WSSession, 16)}
}

func (s *gapSignalSink) RecordWSSession(session *flow.WSSession) {
	select {
	case s.updates <- session:
	default:
	}
}

// drain 取走已投递的快照,返回最后一份(没有则返回 nil)。
func (s *gapSignalSink) drain() *flow.WSSession {
	var last *flow.WSSession
	for {
		select {
		case session := <-s.updates:
			last = session
		default:
			return last
		}
	}
}

// TestResolveProcessAsyncSkipsWithoutRecorderOrConn 覆盖两条不启动解析协程的守卫:
// 未登记会话、以及连接已被换成 nil(GetConn 返回 nil 时若不守卫,取地址会 panic)。
// 这两条路径必须同步返回,否则下面的 drain 会因协程尚未跑完而漏判。
func TestResolveProcessAsyncSkipsWithoutRecorderOrConn(t *testing.T) {
	prevSink, prevResolver := wsSink, processResolver
	t.Cleanup(func() { wsSink, processResolver = prevSink, prevResolver })

	// 零值 Resolver 的 detector 为 nil,Resolve 恒返回 nil:守卫是否生效因而与
	// 平台探测能力无关。
	processResolver = &procinfo.Resolver{}
	sink := newGapSignalSink()
	wsSink = sink
	server := newMockServer()

	noRecorder := New(newMockConnection(newMockConn(""), server), &http.Request{}, false)
	noRecorder.resolveProcessAsync()

	withRecorder := New(&mockConnection{server: server}, &http.Request{}, false)
	withRecorder.recorder = newWSRecorder("ws://example.test/ws")
	sink.drain() // 丢掉 newWSRecorder 的首次登记快照
	withRecorder.resolveProcessAsync()

	if session := sink.drain(); session != nil {
		t.Fatalf("守卫命中时不应推送会话更新: %+v", session)
	}
}

func TestResolveProcessAsyncAttachesProcess(t *testing.T) {
	resolver := procinfo.NewResolver()
	if resolver == nil {
		t.Skip("当前平台无进程检测器")
	}
	prevSink, prevResolver := wsSink, processResolver
	t.Cleanup(func() { wsSink, processResolver = prevSink, prevResolver })
	processResolver = resolver

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	defer listener.Close()
	client, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("拨号失败: %v", err)
	}
	defer client.Close()
	accepted, err := listener.Accept()
	if err != nil {
		t.Fatalf("accept 失败: %v", err)
	}
	defer accepted.Close()

	// accepted 的对端就是本测试进程的客户端 socket,解析结果应指向测试二进制自身。
	if resolver.Resolve(accepted.RemoteAddr(), accepted.LocalAddr()) == nil {
		t.Skip("当前环境无法解析本机回环连接的进程")
	}

	sink := newGapSignalSink()
	wsSink = sink
	processor := New(newMockConnection(accepted, newMockServer()), &http.Request{}, false)
	processor.recorder = newWSRecorder("ws://example.test/ws")
	sink.drain()
	processor.resolveProcessAsync()

	deadline := time.After(3 * time.Second)
	for {
		select {
		case session := <-sink.updates:
			if session.Process == nil {
				continue
			}
			// 对端 socket 属于测试进程自身,解析结果必须精确指向本进程。
			if session.Process.PID != uint32(os.Getpid()) {
				t.Fatalf("回填的进程 PID = %d, want %d", session.Process.PID, os.Getpid())
			}
			return
		case <-deadline:
			t.Fatal("进程信息未回填到 WebSocket 会话")
		}
	}
}

// gapTLSConfig 生成一份仅供本机回环使用的自签服务端证书。
func gapTLSConfig(t *testing.T) *tls.Config {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("生成密钥失败: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("签发证书失败: %v", err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
		NextProtos:   []string{"http/1.1"},
	}
}

// startTLSHandshakeFixture 启动 TLS 上游:完成握手后读掉请求头块并回写 response。
func startTLSHandshakeFixture(t *testing.T, response string) (string, <-chan error) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	deadline := armHandshakeListener(t, listener)
	secure := tls.NewListener(listener, gapTLSConfig(t))
	done := make(chan error, 1)
	go func() {
		conn, err := secure.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		if err := conn.SetDeadline(deadline); err != nil {
			done <- err
			return
		}
		if _, err := readHeadBlock(bufio.NewReader(conn)); err != nil {
			done <- err
			return
		}
		_, err = io.WriteString(conn, response)
		done <- err
	}()
	return listener.Addr().String(), done
}

func TestDialUpstreamFaithfulOverTLS(t *testing.T) {
	const response = "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n"
	addr, serverDone := startTLSHandshakeFixture(t, response)
	server := newMockServer()
	request := websocketRequest(t, addr)
	processor := New(newMockConnection(newMockConn(""), server), request, true)

	conn, reader, respBytes, status, err := processor.dialUpstreamFaithful()
	if err != nil {
		t.Fatalf("wss 握手失败: %v", err)
	}
	defer conn.Close()
	if _, ok := conn.(*tls.Conn); !ok {
		t.Fatalf("wss 上游连接类型 = %T, want *tls.Conn", conn)
	}
	if status != http.StatusSwitchingProtocols || string(respBytes) != response {
		t.Fatalf("上游握手响应 = (%d, %q)", status, respBytes)
	}
	waitHandshakeFixture(t, serverDone)
	// 读缓冲必须接在同一条 TLS 连接上且不含握手响应之外的残留字节;此处不设 deadline,
	// 也间接验证握手期间的 30s 绝对超时已被清除(否则这里会挂到超时而非立刻拿到 EOF)。
	if _, err := reader.Peek(1); !errors.Is(err, io.EOF) {
		t.Fatalf("101 之后的读取结果 = %v, want EOF", err)
	}
}

// startResettingUpstream 启动一个 accept 之后立刻以 RST 断开的上游。
func startResettingUpstream(t *testing.T) (string, *sync.WaitGroup) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	armHandshakeListener(t, listener)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		// SetLinger(0) 让 Close 立即发 RST,而不是走 FIN:对端仍在写的大请求才会中途报错。
		if tcp, ok := conn.(*net.TCPConn); ok {
			_ = tcp.SetLinger(0)
		}
		_ = conn.Close()
	}()
	return listener.Addr().String(), &wg
}

// TestDialUpstreamFaithfulUpstreamWriteFailure 覆盖握手写失败分支:上游收到连接即 RST,
// 而请求头被撑到远超发送缓冲,writeFaithfulHandshake 必然在写到一半时失败。
func TestDialUpstreamFaithfulUpstreamWriteFailure(t *testing.T) {
	addr, serverDone := startResettingUpstream(t)
	server := newMockServer()
	request := websocketRequest(t, addr)
	raw := [][2]string{
		{"Host", addr},
		{"Connection", "Upgrade"},
		{"Upgrade", "websocket"},
		{"X-Padding", strings.Repeat("p", 16<<20)},
	}
	request = request.WithContext(flow.WithRawHeaders(request.Context(), raw))
	processor := New(newMockConnection(newMockConn(""), server), request, false)

	conn, reader, respBytes, status, err := processor.dialUpstreamFaithful()
	if err == nil {
		_ = conn.Close()
		t.Fatal("上游 RST 时握手应失败")
	}
	if conn != nil || reader != nil || respBytes != nil || status != 0 {
		t.Fatalf("失败时返回值 = conn %v, reader %v, response %q, status %d", conn, reader, respBytes, status)
	}
	serverDone.Wait()
}
