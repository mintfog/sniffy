// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package app

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/mintfog/sniffy/ca"
	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/internal/truststore"
)

// maxComposeResponseBytes 是构造器一次性往返读取上游响应体的上限。
// 响应体整体存入 Flow 与会话快照，32 MiB 适用于详情展示。
var maxComposeResponseBytes int64 = 32 << 20

// errComposeResponseTooLarge 标记响应体超过上限且读取被中止。
var errComposeResponseTooLarge = errors.New("上游响应体超过上限")

func responseTooLargeError(limit int64) error {
	return fmt.Errorf("%w %d 字节,已中止(内容不完整)", errComposeResponseTooLarge, limit)
}

// composeSizeLimitError 将传输字节或解压后字节超限转换为用户可见原因。
func composeSizeLimitError(err error) (string, bool) {
	switch {
	case errors.Is(err, errComposeResponseTooLarge):
		return err.Error(), true
	case errors.Is(err, flow.ErrBodyTooLarge):
		return fmt.Sprintf("上游响应体解压后超过上限 %d 字节,已中止(内容不完整)", maxComposeResponseBytes), true
	}
	return "", false
}

// cappedBody 为上游响应体提供字节上限，超限时返回明确错误。
type cappedBody struct {
	rc    io.ReadCloser
	limit int64
	read  int64
}

func (b *cappedBody) Read(p []byte) (int, error) {
	if b.read > b.limit {
		return 0, responseTooLargeError(b.limit)
	}
	// 额外读取一个字节以区分恰好达到上限与超过上限。
	if room := b.limit + 1 - b.read; int64(len(p)) > room {
		p = p[:room]
	}
	n, err := b.rc.Read(p)
	b.read += int64(n)
	// 在本次 Read 中完成超限判定，兼容数据与 EOF 同时返回的响应体。
	if b.read > b.limit {
		return n, responseTooLargeError(b.limit)
	}
	return n, err
}

func (b *cappedBody) Close() error { return b.rc.Close() }

// capResponseBody 就地给 resp.Body 套上字节上限(limit<=0 时不设限)。
func capResponseBody(resp *http.Response, limit int64) {
	if resp.Body == nil || limit <= 0 {
		return
	}
	resp.Body = &cappedBody{rc: resp.Body, limit: limit}
}

type invalidCAImportError struct {
	err error
}

func (e *invalidCAImportError) Error() string      { return e.err.Error() }
func (e *invalidCAImportError) Unwrap() error      { return e.err }
func (e *invalidCAImportError) InvalidInput() bool { return true }

func invalidCAImport(err error) error {
	return &invalidCAImportError{err: err}
}

