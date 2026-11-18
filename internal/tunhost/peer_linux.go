// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package tunhost

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"strconv"

	"golang.org/x/sys/unix"
)

func peerPID(c *net.UnixConn) (int, error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return 0, err
	}
	var cred *unix.Ucred
	var credErr error
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return 0, err
	}
	if credErr != nil {
		return 0, credErr
	}
	return int(cred.Pid), nil
}

// executableOf 经 /proc/<pid>/exe 取进程正在运行的那个文件，可执行文件在磁盘上被替换后仍指向
// 原来的文件。
//
// 以 hidepid=2（invisible）挂载的 procfs 把本进程无权查看的进程整个藏起来，读到的是不存在而不是
// 无权访问。进程其实还在时按无权查看处理，与普通 procfs 上的 EACCES 一致。
func executableOf(pid int) (os.FileInfo, error) {
	fi, err := os.Stat("/proc/" + strconv.Itoa(pid) + "/exe")
	if errors.Is(err, fs.ErrNotExist) && unix.Kill(pid, 0) != unix.ESRCH {
		return nil, fmt.Errorf("进程 %d 在 /proc 中不可见: %w", pid, fs.ErrPermission)
	}
	return fi, err
}
