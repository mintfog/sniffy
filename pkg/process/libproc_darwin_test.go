// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build darwin

package process

import (
	"encoding/binary"
	"fmt"
	"net/netip"
	"os/exec"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

func TestParseDarwinSocket(t *testing.T) {
	for _, test := range []struct {
		name, local, remote string
		state               uint32
		valid               bool
	}{
		{"IPv4", "127.0.0.1:50000", "127.0.0.2:8080", 4, true},
		{"IPv6", "[2001:db8::1]:50000", "[2001:db8::2]:8080", 4, true},
		{"半关闭", "127.0.0.1:50000", "127.0.0.2:8080", 5, true},
		{"监听", "127.0.0.1:50000", "127.0.0.2:8080", 1, false},
		{"TIME_WAIT", "127.0.0.1:50000", "127.0.0.2:8080", 10, false},
		{"零端口", "127.0.0.1:0", "127.0.0.2:8080", 4, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			key := darwinConnectionKey{
				local:  netip.MustParseAddrPort(test.local),
				remote: netip.MustParseAddrPort(test.remote),
			}
			buffer := darwinSocketFixture(key, test.state)
			got, ok := parseDarwinSocket(buffer)
			if ok != test.valid || ok && got.darwinConnectionKey != key {
				t.Fatalf("解析 socket = (%+v, %t)", got, ok)
			}
			binary.LittleEndian.PutUint32(buffer[darwinProtocolOffset:], unix.IPPROTO_UDP)
			if _, ok := parseDarwinSocket(buffer); ok {
				t.Fatal("接受 UDP socket")
			}
			if _, ok := parseDarwinSocket(buffer[:12]); ok {
				t.Fatal("接受截断结构体")
			}
		})
	}
}

func TestDarwinIPv6(t *testing.T) {
	for _, test := range []struct {
		raw   string
		scope uint16
		want  string
	}{
		{"fe80:7::1", 0, "fe80::1%7"},
		{"fe80::1", 12, "fe80::1%12"},
		{"::1", 12, "::1"},
		{"::ffff:127.0.0.1", 0, "127.0.0.1"},
	} {
		got := darwinIPv6(netip.MustParseAddr(test.raw).As16(), test.scope)
		if got.String() != test.want {
			t.Errorf("IPv6 %s = %s，期望 %s", test.raw, got, test.want)
		}
	}
}

func TestDarwinLibprocABI(t *testing.T) {
	clang, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("未安装 clang，跳过 SDK 布局核验")
	}
	constants := map[string]int{
		"darwinSocketSize":        darwinSocketSize,
		"darwinBSDSize":           darwinBSDSize,
		"darwinFDSize":            darwinFDSize,
		"darwinProtocolOffset":    darwinProtocolOffset,
		"darwinFamilyOffset":      darwinFamilyOffset,
		"darwinKindOffset":        darwinKindOffset,
		"darwinInetOffset":        darwinInetOffset,
		"darwinStateOffset":       darwinStateOffset,
		"darwinForeignPortOffset": darwinForeignPortOffset,
		"darwinLocalPortOffset":   darwinLocalPortOffset,
		"darwinVersionOffset":     darwinVersionOffset,
		"darwinForeignAddrOffset": darwinForeignAddrOffset,
		"darwinLocalAddrOffset":   darwinLocalAddrOffset,
		"darwinInterfaceOffset":   darwinInterfaceOffset,
		"darwinUIDOffset":         darwinUIDOffset,
		"darwinCommOffset":        darwinCommOffset,
		"darwinNameOffset":        darwinNameOffset,
		"procPIDListFDs":          procPIDListFDs,
		"procPIDTBSDInfo":         procPIDTBSDInfo,
		"procPIDFDSocketInfo":     procPIDFDSocketInfo,
		"procFDSocket":            procFDSocket,
		"socketInfoTCP":           socketInfoTCP,
		"darwinTCPEstablished":    darwinTCPEstablished,
		"darwinTCPLastConnected":  darwinTCPLastConnected,
	}
	for _, arch := range []string{"arm64", "x86_64"} {
		args := []string{"-arch", arch, "-fsyntax-only", "testdata/libproc_layout.c"}
		for name, value := range constants {
			args = append(args, fmt.Sprintf("-D%s=%d", name, value))
		}
		if output, err := exec.Command(clang, args...).CombinedOutput(); err != nil {
			t.Fatalf("%s SDK 布局不匹配: %v\n%s", arch, err, output)
		}
	}
}

