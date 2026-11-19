// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package tunhost

import (
	"context"
	"errors"
	"maps"
	"net"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// 通道 socket 不能沿用 TEMP 的权限：TEMP 指向共享目录时，别的用户也能连入。只授权当前用户与
// 提权后的管理员。
func TestChannelAccess(t *testing.T) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{user.User.Sid.String(): true, admins.String(): true}
	stop := errors.New("检查完毕")
	_, err = StartHelper(context.Background(), testEgress(t), Options{}, func(c net.Conn) { c.Close() }, func(cmd []string, _ func(error)) error {
		sd, err := windows.GetNamedSecurityInfo(cmd[3], windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
		if err != nil {
			return err
		}
		acl, _, err := sd.DACL()
		if err != nil {
			return err
		}
		got := map[string]bool{}
		for i := range uint32(acl.AceCount) {
			var ace *windows.ACCESS_ALLOWED_ACE
			if err := windows.GetAce(acl, i, &ace); err != nil {
				return err
			}
			got[(*windows.SID)(unsafe.Pointer(&ace.SidStart)).String()] = true
		}
		if !maps.Equal(got, want) {
			t.Errorf("通道 %s 的 DACL 授予 %v，应恰好是当前用户与 Administrators %v", cmd[3], got, want)
		}
		return stop
	})
	if !errors.Is(err, stop) {
		t.Fatal(err)
	}
}
