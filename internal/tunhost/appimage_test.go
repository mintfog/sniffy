// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package tunhost

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mintfog/sniffy/internal/netbind"
)

// fakeAppImage 在临时目录里建一个充当 AppImage 文件的普通文件，返回它的路径。
func fakeAppImage(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "Sniffy.AppImage")
	if err := os.WriteFile(path, []byte("appimage"), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

// useAppImage 让本进程按运行在 image 里处理，测试结束时复原。改的是包级变量，调用它的测试不能并行。
func useAppImage(t *testing.T, image string) {
	t.Helper()
	info, err := os.Stat(image)
	if err != nil {
		t.Fatal(err)
	}
	previous := launchedAppImage
	launchedAppImage = &appImage{path: image, file: info}
	t.Cleanup(func() { launchedAppImage = previous })
}

// replaceFile 以同路径的另一个文件替换 path，如升级时的做法。
func replaceFile(t *testing.T, path string) {
	t.Helper()
	next := path + ".new"
	if err := os.WriteFile(next, []byte("appimage v2"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(next, path); err != nil {
		t.Fatal(err)
	}
}

func TestDetectAppImage(t *testing.T) {
	t.Parallel()
	image := fakeAppImage(t)
	appDir := filepath.Join(t.TempDir(), ".mount_Sniffy")
	for _, tc := range []struct {
		name               string
		image, appDir, exe string
		want               bool
	}{
		{"在挂载点之下", image, appDir, filepath.Join(appDir, "usr", "bin", "sniffy"), true},
		// 从别的 AppImage 程序（如终端）里启动时，这两个环境变量是继承来的。
		{"不在挂载点之下", image, appDir, "/usr/bin/sniffy", false},
		{"挂载点的同名前缀", image, appDir, appDir + "x/usr/bin/sniffy", false},
		{"没有 APPIMAGE", "", appDir, filepath.Join(appDir, "sniffy"), false},
		{"没有 APPDIR", image, "", filepath.Join(appDir, "sniffy"), false},
		{"AppImage 文件不在", filepath.Join(t.TempDir(), "missing"), appDir, filepath.Join(appDir, "sniffy"), false},
		{"AppImage 不是普通文件", t.TempDir(), appDir, filepath.Join(appDir, "sniffy"), false},
	} {
		if got := detectAppImage(tc.image, tc.appDir, tc.exe) != nil; got != tc.want {
			t.Errorf("%s：detectAppImage = %v，期望 %v", tc.name, got, tc.want)
		}
	}
}

// TestHelperExecutableIsAppImage：在 AppImage 里运行时从 AppImage 文件拉起 helper，文件在启动后被替换时
// 在提权之前报告。
func TestHelperExecutableIsAppImage(t *testing.T) {
	image := fakeAppImage(t)
	useAppImage(t, image)
	got, err := helperExecutable()
	if err != nil || got != image {
		t.Fatalf("helperExecutable = %q, %v，期望 AppImage 文件 %q", got, err, image)
	}
	replaceFile(t, image)
	if _, err := helperExecutable(); err == nil || !strings.Contains(err.Error(), "被替换") {
		t.Fatalf("AppImage 被替换后返回 %v，应报告已被替换", err)
	}
}

func TestStartHelperLaunchesAppImage(t *testing.T) {
	image := fakeAppImage(t)
	useAppImage(t, image)
	var launched []string
	stop := errors.New("不运行")
	_, err := startHelper(context.Background(), netbind.Egress{}, Options{}, func(net.Conn) {},
		func(cmd []string, _ func(error)) error {
			launched = cmd
			return stop
		}, func() bool { return false }, nil)
	if !errors.Is(err, stop) {
		t.Fatalf("startHelper 返回 %v", err)
	}
	if len(launched) < 2 || launched[0] != image || launched[1] != Command {
		t.Fatalf("helper 命令行 = %q，期望以 AppImage 文件 %q 运行 %s", launched, image, Command)
	}
}
