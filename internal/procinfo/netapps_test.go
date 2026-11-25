// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package procinfo

import (
	"errors"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/mintfog/sniffy/pkg/process"
)

// connectionsDetector 只实现 GetAllConnections。
type connectionsDetector struct {
	process.Detector
	conns []*process.ConnectionProcess
}

func (d connectionsDetector) GetAllConnections() ([]*process.ConnectionProcess, error) {
	return d.conns, nil
}

// TestNetworkApps：同一条目只列一次、按条目排序、不含本进程与查不到的进程（占位名），带上检测器取到的图标。
func TestNetworkApps(t *testing.T) {
	conn := func(pid uint32, name, path, icon string) *process.ConnectionProcess {
		return &process.ConnectionProcess{ProcessInfo: &process.ProcessInfo{PID: pid, Name: name, Path: path, IconData: icon, HasIcon: icon != "", IconType: "png"}}
	}
	r := &Resolver{detector: connectionsDetector{conns: []*process.ConnectionProcess{
		conn(10, "curl", "/usr/bin/curl", ""),
		conn(11, "chrome", "/opt/google/chrome/chrome", "Y2hyb21l"),
		conn(12, "curl", "/usr/bin/curl", ""),
		conn(uint32(os.Getpid()), "sniffy", "/opt/sniffy/sniffy", ""),
		conn(13, "PID_13", "", ""),
		{ProcessInfo: nil},
	}}}
	got, err := r.NetworkApps()
	if err != nil {
		t.Fatal(err)
	}
	want := []AppChoice{
		{Entry: "chrome", Path: "/opt/google/chrome/chrome", IconData: "Y2hyb21l", IconType: "png"},
		{Entry: "curl", Path: "/usr/bin/curl", IconType: "png"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NetworkApps = %+v\n期望 %+v", got, want)
	}

	var unavailable *Resolver
	if _, err := unavailable.NetworkApps(); err == nil {
		t.Fatal("没有解析器时应返回错误")
	}
}

// TestAppIcons：同一路径只提取一次，提不出图标的与空路径不返回。
func TestAppIcons(t *testing.T) {
	var mu sync.Mutex
	calls := map[string]int{}
	r := &Resolver{icons: iconExtractorFunc(func(path string) (*process.ProcessIconInfo, error) {
		mu.Lock()
		calls[path]++
		mu.Unlock()
		if path == "/broken" {
			return nil, errors.New("读不出")
		}
		return &process.ProcessIconInfo{HasIcon: path != "/plain", IconData: "icon:" + path, IconType: "png"}, nil
	})}
	got := r.AppIcons([]string{"/a", "/b", "/a", "", "/broken", "/plain"})
	slices.SortFunc(got, func(x, y AppIcon) int { return strings.Compare(x.Path, y.Path) })
	want := []AppIcon{{Path: "/a", IconData: "icon:/a", IconType: "png"}, {Path: "/b", IconData: "icon:/b", IconType: "png"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("AppIcons = %+v，期望 %+v", got, want)
	}
	if calls["/a"] != 1 || calls[""] != 0 {
		t.Fatalf("提取次数 %v", calls)
	}
	var unavailable *Resolver
	if icons := unavailable.AppIcons([]string{"/a"}); len(icons) != 0 {
		t.Fatalf("没有解析器时返回 %+v", icons)
	}
}
