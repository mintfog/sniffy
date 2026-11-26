// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package process

import "net"

// OwnerFinder 判断本机 TCP 连接是否属于应用名单里的程序，由 NewOwnerFinder 创建，可并发使用。
//
// Detector 要找出任意连接的持有者，Linux、macOS 上得查看全部进程打开的文件；OwnerFinder 只查看名单
// 选中的那些进程，主要开销随它们打开的文件数增长。Windows 的连接表直接带着 PID，两者做法相同。
type OwnerFinder struct{ ownerFinder }

// NewOwnerFinder 创建查找器，当前平台的系统接口不可用时返回错误。
func NewOwnerFinder() (*OwnerFinder, error) {
	f, err := newOwnerFinder()
	if err != nil {
		return nil, err
	}
	return &OwnerFinder{f}, nil
}

// FindOwner 在 apps 选中的进程里找持有这条连接的那个，返回它的 PID。local、remote 是应用一侧
// socket 的本端与对端地址，即透明连接的 RemoteAddr 与 LocalAddr。
//
// 连接不属于名单里的程序时 ok 为 false、err 为 nil，持有者是本进程无权查看的进程（其它用户的进程）
// 时同样如此。应用关了写端、socket 还开着的连接仍算它持有。应用已经关掉的连接：Linux、macOS 上进程
// 不再持有 socket，ok 为 false；Windows 的连接表在连接关完之前仍记着创建它的进程，照旧算它的。地址
// 无效、查不到这条连接、系统接口出错时返回错误。
func (f *OwnerFinder) FindOwner(local, remote net.Addr, apps *AppSet) (pid uint32, ok bool, err error) {
	if apps.Empty() {
		return 0, false, nil
	}
	localAddr, err := connectionAddrPort(local)
	if err != nil {
		return 0, false, err
	}
	remoteAddr, err := connectionAddrPort(remote)
	if err != nil {
		return 0, false, err
	}
	return f.find(localAddr, remoteAddr, apps)
}
