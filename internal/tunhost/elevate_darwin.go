// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package tunhost

import (
	"fmt"
	"os/exec"
)

// elevateScript 以管理员身份运行 argv 第 2 项起的命令行，argv 第 1 项是密码框里的说明。命令行与
// 说明都经 argv 传入、由 quoted form of 转成 shell 参数，不拼进脚本文本。
//
// do shell script 等命令结束才返回，osascript 因此一直运行到 helper 退出，它的退出结果就是
// helper 的：helper 以非零状态退出时，osascript 报出的编号即退出状态，原因即 helper 的标准错误输出。
const elevateScript = `on run argv
	set cmd to ""
	repeat with i from 2 to count of argv
		set cmd to cmd & quoted form of (item i of argv) & " "
	end repeat
	do shell script cmd with prompt (item 1 of argv) with administrator privileges
end run`

const elevationPrompt = "sniffy 需要管理员权限来开启虚拟网卡。"

func elevate(cmd []string, exited func(error)) error {
	c := exec.Command("/usr/bin/osascript", append([]string{"-e", elevateScript, elevationPrompt}, cmd...)...)
	out := &tailBuffer{}
	c.Stdout, c.Stderr = out, out
	if err := c.Start(); err != nil {
		return fmt.Errorf("%w: 运行 osascript: %v", ErrElevationUnavailable, err)
	}
	go func() {
		err := c.Wait()
		exited(osascriptExitReason(err, out.String()))
	}()
	return nil
}
