// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package process

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
	"unicode/utf16"
)

// shortcutParts 是拼一个快捷方式文件用的各个部分，未设置的部分不写入。
type shortcutParts struct {
	idList   []byte
	linkInfo []byte
	// strings 依次是名称、相对路径、工作目录、参数、图标位置，nil 表示没有这一项。
	strings [5]*string
	extra   []byte
}

func (p shortcutParts) bytes() []byte {
	flags := uint32(linkIsUnicode)
	var body bytes.Buffer
	if p.idList != nil {
		flags |= linkHasTargetIDList
		binary.Write(&body, binary.LittleEndian, uint16(len(p.idList)))
		body.Write(p.idList)
	}
	if p.linkInfo != nil {
		flags |= linkHasLinkInfo
		body.Write(p.linkInfo)
	}
	for i, s := range p.strings {
		if s == nil {
			continue
		}
		flags |= []uint32{linkHasName, linkHasRelativePath, linkHasWorkingDir, linkHasArguments, linkHasIconLocation}[i]
		units := utf16.Encode([]rune(*s))
		binary.Write(&body, binary.LittleEndian, uint16(len(units)))
		binary.Write(&body, binary.LittleEndian, units)
	}
	body.Write(p.extra)
	body.Write([]byte{0, 0, 0, 0}) // TerminalBlock

	header := make([]byte, shellLinkHeaderSize)
	binary.LittleEndian.PutUint32(header, shellLinkHeaderSize)
	copy(header[4:], shellLinkCLSID)
	binary.LittleEndian.PutUint32(header[20:], flags)
	return append(header, body.Bytes()...)
}

func utf16z(s string) []byte {
	var b bytes.Buffer
	binary.Write(&b, binary.LittleEndian, append(utf16.Encode([]rune(s)), 0))
	return b.Bytes()
}

// linkInfo 拼一个本地路径的 LinkInfo。unicode 为 false 时只有 ANSI 字符串（0x1C 字节的头）。
func linkInfo(ansiBase, ansiSuffix []byte, unicodeBase, unicodeSuffix string, unicode bool, flags uint32) []byte {
	headerSize := 0x1C
	if unicode {
		headerSize = linkInfoUnicodeHeader
	}
	var tail bytes.Buffer
	offset := func(b []byte) uint32 {
		off := uint32(headerSize + tail.Len())
		tail.Write(b)
		return off
	}
	localBase := offset(append(append([]byte{}, ansiBase...), 0))
	suffix := offset(append(append([]byte{}, ansiSuffix...), 0))
	var unicodeOffsets [2]uint32
	if unicode {
		unicodeOffsets = [2]uint32{offset(utf16z(unicodeBase)), offset(utf16z(unicodeSuffix))}
	}
	header := make([]byte, headerSize)
	binary.LittleEndian.PutUint32(header[0:], uint32(headerSize+tail.Len()))
	binary.LittleEndian.PutUint32(header[4:], uint32(headerSize))
	binary.LittleEndian.PutUint32(header[8:], flags)
	binary.LittleEndian.PutUint32(header[16:], localBase)
	binary.LittleEndian.PutUint32(header[24:], suffix)
	if unicode {
		binary.LittleEndian.PutUint32(header[0x1C:], unicodeOffsets[0])
		binary.LittleEndian.PutUint32(header[0x20:], unicodeOffsets[1])
	}
	return append(header, tail.Bytes()...)
}

func envVarBlock(target string) []byte {
	b := make([]byte, envVarBlockSize)
	binary.LittleEndian.PutUint32(b, envVarBlockSize)
	binary.LittleEndian.PutUint32(b[4:], envVarBlockSignature)
	copy(b[8:], target)
	copy(b[8+260:], utf16z(target))
	return b
}

