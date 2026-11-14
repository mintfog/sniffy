// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package http

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

// benchConnTimeout 是单条基准连接从拨号到关闭的总期限。go test 的 -timeout 在基准阶段
// 不生效，代理停止响应时靠它让基准失败退出。
const benchConnTimeout = 10 * time.Second

// dialBenchTunnel 经代理建立到 target 的 CONNECT 隧道。隧道内的数据须从返回的 reader 读取。
func dialBenchTunnel(proxyAddr, target string) (_ net.Conn, _ *bufio.Reader, err error) {
	conn, err := net.DialTimeout("tcp", proxyAddr, benchConnTimeout)
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		if err != nil {
			_ = conn.Close()
		}
	}()

	if err := conn.SetDeadline(time.Now().Add(benchConnTimeout)); err != nil {
		return nil, nil, err
	}
	if _, err := fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target); err != nil {
		return nil, nil, fmt.Errorf("写 CONNECT 请求: %w", err)
	}
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("读 CONNECT 响应: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("CONNECT 状态码 = %d，期望 200", resp.StatusCode)
	}
	return conn, reader, nil
}

// echoThroughTunnel 新建一条隧道并完成 1 字节往返。
func echoThroughTunnel(proxyAddr, target string) error {
	conn, reader, err := dialBenchTunnel(proxyAddr, target)
	if err != nil {
		return err
	}
	defer conn.Close()

	if _, err := conn.Write([]byte{'x'}); err != nil {
		return fmt.Errorf("写隧道: %w", err)
	}
	echoed, err := reader.ReadByte()
	if err != nil {
		return fmt.Errorf("读隧道: %w", err)
	}
	if echoed != 'x' {
		return fmt.Errorf("回显字节 = %q，期望 'x'", echoed)
	}
	return nil
}

// getThroughTunnel 新建一条隧道并在其中完成一次明文 HTTP 请求。
func getThroughTunnel(proxyAddr, target string) error {
	conn, reader, err := dialBenchTunnel(proxyAddr, target)
	if err != nil {
		return err
	}
	defer conn.Close()

	if _, err := fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", target); err != nil {
		return fmt.Errorf("写隧道内请求: %w", err)
	}
	resp, err := http.ReadResponse(reader, nil)
	if err != nil {
		return fmt.Errorf("读隧道内响应: %w", err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return fmt.Errorf("读隧道内响应体: %w", err)
	}
	if resp.StatusCode != http.StatusOK || string(body) != "ok" {
		return fmt.Errorf("隧道内响应 = %d %q，期望 200 \"ok\"", resp.StatusCode, body)
	}
	return nil
}

func serveEcho(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go func() {
			defer conn.Close()
			_, _ = io.Copy(conn, conn)
		}()
	}
}

// BenchmarkConnectBlindTunnel 每轮新建一条解密范围外的 CONNECT 隧道并完成 1 字节往返。
func BenchmarkConnectBlindTunnel(b *testing.B) {
	restoreScopeGlobals(b)
	SetDecryptScope(false, "all", nil, nil)
	tunnelUpstream.Store(nil)

	origin, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatal(err)
	}
	defer origin.Close()
	go serveEcho(origin)
	proxy := startProxy(b)
	defer proxy.Close()

	proxyAddr, target := proxy.Addr().String(), origin.Addr().String()
	b.ReportAllocs()
	for b.Loop() {
		if err := echoThroughTunnel(proxyAddr, target); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkConnectPlainHTTP 每轮新建一条解密范围内的 CONNECT 隧道，并在其中完成一次明文 HTTP 请求。
func BenchmarkConnectPlainHTTP(b *testing.B) {
	restoreScopeGlobals(b)
	SetDecryptScope(true, "all", nil, nil)
	tunnelUpstream.Store(nil)

	originLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatal(err)
	}
	origin := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	})}
	go func() { _ = origin.Serve(originLn) }()
	defer origin.Close()
	proxy := startProxy(b)
	defer proxy.Close()

	proxyAddr, target := proxy.Addr().String(), originLn.Addr().String()
	b.ReportAllocs()
	for b.Loop() {
		if err := getThroughTunnel(proxyAddr, target); err != nil {
			b.Fatal(err)
		}
	}
}
