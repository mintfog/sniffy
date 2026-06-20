// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package websocket

import (
	"bufio"
	"bytes"
	"crypto/sha1"
	"encoding/base64"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	gws "github.com/gorilla/websocket"

	"github.com/mintfog/sniffy/capture/types"
	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/internal/pipeline"
)

const wsMagicGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

func wsAccept(key string) string {
	h := sha1.New()
	h.Write([]byte(key + wsMagicGUID))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// e2eConn 是把已读掉握手头的 bufio.Reader 注入 types.Connection 的测试实现,
// 使后续帧读取沿用同一缓冲(不丢弃已缓冲的帧字节)。
type e2eConn struct {
	conn net.Conn
	br   *bufio.Reader
	srv  types.Server
}

func (c *e2eConn) GetConn() net.Conn        { return c.conn }
func (c *e2eConn) SetConn(n net.Conn)       { c.conn = n }
func (c *e2eConn) GetReader() *bufio.Reader { return c.br }
func (c *e2eConn) GetWriter() *bufio.Writer { return bufio.NewWriter(c.conn) }
func (c *e2eConn) GetServer() types.Server  { return c.srv }
func (c *e2eConn) Close() error             { return c.conn.Close() }

// readHeadBlock 从 br 读出完整的 HTTP 头块(直到 CRLFCRLF),返回原始字节。
func readHeadBlock(br *bufio.Reader) ([]byte, error) {
	var head []byte
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return nil, err
		}
		head = append(head, line...)
		if line == "\r\n" {
			return head, nil
		}
	}
}

// parseHeaderLines 把头块解析为 [][2]string(跳过请求/状态行,保留顺序与大小写)。
func parseHeaderLines(head []byte) [][2]string {
	lines := strings.Split(string(head), "\n")
	out := make([][2]string, 0, len(lines))
	for i, ln := range lines {
		ln = strings.TrimSuffix(ln, "\r")
		if i == 0 || ln == "" {
			continue
		}
		c := strings.IndexByte(ln, ':')
		if c < 0 {
			continue
		}
		out = append(out, [2]string{ln[:c], strings.TrimLeft(ln[c+1:], " \t")})
	}
	return out
}

// startEchoUpstream 启动一个原始 TCP 上游:记录收到的握手头块原文,完成 WS 握手,
// 随后用本包的帧编解码回显数据帧、回应 ping、转发 close。
func startEchoUpstream(t *testing.T) (addr string, gotHead chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	gotHead = make(chan string, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		br := bufio.NewReader(c)
		head, err := readHeadBlock(br)
		if err != nil {
			return
		}
		gotHead <- string(head)
		key := ""
		for _, kv := range parseHeaderLines(head) {
			if strings.EqualFold(kv[0], "Sec-WebSocket-Key") {
				key = kv[1]
			}
		}
		resp := "HTTP/1.1 101 Switching Protocols\r\n" +
			"Upgrade: websocket\r\nConnection: Upgrade\r\n" +
			"Sec-WebSocket-Accept: " + wsAccept(key) + "\r\n\r\n"
		if _, err := c.Write([]byte(resp)); err != nil {
			return
		}
		for {
			fr, err := readFrame(br)
			if err != nil {
				return
			}
			switch fr.opcode {
			case opClose:
				_ = writeFrame(c, fr, false)
				return
			case opPing:
				fr.opcode = opPong
				_ = writeFrame(c, fr, false)
			default:
				_ = writeFrame(c, fr, false) // 回显数据帧(服务端不加掩)
			}
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return ln.Addr().String(), gotHead
}

// startProxy 启动代理监听:读出客户端握手头(原序+大小写),交本包 Processor 以保真方式
// 转发到 upAddr 上游并做帧级代理。
func startProxy(t *testing.T, upAddr string, server types.Server) (proxyAddr string, gotHead chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	gotHead = make(chan string, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		br := bufio.NewReaderSize(c, 64*1024)
		head, err := readHeadBlock(br)
		if err != nil {
			return
		}
		gotHead <- string(head)
		req, err := http.ReadRequest(bufio.NewReader(bytes.NewReader(head)))
		if err != nil {
			return
		}
		req = req.WithContext(flow.WithRawHeaders(req.Context(), parseHeaderLines(head)))
		_ = New(&e2eConn{conn: c, br: br, srv: server}, req, false).Process(server)
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return ln.Addr().String(), gotHead
}

// TestFaithfulWSProxyEndToEnd 端到端校验:
//   - 上游收到的握手头块与客户端发出的逐字一致(顺序/大小写/Sec-WebSocket-Key 不被改写);
//   - 帧经代理双向回显(文本/二进制)内容不变。
func TestFaithfulWSProxyEndToEnd(t *testing.T) {
	prev := activePipeline
	activePipeline = pipeline.New(nil, nil) // 走 pipeline 分支(无钩子=透传),贴近生产
	defer func() { activePipeline = prev }()

	srv := newMockServer()
	upAddr, upHead := startEchoUpstream(t)
	proxyAddr, proxyHead := startProxy(t, upAddr, srv)

	// gorilla 客户端:经 NetDial 连到代理,但握手 URL/Host 指向上游(MITM 场景)。
	dialer := gws.Dialer{
		NetDial:          func(network, addr string) (net.Conn, error) { return net.Dial("tcp", proxyAddr) },
		HandshakeTimeout: 5 * time.Second,
	}
	ws, _, err := dialer.Dial("ws://"+upAddr+"/chat?room=1", nil)
	if err != nil {
		t.Fatalf("gorilla 握手失败: %v", err)
	}
	defer ws.Close()

	// 握手保真:上游收到的头块 == 代理从客户端读到的头块(逐字)。
	ph := <-proxyHead
	uh := <-upHead
	if ph != uh {
		t.Fatalf("握手头未保真转发:\n--- 客户端发出/代理读到 ---\n%q\n--- 上游收到 ---\n%q", ph, uh)
	}

	// 文本帧回显。
	if err := ws.WriteMessage(gws.TextMessage, []byte("hello-ws")); err != nil {
		t.Fatalf("写文本失败: %v", err)
	}
	mt, msg, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("读回显失败: %v", err)
	}
	if mt != gws.TextMessage || string(msg) != "hello-ws" {
		t.Fatalf("文本回显不符: type=%d msg=%q", mt, msg)
	}

	// 二进制帧回显(含非 UTF-8 字节,验证 opcode 被保真,不被当文本)。
	bin := []byte{0x00, 0xff, 0x10, 0x80, 0x7f}
	if err := ws.WriteMessage(gws.BinaryMessage, bin); err != nil {
		t.Fatalf("写二进制失败: %v", err)
	}
	mt, msg, err = ws.ReadMessage()
	if err != nil {
		t.Fatalf("读二进制回显失败: %v", err)
	}
	if mt != gws.BinaryMessage || !bytes.Equal(msg, bin) {
		t.Fatalf("二进制回显不符: type=%d msg=%x", mt, msg)
	}
}