// SendRequest 按 spec 发起请求，记录并广播新 flow，返回 flow ID。
func (a *App) SendRequest(spec flow.RequestSpec) (string, error) {
	method := strings.ToUpper(strings.TrimSpace(spec.Method))
	if method == "" {
		method = http.MethodGet
	}

	raw := strings.TrimSpace(spec.URL)
	if raw == "" {
		return "", errors.New("请求 URL 为空")
	}
	// 裸 host/path 使用 https 作为默认协议。
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("URL 无法解析: %w", err)
	}
	if u.Host == "" {
		return "", fmt.Errorf("URL 缺少主机名: %s", spec.URL)
	}
	proto := flow.ProtoHTTPS
	switch u.Scheme {
	case "https":
	case "http":
		proto = flow.ProtoHTTP
	case "ws", "wss":
		return "", errors.New("WebSocket 请用 OpenWebSocket 建立连接")
	default:
		return "", fmt.Errorf("不支持的协议: %s", u.Scheme)
	}

	// 先解析头部字节旁路，再校验最终写线的头部字节。
	headers, err := a.restoreComposedHeaders(spec)
	if err != nil {
		return "", err
	}

	// 构造器头部按原始序列写线，入口校验确保头名和值符合报文格式。
	if err := flow.ValidateHeaderPairs(headers); err != nil {
		return "", err
	}
	if len(spec.Body) > flow.MaxComposeBodyBytes {
		return "", fmt.Errorf("请求体 %d 字节超过上限 %d", len(spec.Body), flow.MaxComposeBodyBytes)
	}

	host, header, rawHeaders := splitComposedHeaders(headers, u.Host)
	nf := flow.New(proto)
	nf.Request = &flow.Request{
		Method:     method,
		URL:        u.String(),
		Host:       host,
		Path:       u.Path,
		Proto:      "HTTP/1.1",
		Header:     header,
		Body:       []byte(spec.Body),
		RawHeaders: rawHeaders,
	}
	nf.Tags = append(nf.Tags, "composed")
	if spec.FromID != "" {
		nf.Tags = append(nf.Tags, "resent")
		nf.Metadata["resentFrom"] = spec.FromID
	}

	switch spec.Kind {
	case flow.SpecKindSSE:
		nf.Tags = append(nf.Tags, "sse")
		nf.Metadata["stream"] = flow.StreamSSE
		// 在启动 SSE goroutine 前创建上下文并登记连接名额。
		ctx, cancel := context.WithCancel(context.Background())
		if err := a.outStreams.add(nf.ID, cancel); err != nil {
			cancel()
			return "", err
		}
		a.Service.ImportFlowStarted(nf.Clone())
		go a.runComposeSSE(ctx, cancel, nf, spec.ViaPipeline)
	case flow.SpecKindGraphQL:
		// GraphQL 字段由前端合成为 JSON body，沿用 HTTP 请求路径。
		nf.Tags = append(nf.Tags, "graphql")
		a.Service.ImportFlowStarted(nf.Clone())
		go a.runResend(nf, spec.ViaPipeline)
	default:
		// 空 Kind 与 SpecKindHTTP 等价，按一次性 HTTP 往返处理。
		a.Service.ImportFlowStarted(nf.Clone())
		go a.runResend(nf, spec.ViaPipeline)
	}
	return nf.ID, nil
}

// restoreComposedHeaders 解析构造器请求将要写线的头部字节。
// 带 HeadersB64 时按逐项字节旁路解析；其余请求使用蓝本头部还原可恢复的原始字节。
// 缺少蓝本或 FromID 时直接采用请求中的头部列表。
func (a *App) restoreComposedHeaders(spec flow.RequestSpec) ([][2]string, error) {
	if len(spec.HeadersB64) > 0 {
		return flow.ResolveEditedHeaders(spec.Headers, spec.HeadersB64, nil)
	}
	if spec.FromID == "" || len(spec.Headers) == 0 {
		return spec.Headers, nil
	}
	basis, ok := a.Service.ComposeHeaderBasis(spec.FromID)
	if !ok {
		return spec.Headers, nil
	}
	return flow.RestoreHeaderPairBytes(spec.Headers, basis), nil
}

// splitComposedHeaders 将有序头拆为出站 Host、规范化 map 与写线原始序列。
// Host 通过 req.Host 发送；缺少时按 URL 主机补到原始序列首位。
func splitComposedHeaders(pairs [][2]string, urlHost string) (string, map[string][]string, [][2]string) {
	host := urlHost
	header := make(map[string][]string, len(pairs))
	rawHeaders := make([][2]string, 0, len(pairs)+1)
	hasHost := false

	for _, kv := range pairs {
		name := strings.TrimSpace(kv[0])
		if name == "" {
			continue
		}
		rawHeaders = append(rawHeaders, [2]string{name, kv[1]})
		if textproto.CanonicalMIMEHeaderKey(name) == "Host" {
			hasHost = true
			if v := strings.TrimSpace(kv[1]); v != "" {
				host = v
			}
			continue
		}
		ck := textproto.CanonicalMIMEHeaderKey(name)
		header[ck] = append(header[ck], kv[1])
	}

	// 空头列表交给 net/http 生成标准请求头。
	if len(rawHeaders) == 0 {
		return host, header, nil
	}
	if !hasHost {
		rawHeaders = append([][2]string{{"Host", host}}, rawHeaders...)
	}
	return host, header, rawHeaders
}

// ResendFlow 以已捕获 flow 的请求为蓝本重新发起请求并广播新 flow。
// 重发经过插件、规则和断点管道；返回原始 flow 是否存在。
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

	// 会话存储使用快照副本，runResend 在私有 nf 上处理请求。
	a.Service.ImportFlowStarted(nf.Clone())
	go a.runResend(nf, true)
	return true
}

