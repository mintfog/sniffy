// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package platform

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// grantedTo 断言 path 的 DACL 恰好授予 want 中的每个 SID，不授予别人。目录上带继承的
// GENERIC_ALL 会被系统拆成作用于目录本身与仅供继承的两条 ACE，故按 SID 去重后比较。
func grantedTo(t *testing.T, path string, want ...*windows.SID) {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for i := range uint32(acl.AceCount) {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, i, &ace); err != nil {
			t.Fatal(err)
		}
		got[(*windows.SID)(unsafe.Pointer(&ace.SidStart)).String()] = true
	}
	wantSet := map[string]bool{}
	for _, sid := range want {
		wantSet[sid.String()] = true
	}
	if !maps.Equal(got, wantSet) {
		t.Errorf("%s 的 DACL 授予 %v，应恰好是 %v", path, slices.Sorted(maps.Keys(got)), slices.Sorted(maps.Keys(wantSet)))
	}
}

func userSID(t *testing.T) *windows.SID {
	t.Helper()
	sid, err := currentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	return sid
}

func ownerOnly(t *testing.T, path string) {
	t.Helper()
	grantedTo(t, path, userSID(t))
}

func TestRestrictToOwnerAndAdmins(t *testing.T) {
	dir := t.TempDir()
	if err := RestrictToOwnerAndAdmins(dir); err != nil {
		t.Fatal(err)
	}
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		t.Fatal(err)
	}
	grantedTo(t, dir, userSID(t), admins)
	file := filepath.Join(dir, "child")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	grantedTo(t, file, userSID(t), admins)
}

func TestRestrictToOwnerDirInherits(t *testing.T) {
	dir := t.TempDir()
	if err := RestrictToOwner(dir); err != nil {
		t.Fatal(err)
	}
	ownerOnly(t, dir)
	file := filepath.Join(dir, "child")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ownerOnly(t, file)
}

func TestRestrictToOwnerFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(file, nil, 0o666); err != nil {
		t.Fatal(err)
	}
	if err := RestrictToOwner(file); err != nil {
		t.Fatal(err)
	}
	ownerOnly(t, file)
}