func darwinSocketFixture(key darwinConnectionKey, state uint32) []byte {
	buffer := make([]byte, darwinSocketSize)
	binary.LittleEndian.PutUint32(buffer[darwinProtocolOffset:], unix.IPPROTO_TCP)
	binary.LittleEndian.PutUint32(buffer[darwinKindOffset:], socketInfoTCP)
	binary.LittleEndian.PutUint32(buffer[darwinStateOffset:], state)
	inet := buffer[darwinInetOffset:]
	if key.local.Addr().Is4() {
		binary.LittleEndian.PutUint32(buffer[darwinFamilyOffset:], unix.AF_INET)
		inet[darwinVersionOffset] = 1
	} else {
		binary.LittleEndian.PutUint32(buffer[darwinFamilyOffset:], unix.AF_INET6)
		inet[darwinVersionOffset] = 2
	}
	local, remote := key.local.Addr().As16(), key.remote.Addr().As16()
	copy(inet[darwinLocalAddrOffset:], local[:])
	copy(inet[darwinForeignAddrOffset:], remote[:])
	binary.BigEndian.PutUint16(inet[darwinLocalPortOffset:], key.local.Port())
	binary.BigEndian.PutUint16(inet[darwinForeignPortOffset:], key.remote.Port())
	return buffer
}

func darwinSingleProcessAPI() (*darwinLibproc, darwinConnectionKey) {
	key := darwinConnectionKey{
		local:  netip.MustParseAddrPort("127.0.0.1:50000"),
		remote: netip.MustParseAddrPort("127.0.0.1:8080"),
	}
	socket := darwinSocketFixture(key, darwinTCPEstablished)
	api := &darwinLibproc{
		listPIDs: func(buffer *int32, size int32) int32 {
			if buffer != nil {
				*buffer = 42
			}
			return 1
		},
		pidInfo: func(pid, flavor int32, arg uint64, buffer *byte, size int32) int32 {
			if buffer != nil {
				data := unsafe.Slice(buffer, int(size))
				binary.LittleEndian.PutUint32(data, 7)
				binary.LittleEndian.PutUint32(data[4:], procFDSocket)
			}
			return darwinFDSize
		},
		fdInfo: func(pid, fd, flavor int32, buffer *byte, size int32) int32 {
			copy(unsafe.Slice(buffer, int(size)), socket)
			return darwinSocketSize
		},
	}
	return api, key
}

func TestDarwinLargeDescriptorList(t *testing.T) {
	for _, test := range []struct {
		name           string
		count, initial int
	}{
		{"32768个", 32768, 32768},
		{"61440个", 61440, 61440},
		{"查询后增长", 61440, 256},
	} {
		t.Run(test.name, func(t *testing.T) {
			api, _ := darwinSingleProcessAPI()
			hints := 0
			api.pidInfo = func(pid, flavor int32, arg uint64, buffer *byte, size int32) int32 {
				if buffer == nil {
					hints++
					if hints == 1 {
						return int32(test.initial * darwinFDSize)
					}
					return int32(test.count * darwinFDSize)
				}
				data := unsafe.Slice(buffer, int(size))
				clear(data)
				n := min(test.count, len(data)/darwinFDSize)
				if n == test.count {
					binary.LittleEndian.PutUint32(data[(n-1)*darwinFDSize:], 7)
					binary.LittleEndian.PutUint32(data[(n-1)*darwinFDSize+4:], procFDSocket)
				}
				return int32(n * darwinFDSize)
			}
			rows, err := api.connections()
			if err != nil || len(rows) != 1 {
				t.Fatalf("大描述符列表查询 = (%d 条连接, %v)，期望 1 条", len(rows), err)
			}
		})
	}
}
