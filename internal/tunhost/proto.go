// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package tunhost

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"
)

// 主进程与 helper 之间的通道协议。主进程监听一个 Unix socket，helper 连入，每条连接以
// preamble 开头：4 字节魔数、1 字节协议版本、1 字节连接类型。helper 与主进程是同一个可执行
// 文件，版本总是一致，不做协商。
//
// 控制连接（每个 helper 一条）：此后双方收发控制消息，每条是 4 字节大端长度加 JSON。主进程先发
// start，helper 开启网卡后回 started 或 error；之后双方每隔 heartbeatInterval 发一次 ping，
// 连续 heartbeatMisses 个周期收不到对方的任何消息即认定对方失联。运行中主进程在出口网卡变了时发
// egress，helper 把不影响继续运行的问题（交接连接失败、更新出口网卡失败）以 warning 回报。主进程
// 关闭写端即要求 helper 收尾：helper 拆掉网卡后才关闭连接，主进程读到 EOF 时路由已经消失。
//
// 交接连接（每条 TCP 流一条）：preamble 之后是定长头，再往后是应用与源站之间的原始字节流，
// 半关闭照常传递。定长头依次是原始源地址、原始目标地址（各为 1 字节地址长度 4 或 16、16 字节
// 地址（IPv4 只用前 4 字节）、2 字节端口），最后是 2 字节的可选字段区长度。可选字段区留给日后
// 由 helper 填写的信息（如进程 PID），每个字段为 1 字节类型、2 字节长度与值，读方跳过不认识的
// 类型；当前版本不写任何字段。
const (
	protoVersion byte = 1

	kindControl byte = 1
	kindHandoff byte = 2

	preambleLen  = 6
	addrPortLen  = 1 + 16 + 2
	handoffLen   = preambleLen + 2*addrPortLen + 2
	maxOptionLen = 4 << 10
	maxMsgLen    = 64 << 10

	heartbeatInterval = time.Second
	heartbeatMisses   = 5
	// heartbeatTimeout 是对方停止响应后多久被认定失联，也是控制消息的写期限。
	heartbeatTimeout = heartbeatMisses * heartbeatInterval
	// handshakeTimeout 限制连上通道、读写 preamble 与交接头的时间，两端在同一台机器上，
	// 正常情况下是微秒级。
	handshakeTimeout = 5 * time.Second
	// startTimeout 是主进程发出 start 后等 helper 开启网卡的时限。Windows 上创建 wintun 网卡
	// 要几秒。
	startTimeout = 30 * time.Second
)

var magic = [4]byte{'s', 'n', 't', 'n'}

func preamble(kind byte) []byte {
	return []byte{magic[0], magic[1], magic[2], magic[3], protoVersion, kind}
}

func checkPreamble(b []byte, kind byte) error {
	if [4]byte(b[:4]) != magic {
		return errors.New("不是 sniffy 的通道连接")
	}
	if b[4] != protoVersion {
		return fmt.Errorf("通道协议版本 %d 与本程序的 %d 不符", b[4], protoVersion)
	}
	if b[5] != kind {
		return fmt.Errorf("连接类型 %d，应为 %d", b[5], kind)
	}
	return nil
}

// encodeHandoff 返回交接连接开头的全部字节：preamble 与定长头，可选字段区为空。
func encodeHandoff(src, dst netip.AddrPort) []byte {
	b := make([]byte, handoffLen)
	copy(b, preamble(kindHandoff))
	putAddrPort(b[preambleLen:], src)
	putAddrPort(b[preambleLen+addrPortLen:], dst)
	return b
}

func putAddrPort(b []byte, ap netip.AddrPort) {
	addr := ap.Addr().Unmap()
	b[0] = byte(addr.BitLen() / 8)
	copy(b[1:17], addr.AsSlice())
	binary.BigEndian.PutUint16(b[17:], ap.Port())
}

// readHandoff 读出交接连接开头的 preamble 与交接头，返回原始源地址与原始目标地址。
func readHandoff(r io.Reader) (src, dst netip.AddrPort, err error) {
	b := make([]byte, handoffLen)
	if _, err := io.ReadFull(r, b); err != nil {
		return src, dst, err
	}
	if err := checkPreamble(b, kindHandoff); err != nil {
		return src, dst, err
	}
	if src, err = parseAddrPort(b[preambleLen:]); err != nil {
		return src, dst, err
	}
	if dst, err = parseAddrPort(b[preambleLen+addrPortLen:]); err != nil {
		return src, dst, err
	}
	optLen := int64(binary.BigEndian.Uint16(b[handoffLen-2:]))
	if optLen > maxOptionLen {
		return src, dst, fmt.Errorf("可选字段区 %d 字节，超过上限", optLen)
	}
	if _, err := io.CopyN(io.Discard, r, optLen); err != nil {
		return src, dst, err
	}
	return src, dst, nil
}

