// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package app

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"sync"
	"time"

	gws "github.com/gorilla/websocket"

	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/internal/pipeline"
	"github.com/mintfog/sniffy/internal/service"
)

// maxComposeWS 是构造器可同时保持的出站连接数上限。
const maxComposeWS = 16

// composeWSWriteWait 是单次写的截止时间，保护 Bridge 调用线程。
const composeWSWriteWait = 10 * time.Second

// composeWSReadLimit 是单帧入站上限；会话时间线另有保留策略。
const composeWSReadLimit = 8 << 20

// composeWSWriteLimit 是单帧出站载荷上限，与入站上限对称。
const composeWSWriteLimit = composeWSReadLimit

// composeWSHandshakeTimeout 是构造器握手的最大等待时间。
const composeWSHandshakeTimeout = 15 * time.Second

// maxControlFramePayload 是 RFC 6455 §5.5 定义的控制帧载荷上限。
const maxControlFramePayload = 125

// composeWSPongWait 是读超时；在该时间内未收到数据或 pong 时结束连接。
const composeWSPongWait = 90 * time.Second

// composeWSPingPeriod 是心跳周期，短于 composeWSPongWait；隔离测试可缩短周期验证定时器路径。
var composeWSPingPeriod = 30 * time.Second

// composeWSHopHeaders 是由 Dialer 独占的握手头。
var composeWSHopHeaders = map[string]bool{
	"Upgrade":                  true,
	"Connection":               true,
	"Sec-Websocket-Key":        true,
	"Sec-Websocket-Version":    true,
	"Sec-Websocket-Extensions": true,
}

// composeWSConn 是一条出站 WebSocket 连接及其会话记录。
//
// gorilla Conn 支持一个并发读者和一个并发写者；写操作由 writeMu 串行化。
type composeWSConn struct {
	conn    *gws.Conn
	writeMu sync.Mutex

	mu      sync.Mutex // 保护 session:readLoop 与 UI 调用并发写入
	session *flow.WSSession

	svc      *service.Service
	pipe     *pipeline.Pipeline // nil 表示不过管道
	log      *Logger
	registry *composeWSRegistry
	done     sync.Once
	// closed 在 finish 里关闭,用来叫停心跳 goroutine。
	closed chan struct{}
}

// composeWSRegistry 以会话 ID 索引出站连接，零值可用；名额覆盖握手中和已建立的连接。
type composeWSRegistry struct {
	mu    sync.Mutex
	dials map[string]context.CancelFunc // 握手中:cancel 用于窗口关闭时就地取消拨号
	conns map[string]*composeWSConn
}

// reserve 在拨号前占用名额并登记取消函数；达到上限时返回错误。
func (r *composeWSRegistry) reserve(id string, cancel context.CancelFunc) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.dials)+len(r.conns) >= maxComposeWS {
		return fmt.Errorf("出站 WebSocket 连接数已达上限 %d,请先关闭一些连接", maxComposeWS)
	}
	if r.dials == nil {
		r.dials = make(map[string]context.CancelFunc)
	}
	r.dials[id] = cancel
	return nil
}

// release 释放尚未绑定连接的预留名额。
func (r *composeWSRegistry) release(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.dials, id)
}

// bind 将握手成功的连接绑定到预留名额；名额已回收时返回 false。
func (r *composeWSRegistry) bind(c *composeWSConn) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	id := c.session.ID
	if _, ok := r.dials[id]; !ok {
		return false
	}
	delete(r.dials, id)
	if r.conns == nil {
		r.conns = make(map[string]*composeWSConn)
	}
	r.conns[id] = c
	return true
}

func (r *composeWSRegistry) get(id string) *composeWSConn {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.conns[id]
}

func (r *composeWSRegistry) remove(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.conns, id)
}

func (r *composeWSRegistry) closeAll() {
	r.mu.Lock()
	all := make([]*composeWSConn, 0, len(r.conns))
	for _, c := range r.conns {
		all = append(all, c)
	}
	dialing := make([]context.CancelFunc, 0, len(r.dials))
	for _, cancel := range r.dials {
		dialing = append(dialing, cancel)
	}
	// 已建立条目由 readLoop 在退出时摘除；握手中的条目在此处清理。
	r.dials = nil
	r.mu.Unlock()
	for _, cancel := range dialing {
		cancel()
	}
	for _, c := range all {
		c.shutdown()
	}
}

