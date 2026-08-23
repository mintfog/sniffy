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

// maxComposeWS 是构造器可同时保持的出站连接数上限。UI 窗口可能在不通知 Go 的情况下被销毁,
// 遗留连接只能靠这个上限与进程退出兜底,故取一个手工调试足够的小值。
const maxComposeWS = 16

// composeWSWriteWait 是单次写的期限:防止一个不读的对端把 TCP 发送缓冲填满后
// 永久卡住调用线程(Bridge 调用跑在 UI 线程上)。
const composeWSWriteWait = 10 * time.Second

// composeWSReadLimit 是单帧上限(8 MiB)。gorilla 默认无上限,一个多话的服务端能把内存打满。
// 会话时间线那侧另有保留策略兜底(flow/retention.go),但那是「留多少」,这里是「收不收」。
const composeWSReadLimit = 8 << 20

// composeWSWriteLimit 是单帧出站载荷上限,与入站的 composeWSReadLimit 对称。
// 一帧从入口到界面要被复制好几遍(base64 解码 → 过管道的副本 → 会话副本 → 每个窗口一份 DTO),
// 内存放大是帧长的数倍。上限必须落在这一层:桌面 Bridge 直接调 SendWSMessage,API 层的
// 请求体上限管不到它。
const composeWSWriteLimit = composeWSReadLimit

// composeWSHandshakeTimeout 比捕获侧的 30s 短:那边是客户端自己也在等的透传场景,
// 这里是用户点了「连接」在看着,15s 之后再等没有意义。
const composeWSHandshakeTimeout = 15 * time.Second

// maxControlFramePayload 是控制帧载荷的协议上限(RFC 6455 §5.5)。gorilla 超限只回一句
// "invalid control frame",在入口按字节数说清楚才知道该怎么改。
const maxControlFramePayload = 125

// composeWSPongWait 是读超时:对端在这段时间里既没发帧也没回 pong,就当它已经走了。
//
// 出站连接活在后端、UI 只是看客:桌面侧靠窗口关闭钩子收口,headless 那边没有对应物,
// 而半开的 TCP(对端进程消失、NAT 表项过期)连读错误都不会有。没有这道超时,
// 这类连接就只能挂到进程退出,一直占着 maxComposeWS 的名额。
const composeWSPongWait = 90 * time.Second

// composeWSPingPeriod 必须明显短于 composeWSPongWait,否则 ping 还没发出去读就先超时了。
// 90/30 留出三次机会,比 gorilla 示例的 60/54 宽容 —— 宁可晚一点回收,也别误杀安静的服务端。
const composeWSPingPeriod = 30 * time.Second

// composeWSHopHeaders 是握手阶段必须由 Dialer 独占的头:用户若在构造器里写了同名头,
// gorilla 会以 duplicate header not allowed 拒绝整次拨号。
var composeWSHopHeaders = map[string]bool{
	"Upgrade":                  true,
	"Connection":               true,
	"Sec-Websocket-Key":        true,
	"Sec-Websocket-Version":    true,
	"Sec-Websocket-Extensions": true,
}

// composeWSConn 是一条出站 WebSocket 连接及其会话记录。
//
// gorilla 的 Conn 支持「一个并发读者 + 一个并发写者」:读只发生在本连接私有的 readLoop 里,
// 写来自任意 UI 调用线程,故必须用 writeMu 串行化(SetWriteDeadline 也算写方法);
// Close 与 WriteControl 是文档明确的例外,可与其它方法并发,所以主动断开用 Close 唤醒读循环,
// 而不是从别的 goroutine 去设读超时。
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

// composeWSRegistry 以会话 ID 索引出站连接,零值可用。
//
// 名额分两段占:握手中的进 dials,握手成功后转入 conns。上限按两者之和算 —— 若等握手成功
// 再检查,并发的 OpenWebSocket 会各自先拨号(最长 15 秒)再排队,上限就只约束了「同时活着的
// 连接数」,约束不住「同时占着的 socket 数」。
type composeWSRegistry struct {
	mu    sync.Mutex
	dials map[string]context.CancelFunc // 握手中:cancel 用于窗口关闭时就地取消拨号
	conns map[string]*composeWSConn
}

