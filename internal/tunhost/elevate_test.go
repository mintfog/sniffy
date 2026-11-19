// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package tunhost

import (
	"context"
	"errors"
	"net"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestPkexecExitReason(t *testing.T) {
	t.Parallel()
	failed := &exec.ExitError{}
	tests := []struct {
		name   string
		err    error
		code   int
		output string
		is     error
		has    string
	}{
		{name: "正常退出", err: nil, code: 0},
		{name: "关掉认证框", err: failed, code: 126, is: ErrElevationCanceled},
		{name: "找不到认证代理", err: failed, code: 127, output: "Error executing command as another user: No authentication agent found.\n",
			is: ErrElevationUnavailable, has: "No authentication agent found."},
		{name: "helper 出错", err: errors.New("exit status 1"), code: 1, output: "tun-helper: 通道对端不是 sniffy\n", has: "helper 退出（exit status 1）: tun-helper: 通道对端不是 sniffy"},
		{name: "被信号终止", err: errors.New("signal: killed"), code: -1, has: "signal: killed"},
	}
	for _, tt := range tests {
		got := pkexecExitReason(tt.err, tt.code, tt.output)
		checkReason(t, tt.name, got, tt.err == nil, tt.is, tt.has)
	}
}

func TestOsascriptExitReason(t *testing.T) {
	t.Parallel()
	failed := errors.New("exit status 1")
	tests := []struct {
		name   string
		err    error
		output string
		is     error
		has    string
	}{
		{name: "正常退出"},
		{name: "取消密码框", err: failed, output: "0:180: execution error: User canceled. (-128)\n", is: ErrElevationCanceled},
		{name: "没有图形会话", err: failed, output: "0:180: execution error: The administrator user name or password was incorrect. (-60007)\n",
			is: ErrElevationUnavailable, has: "password was incorrect."},
		{name: "helper 出错", err: failed, output: "0:180: execution error: tun-helper: 通道对端不是 sniffy (1)\n",
			has: "exit status 1: 0:180: execution error: tun-helper: 通道对端不是 sniffy (1)"},
	}
	for _, tt := range tests {
		got := osascriptExitReason(tt.err, tt.output)
		checkReason(t, tt.name, got, tt.err == nil, tt.is, tt.has)
	}
}

func checkReason(t *testing.T, name string, got error, wantNil bool, is error, has string) {
	t.Helper()
	if wantNil {
		if got != nil {
			t.Errorf("%s: 得到 %v，应为 nil", name, got)
		}
		return
	}
	if got == nil {
		t.Errorf("%s: 得到 nil", name)
		return
	}
	if is != nil && !errors.Is(got, is) {
		t.Errorf("%s: 得到 %v，应满足 errors.Is(·, %v)", name, got, is)
	}
	if is == nil && (errors.Is(got, ErrElevationCanceled) || errors.Is(got, ErrElevationUnavailable)) {
		t.Errorf("%s: 得到 %v，不应归为取消或不可用", name, got)
	}
	if !strings.Contains(got.Error(), has) {
		t.Errorf("%s: 得到 %q，应含 %q", name, got, has)
	}
}

func TestTailBuffer(t *testing.T) {
	t.Parallel()
	var b tailBuffer
	_, _ = b.Write([]byte(strings.Repeat("a", outputLimit)))
	_, _ = b.Write([]byte("尾部\n"))
	got := b.String()
	if len(got) > outputLimit || !strings.HasSuffix(got, "尾部") {
		t.Errorf("保留了 %d 字节，结尾 %q，应只留最后 %d 字节", len(got), got[len(got)-10:], outputLimit)
	}
}

func TestStartHelperStopsWhenHelperExits(t *testing.T) {
	t.Parallel()
	// 不放弃等待的话会一直等到 ctx 超时，错误就成了 DeadlineExceeded。
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := startHelper(ctx, testEgress(t), Options{}, func(c net.Conn) { c.Close() }, func(_ []string, exited func(error)) error {
		go exited(ErrElevationCanceled)
		return nil
	}, absent)
	if !errors.Is(err, ErrElevationCanceled) || !strings.Contains(err.Error(), "没有连入就结束了") {
		t.Fatalf("开启返回 %v，应报告 helper 先行结束及原因", err)
	}
}

func TestStartHelperReportsRejectionReason(t *testing.T) {
	t.Parallel()
	_, err := startHelper(context.Background(), testEgress(t), Options{}, func(c net.Conn) { c.Close() }, func(cmd []string, exited func(error)) error {
		go func() {
			// 如同 helper 核对对端失败：连上后一个字节不发就断开，随即带着输出退出。
			if c, err := net.Dial("unix", cmd[3]); err == nil {
				c.Close()
			}
			time.Sleep(100 * time.Millisecond)
			exited(errors.New("helper 退出（exit status 1）: tun-helper: 通道对端不是 sniffy"))
		}()
		return nil
	}, absent)
	if !errors.Is(err, errExitedEarly) || !strings.Contains(err.Error(), "通道对端不是 sniffy") {
		t.Fatalf("开启返回 %v，应带上 helper 输出的拒绝原因", err)
	}
}