// OpenWebSocket 按 spec 建立出站 WebSocket，登记 WSSession 并广播，返回会话 ID。
// 返回的 ID 与 ws_message 事件中的 WSSession.id 对应，供 UI 关联连接与事件。
//
// 握手头只有值字节保真：头名经 CanonicalMIMEHeaderKey 规范化后存入 http.Header，跨名顺序不保留，
// HTTP 重放侧的原始头序列在 WS 握手上没有对应物。
//
// spec.ViaPipeline 只作用于连接建立后的双向逐帧 OnWebSocketMessage，握手本身不过管道。
func (a *App) OpenWebSocket(spec flow.RequestSpec) (string, error) {
	target, err := composeWSURL(spec.URL)
	if err != nil {
		return "", err
	}
	// 先解析头部字节旁路，再交给 composeWSHeaders 校验与构造握手请求。
	restored, err := a.restoreComposedHeaders(spec)
	if err != nil {
		return "", err
	}
	header, err := composeWSHeaders(restored)
	if err != nil {
		return "", err
	}

	// 会话 ID 在拨号前生成，并与取消函数一起登记名额。
	id := flow.NewID()
	// 拨号结束后 cancel 仅回收握手上下文；已建立连接由自身生命周期管理。
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := a.outWS.reserve(id, cancel); err != nil {
		return "", err
	}

	dialer := &gws.Dialer{
		Proxy:            a.composeWSProxy,
		TLSClientConfig:  &tls.Config{InsecureSkipVerify: true}, // 与全仓出站 TLS 一致:代理能抓的站点构造器就该能连
		HandshakeTimeout: composeWSHandshakeTimeout,
	}
	// DialContext 支持 closeAll 取消握手上下文。
	conn, resp, err := dialer.DialContext(ctx, target, header)
	if err != nil {
		a.outWS.release(id)
		if resp != nil {
			a.logDebug("出站 WebSocket 握手被拒: %s %s header=%v", target, resp.Status, resp.Header)
			return "", fmt.Errorf("WebSocket 握手失败(%s): %w", resp.Status, err)
		}
		return "", fmt.Errorf("WebSocket 连接失败: %w", err)
	}
	conn.SetReadLimit(composeWSReadLimit)
	// 读超时与心跳由 readLoop goroutine 统一维护，回调与 SetReadDeadline 在同一执行序列。
	_ = conn.SetReadDeadline(time.Now().Add(composeWSPongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(composeWSPongWait))
	})
	if resp != nil {
		a.logDebug("出站 WebSocket 已建立: %s %s header=%v", target, resp.Status, resp.Header)
	}

	c := &composeWSConn{
		conn:   conn,
		closed: make(chan struct{}),
		session: &flow.WSSession{
			ID:        id,
			URL:       target,
			Status:    "open",
			StartTime: time.Now(),
			Messages:  make([]flow.WSMessage, 0, 16),
		},
		svc:      a.Service,
		log:      a.Logger,
		registry: &a.outWS,
	}
	if spec.ViaPipeline {
		c.pipe = a.Pipeline
	}
	// closeAll 回收名额后，握手得到的连接立即关闭。
	if !a.outWS.bind(c) {
		_ = conn.Close()
		return "", errors.New("出站 WebSocket 已被关闭")
	}
	c.svc.ImportWSSession(c.snapshot(), nil)
	go c.readLoop()
	go c.heartbeat()
	return c.session.ID, nil
}

// SendWSMessage 在已建立的连接上发一帧。
// msgType 取 flow.WSText、WSBinary 或 WSPing；binary/ping 的 data 使用 base64，text 使用原文。
// ping 作为控制帧发送且不记录到 Messages；连接不存在或已关闭时返回错误。
func (a *App) SendWSMessage(flowID, msgType, data string) error {
	c := a.outWS.get(flowID)
	if c == nil {
		return errors.New("WebSocket 连接不存在或已关闭")
	}
	typ := strings.ToLower(strings.TrimSpace(msgType))
	if typ == "" {
		typ = flow.WSText
	}
	var payload []byte
	switch typ {
	case flow.WSText:
		payload = []byte(data)
	case flow.WSBinary, flow.WSPing:
		b, err := base64.StdEncoding.DecodeString(data)
		if err != nil {
			return fmt.Errorf("%s 帧的 data 需为 base64: %w", typ, err)
		}
		payload = b
	case flow.WSClose:
		return errors.New("关闭连接请用 CloseWebSocket")
	default:
		return fmt.Errorf("不支持的消息类型: %s", msgType)
	}
	// 载荷上限按 base64 解码后的字节数校验。
	if len(payload) > composeWSWriteLimit {
		return fmt.Errorf("单帧载荷 %d 字节超过上限 %d", len(payload), composeWSWriteLimit)
	}

	if typ == flow.WSPing {
		if len(payload) > maxControlFramePayload {
			return fmt.Errorf("ping 载荷 %d 字节超过控制帧上限 %d", len(payload), maxControlFramePayload)
		}
		return c.writeControl(gws.PingMessage, payload)
	}
	opcode := gws.TextMessage
	if typ == flow.WSBinary {
		opcode = gws.BinaryMessage
	}
	// 先经过插件管道，再发送和记录最终载荷。
	out, ok := c.applyPipeline(flow.WSClientToServer, typ, payload)
	if !ok {
		return nil
	}
	if err := c.write(opcode, out); err != nil {
		return err
	}
	c.record(flow.WSClientToServer, typ, out)
	return nil
}

