// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package websocket

import (
	"bufio"
	"crypto"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/mintfog/sniffy/internal/outboundtls"
)

type wsTLSHandshakeResult struct {
	head string
	err  error
}

func startRecordedWSTLSHandshake(t *testing.T, config *tls.Config) (string, <-chan wsTLSHandshakeResult) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	deadline := armHandshakeListener(t, listener)
	done := make(chan wsTLSHandshakeResult, 1)
	go func() {
		raw, err := listener.Accept()
		if err != nil {
			done <- wsTLSHandshakeResult{err: err}
			return
		}
		defer raw.Close()
		_ = raw.SetDeadline(deadline)
		conn := tls.Server(raw, config)
		if err := conn.Handshake(); err != nil {
			done <- wsTLSHandshakeResult{err: err}
			return
		}
		head, err := readHeadBlock(bufio.NewReader(conn))
		if err == nil {
			_, err = io.WriteString(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		}
		done <- wsTLSHandshakeResult{head: string(head), err: err}
	}()
	return listener.Addr().String(), done
}

func waitRecordedWSTLSHandshake(t *testing.T, done <-chan wsTLSHandshakeResult) wsTLSHandshakeResult {
	t.Helper()
	select {
	case result := <-done:
		return result
	case <-time.After(websocketFixtureTimeout):
		t.Fatal("TLS 上游未结束握手")
		return wsTLSHandshakeResult{}
	}
}

func TestDialUpstreamFaithfulTLSHostPolicy(t *testing.T) {
	previousPolicy := outboundTLSPolicy.Load()
	t.Cleanup(func() { SetOutboundTLSPolicy(previousPolicy) })
	var policy outboundtls.Policy
	for _, tc := range []struct {
		name    string
		hosts   []string
		useNil  bool
		allowed bool
	}{
		{name: "默认拒绝未知 CA", useNil: true},
		{name: "例外不外溢到其他主机", hosts: []string{"localhost"}},
		{name: "精确主机例外", hosts: []string{"127.0.0.1"}, allowed: true},
		{name: "撤销后恢复验证"},
		{name: "清空策略指针恢复安全默认", useNil: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.useNil {
				SetOutboundTLSPolicy(nil)
			} else {
				if _, err := policy.SetInsecureHosts(tc.hosts); err != nil {
					t.Fatal(err)
				}
				SetOutboundTLSPolicy(&policy)
			}
			addr, serverDone := startRecordedWSTLSHandshake(t, gapTLSConfig(t))
			request := websocketRequest(t, addr)
			request.Header.Set("Authorization", "Bearer secret")
			request.Header.Set("Cookie", "session=secret")
			processor := New(newMockConnection(newMockConn(""), newMockServer()), request, true)
			conn, _, _, _, err := processor.dialUpstreamFaithful()
			if conn != nil {
				defer conn.Close()
			}
			if tc.allowed {
				if err != nil {
					t.Fatalf("显式例外应能建立 wss 连接: %v", err)
				}
			} else {
				var unknownAuthority x509.UnknownAuthorityError
				if !errors.As(err, &unknownAuthority) {
					t.Fatalf("未受信任证书应被拒绝,实际错误: %v", err)
				}
			}
			result := waitRecordedWSTLSHandshake(t, serverDone)
			if tc.allowed {
				if result.err != nil || !strings.Contains(result.head, "Authorization: Bearer secret\r\n") || !strings.Contains(result.head, "Cookie: session=secret\r\n") {
					t.Fatalf("显式例外握手结果: %+v", result)
				}
			} else if result.head != "" || result.err == nil {
				t.Fatalf("证书拒绝时不得发送敏感握手头: %+v", result)
			}
		})
	}
}

func TestDialUpstreamFaithfulRejectsInvalidTrustedTLS(t *testing.T) {
	previousPolicy := outboundTLSPolicy.Load()
	t.Cleanup(func() { SetOutboundTLSPolicy(previousPolicy) })
	for _, tc := range []struct {
		name      string
		wrongHost bool
		expired   bool
	}{
		{name: "主机名不匹配", wrongHost: true},
		{name: "证书已过期", expired: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := gapTLSConfig(t)
			cert, err := x509.ParseCertificate(config.Certificates[0].Certificate[0])
			if err != nil {
				t.Fatal(err)
			}
			if tc.expired {
				cert.NotBefore = time.Now().Add(-2 * time.Hour)
				cert.NotAfter = time.Now().Add(-time.Hour)
				key := config.Certificates[0].PrivateKey.(crypto.Signer)
				der, err := x509.CreateCertificate(rand.Reader, cert, cert, key.Public(), key)
				if err != nil {
					t.Fatal(err)
				}
				config.Certificates[0].Certificate = [][]byte{der}
				cert, err = x509.ParseCertificate(der)
				if err != nil {
					t.Fatal(err)
				}
			}
			roots := x509.NewCertPool()
			roots.AddCert(cert)
			SetOutboundTLSPolicy(outboundtls.NewWithRootCAs(roots))
			addr, serverDone := startRecordedWSTLSHandshake(t, config)
			if tc.wrongHost {
				_, port, err := net.SplitHostPort(addr)
				if err != nil {
					t.Fatal(err)
				}
				addr = net.JoinHostPort("localhost", port)
			}
			request := websocketRequest(t, addr)
			request.Header.Set("Authorization", "Bearer secret")
			processor := New(newMockConnection(newMockConn(""), newMockServer()), request, true)
			conn, _, _, _, err := processor.dialUpstreamFaithful()
			if conn != nil {
				_ = conn.Close()
			}
			if tc.wrongHost {
				var mismatch x509.HostnameError
				if !errors.As(err, &mismatch) {
					t.Fatalf("主机名不匹配应拒绝连接: %v", err)
				}
			} else {
				var invalid x509.CertificateInvalidError
				if !errors.As(err, &invalid) || invalid.Reason != x509.Expired {
					t.Fatalf("已过期的受信任证书应拒绝连接: %v", err)
				}
			}
			result := waitRecordedWSTLSHandshake(t, serverDone)
			if result.head != "" || result.err == nil {
				t.Fatalf("证书拒绝时不得发送敏感握手头: %+v", result)
			}
		})
	}
}
