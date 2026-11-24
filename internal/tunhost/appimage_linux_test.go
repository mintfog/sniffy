// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package tunhost

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestVerifyMainFromAppImage：helper 运行在 AppImage 里时，主进程运行在同一个 AppImage 文件里才放行。
// 主进程由测试二进制的副本扮演，副本放在充当挂载点的目录下，带着 AppImage 运行时设下的环境变量。
func TestVerifyMainFromAppImage(t *testing.T) {
	image := fakeAppImage(t)
	useAppImage(t, image)
	appDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	insideAppDir := copyTestExecutable(t, filepath.Join(appDir, "usr", "bin", "sniffy"))
	// 临时目录是符号链接时，APPDIR 经链接给出，而对端的可执行文件路径是解析过的。
	linkedParent := filepath.Join(t.TempDir(), "tmp")
	if err := os.Symlink(filepath.Dir(appDir), linkedParent); err != nil {
		t.Fatal(err)
	}
	linkedAppDir := filepath.Join(linkedParent, filepath.Base(appDir))

	for _, tc := range []struct {
		name    string
		exe     string
		image   string
		appDir  string
		wantErr string
	}{
		{"同一个 AppImage", insideAppDir, image, appDir, ""},
		{"挂载点所在目录是符号链接", insideAppDir, image, linkedAppDir, ""},
		{"另一个 AppImage", insideAppDir, fakeAppImage(t), appDir, "运行的不是"},
		{"不在挂载点之下", testExecutable(t), image, appDir, "不是从 AppImage 运行的"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			endpoint := socketPath(t, "ch")
			p := startImpostor(t, tc.exe, "listen", endpoint, "APPIMAGE="+tc.image, "APPDIR="+tc.appDir)
			conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: endpoint, Net: "unix"})
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			err = verifyMain(conn, p.pid)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("verifyMain 拒绝了同一个 AppImage 里的主进程: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("verifyMain 返回 %v，期望含 %q 的错误", err, tc.wantErr)
			}
		})
	}
}
