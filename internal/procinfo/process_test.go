// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package procinfo

import (
	"errors"
	"fmt"
	"math"
	"net"
	"reflect"
	"strconv"
	"testing"

	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/pkg/process"
)

type iconExtractorFunc func(string) (*process.ProcessIconInfo, error)

func (f iconExtractorFunc) ExtractIcon(path string) (*process.ProcessIconInfo, error) {
	return f(path)
}

func TestResolveIconResult(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		icon *process.ProcessIconInfo
		err  error
		want flow.ProcessInfo
	}{
		{
			name: "应用图标",
			icon: &process.ProcessIconInfo{
				HasIcon: true, IconData: "图标数据", IconType: "svg",
				IconSize: "48x16", IconCategory: "browser",
			},
			want: flow.ProcessInfo{
				HasIcon: true, IconData: "图标数据", IconType: "svg",
				IconSize: 48, IconCategory: "browser",
			},
		},
		{
			name: "回退图标保留元数据",
			icon: &process.ProcessIconInfo{
				IconData: "默认图标", IconType: "png",
				IconSize: "32x32", IconCategory: "application",
			},
			want: flow.ProcessInfo{
				IconData: "默认图标", IconType: "png",
				IconSize: 32, IconCategory: "application",
			},
		},
		{
			name: "无法识别尺寸",
			icon: &process.ProcessIconInfo{HasIcon: true, IconData: "图标数据", IconSize: "auto"},
			want: flow.ProcessInfo{HasIcon: true, IconData: "图标数据"},
		},
		{name: "空图标结果"},
		{name: "提取失败", err: errors.New("无法读取图标")},
		{
			name: "错误伴随部分图标",
			icon: &process.ProcessIconInfo{HasIcon: true, IconData: "不完整图标"},
			err:  errors.New("图标损坏"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			input := process.ProcessInfo{PID: 42, Name: "浏览器", Path: "/应用/浏览器", User: "用户"}
			before := input
			var iconBefore process.ProcessIconInfo
			if tc.icon != nil {
				iconBefore = *tc.icon
			}
			calls := 0
			r := newTestResolver(func(net.Addr, net.Addr) (*process.ProcessInfo, error) { return &input, nil })
			r.icons = iconExtractorFunc(func(path string) (*process.ProcessIconInfo, error) {
				calls++
				if path != input.Path {
					t.Errorf("图标提取路径 = %q，期望 %q", path, input.Path)
				}
				return tc.icon, tc.err
			})
			want := tc.want
			want.PID, want.Name, want.Path, want.User = input.PID, input.Name, input.Path, input.User
			for range 2 {
				if got := r.Resolve(&net.TCPAddr{Port: 50000}, nil); got == nil || *got != want {
					t.Fatalf("带图标的解析结果 = %+v，期望 %+v", got, want)
				}
			}
			if calls != 1 {
				t.Fatalf("图标提取调用 %d 次，期望 1", calls)
			}
			if input != before {
				t.Fatalf("映射修改了输入的进程信息：%+v，期望 %+v", input, before)
			}
			if tc.icon != nil && *tc.icon != iconBefore {
				t.Fatalf("映射修改了输入的图标信息：%+v，期望 %+v", tc.icon, iconBefore)
			}
		})
	}
}

func TestToFlowProcess(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		input process.ProcessInfo
		want  flow.ProcessInfo
	}{
		{name: "空字段"},
		{
			name: "完整身份信息",
			input: process.ProcessInfo{
				PID: math.MaxUint32, Name: "浏览器", Path: "/应用/浏览器", User: "用户",
				CommandLine: "浏览器 --proxy-server=127.0.0.1:8080",
				HasIcon:     true, IconData: "检测器附带图标", IconType: "svg", IconSize: "64x64", IconCategory: "browser",
			},
			want: flow.ProcessInfo{PID: math.MaxUint32, Name: "浏览器", Path: "/应用/浏览器", User: "用户"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			before := tc.input
			got := (&Resolver{}).toFlowProcess(&tc.input)
			if got == nil || *got != tc.want {
				t.Fatalf("进程映射 = %+v，期望 %+v", got, tc.want)
			}
			if tc.input != before {
				t.Fatalf("映射修改了检测器结果: %+v", tc.input)
			}
		})
	}
}

func TestToFlowProcessDefaultIcon(t *testing.T) {
	t.Parallel()
	icons := process.NewIconExtractor()
	// 空路径在各平台均生成默认图标，适合作为稳定的映射测试输入。
	icon, err := icons.ExtractIcon("")
	if err != nil || icon == nil || icon.IconData == "" {
		t.Fatalf("默认图标 = %+v，错误 = %v", icon, err)
	}
	input := process.ProcessInfo{PID: 42, Name: "客户端", User: "用户"}
	want := &flow.ProcessInfo{
		PID: 42, Name: "客户端", User: "用户",
		HasIcon: icon.HasIcon, IconData: icon.IconData, IconType: icon.IconType,
		IconSize: 32, IconCategory: icon.IconCategory,
	}
	if got := (&Resolver{icons: icons}).toFlowProcess(&input); !reflect.DeepEqual(got, want) {
		t.Fatalf("带图标的映射 = %+v，期望 %+v", got, want)
	}
}

func TestParseIconSize(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		input string
		want  int
	}{
		{name: "空字符串"},
		{name: "方形", input: "32x32", want: 32},
		{name: "矩形取宽度", input: "48x16", want: 48},
		{name: "单一宽度", input: "64", want: 64},
		{name: "首尾空白", input: " \t32x16\n", want: 32},
		{name: "宽度两侧空白", input: "\u300032 \tx16", want: 32},
		{name: "零宽度", input: "0x32"},
		{name: "前导零", input: "032x16", want: 32},
		{name: "显式正号", input: "+32x16", want: 32},
		{name: "带符号宽度", input: "-32x16", want: -32},
		{name: "仅空白", input: " \t\n"},
		{name: "缺少宽度", input: "x32"},
		{name: "非数字宽度", input: "widthx32"},
		{name: "大写分隔符", input: "32X32"},
		{name: "乘号", input: "32×32"},
		{name: "小数", input: "32.5x32"},
		{name: "内嵌空白", input: "3 2x32"},
		{name: "全角数字", input: "３２x32"},
		{name: "整数溢出", input: "999999999999999999999999999999x32"},
		{name: "最大整数", input: strconv.Itoa(math.MaxInt) + "x16", want: math.MaxInt},
		{name: "高度不参与解析", input: "32xauto", want: 32},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := parseIconSize(tc.input); got != tc.want {
				t.Fatalf("parseIconSize(%q) = %d，期望 %d", tc.input, got, tc.want)
			}
		})
	}
}

func FuzzParseIconSize(f *testing.F) {
	for _, seed := range []struct {
		width  uint16
		height uint16
	}{
		{0, 0}, {32, 32}, {48, 16}, {math.MaxUint16, 1},
	} {
		f.Add(seed.width, seed.height, "auto")
	}
	f.Fuzz(func(t *testing.T, width, height uint16, suffix string) {
		// 以生成的宽度作为独立期望值，覆盖高度与后缀变化。
		for _, size := range []string{
			fmt.Sprintf("%dx%d", width, height),
			fmt.Sprintf(" \t%dx%s\n", width, suffix),
		} {
			if got := parseIconSize(size); got != int(width) {
				t.Fatalf("parseIconSize(%q) = %d，期望 %d", size, got, width)
			}
		}
		parseIconSize(suffix)
	})
}
