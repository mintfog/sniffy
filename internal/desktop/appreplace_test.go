// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build desktop

package desktop

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mintfog/sniffy/internal/service"
)

func TestRemoveStagingBesideOnlyTouchesOwnStaging(t *testing.T) {
	apps := t.TempDir()
	mkdir := func(parts ...string) string {
		dir := filepath.Join(append([]string{apps}, parts...)...)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	idleStaging := func(name string, inner ...string) string {
		dir := mkdir(append([]string{name}, inner...)...)
		old := time.Now().Add(-2 * staleStagingAge)
		if err := os.Chtimes(filepath.Join(apps, name), old, old); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	// 用户改过名的应用包,暂存目录内仍是归档里的固定名字。
	bundle := mkdir("Sniffy Beta.app", "Contents")
	stale := idleStaging("wails-update-1", archiveAppName, "Contents")
	other := idleStaging("wails-update-2", "Other.app")
	downloading := idleStaging("wails-update-3")
	recent := mkdir("wails-update-4", archiveAppName)
	unrelated := mkdir("Tools", archiveAppName)

	removeStagingBeside(apps)

	if _, err := os.Stat(filepath.Dir(filepath.Dir(stale))); !os.IsNotExist(err) {
		t.Errorf("残留的暂存目录应被删除: %v", err)
	}
	for _, keep := range []string{bundle, recent, other, downloading, unrelated} {
		if _, err := os.Stat(keep); err != nil {
			t.Errorf("%s 不应被删除: %v", keep, err)
		}
	}
}

func TestReplaceWithoutDownloadKeepsReplaceRecord(t *testing.T) {
	dir := t.TempDir()
	svc := service.New(nil, nil, dir, t.TempDir())
	if err := svc.BeginAppReplace("1.3.0"); err != nil {
		t.Fatal(err)
	}
	// 读取下载记录失败时还没进入交接,不应撤销替换或改用安装介质。
	var r appReplacer
	if err := r.replace(svc, nil); err == nil {
		t.Fatal("没有已下载的应用归档时应拒绝替换")
	}
	if _, err := os.Stat(filepath.Join(dir, "update-replace")); err != nil {
		t.Errorf("未进入交接时不应撤销替换记录: %v", err)
	}
}
