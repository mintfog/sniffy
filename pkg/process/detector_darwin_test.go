// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build darwin

package process

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

func TestMain(m *testing.M) {
	if os.Getenv("SNIFFY_TEST_PROCARGS_CHILD") == "1" {
		fmt.Println("ready")
		_, _ = io.Copy(io.Discard, os.Stdin)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestDarwinDetectorLifecycle(t *testing.T) {
	d, err := NewDarwinDetector()
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := d.Start(); err != nil {
			t.Fatal(err)
		}
	}
	client, _ := darwinTCPPair(t, "tcp4", "127.0.0.1:0")
	if _, err := d.GetProcessByConnection(client.LocalAddr(), client.RemoteAddr()); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() { _ = d.Start() })
		wg.Go(func() { _ = d.Stop() })
	}
	wg.Wait()
	for range 2 {
		if err := d.Stop(); err != nil {
			t.Fatal(err)
		}
	}
	if d.connections != nil || !d.scanStartedAt.IsZero() || !d.scanFinishedAt.IsZero() {
		t.Fatal("检测器未释放快照")
	}
}

func darwinTCPPair(t testing.TB, network, address string) (net.Conn, net.Conn) {
	t.Helper()
	listener, err := net.Listen(network, address)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	client, err := net.Dial(network, listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	server, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	return client, server
}

func startDarwinTestProcess(t *testing.T, child *exec.Cmd) io.WriteCloser {
	t.Helper()
	stdin, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	child.Stderr = os.Stderr
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		if child.ProcessState == nil {
			_ = child.Process.Kill()
			_ = child.Wait()
		}
	})
	output := bufio.NewScanner(stdout)
	if !output.Scan() || output.Text() != "ready" {
		t.Fatalf("子进程未就绪: 输出 %q，读取错误 %v", output.Text(), output.Err())
	}
	return stdin
}

func TestDarwinConnectionLookup(t *testing.T) {
	d, err := NewDarwinDetector()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		network string
		address string
	}{
		{"tcp4", "127.0.0.1:0"},
		{"tcp6", "[::1]:0"},
	} {
		t.Run(test.network, func(t *testing.T) {
			client, server := darwinTCPPair(t, test.network, test.address)
			var wg sync.WaitGroup
			for range 8 {
				wg.Go(func() {
					info, err := d.GetProcessByConnection(server.RemoteAddr(), server.LocalAddr())
					if err != nil || info == nil || info.PID != uint32(os.Getpid()) {
						t.Errorf("识别本机连接 = (%+v, %v)", info, err)
					}
				})
			}
			wg.Wait()
			rows, err := d.GetAllConnections()
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, row := range rows {
				if row.LocalAddr.String() != client.LocalAddr().String() || row.RemoteAddr.String() != client.RemoteAddr().String() {
					continue
				}
				found = true
				if row.Protocol != "TCP" || row.ProcessInfo.PID != uint32(os.Getpid()) {
					t.Fatalf("连接详情 = %+v", row)
				}
			}
			if !found {
				t.Fatal("连接列表缺少客户端 socket")
			}

			wrongIP := *client.LocalAddr().(*net.TCPAddr)
			wrongIP.IP = net.ParseIP("127.0.0.2")
			if test.network == "tcp6" {
				wrongIP.IP = net.ParseIP("::2")
			}
			if info, err := d.GetProcessByConnection(&wrongIP, client.RemoteAddr()); err == nil || info != nil {
				t.Fatalf("误认不同 IP: %+v, %v", info, err)
			}
			_ = client.Close()
			_ = server.Close()
			if info, err := d.GetProcessByConnection(client.LocalAddr(), client.RemoteAddr()); err == nil || info != nil {
				t.Fatalf("命中已关闭的缓存连接: %+v, %v", info, err)
			}
		})
	}
	for _, addr := range []net.Addr{nil, (*net.TCPAddr)(nil), &net.TCPAddr{Port: 1234}} {
		if _, err := d.GetProcessByConnection(addr, &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 80}); err == nil {
			t.Fatalf("接受无效地址 %v", addr)
		}
	}
}

func TestDarwinGetProcessByPID(t *testing.T) {
	d, err := NewDarwinDetector()
	if err != nil {
		t.Fatal(err)
	}
	info, err := d.GetProcessByPID(uint32(os.Getpid()))
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	if info.Path != executable || info.Name != filepath.Base(executable) || info.User != fmt.Sprintf("uid:%d", os.Getuid()) || info.CommandLine == "" {
		t.Fatalf("当前进程信息 = %+v", info)
	}
	for _, pid := range []uint32{0, 1<<31 - 1, ^uint32(0)} {
		if info, err := d.GetProcessByPID(pid); err == nil || info != nil {
			t.Fatalf("无效 PID %d = (%+v, %v)", pid, info, err)
		}
	}
}

func TestDarwinClientProcess(t *testing.T) {
	address := os.Getenv("SNIFFY_LIBPROC_TEST_ADDRESS")
	if address == "" {
		return
	}
	conn, err := net.Dial("tcp4", address)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Println("ready")
	_, _ = io.Copy(io.Discard, os.Stdin)
}

