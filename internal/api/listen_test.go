// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package api

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/mintfog/sniffy/internal/core"
	"github.com/mintfog/sniffy/internal/service"
)

func TestListenTLSCertErrorIsSynchronous(t *testing.T) {
	s := New(nil, nil, nil, nil, "127.0.0.1:0", "tok")
	s.SetTLS("/nonexistent/cert.pem", "/nonexistent/key.pem")
	if err := s.Listen(); err == nil {
		t.Fatal("证书缺失时 Listen 应返回错误")
	}
}

func TestListenBindErrorIsSynchronous(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	s := New(nil, nil, nil, nil, ln.Addr().String(), "tok")
	if err := s.Listen(); err == nil {
		t.Fatal("端口被占用时 Listen 应返回错误")
	}
}

func TestServeBeforeListenFails(t *testing.T) {
	s := New(nil, nil, nil, nil, "127.0.0.1:0", "tok")
	if err := s.Serve(); err == nil {
		t.Fatal("未 Listen 就 Serve 应返回错误")
	}
}

func TestListenThenServeAndStop(t *testing.T) {
	s := New(service.New(nil, core.NewEventBus(), "", ""), nil, nil, nil, "127.0.0.1:0", "tok")
	if err := s.Listen(); err != nil {
		t.Fatalf("Listen 失败: %v", err)
	}
	errc := make(chan error, 1)
	go func() { errc <- s.Serve() }()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.Stop(ctx); err != nil {
		t.Fatalf("Stop 失败: %v", err)
	}
	select {
	case err := <-errc:
		if err != nil && err.Error() != "http: Server closed" {
			t.Fatalf("Serve 退出异常: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve 未在关闭后退出")
	}
}
