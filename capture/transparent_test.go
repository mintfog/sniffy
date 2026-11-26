// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package capture

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/mintfog/sniffy/capture/types"
)

// transparentTestConn 给 net.Pipe 的一端套上透明连接的地址约定。
type transparentTestConn struct {
	net.Conn
	local, remote net.Addr
}

func (c *transparentTestConn) LocalAddr() net.Addr  { return c.local }
func (c *transparentTestConn) RemoteAddr() net.Addr { return c.remote }

func startEchoOrigin(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_, _ = io.Copy(conn, conn)
			}()
		}
	}()
	return ln
}

func newTransparentPipe(t *testing.T, dst net.Addr) (proxySide, appSide net.Conn) {
	t.Helper()
	proxySide, appSide = net.Pipe()
	t.Cleanup(func() { appSide.Close() })
	_ = appSide.SetDeadline(time.Now().Add(10 * time.Second))
	return &transparentTestConn{
		Conn:   proxySide,
		local:  dst,
		remote: &net.TCPAddr{IP: net.IPv4(10, 0, 0, 2), Port: 50000},
	}, appSide
}

// TestServeTransparentTracksConnection：透明连接经监听器转发，受连接跟踪约束并在关闭时被切断，
// 且它的 LocalAddr（原始目标）不计入本机入站端点。
func TestServeTransparentTracksConnection(t *testing.T) {
	origin := startEchoOrigin(t)
	tl := newTestTCPListener(defaultTestConfig())
	tl.SetLogger(&recordingLogger{})
	proxySide, appSide := newTransparentPipe(t, origin.Addr())

	done := make(chan struct{})
	go func() {
		defer close(done)
		tl.ServeTransparent(proxySide)
	}()

	const payload = "\x00\x01opaque"
	if _, err := io.WriteString(appSide, payload); err != nil {
		t.Fatal(err)
	}
	echoed := make([]byte, len(payload))
	if _, err := io.ReadFull(appSide, echoed); err != nil {
		t.Fatalf("读回显: %v", err)
	}
	if string(echoed) != payload {
		t.Fatalf("回显 = %q，期望 %q", echoed, payload)
	}

	tl.connMu.Lock()
	tracked, endpoints := len(tl.conns), len(tl.localEndpoints)
	tl.connMu.Unlock()
	if tracked != 1 || endpoints != 0 {
		t.Fatalf("跟踪中的连接 = %d、入站端点 = %d，期望 1、0", tracked, endpoints)
	}

	tl.closeAllConns()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("关闭监听器后透明连接未结束")
	}
	if _, err := appSide.Read(make([]byte, 1)); err == nil {
		t.Fatal("关闭监听器后应用一侧应读到连接结束")
	}
}

func TestServeTransparentRejectsWhileClosing(t *testing.T) {
	tl := newTestTCPListener(defaultTestConfig())
	tl.closeAllConns()
	conn := newMemoryConn(nil)

	tl.ServeTransparent(conn)

	if !conn.isClosed() {
		t.Fatal("关闭中的监听器应直接关闭交来的连接")
	}
	tl.connMu.Lock()
	tracked := len(tl.conns)
	tl.connMu.Unlock()
	if tracked != 0 {
		t.Fatalf("跟踪中的连接 = %d，期望 0", tracked)
	}
}

// TestDialContextPinsOriginalDst：透明连接上的请求拨它原本的 authority 时，实际连的是应用的原始目标。
func TestDialContextPinsOriginalDst(t *testing.T) {
	origin := startEchoOrigin(t)
	tl := newTestTCPListener(defaultTestConfig())
	const authority = "pinned.invalid:443"
	ctx := types.WithOriginalDst(context.Background(), authority, origin.Addr().String())

	conn, err := tl.DialContext(ctx, "tcp", authority)
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	defer conn.Close()
	if got := conn.RemoteAddr().String(); got != origin.Addr().String() {
		t.Fatalf("实际连到 %s，期望原始目标 %s", got, origin.Addr())
	}
}

// halfCloseConn 在套用透明连接地址约定的同时保留底层 TCP 连接的半关闭。
type halfCloseConn struct {
	*net.TCPConn
	local net.Addr
}

func (c *halfCloseConn) LocalAddr() net.Addr { return c.local }

