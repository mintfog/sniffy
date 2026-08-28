// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package flow

import "testing"

// latin1Filename 模拟含 Latin-1 文件名的 Content-Disposition 值。
const latin1Filename = "attachment; filename=\"caf\xe9.pdf\""

// 合法 UTF-8 基准直接复用输入切片。
func TestRestoreHeaderPairBytesNoopOnValidBasis(t *testing.T) {
	basis := [][2]string{{"Accept", "*/*"}, {"X-Trace", "中文"}}
	edited := [][2]string{{"Accept", "*/*"}}

	got := RestoreHeaderPairBytes(edited, basis)

	if len(got) != 1 || &got[0] != &edited[0] {
		t.Fatalf("合法基准应原样返回入参切片,得到 %v", got)
	}
}

// 保留原样回传值的原始字节，并采用编辑后的新值。
func TestRestoreHeaderPairBytesKeepsEdits(t *testing.T) {
	basis := [][2]string{{"Content-Disposition", latin1Filename}, {"X-Note", "b\x80c"}}
	edited := [][2]string{
		{"Content-Disposition", jsonRoundTrip(t, latin1Filename)},
		{"X-Note", "改过了"},
	}

	got := RestoreHeaderPairBytes(edited, basis)

	if got[0][1] != latin1Filename {
		t.Errorf("原样回传的值 = %q, want %q", got[0][1], latin1Filename)
	}
	if got[1][1] != "改过了" {
		t.Errorf("改过的值 = %q, want 改过了", got[1][1])
	}
}

// 每个基准值只恢复一次，重复行使用编辑值。
func TestRestoreHeaderPairBytesConsumesEachOriginalOnce(t *testing.T) {
	basis := [][2]string{{"Content-Disposition", latin1Filename}}
	seen := jsonRoundTrip(t, latin1Filename)
	edited := [][2]string{{"Content-Disposition", seen}, {"content-disposition", seen}}

	got := RestoreHeaderPairBytes(edited, basis)

	if got[0][1] != latin1Filename {
		t.Errorf("首行 = %q, want %q", got[0][1], latin1Filename)
	}
	if got[1][1] != seen {
		t.Errorf("复制出的第二行 = %q, want %q", got[1][1], seen)
	}
	if edited[0][1] != seen {
		t.Error("还原不得就地改写调用方传入的切片")
	}
}

// 头名变化时按值匹配对应的原始字节。
func TestRestoreHeaderPairBytesRenamedRow(t *testing.T) {
	basis := [][2]string{{"Content-Disposition", latin1Filename}}
	edited := [][2]string{{"X-Renamed", jsonRoundTrip(t, latin1Filename)}}

	got := RestoreHeaderPairBytes(edited, basis)

	if got[0][1] != latin1Filename {
		t.Errorf("改名行的值 = %q, want %q", got[0][1], latin1Filename)
	}
}

// 同名匹配优先于忽略头名的匹配，保持基准值与头名的对应关系。
func TestRestoreHeaderPairBytesExactNameWinsOverRename(t *testing.T) {
	const a = "v\x80"
	const b = "v\x81"
	basis := [][2]string{{"X-A", a}, {"X-B", b}}
	seen := jsonRoundTrip(t, a)
	if seen != jsonRoundTrip(t, b) {
		t.Fatalf("本用例要求两条值出境后相同,否则失去区分力: %q vs %q", seen, jsonRoundTrip(t, b))
	}
	// 改名行排在前面，同名行排在后面。
	edited := [][2]string{{"X-Renamed", seen}, {"X-B", seen}}

	got := RestoreHeaderPairBytes(edited, basis)

	if got[1][1] != b {
		t.Errorf("同名行 = %q, want %q", got[1][1], b)
	}
	if got[0][1] != a {
		t.Errorf("改名行 = %q, want %q", got[0][1], a)
	}
}

// 空名字的行不参与匹配，让有名字的行优先恢复基准值。
func TestRestoreHeaderPairBytesEmptyNameYieldsToNamedRow(t *testing.T) {
	basis := [][2]string{{"Content-Disposition", latin1Filename}}
	seen := jsonRoundTrip(t, latin1Filename)
	edited := [][2]string{{"  ", seen}, {"Content-Disposition", seen}}

	got := RestoreHeaderPairBytes(edited, basis)

	if got[1][1] != latin1Filename {
		t.Errorf("有名字的那一行 = %q, want %q", got[1][1], latin1Filename)
	}
}
