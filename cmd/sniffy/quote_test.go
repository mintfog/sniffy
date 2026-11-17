// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package main

import (
	"os/exec"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

var awkwardArgs = []string{
	"/opt/my apps/sniffy", "tun-helper", "--endpoint", "/tmp/it's $HOME/`id`/a\\b/ch", "", "@x,y", "~",
}

func TestQuoteCommandPOSIX(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("需要 POSIX shell")
	}
	// 把命令行交给 sh 解析，逐个打印参数：应与原参数完全一致，没有任何展开。
	line := quoteCommand("linux", append([]string{"printf", "%s\\n"}, awkwardArgs...))
	out, err := exec.Command("sh", "-c", line).Output()
	if err != nil {
		t.Fatalf("sh 执行 %s: %v", line, err)
	}
	got := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	if !reflect.DeepEqual(got, awkwardArgs) {
		t.Errorf("sh 解析 %s\n得到 %q\n应为 %q", line, got, awkwardArgs)
	}
}

func TestQuoteCommandPowerShell(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		cmd  []string
		want string
	}{
		{
			[]string{`C:\Program Files\sniffy\sniffy.exe`, "tun-helper", "--endpoint", `C:\Users\Li Lei\AppData\Local\Temp\sniffy-tun-1\ch`},
			`& 'C:\Program Files\sniffy\sniffy.exe' tun-helper --endpoint 'C:\Users\Li Lei\AppData\Local\Temp\sniffy-tun-1\ch'`,
		},
		{
			[]string{`D:\sniffy\sniffy.exe`, "tun-helper", "--endpoint", `C:\Users\o'neil\Temp\ch`},
			`& D:\sniffy\sniffy.exe tun-helper --endpoint 'C:\Users\o''neil\Temp\ch'`,
		},
		{
			[]string{`D:\sniffy\sniffy.exe`, "@x", "a,b", "$env:TEMP"},
			`& D:\sniffy\sniffy.exe '@x' 'a,b' '$env:TEMP'`,
		},
	} {
		if got := quoteCommand("windows", tc.cmd); got != tc.want {
			t.Errorf("得到 %s\n应为 %s", got, tc.want)
		}
	}
}
