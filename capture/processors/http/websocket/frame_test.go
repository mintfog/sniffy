// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package websocket

import (
	"bytes"
	"testing"
)

// TestFrameRoundTrip 校验各种长度/掩码组合下,写出再读回的帧与原帧一致。
func TestFrameRoundTrip(t *testing.T) {
	sizes := []int{0, 1, 125, 126, 127, 1000, 0xffff, 0x10000}
	for _, n := range sizes {
		payload := make([]byte, n)
		for i := range payload {
			payload[i] = byte(i)
		}
		for _, masked := range []bool{false, true} {
			in := wsFrame{fin: true, opcode: opBinary, payload: payload}
			var buf bytes.Buffer
			if err := writeFrame(&buf, in, masked); err != nil {
				t.Fatalf("writeFrame(n=%d,mask=%v): %v", n, masked, err)
			}
			got, err := readFrame(&buf)
			if err != nil {
				t.Fatalf("readFrame(n=%d,mask=%v): %v", n, masked, err)
			}
			if got.fin != in.fin || got.opcode != in.opcode || !bytes.Equal(got.payload, payload) {
				t.Fatalf("帧往返不一致 n=%d mask=%v: got fin=%v op=%x len=%d",
					n, masked, got.fin, got.opcode, len(got.payload))
			}
			if buf.Len() != 0 {
				t.Fatalf("读回后应无残留字节, 实剩 %d (n=%d,mask=%v)", buf.Len(), n, masked)
			}
		}
	}
}

// TestFrameMaskedWireFormat 校验加掩帧的线缆格式:MASK 位置位、含 4 字节键、payload 被掩。
func TestFrameMaskedWireFormat(t *testing.T) {
	payload := []byte("hello")
	var buf bytes.Buffer
	if err := writeFrame(&buf, wsFrame{fin: true, opcode: opText, payload: payload}, true); err != nil {
		t.Fatalf("writeFrame: %v", err)
	}
	b := buf.Bytes()
	if b[0] != 0x81 { // FIN + text
		t.Fatalf("首字节应为 0x81, 实得 0x%02x", b[0])
	}
	if b[1]&0x80 == 0 {
		t.Fatalf("加掩帧应置 MASK 位")
	}
	if int(b[1]&0x7f) != len(payload) {
		t.Fatalf("长度位应为 %d", len(payload))
	}
	// 掩码后的 payload 不应等于明文(键非全零的概率极高;此处仅做基本健全性)。
	masked := b[6:]
	if bytes.Equal(masked, payload) {
		t.Fatalf("payload 应已被掩码")
	}
}

// TestFrameUnmaskedNoKey 校验不加掩帧无掩码键、payload 为明文。
func TestFrameUnmaskedNoKey(t *testing.T) {
	payload := []byte("world")
	var buf bytes.Buffer
	if err := writeFrame(&buf, wsFrame{fin: true, opcode: opText, payload: payload}, false); err != nil {
		t.Fatalf("writeFrame: %v", err)
	}
	b := buf.Bytes()
	if b[1]&0x80 != 0 {
		t.Fatalf("不加掩帧不应置 MASK 位")
	}
	if !bytes.Equal(b[2:], payload) {
		t.Fatalf("不加掩 payload 应为明文, 实得 %q", b[2:])
	}
}

// TestFrameControlAndFragmentPreserved 校验控制帧 opcode 与分片 FIN 位被保留。
func TestFrameControlAndFragmentPreserved(t *testing.T) {
	cases := []wsFrame{
		{fin: false, opcode: opText, payload: []byte("frag1")},      // 分片首帧
		{fin: false, opcode: opContinuation, payload: []byte("f2")}, // 续帧
		{fin: true, opcode: opContinuation, payload: []byte("f3")},  // 末帧
		{fin: true, opcode: opPing, payload: []byte("p")},           // 控制帧 ping
		{fin: true, opcode: opClose, payload: []byte{0x03, 0xe8}},   // close(1000)
	}
	for i, in := range cases {
		var buf bytes.Buffer
		if err := writeFrame(&buf, in, false); err != nil {
			t.Fatalf("case %d writeFrame: %v", i, err)
		}
		got, err := readFrame(&buf)
		if err != nil {
			t.Fatalf("case %d readFrame: %v", i, err)
		}
		if got.fin != in.fin || got.opcode != in.opcode || !bytes.Equal(got.payload, in.payload) {
			t.Fatalf("case %d 不一致: got fin=%v op=%x", i, got.fin, got.opcode)
		}
	}
}
