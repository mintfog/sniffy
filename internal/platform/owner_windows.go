// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package platform

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// RestrictToOwner 给 path 设受保护的 DACL，只允许当前用户访问，不再继承父目录的权限。
// path 是目录时，此后在其中新建的文件与子目录继承同样的限制。
func RestrictToOwner(path string) error {
	user, err := currentUserSID()
	if err != nil {
		return err
	}
	return restrictTo(path, trustee(user, windows.TRUSTEE_IS_USER))
}

// RestrictToOwnerAndAdmins 同 RestrictToOwner，另外允许已提权的管理员访问：提权后的进程可能
// 属于另一个管理员账户（标准用户在 UAC 提示里输入管理员凭据）。未提权进程令牌里的
// Administrators 组只用于拒绝，不因此获得访问权。
func RestrictToOwnerAndAdmins(path string) error {
	user, err := currentUserSID()
	if err != nil {
		return err
	}
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return fmt.Errorf("获取 Administrators 组 SID: %w", err)
	}
	return restrictTo(path, trustee(user, windows.TRUSTEE_IS_USER), trustee(admins, windows.TRUSTEE_IS_WELL_KNOWN_GROUP))
}

func currentUserSID() (*windows.SID, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("获取当前用户 SID: %w", err)
	}
	return user.User.Sid, nil
}

func trustee(sid *windows.SID, kind windows.TRUSTEE_TYPE) windows.TRUSTEE {
	return windows.TRUSTEE{
		TrusteeForm:  windows.TRUSTEE_IS_SID,
		TrusteeType:  kind,
		TrusteeValue: windows.TrusteeValueFromSID(sid),
	}
}

// restrictTo 给 path 设受保护的 DACL，只授予 trustees 完全访问。
func restrictTo(path string, trustees ...windows.TRUSTEE) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	inheritance := uint32(windows.NO_INHERITANCE)
	if info.IsDir() {
		inheritance = windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT
	}
	entries := make([]windows.EXPLICIT_ACCESS, len(trustees))
	for i, t := range trustees {
		entries[i] = windows.EXPLICIT_ACCESS{
			AccessPermissions: windows.GENERIC_ALL,
			AccessMode:        windows.GRANT_ACCESS,
			Inheritance:       inheritance,
			Trustee:           t,
		}
	}
	acl, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		return fmt.Errorf("构造 DACL: %w", err)
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, acl, nil)
}
