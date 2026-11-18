// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package tunhost

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Elevate 是自动提权的 Launch：Windows 弹 UAC（ShellExecuteEx 的 runas），macOS 经 osascript 弹
// 管理员密码框，Linux 经 pkexec 由 polkit 认证。
//
// 当前环境用不了自动提权时（Windows 上 UAC 没能拉起 helper，Linux 没有 pkexec、polkit 找不到
// 认证代理或不予授权，macOS 没有可以弹框的图形会话），返回或经 exited 报告的错误满足
// errors.Is(err, ErrElevationUnavailable)，调用方可改让用户另行以管理员身份运行 cmd。
func Elevate(cmd []string, exited func(error)) error {
	return elevate(cmd, exited)
}

var (
	// ErrElevationUnavailable 表示当前环境用不了自动提权。
	ErrElevationUnavailable = errors.New("无法自动提权")
	errElevationCanceled    = errors.New("已取消提权")
)

// outputLimit 是保留的 helper 输出上限。helper 只在退出时打印一行原因，留尾部即可。
const outputLimit = 4 << 10

// tailBuffer 保留写入内容的最后 outputLimit 字节。
type tailBuffer struct {
	buf []byte
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.buf = append(b.buf, p...)
	if over := len(b.buf) - outputLimit; over > 0 {
		b.buf = append(b.buf[:0], b.buf[over:]...)
	}
	return len(p), nil
}

func (b *tailBuffer) String() string {
	return strings.TrimSpace(string(b.buf))
}

// osascriptError 形如「0:180: execution error: 原因 (-128)」，原因与编号之间可能换行。
var osascriptError = regexp.MustCompile(`(?s)execution error: (.*) \((-?\d+)\)\s*$`)

// 编号见 AppleScript 的 userCanceledErr 与 Authorization Services 的
// errAuthorizationInteractionNotAllowed。
const (
	osascriptCanceled      = -128
	osascriptNoInteraction = -60007
)

// osascriptExitReason 把 osascript 的退出结果换成 helper 结束的原因。runErr 为 nil 即 helper
// 正常退出。
func osascriptExitReason(runErr error, output string) error {
	if runErr == nil {
		return nil
	}
	if m := osascriptError.FindStringSubmatch(output); m != nil {
		switch code, _ := strconv.Atoi(m[2]); code {
		case osascriptCanceled:
			return errElevationCanceled
		case osascriptNoInteraction:
			return withOutput(ErrElevationUnavailable, strings.TrimSpace(m[1]))
		}
	}
	return withOutput(runErr, output)
}

// pkexec 认证被用户关掉时以 126 退出；不予授权、找不到认证代理等以 127 退出。认证通过后它 exec
// 成 helper，退出状态就是 helper 的。
const (
	pkexecCanceled    = 126
	pkexecUnavailable = 127
)

// pkexecExitReason 把 pkexec 的退出结果换成 helper 结束的原因。exitCode 为 -1 表示没有正常退出
// （如被信号终止），此时以 runErr 为准。
func pkexecExitReason(runErr error, exitCode int, output string) error {
	switch {
	case runErr == nil:
		return nil
	case exitCode == pkexecCanceled:
		return errElevationCanceled
	case exitCode == pkexecUnavailable:
		return withOutput(ErrElevationUnavailable, output)
	}
	return withOutput(fmt.Errorf("helper 退出（%w）", runErr), output)
}

// withOutput 把 helper 或提权工具的输出接在 err 后面，仍满足 errors.Is(·, err)。
func withOutput(err error, output string) error {
	output = strings.TrimSpace(output)
	if output == "" {
		return err
	}
	return fmt.Errorf("%w: %s", err, output)
}