func parseAddrPort(b []byte) (netip.AddrPort, error) {
	var addr netip.Addr
	switch b[0] {
	case 4:
		addr = netip.AddrFrom4([4]byte(b[1:5]))
	case 16:
		addr = netip.AddrFrom16([16]byte(b[1:17]))
	default:
		return netip.AddrPort{}, fmt.Errorf("地址长度 %d 无效", b[0])
	}
	return netip.AddrPortFrom(addr, binary.BigEndian.Uint16(b[17:])), nil
}

const (
	msgStart   = "start"
	msgStarted = "started"
	msgError   = "error"
	msgPing    = "ping"
	// msgStopped 是 helper 自行正常停止（收到退出信号）的通知，随后它拆掉网卡、关闭控制连接。
	msgStopped = "stopped"
	// msgEgress 是主进程在运行中下发的新出口网卡，字段同 start。
	msgEgress = "egress"
	// msgWarning 是 helper 运行中遇到、不影响继续运行的问题，主进程记入日志。
	msgWarning = "warning"
)

// message 是一条控制消息，按 Type 只用到其中的部分字段。
type message struct {
	Type string `json:"type"`

	// start、egress：两个地址族默认路由所在网卡的索引，0 表示该地址族没有出口（IPv6 为 0 时不接管
	// IPv6）；是否拦截 QUIC（只在 start 里）。
	Egress4   int  `json:"egress4,omitempty"`
	Egress6   int  `json:"egress6,omitempty"`
	BlockQUIC bool `json:"blockQUIC,omitempty"`

	// started：虚拟网卡在系统里的名字。
	Name string `json:"name,omitempty"`

	// error：开启失败或网卡失效的原因；warning：问题的描述。
	Error string `json:"error,omitempty"`
}

// control 是控制连接的一端。send 可并发调用，recv 只在一个 goroutine 里调用。
type control struct {
	conn *net.UnixConn
	wmu  sync.Mutex
	// heard 表示上一次心跳检查以来收到过对方的消息，见 heartbeat。
	heard atomic.Bool
}

func (c *control) send(m message) error {
	body, err := json.Marshal(m)
	if err != nil {
		return err
	}
	frame := binary.BigEndian.AppendUint32(make([]byte, 0, 4+len(body)), uint32(len(body)))
	frame = append(frame, body...)
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(heartbeatTimeout))
	_, err = c.conn.Write(frame)
	return err
}

// recv 读一条消息，timeout 内没有读完即返回超时错误；timeout 为 0 时不设期限，对方失联由
// heartbeat 发现，此前要先清掉之前设下的期限。对端关闭连接时返回 io.EOF。
func (c *control) recv(timeout time.Duration) (message, error) {
	if timeout > 0 {
		_ = c.conn.SetReadDeadline(time.Now().Add(timeout))
	}
	var size [4]byte
	if _, err := io.ReadFull(c.conn, size[:]); err != nil {
		return message{}, err
	}
	n := binary.BigEndian.Uint32(size[:])
	if n > maxMsgLen {
		return message{}, fmt.Errorf("控制消息 %d 字节，超过上限", n)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(c.conn, body); err != nil {
		return message{}, err
	}
	c.heard.Store(true)
	var m message
	if err := json.Unmarshal(body, &m); err != nil {
		return message{}, fmt.Errorf("控制消息: %w", err)
	}
	return m, nil
}

// heartbeat 每隔 heartbeatInterval 发一次 ping 并调用一次 tick，直到 stop 关闭或认定对方
// 失联。连续 heartbeatMisses 个周期都没收到对方的任何消息即认定失联，让阻塞中的 recv 以超时错误
// 返回。发送失败（如本端已关闭写端，在等对方收尾）后不再发 ping，照常判断对方是否失联。
//
// 失联按心跳周期计数，运行阶段的 recv 不能设读期限：Windows 上 Go 的单调时钟在系统休眠期间照走，
// 唤醒时读期限早已过去，双方会在收到对方的 ping 之前互判失联；计时器在休眠中错过的周期，唤醒后
// 只补一次。
func (c *control) heartbeat(stop <-chan struct{}, tick func()) {
	t := time.NewTicker(heartbeatInterval)
	defer t.Stop()
	c.beat(t.C, stop, tick)
}

// beat 即 heartbeat，每从 ticks 收到一次算一个周期，测试直接驱动它。
func (c *control) beat(ticks <-chan time.Time, stop <-chan struct{}, tick func()) {
	missed, sending := 0, true
	for {
		select {
		case <-stop:
			return
		case <-ticks:
		}
		if c.heard.Swap(false) {
			missed = 0
		} else {
			missed++
		}
		if missed >= heartbeatMisses {
			_ = c.conn.SetReadDeadline(time.Now())
			return
		}
		if sending && c.send(message{Type: msgPing}) != nil {
			sending = false
		}
		tick()
	}
}

func isTimeout(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}
