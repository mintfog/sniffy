// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package process

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
)

// bgraToPNG 把"自上而下"排列的 32 位 BGRA 像素缓冲编码为 PNG。
//
// Windows GetDIBits 以负高度取出的 DIB 即此布局(每像素 4 字节 B,G,R,A)。
// 当全部 alpha 为 0(常见于旧式无 alpha 通道的图标)时按不透明处理,
// 以免整张图标变全透明。该函数与平台无关,便于跨平台单测像素转换逻辑。
func bgraToPNG(buf []byte, w, h int) ([]byte, error) {
	hasAlpha := false
	for i := 0; i < w*h; i++ {
		if buf[i*4+3] != 0 {
			hasAlpha = true
			break
		}
	}

	// 用 NRGBA(非预乘 alpha):GetDIBits 给出的是直通 alpha,与 PNG 存储一致,
	// 半透明像素不会因预乘/反预乘而失真。
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := (y*w + x) * 4
			a := buf[i+3]
			if !hasAlpha {
				a = 255
			}
			img.SetNRGBA(x, y, color.NRGBA{R: buf[i+2], G: buf[i+1], B: buf[i], A: a})
		}
	}

	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
