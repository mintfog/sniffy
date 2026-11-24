// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package tunhost

import (
	"errors"
	"fmt"
	"net"
	"os"
)

// 通道两端在交换任何数据之前先核对对端进程，PID 取自 socket 本身（Linux SO_PEERCRED、macOS
// LOCAL_PEERPID、Windows SIO_AF_UNIX_GETPEERPID），连接方取到的是监听方的 PID。
//
// helper 要求对端是命令行里指定的主进程，且运行着与自己相同的可执行文件。这挡得住普通的通道替换：
// 别的用户另起一个 sniffy、把共享临时目录里的通道目录换成自己的，PID 对不上；直接监听通道的其它
// 程序，可执行文件对不上。在 AppImage 里运行时比对的是 AppImage 文件，见 appImage。
//
// 两项检查都不能证明实际处理连接的是哪个进程：PID 记的是调用 listen 的进程，可执行文件是查询时的
// 映像，而监听描述符可以由别的进程持有（如监听后 fork、再 exec 成 sniffy）。自己拉起 helper 的程序
// 可以借此通过检查，这是已知的局限。主进程这一侧见 admitControl。

// errPeerGone 表示取对端的 PID 时对端已经关闭了连接，无从核对。macOS 的 LOCAL_PEERPID 只在连接还在时
// 取得到；Linux 的 SO_PEERCRED 在建立连接时记下，不受对端关闭影响。
var errPeerGone = errors.New("对端已关闭连接")

// peerProcess 返回通道对端的 PID。
func peerProcess(c *net.UnixConn) (int, error) {
	pid, err := peerPID(c)
	if err != nil {
		return 0, fmt.Errorf("取通道对端的进程: %w", err)
	}
	// Linux 上对端在本进程看不到的 PID 命名空间里时为 0。
	if pid <= 0 {
		return 0, errors.New("取不到通道对端的进程")
	}
	return pid, nil
}

// checkRunningImage 确认 path 上的文件就是本进程正在运行的可执行文件。sniffy 运行期间被同路径替换
// （如升级）后两者不同，按 path 拉起的 helper 会判定本进程不是同一个程序。
func checkRunningImage(path string) error {
	self, err := executableOf(os.Getpid())
	if err != nil {
		return fmt.Errorf("读取本进程的可执行文件: %w", err)
	}
	file, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !os.SameFile(self, file) {
		return fmt.Errorf("%s 在 sniffy 启动后被替换（如已更新），请重启 sniffy 后再开启", path)
	}
	return nil
}

// checkSameExecutable 确认进程 pid 运行的是与本进程相同的可执行文件。比的是文件本身，不受符号链接、
// 硬链接与路径写法影响。无权查看该进程时返回的错误满足 errors.Is(err, fs.ErrPermission)。
func checkSameExecutable(pid int) error {
	peer, err := executableOf(pid)
	if err != nil {
		return fmt.Errorf("读取进程 %d 的可执行文件: %w", pid, err)
	}
	self, err := executableOf(os.Getpid())
	if err != nil {
		// 不带上原错误的身份：调用方按 fs.ErrPermission 判断的是对端能否查看。
		return fmt.Errorf("读取本进程的可执行文件: %v", err)
	}
	if !os.SameFile(peer, self) {
		return fmt.Errorf("进程 %d 运行的不是本程序", pid)
	}
	return nil
}
