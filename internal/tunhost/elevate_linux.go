// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package tunhost

import (
	"errors"
	"fmt"
	"os/exec"
)

// pkexec 认证通过后 exec 成 helper，Wait 等的就是 helper 本身，它的输出也经同一对管道回来。
// 没有图形认证代理但有控制终端时，pkexec 在终端里要密码。
func elevate(cmd []string, exited func(error)) error {
	pkexec, err := exec.LookPath("pkexec")
	if err != nil {
		return fmt.Errorf("%w: 找不到 pkexec", ErrElevationUnavailable)
	}
	c := exec.Command(pkexec, cmd...)
	out := &tailBuffer{}
	c.Stdout, c.Stderr = out, out
	if err := c.Start(); err != nil {
		return fmt.Errorf("%w: 运行 pkexec: %v", ErrElevationUnavailable, err)
	}
	go func() {
		err := c.Wait()
		code := -1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		}
		exited(pkexecExitReason(err, code, out.String()))
	}()
	return nil
}