// reserve 在拨号前占一个名额并登记取消函数。返回错误表示已达上限,调用方不应发起拨号。
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

// release 退还一个没能建立起来的名额。
func (r *composeWSRegistry) release(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.dials, id)
}

// bind 把握手成功的连接接到它预留的名额上。名额已被 closeAll 收走时返回 false ——
// 此时窗口已经关了,再没人会持有这个 ID,连接必须由调用方就地关掉,否则就是一条谁也够不着的活连接。
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
	// 已建立的条目不在这里清空:由各自的 readLoop 在退出时摘除(单一删除点)。握手中的没有
	// readLoop 收尾,只能在这里摘 —— 摘掉之后它的 bind 会失败,拨号即便成功也会被就地关掉。
	r.dials = nil
	r.mu.Unlock()
	for _, cancel := range dialing {
		cancel()
	}
	for _, c := range all {
		c.shutdown()
	}
}

// OpenWebSocket 按 spec 拨一条出站 WebSocket,登记为一条 WSSession 并广播,返回会话 ID。
//
// 返回的 ID 就是 ws_message 事件里 WSSession.id —— 整个 UI 的认领逻辑都挂在这条等式上,
// 改实现时不要让它们脱钩。
//
// 握手同步完成:失败即返回错误且不留下任何会话记录(尤其不能留下 status=open 的空壳),
// 与 SendRequest 对「URL 无法解析」的处理一致 —— 用户点了连接就该当场看到失败原因。
//
// 与 HTTP 那侧不同,握手报文由 gorilla 经 req.Write 输出,头名被规范化、顺序被 net/http 排序,
// 「所见即所发」在 WS 上做不到;要做到得放弃 gorilla 自写握手 + 复用帧编解码,成本远高于收益。
//
// spec.ViaPipeline 在 WS 上只覆盖逐帧的 OnWebSocketMessage:握手由 Dialer 直发,不过
// OnRequest / OnResponse —— 与捕获侧同构(processor 在 handleRequest 之前就把升级请求
// 分流给 websocket 包)。规则引擎与断点都只挂在 OnRequest/OnResponse 上,故对 WS 不生效,
// UI 的开关提示据此另写了一版(compose.status.pipelineHintWs)。
func (a *App) OpenWebSocket(spec flow.RequestSpec) (string, error) {
	target, err := composeWSURL(spec.URL)
	if err != nil {
		return "", err
	}
	header, err := composeWSHeaders(spec.Headers)
	if err != nil {
		return "", err
	}

	// 会话 ID 在拨号前就定下来:名额要连着取消函数一起登记,而登记的键就是它。
	id := flow.NewID()
	// 拨号结束后 cancel 只是回收 ctx:gorilla 在握手成功后已把连接期限清掉,也没有 goroutine
	// 还看着这个 ctx,取消它不会影响已建立的连接(见 DialContext 结尾的 SetDeadline(零值))。
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
	// DialContext 而非 Dial:窗口关闭时 closeAll 取消 ctx,TCP 连接与 TLS 握手当场中断,
	// 不必干等 15 秒的握手超时。
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
	// 读超时 + 心跳(见 composeWSPongWait)。两个回调都只在 readLoop 里被 gorilla 调用,
	// 与 SetReadDeadline 同一个 goroutine,不必加锁。
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
	// 握手与 closeAll 撞上了:名额已被收走,这条连接再没人持有,就地关掉而不是留成孤儿。
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
// msgType 取 flow.WSText / WSBinary / WSPing;binary 与 ping 的 data 为 base64,text 为原文
// —— 与回程 WSMessageDTO.Data 的编码约定对称。
// ping 走控制帧且不记入 Messages:捕获侧也只记数据帧,而 WSSessionDTO 会把非 text 的一切
// 压成 "binary",混进去只会变成一条看不懂的空帧。
// 连接不存在或已关闭返回错误;关闭请用 CloseWebSocket。
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
	// 校验放在解码之后:base64 的长度不等于载荷长度,只有解出来才知道真正要发多少字节。
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
	// 先过管道再上线:插件对 client->server 的改写必须影响真正发出的字节,
	// 否则会话里记的和线上跑的是两份内容。
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

// CloseWebSocket 发送正常关闭帧并断开。连接已不在表中时返回 nil:窗口卸载会批量兜底关闭,
// 重复关闭不是错误。
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

// composeWSProxy 为 gorilla Dialer 提供上游代理。
//
// gorilla 内建的代理拨号器只认 http / socks5,与 net/http.Transport 支持的集合不同。
// socks5h 的差别仅在域名由谁解析,退到 socks5 语义等价;https(到代理本身也 TLS)无从表达,
// 如实报错胜过静默直连。每次现读引擎的原子指针,保持上游代理运行时即时切换的语义。
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
	// 裸 host/path 补 wss 而非 ws:猜错时握手立刻失败可见,反过来会把本该加密的流量明文发出去。
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
	// gorilla 直接拒带 userinfo 的 URL,提前给一句能看懂的话。
	if u.User != nil {
		return "", errors.New("WebSocket URL 不支持内嵌用户名密码,请改用 Authorization 头")
	}
	u.Fragment = ""
	return u.String(), nil
}

