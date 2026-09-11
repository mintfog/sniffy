// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package app

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mintfog/sniffy/internal/platform"
)

type tokenTestProcess struct {
	cmd        *exec.Cmd
	input      io.WriteCloser
	lines      chan string
	stderr     bytes.Buffer
	outputDone chan struct{}
}

func startTokenTestProcess(t *testing.T, operation string) *tokenTestProcess {
	t.Helper()
	p := &tokenTestProcess{cmd: appTestCommand(t), lines: make(chan string, 16), outputDone: make(chan struct{})}
	p.cmd.Env = append(p.cmd.Env, "SNIFFY_TOKEN_TEST_OPERATION="+operation)
	var err error
	p.input, err = p.cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := p.cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	p.cmd.Stderr = &p.stderr
	if err := p.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = p.input.Close()
		if p.cmd.ProcessState == nil {
			_ = p.cmd.Process.Kill()
			_ = p.cmd.Wait()
		}
		<-p.outputDone
	})
	go func() {
		defer close(p.outputDone)
		defer close(p.lines)
		scanner := bufio.NewScanner(output)
		for scanner.Scan() {
			select {
			case p.lines <- scanner.Text():
			case <-t.Context().Done():
				return
			}
		}
	}()
	if got := p.nextLine(t); got != "ready" {
		t.Fatalf("子进程未就绪: %q", got)
	}
	return p
}

func (p *tokenTestProcess) nextLine(t *testing.T) string {
	t.Helper()
	select {
	case line, ok := <-p.lines:
		if !ok {
			t.Fatal("token 子进程提前关闭输出")
		}
		return line
	case <-time.After(10 * time.Second):
		t.Fatal("等待 token 子进程响应超时")
		return ""
	}
}

func (p *tokenTestProcess) proceed(t *testing.T) {
	t.Helper()
	if _, err := io.WriteString(p.input, "start\n"); err != nil {
		t.Fatal(err)
	}
}

func (p *tokenTestProcess) wait(t *testing.T) {
	t.Helper()
	// StdoutPipe 必须读完后再 Wait，避免退出时的输出被提前关闭。
	<-p.outputDone
	if err := p.cmd.Wait(); err != nil {
		t.Fatalf("token 子进程失败: %v\n%s", err, p.stderr.String())
	}
}

func runTokenTestChild(t *testing.T) {
	t.Helper()
	fmt.Println("ready")
	var start string
	if _, err := fmt.Fscanln(os.Stdin, &start); err != nil {
		t.Fatal(err)
	}
	fmt.Println("started")
	switch os.Getenv("SNIFFY_TOKEN_TEST_OPERATION") {
	case "create":
		token, _, err := EnsureAPIToken()
		if err != nil {
			t.Fatal(err)
		}
		fmt.Printf("%s false\n", token)
	case "rotate":
		token, rotated, err := EnsureTokenSecrecy()
		if err != nil {
			t.Fatal(err)
		}
		fmt.Printf("%s %t\n", token, rotated)
	case "hold":
		dir, err := platform.ConfigDir()
		if err != nil {
			t.Fatal(err)
		}
		unlock, err := lockTokenDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		defer unlock()
		fmt.Println("locked")
		// 父进程强制终止持锁者，验证内核释放锁，不经过正常解锁路径。
		_, _ = io.Copy(io.Discard, os.Stdin)
	default:
		t.Fatal("未知 token 子进程操作")
	}
}

func TestAPITokenCompetingProcesses(t *testing.T) {
	for _, tt := range []struct {
		operation     string
		wantRotations int
	}{
		{"create", 0},
		{"rotate", 1},
	} {
		t.Run(tt.operation, func(t *testing.T) {
			if inAppTestSubprocess(t) {
				runTokenTestChild(t)
				return
			}
			if tt.operation == "rotate" && runtime.GOOS == "windows" {
				t.Skip("POSIX 权限位轮换场景")
			}
			isolateAppDirs(t)
			dir, err := platform.ConfigDir()
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, apiTokenFileName)
			if tt.operation == "rotate" {
				writeAppFixture(t, path, "exposed-token")
				if err := os.Chmod(path, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			var processes []*tokenTestProcess
			for i := 0; i < 6; i++ {
				processes = append(processes, startTokenTestProcess(t, tt.operation))
			}
			for _, p := range processes {
				p.proceed(t)
			}
			var winner string
			rotations := 0
			for _, p := range processes {
				if got := p.nextLine(t); got != "started" {
					t.Fatalf("子进程未开始执行: %q", got)
				}
				result := p.nextLine(t)
				token, rotation, ok := strings.Cut(result, " ")
				rotated, err := strconv.ParseBool(rotation)
				if !ok || err != nil {
					t.Fatalf("子进程返回格式错误: %q", result)
				}
				if winner == "" {
					winner = token
				}
				if token != winner || len(token) != 64 {
					t.Fatalf("竞争进程返回的 token 不一致: %q", result)
				}
				if rotated {
					rotations++
				}
				p.wait(t)
			}
			if rotations != tt.wantRotations {
				t.Fatalf("轮换次数=%d，期望 %d", rotations, tt.wantRotations)
			}
			if got := LoadAPIToken(); got != winner {
				t.Fatal("磁盘 token 与竞争结果不一致")
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if entry.Name() != apiTokenFileName && entry.Name() != "api_token.lock" {
					t.Fatalf("竞争结束后残留文件: %s", entry.Name())
				}
			}
		})
	}
}

func TestTokenLockReleasedAfterProcessExit(t *testing.T) {
	if inAppTestSubprocess(t) {
		runTokenTestChild(t)
		return
	}
	isolateAppDirs(t)
	holder := startTokenTestProcess(t, "hold")
	holder.proceed(t)
	if got := holder.nextLine(t); got != "started" {
		t.Fatalf("持锁进程未开始执行: %q", got)
	}
	if got := holder.nextLine(t); got != "locked" {
		t.Fatalf("未持有锁: %q", got)
	}
	contender := startTokenTestProcess(t, "create")
	contender.proceed(t)
	if got := contender.nextLine(t); got != "started" {
		t.Fatalf("竞争进程未开始执行: %q", got)
	}
	select {
	case line := <-contender.lines:
		t.Fatalf("持锁进程退出前竞争者已返回: %q", line)
	case <-time.After(150 * time.Millisecond):
	}
	if err := holder.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := holder.cmd.Wait(); err == nil {
		t.Fatal("持锁进程应被强制终止")
	}
	result := contender.nextLine(t)
	token, rotation, ok := strings.Cut(result, " ")
	if !ok || rotation != "false" {
		t.Fatalf("创建结果格式错误: %q", result)
	}
	contender.wait(t)
	if len(token) != 64 || LoadAPIToken() != token {
		t.Fatal("持锁进程退出后未能发布凭据")
	}
}
