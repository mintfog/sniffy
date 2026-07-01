// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package app

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/internal/truststore"
)

// ResendFlow 以一条已捕获 flow 的请求为蓝本重新发起请求,作为一条新 flow 记录并广播。
// 重发会完整走插件/规则/断点管道。返回是否找到了原始 flow。
func (a *App) ResendFlow(id string) bool {
	orig, ok := a.Service.RawFlow(id)
	if !ok || orig.Request == nil {
		return false
	}

	nf := flow.New(orig.Protocol)
	nf.ConnID = orig.ConnID
	src := orig.Request

	hdr := make(map[string][]string, len(src.Header))
	for k, v := range src.Header {
		cp := make([]string, len(v))
		copy(cp, v)
		hdr[k] = cp
	}
	body := make([]byte, len(src.Body))
	copy(body, src.Body)

	nf.Request = &flow.Request{
		Method:   src.Method,
		URL:      src.URL,
		Host:     src.Host,
		Path:     src.Path,
		Proto:    src.Proto,
		Header:   hdr,
		Body:     body,
		ClientIP: src.ClientIP,
	}
	nf.Tags = append(nf.Tags, "resent")
	if nf.Metadata == nil {
		nf.Metadata = map[string]any{}
	}
	nf.Metadata["resentFrom"] = id

	// 存入会话存储的是快照副本:runResend 在私有的 nf 上就地改写(含规则引擎对
	// Header map 的写入),存储里始终是不可变快照,从而消除与 UI 读取(SessionDTO)的竞态。
	a.Service.ImportFlowStarted(nf.Clone())
	go a.runResend(nf)
	return true
}

// runResend 在后台执行一次重发的完整往返(请求管道 → 转发/mock/abort → 响应管道)。
func (a *App) runResend(nf *flow.Flow) {
	ctx := context.Background()

	switch d := a.Pipeline.OnRequest(ctx, nf); d.Kind {
	case flow.Abort:
		nf.State = flow.StateBlocked
		nf.Error = d.Reason
		a.finishResend(nf)
		return
	case flow.Mock:
		nf.State = flow.StateMocked
		nf.Timing.ResponseAt = time.Now()
		a.Pipeline.OnResponse(ctx, nf)
		a.finishResend(nf)
		return
	}

	req, err := http.NewRequest(nf.Request.Method, nf.Request.URL, bytes.NewReader(nf.Request.Body))
	if err != nil {
		nf.State = flow.StateErrored
		nf.Error = err.Error()
		a.finishResend(nf)
		return
	}
	req = flow.ApplyRequestToHTTP(nf, req)

	nf.State = flow.StateAwaitingResponse
	resp, err := a.Engine.UpstreamClient().Do(req)
	if err != nil {
		nf.State = flow.StateErrored
		nf.Error = err.Error()
		a.finishResend(nf)
		return
	}
	defer resp.Body.Close()

	nf.Timing.ResponseAt = time.Now()
	flow.CaptureResponseToFlow(nf, resp)
	nf.State = flow.StateCompleted
	// 响应阶段管道
	if d2 := a.Pipeline.OnResponse(ctx, nf); d2.Kind == flow.Abort {
		nf.State = flow.StateBlocked
		if d2.Reason != "" {
			nf.Error = d2.Reason
		}
	}
	a.finishResend(nf)
}

func (a *App) finishResend(nf *flow.Flow) {
	if nf.Timing.CompletedAt.IsZero() {
		nf.Timing.CompletedAt = time.Now()
	}
	nf.Timing.DurationMs = time.Since(nf.Timing.RequestAt).Milliseconds()
	a.Service.ImportFlowCompleted(nf.Clone())
}

// RegenerateCA 重新生成根 CA(覆盖磁盘),刷新 service 的证书导出,返回新证书 PEM。
func (a *App) RegenerateCA() (string, error) {
	newCA, err := a.Engine.RegenerateCA()
	if err != nil {
		return "", err
	}
	a.Service.SetCA(newCA)
	return string(a.Service.CertificatePEM()), nil
}

// InstallCAToSystem 把当前根 CA 装入本机系统信任库;授权对话框由平台实现触发。
func (a *App) InstallCAToSystem() error {
	pem := a.Service.CertificatePEM()
	if len(pem) == 0 {
		return errors.New("根证书尚未就绪")
	}
	return truststore.Install(pem)
}