// CloseWebSocket 发送正常关闭帧并断开；连接已不在表中时返回 nil。
func (a *App) CloseWebSocket(flowID string) error {
	c := a.outWS.get(flowID)
	if c == nil {
		return nil
	}
	c.shutdown()
	return nil
}

// CloseAllWebSockets 关闭全部出站连接(构造器窗口关闭 / 进程退出兜底)。
func (a *App) CloseAllWebSockets() { a.outWS.closeAll() }

// composeWSProxy 为 gorilla Dialer 提供当前上游代理；socks5h 按 socks5 处理。
func (a *App) composeWSProxy(*http.Request) (*url.URL, error) {
	u := a.Engine.UpstreamProxyURL()
	if u == nil {
		return nil, nil
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "socks5":
		return u, nil
	case "socks5h":
		cp := *u
		cp.Scheme = "socks5"
		return &cp, nil
	default:
		return nil, fmt.Errorf("上游代理协议 %s 不被 WebSocket 客户端支持", u.Scheme)
	}
}

// composeWSURL 把用户输入归一为 gorilla 认得的 ws/wss URL。
func composeWSURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("WebSocket URL 为空")
	}
	// 裸 host/path 使用 wss 作为默认协议。
	if !strings.Contains(raw, "://") {
		raw = "wss://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("URL 无法解析: %w", err)
	}
	switch strings.ToLower(u.Scheme) {
	case "ws", "wss":
		u.Scheme = strings.ToLower(u.Scheme)
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	default:
		return "", fmt.Errorf("不支持的协议: %s", u.Scheme)
	}
	if u.Host == "" {
		return "", fmt.Errorf("URL 缺少主机名: %s", raw)
	}
	// URL 不允许包含 userinfo。
	if u.User != nil {
		return "", errors.New("WebSocket URL 不支持内嵌用户名密码,请改用 Authorization 头")
	}
	u.Fragment = ""
	return u.String(), nil
}

// composeWSHeaders 将有序头转换为握手附加头，并移除由 Dialer 生成的头。
// Host 通过 req.Host 传递，拨号目标与 SNI 仍取自 URL。
func composeWSHeaders(pairs [][2]string) (http.Header, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	if err := flow.ValidateHeaderPairs(pairs); err != nil {
		return nil, err
	}
	h := make(http.Header, len(pairs))
	for _, kv := range pairs {
		name := strings.TrimSpace(kv[0])
		if name == "" {
			continue
		}
		ck := textproto.CanonicalMIMEHeaderKey(name)
		if composeWSHopHeaders[ck] {
			continue
		}
		if ck == "Host" {
			// Host 使用最后一个非空值，与 HTTP 构造器保持一致。
			if v := strings.TrimSpace(kv[1]); v != "" {
				h.Set(ck, v)
			}
			continue
		}
		h.Add(ck, kv[1])
	}
	if len(h) == 0 {
		return nil, nil
	}
	return h, nil
}

// readLoop 是本连接唯一的读者；退出时完成会话收口并发布 closed 快照。
func (c *composeWSConn) readLoop() {
	defer c.finish()
	for {
		mt, data, err := c.conn.ReadMessage()
		if err != nil {
			c.logDebug("出站 WebSocket 读结束: %v", err)
			return
		}
		// 收到数据帧时刷新读超时。
		_ = c.conn.SetReadDeadline(time.Now().Add(composeWSPongWait))
		typ := flow.WSBinary
		if mt == gws.TextMessage {
			typ = flow.WSText
		}
		out, ok := c.applyPipeline(flow.WSServerToClient, typ, data)
		if !ok {
			continue
		}
		c.record(flow.WSServerToClient, typ, out)
	}
}

