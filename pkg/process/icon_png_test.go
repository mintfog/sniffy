// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package process

import (
	"bytes"
	"image/color"
	"image/png"
	"testing"
)

// TestBgraToPNG 验证 BGRA→PNG 的字节序、行序与直通 alpha 处理(跨平台,
// 保护 Windows 真实图标提取里最易出错的像素转换部分)。
func TestBgraToPNG(t *testing.T) {
	// 2x2,自上而下,每像素 B,G,R,A。
	buf := []byte{
		10, 20, 30, 255, // (0,0) -> RGBA(30,20,10,255)
		40, 50, 60, 128, // (1,0) -> RGBA(60,50,40,128)
		70, 80, 90, 255, // (0,1) -> RGBA(90,80,70,255)
		1, 2, 3, 0, //     (1,1) -> 透明,A=0
	}

	data, err := bgraToPNG(buf, 2, 2)
	if err != nil {
		t.Fatalf("bgraToPNG: %v", err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("png.Decode: %v", err)
	}
	if b := img.Bounds(); b.Dx() != 2 || b.Dy() != 2 {
		t.Fatalf("尺寸 = %v, 期望 2x2", b)
	}

	want := map[[2]int]color.NRGBA{
		{0, 0}: {30, 20, 10, 255},
		{1, 0}: {60, 50, 40, 128},
		{0, 1}: {90, 80, 70, 255},
		{1, 1}: {1, 2, 3, 0},
	}
	for pt, w := range want {
		got := color.NRGBAModel.Convert(img.At(pt[0], pt[1])).(color.NRGBA)
		// A=0 时 RGB 经预乘往返会归零,只校验 alpha;其余校验全通道。
		if w.A == 0 {
			if got.A != 0 {
				t.Errorf("(%d,%d) alpha = %d, 期望 0", pt[0], pt[1], got.A)
			}
			continue
		}
		if got != w {
			t.Errorf("(%d,%d) = %+v, 期望 %+v", pt[0], pt[1], got, w)
		}
	}
}

// TestBgraToPNGAllZeroAlphaOpaque 验证 alpha 全 0 时按不透明处理(旧式图标)。
func TestBgraToPNGAllZeroAlphaOpaque(t *testing.T) {
	buf := []byte{
		10, 20, 30, 0,
		40, 50, 60, 0,
	}
	data, err := bgraToPNG(buf, 2, 1)
	if err != nil {
		t.Fatalf("bgraToPNG: %v", err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("png.Decode: %v", err)
	}
	for x := 0; x < 2; x++ {
		got := color.NRGBAModel.Convert(img.At(x, 0)).(color.NRGBA)
		if got.A != 255 {
			t.Errorf("x=%d alpha = %d, 期望 255(不透明回退)", x, got.A)
		}
	}
}
