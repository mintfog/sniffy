// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package flow

import (
	"bytes"
	"testing"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	original := []byte("hello sniffy, this is a body that will be compressed and decompressed")

	for _, enc := range []string{"gzip", "deflate"} {
		encoded, ok := EncodeBody(original, enc)
		if !ok {
			t.Fatalf("EncodeBody(%s) reported failure", enc)
		}
		if bytes.Equal(encoded, original) {
			t.Fatalf("EncodeBody(%s) did not change bytes", enc)
		}
		decoded, ok := DecodeBody(encoded, enc)
		if !ok {
			t.Fatalf("DecodeBody(%s) reported failure", enc)
		}
		if !bytes.Equal(decoded, original) {
			t.Fatalf("round trip(%s) mismatch: got %q want %q", enc, decoded, original)
		}
	}
}

func TestDecodeBodyUnknownEncoding(t *testing.T) {
	body := []byte("plain")
	out, ok := DecodeBody(body, "br")
	if ok {
		t.Fatalf("expected unsupported encoding br to report ok=false")
	}
	if !bytes.Equal(out, body) {
		t.Fatalf("unsupported encoding should return original bytes")
	}
}

func TestIsBinary(t *testing.T) {
	if IsBinary([]byte("a normal utf-8 string")) {
		t.Fatalf("text wrongly classified as binary")
	}
	if !IsBinary([]byte{0x00, 0x01, 0x02, 0x03, 0x00, 0x01, 0xff, 0xfe}) {
		t.Fatalf("binary wrongly classified as text")
	}
}

func TestDecisionMergePrecedence(t *testing.T) {
	acc := ContinueDecision()
	acc = Merge(acc, BreakpointDecision(PhaseRequest, ""))
	if acc.Kind != Breakpoint {
		t.Fatalf("breakpoint should beat continue")
	}
	acc = Merge(acc, MockDecision(""))
	if acc.Kind != Mock {
		t.Fatalf("mock should beat breakpoint")
	}
	acc = Merge(acc, AbortDecision(403, ""))
	if acc.Kind != Abort {
		t.Fatalf("abort should beat mock")
	}
	// 较低优先级不应覆盖。
	acc = Merge(acc, ContinueDecision())
	if acc.Kind != Abort {
		t.Fatalf("continue should not override abort")
	}
}
