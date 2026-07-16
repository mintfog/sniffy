// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package http

import (
	"bufio"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mintfog/sniffy/capture/types"
)

const originCN = "sniffy-e2e-origin"

// selfSignedCert 生成一张自签名服务端证书,其 Issuer==Subject==cn,供 e2e 分辨证书来源。
func selfSignedCert(t *testing.T, cn string) tls.Certificate {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// startOrigin 启动一个自签名 TLS 源站,对每条连接读完请求头后回固定响应体 "TUNNELED"。
func startOrigin(t *testing.T) net.Listener {
	t.Helper()
	cert := selfSignedCert(t, originCN)
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				br := bufio.NewReader(c)
				for {
					line, err := br.ReadString('\n')
					if err != nil {
						return
					}
					if line == "\r\n" {
						break
					}
				}
				_, _ = io.WriteString(c, "HTTP/1.1 200 OK\r\nContent-Length: 8\r\nConnection: close\r\n\r\nTUNNELED")
			}(c)
		}
	}()
	return ln
}

// startProxy 启动一个仅运行 HTTP Processor 的代理监听器(等价于引擎的每连接处理)。
func startProxy(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				srv := newMockServer()
				conn := types.NewConnection(c, srv)
				p := New(conn).(*Processor)
				_ = p.handleHttpProtocol(srv, conn.GetReader(), conn.GetWriter())
			}(c)
		}
	}()
	return ln
}

// connectAndHandshake 经代理 CONNECT 到 target 并在隧道上完成 TLS 握手,
// 返回客户端所见证书的 Issuer.CommonName;readBody 时再发一个 HTTP 请求并返回响应体。
func connectAndHandshake(t *testing.T, proxyAddr, target string, readBody bool) (issuer, body string) {
	t.Helper()
	raw, err := net.Dial("tcp", proxyAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()

	fmt.Fprintf(raw, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)
	br := bufio.NewReader(raw)
	status, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("读 CONNECT 响应行: %v", err)
	}
	if !strings.Contains(status, "200") {
		t.Fatalf("CONNECT 未返回 200: %q", status)
	}
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("读 CONNECT 响应头: %v", err)
		}
		if line == "\r\n" {
			break
		}
	}

	// readerConn 让 TLS 从 br 读(含任何已缓冲字节),写与 deadline 委托到裸连接。
	tconn := tls.Client(&readerConn{Conn: raw, reader: br}, &tls.Config{InsecureSkipVerify: true})
	_ = tconn.SetDeadline(time.Now().Add(5 * time.Second))
	if err := tconn.Handshake(); err != nil {
		t.Fatalf("TLS 握手: %v", err)
	}
	certs := tconn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		t.Fatal("服务端未提供证书")
	}
	issuer = certs[0].Issuer.CommonName

	if readBody {
		fmt.Fprintf(tconn, "GET / HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", target)
		resp, err := http.ReadResponse(bufio.NewReader(tconn), nil)
		if err != nil {
			t.Fatalf("读隧道响应: %v", err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		body = string(b)
	}
	return issuer, body
}

func restoreScopeGlobals(t *testing.T) {
	sc, up := decryptScopePtr.Load(), tunnelUpstream.Load()
	t.Cleanup(func() {
		decryptScopePtr.Store(sc)
		tunnelUpstream.Store(up)
	})
}

// TestDecryptScopeOutOfScopeTunnels:范围外主机应被盲转发——客户端看到的是源站自签证书
// (Issuer=originCN),而非 Sniffy 伪造证书;且隧道双向转发真实数据。
func TestDecryptScopeOutOfScopeTunnels(t *testing.T) {
	restoreScopeGlobals(t)
	origin := startOrigin(t)
	defer origin.Close()
	proxy := startProxy(t)
	defer proxy.Close()

	SetDecryptScope(true, "allow", []string{"example.com"}, nil) // origin 不在白名单
	issuer, body := connectAndHandshake(t, proxy.Addr().String(), origin.Addr().String(), true)

	if issuer != originCN {
		t.Fatalf("范围外主机应直通,客户端应看到源站证书(Issuer=%q),实际 %q", originCN, issuer)
	}
	if body != "TUNNELED" {
		t.Fatalf("隧道双向转发失败,响应体=%q", body)
	}
}

// TestDecryptScopeInScopeDecrypts:范围内主机应被 MITM——客户端看到的是 Sniffy CA 签发的证书,
// 而非源站证书。
func TestDecryptScopeInScopeDecrypts(t *testing.T) {
	restoreScopeGlobals(t)
	origin := startOrigin(t)
	defer origin.Close()
	proxy := startProxy(t)
	defer proxy.Close()

	SetDecryptScope(true, "allow", []string{"127.0.0.1"}, nil) // origin 在白名单
	caCN := currentCA().GetCA().Subject.CommonName
	issuer, _ := connectAndHandshake(t, proxy.Addr().String(), origin.Addr().String(), false)

	if issuer == originCN {
		t.Fatal("范围内主机应被 MITM 解密,客户端不应看到源站证书")
	}
	if issuer != caCN {
		t.Fatalf("MITM 证书 Issuer 应为 Sniffy CA(%q),实际 %q", caCN, issuer)
	}
}

// TestDecryptScopeMITMDisabledTunnels:总开关关闭时,即便 all 模式也应一律直通不解密。
func TestDecryptScopeMITMDisabledTunnels(t *testing.T) {
	restoreScopeGlobals(t)
	origin := startOrigin(t)
	defer origin.Close()
	proxy := startProxy(t)
	defer proxy.Close()

	SetDecryptScope(false, "all", nil, nil) // MITM 关闭
	issuer, body := connectAndHandshake(t, proxy.Addr().String(), origin.Addr().String(), true)

	if issuer != originCN {
		t.Fatalf("MITM 关闭时应直通,客户端应看到源站证书(Issuer=%q),实际 %q", originCN, issuer)
	}
	if body != "TUNNELED" {
		t.Fatalf("隧道双向转发失败,响应体=%q", body)
	}
}