func TestDarwinChildProcessLookup(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	childPath := filepath.Join(t.TempDir(), "来源程序 client")
	if err := os.WriteFile(childPath, data, 0700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	_ = listener.(*net.TCPListener).SetDeadline(time.Now().Add(10 * time.Second))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, childPath, "-test.run=^TestDarwinClientProcess$")
	child.Env = append(os.Environ(), "SNIFFY_LIBPROC_TEST_ADDRESS="+listener.Addr().String())
	startDarwinTestProcess(t, child)

	server, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	d, err := NewDarwinDetector()
	if err != nil {
		t.Fatal(err)
	}
	info, err := d.GetProcessByConnection(server.RemoteAddr(), server.LocalAddr())
	if err != nil || info == nil {
		t.Fatalf("查询子进程: %+v, %v", info, err)
	}
	realPath, err := filepath.EvalSymlinks(childPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.PID != uint32(child.Process.Pid) || info.Path != realPath || info.Name != filepath.Base(childPath) {
		t.Fatalf("子进程信息 = %+v", info)
	}
}

func TestDarwinCommandLine(t *testing.T) {
	for _, test := range []struct {
		name, payload string
		argc          uint32
		want          string
	}{
		{"含空格路径", "/Applications/My App/client\x00\x00\x00\x00\x00/Applications/My App/client\x00--serve\x00ENV=secret\x00", 2, "/Applications/My App/client --serve"},
		{"空参数", "/bin/client\x00\x00\x00\x00\x00client\x00\x00end\x00ENV=secret\x00", 3, "client  end"},
		{"截断", "/bin/client\x00\x00\x00\x00\x00client", 1, ""},
		{"无参数", "/bin/client\x00", 0, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			buffer := make([]byte, 4)
			binary.LittleEndian.PutUint32(buffer, test.argc)
			buffer = append(buffer, test.payload...)
			if got := darwinCommandLine(buffer); got != test.want {
				t.Fatalf("命令行 = %q，期望 %q", got, test.want)
			}
		})
	}
}

func TestDarwinConnectionCacheRefresh(t *testing.T) {
	d, err := NewDarwinDetector()
	if err != nil {
		t.Fatal(err)
	}
	first, _ := darwinTCPPair(t, "tcp4", "127.0.0.1:0")
	if _, err := d.GetProcessByConnection(first.LocalAddr(), first.RemoteAddr()); err != nil {
		t.Fatal(err)
	}
	second, server := darwinTCPPair(t, "tcp4", "127.0.0.1:0")
	if info, err := d.GetProcessByConnection(second.LocalAddr(), second.RemoteAddr()); err != nil || info.PID != uint32(os.Getpid()) {
		t.Fatalf("新连接被旧快照遗漏: %+v, %v", info, err)
	}
	if err := server.(*net.TCPConn).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	_ = second.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.Copy(io.Discard, second); err != nil {
		t.Fatal(err)
	}
	_ = d.Stop()
	if info, err := d.GetProcessByConnection(second.LocalAddr(), second.RemoteAddr()); err != nil || info.PID != uint32(os.Getpid()) {
		t.Fatalf("无法识别半关闭连接: %+v, %v", info, err)
	}
}

func TestDarwinManyDescriptors(t *testing.T) {
	var files []*os.File
	defer func() {
		for _, file := range files {
			_ = file.Close()
		}
	}()
	for range 300 {
		file, err := os.Open(os.DevNull)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, file)
	}
	client, _ := darwinTCPPair(t, "tcp4", "127.0.0.1:0")
	d, err := NewDarwinDetector()
	if err != nil {
		t.Fatal(err)
	}
	info, err := d.GetProcessByConnection(client.LocalAddr(), client.RemoteAddr())
	if err != nil || info.PID != uint32(os.Getpid()) {
		t.Fatalf("描述符扩容后识别连接: %+v, %v", info, err)
	}
}

func TestDarwinCommandLineEmptyArguments(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	d, err := NewDarwinDetector()
	if err != nil {
		t.Fatal(err)
	}
	type commandLineCase struct {
		path string
		args []string
	}
	tests := []commandLineCase{
		{executable, []string{"", "value"}},
		{executable, []string{"", "", "value", ""}},
		{executable, []string{""}},
		{executable, []string{"", ""}},
		{executable, []string{"client", "", "value", ""}},
	}
	dir := t.TempDir()
	for padding := range 8 {
		path := filepath.Join(dir, strings.Repeat("a", padding+1))
		if err := os.Symlink(executable, path); err != nil {
			t.Fatal(err)
		}
		tests = append(tests, commandLineCase{path, []string{"", "value"}})
	}
	for i, test := range tests {
		t.Run(fmt.Sprintf("参数边界%d", i), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, test.path)
			child.Args = test.args
			child.Env = []string{"SNIFFY_TEST_PROCARGS_CHILD=1", "SNIFFY_TEST_ENV_SENTINEL=fixture"}
			stdin := startDarwinTestProcess(t, child)
			info, err := d.GetProcessByPID(uint32(child.Process.Pid))
			if err != nil {
				t.Fatal(err)
			}
			if got, want := info.CommandLine, strings.Join(test.args, " "); got != want {
				t.Fatalf("命令行 = %q，期望 %q", got, want)
			}
			if err := stdin.Close(); err != nil {
				t.Fatal(err)
			}
			if err := child.Wait(); err != nil {
				t.Fatalf("参数测试子进程退出失败: %v", err)
			}
		})
	}
}

func TestDarwinSlowScanCache(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api, key := darwinSingleProcessAPI()
		list := api.listPIDs
		scans := 0
		api.listPIDs = func(buffer *int32, size int32) int32 {
			if buffer == nil {
				scans++
				time.Sleep(120 * time.Millisecond)
			}
			return list(buffer, size)
		}
		d := &DarwinDetector{api: api}
		for range 3 {
			if _, err := d.findConnection(key); err != nil {
				t.Fatal(err)
			}
		}
		if scans != 1 {
			t.Fatalf("连续查询进行了 %d 次扫描，期望 1 次", scans)
		}
		time.Sleep(101 * time.Millisecond)
		if _, err := d.findConnection(key); err != nil {
			t.Fatal(err)
		}
		if scans != 2 {
			t.Fatalf("过期后扫描次数 = %d，期望 2", scans)
		}
	})
}
