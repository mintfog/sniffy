// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package process

import (
	"bytes"
	"encoding/binary"
	"errors"
	"unicode/utf16"
)

// 以下布局来自 [MS-SHLLINK] Shell Link (.LNK) Binary File Format。
const (
	shellLinkHeaderSize = 0x4C

	linkHasTargetIDList = 1 << 0
	linkHasLinkInfo     = 1 << 1
	linkHasName         = 1 << 2
	linkHasRelativePath = 1 << 3
	linkHasWorkingDir   = 1 << 4
	linkHasArguments    = 1 << 5
	linkHasIconLocation = 1 << 6
	linkIsUnicode       = 1 << 7

	linkInfoLocalBasePath = 1 << 0
	linkInfoUnicodeHeader = 0x24

	envVarBlockSignature = 0xA0000001
	envVarBlockSize      = 0x314
)

var shellLinkCLSID = []byte{0x01, 0x14, 0x02, 0x00, 0x00, 0x00, 0x00, 0x00, 0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}

var errShortcutFormat = errors.New("不是有效的快捷方式文件")

// shortcutTarget 读出快捷方式指向的本地路径：优先取 LinkInfo 里的本地路径，没有时取环境变量数据块里的
// 目标（如 %ProgramFiles%\...，经 expand 展开）。都没有时为空，如 MSI 的广告快捷方式、只用 Shell 项
// 标识目标的快捷方式。args 是快捷方式带的命令行参数，未经切分。decodeANSI 按系统代码页解码 ANSI 字符串：
// 没有 Unicode 版本的旧快捷方式只存了它。
func shortcutTarget(data []byte, decodeANSI func([]byte) string, expand func(string) string) (target, args string, err error) {
	if len(data) < shellLinkHeaderSize || binary.LittleEndian.Uint32(data) != shellLinkHeaderSize || !bytes.Equal(data[4:20], shellLinkCLSID) {
		return "", "", errShortcutFormat
	}
	flags := binary.LittleEndian.Uint32(data[20:])
	r := linkReader{data: data, off: shellLinkHeaderSize}
	if flags&linkHasTargetIDList != 0 {
		r.skip(int(r.u16()))
	}
	var local string
	if flags&linkHasLinkInfo != 0 {
		// LinkInfoSize 把开头记录它自己的 4 个字节也算在内。
		start := r.off
		size := int(r.u32())
		r.off = start
		if info := r.take(size); info != nil {
			local = linkInfoLocalPath(info, decodeANSI)
		}
	}
	for _, f := range []uint32{linkHasName, linkHasRelativePath, linkHasWorkingDir, linkHasArguments, linkHasIconLocation} {
		if flags&f == 0 {
			continue
		}
		unicode := flags&linkIsUnicode != 0
		size := int(r.u16())
		if unicode {
			size *= 2
		}
		s := r.take(size)
		if f != linkHasArguments || s == nil {
			continue
		}
		if unicode {
			args = utf16At(s, 0)
		} else {
			args = decodeANSI(s)
		}
	}
	if r.err != nil {
		return "", "", r.err
	}
	if local != "" {
		return local, args, nil
	}
	return envVarTarget(data[r.off:], expand), args, nil
}

// linkInfoLocalPath 拼出 LinkInfo 里的本地路径（LocalBasePath 加 CommonPathSuffix），优先用 Unicode 版本。
// 网络路径与结构不完整时为空。
func linkInfoLocalPath(info []byte, decodeANSI func([]byte) string) string {
	if len(info) < 0x1C || binary.LittleEndian.Uint32(info[8:])&linkInfoLocalBasePath == 0 {
		return ""
	}
	headerSize := binary.LittleEndian.Uint32(info[4:])
	if headerSize >= linkInfoUnicodeHeader && len(info) >= linkInfoUnicodeHeader {
		base := utf16At(info, binary.LittleEndian.Uint32(info[0x1C:]))
		suffix := utf16At(info, binary.LittleEndian.Uint32(info[0x20:]))
		if base != "" {
			return base + suffix
		}
	}
	base := cStringAt(info, binary.LittleEndian.Uint32(info[16:]))
	suffix := cStringAt(info, binary.LittleEndian.Uint32(info[24:]))
	if len(base) == 0 {
		return ""
	}
	return decodeANSI(base) + decodeANSI(suffix)
}

// envVarTarget 在 ExtraData 里找环境变量数据块，返回展开后的 Unicode 目标。
func envVarTarget(extra []byte, expand func(string) string) string {
	for len(extra) >= 8 {
		size := int(binary.LittleEndian.Uint32(extra))
		if size < 8 || size > len(extra) {
			return ""
		}
		if binary.LittleEndian.Uint32(extra[4:]) == envVarBlockSignature && size == envVarBlockSize {
			// TargetAnsi 占 260 字节，其后是 260 个 UTF-16 字符的 TargetUnicode。
			if target := utf16At(extra[8+260:size], 0); target != "" {
				return expand(target)
			}
		}
		extra = extra[size:]
	}
	return ""
}

func cStringAt(b []byte, off uint32) []byte {
	if off == 0 || int(off) >= len(b) {
		return nil
	}
	s := b[off:]
	if i := bytes.IndexByte(s, 0); i >= 0 {
		s = s[:i]
	}
	return s
}

func utf16At(b []byte, off uint32) string {
	if int(off) >= len(b) {
		return ""
	}
	var units []uint16
	for i := int(off); i+1 < len(b); i += 2 {
		u := binary.LittleEndian.Uint16(b[i:])
		if u == 0 {
			break
		}
		units = append(units, u)
	}
	return string(utf16.Decode(units))
}

// linkReader 顺序读取快捷方式的各个部分，越界时记下错误，此后的读取都返回零值。
type linkReader struct {
	data []byte
	off  int
	err  error
}

func (r *linkReader) take(n int) []byte {
	if r.err != nil || n < 0 || r.off+n > len(r.data) {
		r.err = errShortcutFormat
		return nil
	}
	b := r.data[r.off : r.off+n]
	r.off += n
	return b
}

func (r *linkReader) skip(n int) { r.take(n) }

func (r *linkReader) u16() uint16 {
	if b := r.take(2); b != nil {
		return binary.LittleEndian.Uint16(b)
	}
	return 0
}

func (r *linkReader) u32() uint32 {
	if b := r.take(4); b != nil {
		return binary.LittleEndian.Uint32(b)
	}
	return 0
}