// composeWSHeaders 把构造器给的有序头装成握手附加头,剔除必须由 Dialer 独占的那几个。
// Sec-WebSocket-Protocol 留在头里即可,不要同时设 Dialer.Subprotocols(那会写出第二份)。
//
// Host 必须留着:gorilla 对它有专门分支(client.go 的 `case k == "Host"` → req.Host),
// 拨号目标与 SNI 仍取自 URL,虚拟主机 / 按 Host 签名的场景正是靠它才成立。
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
			// gorilla 只认第一个值,而 HTTP 侧的构造器是「最后一条非空 Host 生效」;
			// 用 Set 覆盖以对齐两条路径。留空则不写,交回 URL 里的主机名。
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

// readLoop 是本连接唯一的读者。退出即终态:注册表条目只在这里摘除(单一删除点),
// 保证 UI 一定收到一份 status:"closed" 的最终快照。
func (c *composeWSConn) readLoop() {
	defer c.finish()
	for {
		mt, data, err := c.conn.ReadMessage()
		if err != nil {
			c.logDebug("出站 WebSocket 读结束: %v", err)
			return
		}
		// 收到任何数据帧同样算「对端还在」:不回 pong 但一直在推数据的服务端不该被超时收掉。
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

// write 发一帧数据。SetWriteDeadline 算写方法,必须与 WriteMessage 同处 writeMu 内。
func (c *composeWSConn) write(mt int, data []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := c.conn.SetWriteDeadline(time.Now().Add(composeWSWriteWait)); err != nil {
		return err
	}
	return c.conn.WriteMessage(mt, data)
}

// writeControl 发一帧控制帧。WriteControl 自带 deadline 参数且可与其它方法并发,
// 但仍走 writeMu:与数据帧交错写会把两条帧的字节掺在一起。
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

// heartbeat 定期发 ping,给读超时(composeWSPongWait)喂「对端还在」的证据。
// 只由 finish 关闭 closed 叫停,故与 readLoop 一一对应,不会漏下。
func (c *composeWSConn) heartbeat() {
	t := time.NewTicker(composeWSPingPeriod)
	defer t.Stop()
	for {
		select {
		case <-c.closed:
			return
		case <-t.C:
			if err := c.writeControl(gws.PingMessage, nil); err != nil {
				// 连 ping 都写不出去,这条连接已经废了:关掉底层,让 readLoop 走正常收尾。
				c.logDebug("出站 WebSocket 心跳失败: %v", err)
				_ = c.conn.Close()
				return
			}
		}
	}
}

// shutdown 发正常关闭帧并断开底层连接,由此唤醒 readLoop —— 终态一律由 finish 落盘。
func (c *composeWSConn) shutdown() {
	_ = c.writeControl(gws.CloseMessage, gws.FormatCloseMessage(gws.CloseNormalClosure, ""))
	_ = c.conn.Close()
}

// finish 收口:标记会话关闭、摘除注册表条目、推最终快照。只由 readLoop 调用一次。
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