func TestShortcutTarget(t *testing.T) {
	// 测试用的「系统代码页」：给每个字节打上标记，以确认 ANSI 字符串经过了它。
	decodeANSI := func(b []byte) string { return "ansi(" + string(b) + ")" }
	expand := func(s string) string { return strings.ReplaceAll(s, "%ProgramFiles%", `C:\Program Files`) }
	name, args, processStart := "Chrome", "--profile-directory=Default", `--processStart "Discord.exe"`
	for _, tc := range []struct {
		name     string
		parts    shortcutParts
		want     string
		wantArgs string
	}{
		{
			name:  "LinkInfo 的 Unicode 路径",
			parts: shortcutParts{linkInfo: linkInfo([]byte(`C:\PROGRA~1\`), []byte("x.exe"), `C:\Program Files\网易\`, "云音乐.exe", true, linkInfoLocalBasePath)},
			want:  `C:\Program Files\网易\云音乐.exe`,
		},
		{
			name: "LinkInfo 之后的参数",
			parts: shortcutParts{
				linkInfo: linkInfo(nil, nil, `C:\Users\u\AppData\Local\Discord\`, "Update.exe", true, linkInfoLocalBasePath),
				strings:  [5]*string{&name, nil, nil, &processStart, nil},
			},
			want:     `C:\Users\u\AppData\Local\Discord\Update.exe`,
			wantArgs: processStart,
		},
		{
			name:  "只有 ANSI 路径的旧格式按系统代码页解码",
			parts: shortcutParts{linkInfo: linkInfo([]byte(`C:\Tools\`), []byte("app.exe"), "", "", false, linkInfoLocalBasePath)},
			want:  `ansi(C:\Tools\)ansi(app.exe)`,
		},
		{
			name: "前面有 Shell 项与字符串段，目标在环境变量数据块里",
			parts: shortcutParts{
				idList:  []byte{0x14, 0, 0x1f, 0x50, 0xe0, 0x4f, 0xd0, 0x20, 0xea, 0x3a, 0x69, 0x10, 0xa2, 0xd8, 0x08, 0x00, 0x2b, 0x30, 0x30, 0x9d, 0, 0},
				strings: [5]*string{&name, nil, nil, &args, nil},
				extra:   envVarBlock(`%ProgramFiles%\Google\Chrome\Application\chrome.exe`),
			},
			want:     `C:\Program Files\Google\Chrome\Application\chrome.exe`,
			wantArgs: args,
		},
		{
			name:  "网络路径不算本地路径",
			parts: shortcutParts{linkInfo: linkInfo(nil, []byte("app.exe"), "", "", false, 2)},
		},
		{
			name:  "广告快捷方式之类没有可用的目标",
			parts: shortcutParts{strings: [5]*string{&name}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, gotArgs, err := shortcutTarget(tc.parts.bytes(), decodeANSI, expand)
			if err != nil || got != tc.want || gotArgs != tc.wantArgs {
				t.Fatalf("shortcutTarget = (%q, %q, %v)，期望 (%q, %q)", got, gotArgs, err, tc.want, tc.wantArgs)
			}
		})
	}
}

func TestShortcutTargetRejectsMalformed(t *testing.T) {
	identity := func(b []byte) string { return string(b) }
	keep := func(s string) string { return s }
	valid := shortcutParts{extra: envVarBlock(`C:\app.exe`), strings: [5]*string{new(string)}}.bytes()
	bad := append([]byte{}, valid...)
	bad[4] ^= 0xff // CLSID 不对
	// LinkInfo 声称的长度超出文件，其后也没有字符串段。
	oversized := shortcutParts{linkInfo: binary.LittleEndian.AppendUint32(nil, 4096)}.bytes()
	for name, data := range map[string][]byte{"空文件": nil, "CLSID 不对": bad, "头部不全": valid[:40], "LinkInfo 超出文件": oversized} {
		if _, _, err := shortcutTarget(data, identity, keep); err == nil {
			t.Errorf("%s: 应当报错", name)
		}
	}
	// 截在字符串段中间：已读部分越界即报错，不会读到别处去。
	name := strings.Repeat("长", 40)
	truncated := shortcutParts{strings: [5]*string{&name}}.bytes()
	if _, _, err := shortcutTarget(truncated[:shellLinkHeaderSize+10], identity, keep); err == nil {
		t.Error("截断的字符串段应当报错")
	}
}