// TestServeTransparentPropagatesHalfClose：应用发完数据关掉写端后仍在等响应，盲转发要把
// 半关闭传给源站，并把源站随后的响应送回应用。
func TestServeTransparentPropagatesHalfClose(t *testing.T) {
	origin, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer origin.Close()
	go func() {
		conn, err := origin.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		// 读到应用关写端才应答。
		received, _ := io.ReadAll(conn)
		_, _ = io.WriteString(conn, "ACK "+string(received))
	}()

	entry, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer entry.Close()
	tl := newTestTCPListener(defaultTestConfig())
	tl.SetLogger(&recordingLogger{})
	go func() {
		conn, err := entry.Accept()
		if err != nil {
			return
		}
		tl.ServeTransparent(&halfCloseConn{TCPConn: conn.(*net.TCPConn), local: origin.Addr()})
	}()

	app, err := net.Dial("tcp", entry.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	_ = app.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := io.WriteString(app, "\x00payload"); err != nil {
		t.Fatal(err)
	}
	if err := app.(*net.TCPConn).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	response, err := io.ReadAll(app)
	if err != nil {
		t.Fatalf("读响应: %v", err)
	}
	if string(response) != "ACK \x00payload" {
		t.Fatalf("响应 = %q，期望 %q", response, "ACK \x00payload")
	}
}

// TestServeTransparentRelaysAfterEarlyHalfClose：应用建连后没发数据就关了写端，仍在等服务端
// 先说话；连接照样转发到原始目标，服务端的 banner 送回应用。
func TestServeTransparentRelaysAfterEarlyHalfClose(t *testing.T) {
	const banner = "220 ready\r\n"
	origin, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer origin.Close()
	go func() {
		conn, err := origin.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = io.WriteString(conn, banner)
	}()

	entry, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer entry.Close()
	tl := newTestTCPListener(defaultTestConfig())
	tl.SetLogger(&recordingLogger{})
	go func() {
		conn, err := entry.Accept()
		if err != nil {
			return
		}
		tl.ServeTransparent(&halfCloseConn{TCPConn: conn.(*net.TCPConn), local: origin.Addr()})
	}()

	app, err := net.Dial("tcp", entry.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	_ = app.SetDeadline(time.Now().Add(10 * time.Second))
	if err := app.(*net.TCPConn).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	response, err := io.ReadAll(app)
	if err != nil {
		t.Fatalf("读响应: %v", err)
	}
	if string(response) != banner {
		t.Fatalf("响应 = %q，期望 %q", response, banner)
	}
}

// TestServeTransparentThrottlesOnlyCaptured：开着限速时只有要抓取的透明连接受限速约束，所属应用不在
// 抓取范围内的原样转发、不限速。
func TestServeTransparentThrottlesOnlyCaptured(t *testing.T) {
	origin := startEchoOrigin(t)
	SetThrottle(true, 1024)
	t.Cleanup(func() {
		SetThrottle(false, 0)
		SetTransparentFilter(nil)
	})
	// 限速下来回一趟至少 8 秒。
	payload := bytes.Repeat([]byte{0x00, 0x01}, 4<<10)
	for _, capture := range []bool{false, true} {
		SetTransparentFilter(func(net.Addr, net.Addr) (bool, error) { return capture, nil })
		tl := newTestTCPListener(defaultTestConfig())
		tl.SetLogger(&recordingLogger{})
		proxySide, appSide := newTransparentPipe(t, origin.Addr())
		done := make(chan struct{})
		go func() {
			defer close(done)
			tl.ServeTransparent(proxySide)
		}()
		go func() { _, _ = appSide.Write(payload) }()

		_ = appSide.SetReadDeadline(time.Now().Add(time.Second))
		n, err := io.ReadFull(appSide, make([]byte, len(payload)))
		switch {
		case !capture && err != nil:
			t.Errorf("不抓的连接 1 秒内只回显了 %d / %d 字节: %v", n, len(payload), err)
		case capture && err == nil:
			t.Error("要抓取的连接没有受限速约束")
		}
		tl.closeAllConns()
		<-done
	}
}

// TestServeTransparentAsksFilter：过滤器收到的是应用的原始源地址与原始目标地址；查找出错时仍以它给出的
// 结论为准，不抓的连接照样转发到原始目标。
func TestServeTransparentAsksFilter(t *testing.T) {
	origin := startEchoOrigin(t)
	t.Cleanup(func() { SetTransparentFilter(nil) })
	var client, target string
	SetTransparentFilter(func(c, d net.Addr) (bool, error) {
		client, target = c.String(), d.String()
		return false, errors.New("查不到")
	})
	tl := newTestTCPListener(defaultTestConfig())
	tl.SetLogger(&recordingLogger{})
	proxySide, appSide := newTransparentPipe(t, origin.Addr())
	done := make(chan struct{})
	go func() {
		defer close(done)
		tl.ServeTransparent(proxySide)
	}()

	const payload = "\x00\x01opaque"
	if _, err := io.WriteString(appSide, payload); err != nil {
		t.Fatal(err)
	}
	echoed := make([]byte, len(payload))
	if _, err := io.ReadFull(appSide, echoed); err != nil || string(echoed) != payload {
		t.Fatalf("回显 %q, %v，期望 %q", echoed, err, payload)
	}
	tl.closeAllConns()
	<-done
	if client != "10.0.0.2:50000" || target != origin.Addr().String() {
		t.Fatalf("过滤器收到 %s → %s，期望 10.0.0.2:50000 → %s", client, target, origin.Addr())
	}
}
