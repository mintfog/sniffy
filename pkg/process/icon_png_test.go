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

func TestBgraToPNGRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		buf  []byte
		w    int
		h    int
	}{
		{name: "zero width", w: 0, h: 1},
		{name: "negative height", w: 1, h: -1},
		{name: "short buffer", buf: []byte{0, 0, 0}, w: 1, h: 1},
		{name: "overflowing dimensions", w: int(^uint(0) >> 1), h: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got, err := bgraToPNG(tt.buf, tt.w, tt.h); err == nil || got != nil {
				t.Fatalf("bgraToPNG() = (%d bytes, %v)", len(got), err)
			}
		})
	}
}

func FuzzBgraToPNG(f *testing.F) {
	f.Add([]byte{10, 20, 30, 255}, 1, 1)
	f.Add([]byte{}, 0, 0)
	f.Add([]byte{1, 2, 3}, 1, 1)
	f.Fuzz(func(t *testing.T, buf []byte, w, h int) {
		if w > 64 || h > 64 {
			return
		}
		got, err := bgraToPNG(buf, w, h)
		if err != nil {
			if got != nil {
				t.Fatalf("bgraToPNG(%dx%d) 同时返回了 %d 字节与错误 %v", w, h, len(got), err)
			}
			return
		}
		img, decodeErr := png.Decode(bytes.NewReader(got))
		if decodeErr != nil {
			t.Fatalf("bgraToPNG(%dx%d) 产出的字节无法解码为 PNG: %v", w, h, decodeErr)
		}
		if b := img.Bounds(); b.Dx() != w || b.Dy() != h {
			t.Fatalf("PNG 尺寸 = %dx%d，期望 %dx%d", b.Dx(), b.Dy(), w, h)
		}
	})
}