// write 发送一帧数据；SetWriteDeadline 与 WriteMessage 在同一 writeMu 临界区执行。
func (c *composeWSConn) write(mt int, data []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := c.conn.SetWriteDeadline(time.Now().Add(composeWSWriteWait)); err != nil {
		return err
	}
	return c.conn.WriteMessage(mt, data)
}

// writeControl 发送控制帧，并与数据帧共享 writeMu。
func (c *composeWSConn) writeControl(mt int, data []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.conn.WriteControl(mt, data, time.Now().Add(composeWSWriteWait))
}

// applyPipeline 把一帧送进插件管道,返回(可能被改写的)载荷与是否放行。pipe 为 nil 时原样放行。
func (c *composeWSConn) applyPipeline(direction, typ string, data []byte) ([]byte, bool) {
	if c.pipe == nil {
		return data, true
	}
	c.mu.Lock()
	id, url := c.session.ID, c.session.URL
	c.mu.Unlock()
	m := &flow.WSMessage{
		ID:        flow.NewID(),
		FlowID:    id,
		URL:       url,
		Direction: direction,
		Type:      typ,
		Data:      append([]byte(nil), data...),
		Timestamp: time.Now(),
	}
	if d := c.pipe.OnWebSocketMessage(context.Background(), m); d.Kind == flow.Abort {
		c.logDebug("出站 WebSocket 消息被插件 abort: %s", d.Reason)
		return nil, false
	}
	return m.Data, true
}

func (c *composeWSConn) record(direction, typ string, data []byte) {
	c.mu.Lock()
	s := c.session
	payload, size := flow.RetainPayload(data)
	s.MessageCount++
	s.TotalSize += size
	m := flow.WSMessage{
		ID:        flow.NewID(),
		FlowID:    s.ID,
		URL:       s.URL,
		Direction: direction,
		Type:      typ,
		Data:      payload,
		Timestamp: time.Now(),
		Size:      size,
	}
	s.Messages = flow.TrimWSMessages(append(s.Messages, m))
	snap := c.snapshotLocked()
	c.mu.Unlock()
	c.svc.ImportWSSession(snap, &m)
}

// heartbeat 按周期发送 ping，直到 finish 关闭 closed。
func (c *composeWSConn) heartbeat() {
	t := time.NewTicker(composeWSPingPeriod)
	defer t.Stop()
	for {
		select {
		case <-c.closed:
			return
		case <-t.C:
			if err := c.writeControl(gws.PingMessage, nil); err != nil {
				// 心跳发送失败时关闭底层连接，由 readLoop 完成收尾。
				c.logDebug("出站 WebSocket 心跳失败: %v", err)
				_ = c.conn.Close()
				return
			}
		}
	}
}

// shutdown 发送正常关闭帧并断开底层连接，由 readLoop 完成收尾。
func (c *composeWSConn) shutdown() {
	_ = c.writeControl(gws.CloseMessage, gws.FormatCloseMessage(gws.CloseNormalClosure, ""))
	_ = c.conn.Close()
}

// finish 标记会话关闭、摘除注册表条目并推送最终快照，由 readLoop 调用一次。
func (c *composeWSConn) finish() {
	c.done.Do(func() {
		close(c.closed)
		c.mu.Lock()
		now := time.Now()
		c.session.EndTime = &now
		c.session.Status = "closed"
		snap := c.snapshotLocked()
		id := c.session.ID
		c.mu.Unlock()
		c.registry.remove(id)
		c.svc.ImportWSSession(snap, nil)
	})
}

func (c *composeWSConn) snapshot() *flow.WSSession {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.snapshotLocked()
}

func (c *composeWSConn) snapshotLocked() *flow.WSSession {
	s := c.session
	cp := *s
	cp.Messages = make([]flow.WSMessage, len(s.Messages))
	copy(cp.Messages, s.Messages)
	if s.Process != nil {
		p := *s.Process
		cp.Process = &p
	}
	if s.EndTime != nil {
		t := *s.EndTime
		cp.EndTime = &t
	}
	return &cp
}

func (c *composeWSConn) logDebug(msg string, args ...any) {
	if c.log != nil {
		c.log.Debug(msg, args...)
	}
}

func (a *App) logDebug(msg string, args ...any) {
	if a.Logger != nil {
		a.Logger.Debug(msg, args...)
	}
}
