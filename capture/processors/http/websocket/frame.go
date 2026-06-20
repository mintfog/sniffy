// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package websocket

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
)

// 最小 RFC 6455 帧编解码:供「保真」WebSocket 代理逐帧透传。
// 相比 x/net/websocket 的 Conn(会按 PayloadType 重定文本/二进制、且握手由库合成),
// 这里按帧原样转发,保留 FIN / opcode / 分片边界,只在数据帧上做插件拦截 / 记录。

const (
	opContinuation = 0x0
	opText         = 0x1
	opBinary       = 0x2
	opClose        = 0x8
	opPing         = 0x9
	opPong         = 0xA
)

// maxFramePayload 单帧 payload 上限(防御性,64MiB)。超过即报错关连接。
const maxFramePayload = 64 << 20

// wsFrame 是一个已解掩(若来时带掩码)的 WebSocket 帧。
type wsFrame struct {
	fin     bool
	opcode  byte
	payload []byte
}

// isData 报告是否为数据帧(文本/二进制/续帧)——仅这类参与插件拦截 / 记录。
func (f wsFrame) isData() bool {
	return f.opcode == opContinuation || f.opcode == opText || f.opcode == opBinary
}

// readFrame 从 r 读出一帧;若帧带掩码则就地解掩,返回明文 payload。
func readFrame(r io.Reader) (wsFrame, error) {
	var h [2]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return wsFrame{}, err
	}
	fin := h[0]&0x80 != 0
	opcode := h[0] & 0x0f // RSV 位(扩展)不解析,透明转发
	masked := h[1]&0x80 != 0
	n := uint64(h[1] & 0x7f)
	switch n {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(r, ext[:]); err != nil {
			return wsFrame{}, err
		}
		n = uint64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(r, ext[:]); err != nil {
			return wsFrame{}, err
		}
		n = binary.BigEndian.Uint64(ext[:])
	}
	if n > maxFramePayload {
		return wsFrame{}, fmt.Errorf("websocket: 帧 payload 过大 (%d 字节)", n)
	}
	var mask [4]byte
	if masked {
		if _, err := io.ReadFull(r, mask[:]); err != nil {
			return wsFrame{}, err
		}
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return wsFrame{}, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i&3]
		}
	}
	return wsFrame{fin: fin, opcode: opcode, payload: payload}, nil
}

// writeFrame 把一帧写到 w。mask=true 时按「客户端」角色加掩(发往上游必须加掩);
// mask=false 时按「服务端」角色不加掩(发往客户端必须不加掩)。
func writeFrame(w io.Writer, f wsFrame, mask bool) error {
	b0 := f.opcode
	if f.fin {
		b0 |= 0x80
	}
	hdr := []byte{b0}

	n := len(f.payload)
	var maskBit byte
	if mask {
		maskBit = 0x80
	}
	switch {
	case n < 126:
		hdr = append(hdr, maskBit|byte(n))
	case n <= 0xffff:
		hdr = append(hdr, maskBit|126, byte(n>>8), byte(n))
	default:
		hdr = append(hdr, maskBit|127)
		var ext [8]byte
		binary.BigEndian.PutUint64(ext[:], uint64(n))
		hdr = append(hdr, ext[:]...)
	}

	if !mask {
		if _, err := w.Write(hdr); err != nil {
			return err
		}
		if n == 0 {
			return nil
		}
		_, err := w.Write(f.payload)
		return err
	}

	// 加掩:随机 4 字节掩码键 + 掩码后的 payload。
	var key [4]byte
	if _, err := rand.Read(key[:]); err != nil {
		return err
	}
	hdr = append(hdr, key[:]...)
	if _, err := w.Write(hdr); err != nil {
		return err
	}
	if n == 0 {
		return nil
	}
	masked := make([]byte, n)
	for i := 0; i < n; i++ {
		masked[i] = f.payload[i] ^ key[i&3]
	}
	_, err := w.Write(masked)
	return err
}