// runResend 在后台执行一次重发往返，按 viaPipeline 选择是否经过请求与响应管道。
func (a *App) runResend(nf *flow.Flow, viaPipeline bool) {
	ctx := context.Background()

	if viaPipeline {
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
	// 读取中断或响应体超限均记录为 errored，Flow.Body 表示不完整内容。
	capResponseBody(resp, maxComposeResponseBytes)
	readErr := flow.CaptureResponseToFlowLimit(nf, resp, maxComposeResponseBytes)
	switch msg, limited := composeSizeLimitError(readErr); {
	case readErr == nil:
		nf.State = flow.StateCompleted
	case limited:
		nf.State = flow.StateErrored
		nf.Error = msg
	default:
		nf.State = flow.StateErrored
		nf.Error = fmt.Sprintf("响应体读取失败(内容不完整): %v", readErr)
	}
	// 响应阶段管道。
	if viaPipeline {
		if d2 := a.Pipeline.OnResponse(ctx, nf); d2.Kind == flow.Abort {
			nf.State = flow.StateBlocked
			if d2.Reason != "" {
				nf.Error = d2.Reason
			}
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
	a.caMu.Lock()
	defer a.caMu.Unlock()

	newCA, err := ca.RegenerateCA(a.CertDir)
	if err != nil {
		return "", err
	}
	if err := a.Engine.SetCA(newCA); err != nil {
		return "", err
	}
	a.Service.SetCA(newCA)
	return string(a.Service.CertificatePEM()), nil
}

// InstallCAToSystem 把当前根 CA 装入本机信任库,授权对话框由平台实现触发。
// macOS 走用户级(登录钥匙串 + user 域,支持 Touch ID);Windows/Linux 见对应实现。
func (a *App) InstallCAToSystem() error {
	pem := a.Service.CertificatePEM()
	if len(pem) == 0 {
		return errors.New("根证书尚未就绪")
	}
	return truststore.Install(pem)
}

// ExportCAAs 按格式导出根证书内容(不落盘,交由 Bridge 处理文件对话框)。
func (a *App) ExportCAAs(format, password string) ([]byte, string, error) {
	return a.Service.CertificateExportAs(format, password)
}

// ImportCAFromFile 读取给定路径的证书文件，按 PKCS12 或 PEM Bundle 解析并热切换根 CA。
// password 只用于 PKCS12(PEM 分支忽略);解析成功后同步刷新 service 的证书导出。
// 返回新根的 PEM,便于前端直接更新展示。
func (a *App) ImportCAFromFile(path, password string) (string, error) {
	if path == "" {
		return "", errors.New("未选择文件")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return a.ImportCA(data, password)
}

// ImportCA 从客户端提供的字节导入 PKCS12 或 PEM Bundle,持久化后热切换根 CA。
// 它是桌面文件导入与 headless HTTP 上传共用的无运输层入口。
func (a *App) ImportCA(data []byte, password string) (string, error) {
	if len(data) == 0 {
		return "", invalidCAImport(errors.New("导入数据为空"))
	}
	a.caMu.Lock()
	defer a.caMu.Unlock()

	var (
		cert *x509.Certificate
		key  any
		err  error
	)
	// 先解析 PEM Bundle，并移除 UTF-8 BOM 与前导空白；未识别 PEM 块时再解析 PKCS12。
	probe := bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	probe = bytes.TrimLeft(probe, " \r\n\t")
	cert, key, err = ca.ImportFromPEMBundle(probe)
	if err != nil {
		block, _ := pem.Decode(probe)
		if block == nil {
			cert, key, err = ca.ImportFromPKCS12(data, password)
		}
	}
	if err != nil {
		return "", invalidCAImport(err)
	}
	newCA, err := ca.ImportCA(cert, key, a.CertDir)
	if err != nil {
		return "", err
	}
	if err := a.Engine.SetCA(newCA); err != nil {
		return "", err
	}
	a.Service.SetCA(newCA)
	return string(a.Service.CertificatePEM()), nil
}
