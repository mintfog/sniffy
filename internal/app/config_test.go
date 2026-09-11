// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mintfog/sniffy/internal/platform"
)

func TestResolveListen(t *testing.T) {
	for _, tt := range []struct {
		name string
		data string
		want int
	}{
		{"配置缺失", "", 9090},
		{"JSON损坏", `{"port":`, 9090},
		{"端口类型错误", `{"port":"8081"}`, 9090},
		{"端口为零", `{"port":0}`, 9090},
		{"负端口", `{"port":-1}`, 9090},
		{"端口越界", `{"port":65536}`, 9090},
		{"最小端口", `{"port":1}`, 1},
		{"最大端口", `{"port":65535}`, 65535},
		{"保存的端口", `{"port":8181,"address":"0.0.0.0"}`, 8181},
		{"旧配置补齐默认端口", `{"recording":true}`, 8080},
	} {
		t.Run(tt.name, func(t *testing.T) {
			isolateAppDirs(t)
			dir, err := platform.ConfigDir()
			if err != nil {
				t.Fatal(err)
			}
			if tt.data != "" {
				writeAppFixture(t, filepath.Join(dir, "config.json"), tt.data)
			}
			for _, host := range []string{"127.0.0.1", "::1", "0.0.0.0"} {
				gotHost, gotPort := ResolveListen(host, 9090)
				if gotHost != host || gotPort != tt.want {
					t.Errorf("ResolveListen(%q, 9090) = (%q, %d)，期望 (%q, %d)", host, gotHost, gotPort, host, tt.want)
				}
			}
		})
	}
}

func TestResolveListenUnavailableConfigDir(t *testing.T) {
	isolateAppDirs(t)
	base, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatal(err)
	}
	writeAppFixture(t, filepath.Join(base, "sniffy"), "占用配置目录路径")
	host, port := ResolveListen("127.0.0.1", 9090)
	if host != "127.0.0.1" || port != 9090 {
		t.Fatalf("配置目录不可用时得到 (%q, %d)，期望默认监听地址", host, port)
	}
}
