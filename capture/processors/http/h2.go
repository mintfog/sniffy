// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package http

import (
	"io"
	"net"
	"net/http"

	"golang.org/x/net/http2"

	"github.com/mintfog/sniffy/ca"
	"github.com/mintfog/sniffy/capture/types"
	"github.com/mintfog/sniffy/internal/flow"
)

// serveHTTP2 在一条已经过 ALPN 协商为 "h2" 的 TLS 连接上驱动 HTTP/2 服务端。
//
// http2.Server.ServeConn 内部完成连接前导(preface)、帧解析、HPACK、流多路复用与
// 流控,并把每个 stream 以普通的 *http.Request / http.ResponseWriter 呈现给 handler,
// 从而复用与 HTTP/1.x 完全相同的 flow 管道(见 flow_pipeline.go)。
//
// 每个 h2 stream 即一条独立 Flow,多个 stream 共享同一连接、由 ServeConn 并发驱动;
// 管道以 RWMutex 快照实现且 Flow 互不共享,故并发安全。ServeConn 阻塞到连接结束才返回。
func serveHTTP2(server types.Server, conn net.Conn) error {
	srv := &http2.Server{
		// h2 是长连接:整连接空闲到点回收以防 goroutine / 连接泄漏(活跃 stream 会刷新该计时)。
		IdleTimeout: TLSConnectionTimeout,
		// 慢速 / 失联的读端会让响应写阻塞在流控窗口上;无法写出时回收连接。
		WriteByteTimeout: TLSConnectionTimeout,
	}
	srv.ServeConn(conn, &http2.ServeConnOpts{
		Handler: &h2Handler{server: server, conn: conn},
		// 每条 stream 的请求读取上限。h2 分流时清掉了连接级绝对超时(tls.go),这里用
		// ReadTimeout 给每条流的请求读取(含 BuildRequestFlow 里的 io.ReadAll(req.Body))设界,
		// 防止停滞的流(slowloris / 永不半关的客户端流式请求)无限占用 goroutine 与连接。
		BaseConfig: &http.Server{ReadTimeout: TLSConnectionTimeout},
	})
	return nil
}

// h2Handler 把单个 h2 stream 适配进共享的 flow 管道。
type h2Handler struct {
	server types.Server
	conn   net.Conn
}

// ServeHTTP 处理一个 h2 stream:补全 URL 后交给 runFlowPipeline,响应经 h2Responder 写回。
func (h *h2Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// h2 的伪头 :authority/:path 已由 http2 映射到 r.Host / r.URL.Path;
	// 补全 scheme/host 供转发与 UI 展示,并清空 RequestURI(出站请求要求)。
	if r.URL.Scheme == "" {
		r.URL.Scheme = "https"
	}
	if r.URL.Host == "" {
		r.URL.Host = r.Host
	}
	r.RequestURI = ""

	// iOS 证书魔法域名:与 HTTP/1.x 路径一致地直接返回 .mobileconfig。
	if isCertDomain(r.Host) {
		serveIOSProfileH2(h.server, w)
		return
	}

	resp := &h2Responder{w: w}
	_ = runFlowPipeline(h.server, r, flow.ProtoHTTPS, h.conn.RemoteAddr(), h.conn.LocalAddr(), resp)
}

// h2Responder 是 HTTP/2 的 responder:经 stream 的 http.ResponseWriter 写回。
type h2Responder struct {
	w http.ResponseWriter
}

// writeFlowResponse 复用 BuildHTTPResponse 的头部规整(剔除逐跳头 / Content-Encoding、
// 按 identity 重算 Content-Length),但走 ResponseWriter 而非 resp.Write —— 实际线缆
// 协议由 h2 框架决定。响应尾部(gRPC 的 grpc-status 等)在 body 之后以 TrailerPrefix 写出。
func (h *h2Responder) writeFlowResponse(f *flow.Flow, req *http.Request) error {
	resp := flow.BuildHTTPResponse(f, req)

	dst := h.w.Header()
	for k, vs := range resp.Header {
		dst[k] = append([]string(nil), vs...)
	}
	h.w.WriteHeader(resp.StatusCode)

	var err error
	if resp.Body != nil {
		_, err = io.Copy(h.w, resp.Body)
		_ = resp.Body.Close()
	}

	// 写回响应尾部。必须在 body 之后,用 http.TrailerPrefix 让 h2 框架把它们作为
	// 尾部 HEADERS 帧发出,无需在响应头里预先声明 Trailer。
	if f.Response != nil {
		for k, vs := range f.Response.Trailer {
			for _, v := range vs {
				dst.Add(http.TrailerPrefix+k, v)
			}
		}
	}
	return err
}

// writeAbort 写回阻断响应。StatusOnAbort==0 表示「直接中断」:h2 无逐流关连接语义,
// 以 panic(http.ErrAbortHandler) 让 http2 框架对本 stream 发 RST_STREAM。
func (h *h2Responder) writeAbort(d flow.Decision) {
	if d.StatusOnAbort == 0 {
		panic(http.ErrAbortHandler)
	}
	http.Error(h.w, d.Reason, d.StatusOnAbort)
}

func (h *h2Responder) writeBadGateway() error {
	http.Error(h.w, "502 Bad Gateway", http.StatusBadGateway)
	return nil
}

func (h *h2Responder) streamWriter() (streamWriter, bool) {
	return newH2StreamWriter(h.w), true
}

// serveIOSProfileH2 经 ResponseWriter 返回 iOS 证书描述文件(.mobileconfig)。
func serveIOSProfileH2(server types.Server, w http.ResponseWriter) {
	server.LogDebug("拦截 %s(h2),返回 iOS 证书描述文件", certMagicDomain)
	c := currentCA()
	if c == nil || c.GetCA() == nil {
		http.Error(w, "", http.StatusServiceUnavailable)
		return
	}
	profile := ca.Mobileconfig(c.GetCA())
	w.Header().Set("Content-Type", "application/x-apple-aspen-config")
	w.Header().Set("Content-Disposition", "attachment; filename=sniffy.mobileconfig")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(profile)
}
