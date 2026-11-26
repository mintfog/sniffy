// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package service

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/mintfog/sniffy/internal/procinfo"
)

// TestSetTunAppCaptured 覆盖会话右键的「只抓 / 不抓这个应用」在各模式下对两份名单的改动。
func TestSetTunAppCaptured(t *testing.T) {
	// 各平台都是绝对路径：chromeDir 下是这条会话的进程 chrome。
	googleDir := filepath.Join(os.TempDir(), "sniffy-apps-google")
	chromeDir := filepath.Join(googleDir, "chrome")
	chrome := filepath.Join(chromeDir, "chrome")
	type state struct {
		mode             string
		include, exclude []string
	}
	for _, tc := range []struct {
		name    string
		before  state
		capture bool
		want    state
	}{
		{name: "全部抓取时只抓", before: state{mode: "all", include: []string{"curl"}}, capture: true,
			want: state{mode: "include", include: []string{"curl", "chrome"}}},
		{name: "已在只抓名单里，写法不同也不重复加", before: state{mode: "include", include: []string{chromeDir}}, capture: true,
			want: state{mode: "include", include: []string{chromeDir}}},
		{name: "全部抓取时不抓", before: state{mode: "all", exclude: []string{"steam"}}, capture: false,
			want: state{mode: "exclude", exclude: []string{"steam", "chrome"}}},
		{name: "只抓名单时不抓，移出选中它的全部项", before: state{mode: "include", include: []string{"chrome", "curl", googleDir}, exclude: []string{"steam"}}, capture: false,
			want: state{mode: "include", include: []string{"curl"}, exclude: []string{"steam"}}},
		{name: "排除名单时只抓，排除名单原样保留", before: state{mode: "exclude", exclude: []string{"steam"}}, capture: true,
			want: state{mode: "include", include: []string{"chrome"}, exclude: []string{"steam"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := New(nil, nil, t.TempDir(), "")
			svc.UpdateConfig(map[string]any{"tunAppMode": tc.before.mode, "tunIncludeApps": tc.before.include, "tunExcludeApps": tc.before.exclude})
			var applied []string
			svc.SetTunAppsApplier(func(_ string, apps []string) { applied = apps })
			entry, c, err := svc.SetTunAppCaptured(chrome, "chrome", tc.capture)
			if err != nil || entry != "chrome" {
				t.Fatalf("SetTunAppCaptured = (%q, %v)", entry, err)
			}
			got := state{mode: c.TunAppMode, include: c.TunIncludeApps, exclude: c.TunExcludeApps}
			if got.mode != tc.want.mode || !slices.Equal(got.include, tc.want.include) || !slices.Equal(got.exclude, tc.want.exclude) {
				t.Fatalf("改后 %+v，期望 %+v", got, tc.want)
			}
			if !slices.Equal(applied, c.tunScopeApps()) {
				t.Fatalf("下发 %q，期望当前模式的名单 %q", applied, c.tunScopeApps())
			}
		})
	}
}

func TestSetTunAppCapturedRejects(t *testing.T) {
	svc := New(nil, nil, t.TempDir(), "")
	for _, name := range []string{"", "PID_4321"} {
		if _, c, err := svc.SetTunAppCaptured("", name, true); err == nil || c.TunAppMode != procinfo.AppScopeAll {
			t.Fatalf("进程名 %q：没有进程信息时应返回错误、不改模式: %v, %q", name, err, c.TunAppMode)
		}
	}
	// 只有进程名、名字里带分隔符：条目进不了名单，模式也不能变。
	for _, capture := range []bool{true, false} {
		if _, c, err := svc.SetTunAppCaptured("", "foo/bar", capture); err == nil || c.TunAppMode != procinfo.AppScopeAll {
			t.Fatalf("capture=%v：条目无效时应返回错误、不改模式: %v, %q", capture, err, c.TunAppMode)
		}
	}
	full := make([]string, 256)
	for i := range full {
		full[i] = fmt.Sprintf("app%d", i)
	}
	svc.UpdateConfig(map[string]any{"tunIncludeApps": full})
	if _, c, err := svc.SetTunAppCaptured(filepath.Join(os.TempDir(), "curl"), "curl", true); err == nil || c.TunAppMode != procinfo.AppScopeAll {
		t.Fatalf("名单已满时应返回错误、不改模式: %v, %q", err, c.TunAppMode)
	}
}
