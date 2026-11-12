// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package service

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mintfog/sniffy/internal/bodycache"
	"github.com/mintfog/sniffy/internal/flow"
)

const (
	// maxSessionImportValueBytes 限制档案中单个 JSON 值的字节数：解析一条会话的内存开销与其体积成正比。
	maxSessionImportValueBytes = 512 << 20
	// maxSessionImportMemoryBytes 限制一次导入留在内存里的正文总量：请求体、未落盘的响应体与 WebSocket 消息。
	maxSessionImportMemoryBytes = 512 << 20
)

var (
	// ErrSessionImportTooLarge 是各项导入体积限制的共同哨兵，具体限制由包装它的错误说明。
	ErrSessionImportTooLarge = errors.New("导入档案超出限制")
	errImportValueTooLarge   = fmt.Errorf("%w: 单条会话超过 512 MiB", ErrSessionImportTooLarge)
	errImportMemoryTooLarge  = fmt.Errorf("%w: 留在内存的正文合计超过 512 MiB", ErrSessionImportTooLarge)
	errImportCacheTooLarge   = fmt.Errorf("%w: 大体积响应体合计超过响应体缓存容量", ErrSessionImportTooLarge)
	errHARShape              = errors.New("需要 HAR 1.2 格式的 log.entries 数组")
)

type SessionImportResult struct {
	Imported  int `json:"imported"`
	HTTP      int `json:"http"`
	WebSocket int `json:"webSocket"`
	// Dropped 是档案中超出会话容量、未入库的最早记录数。
	Dropped int `json:"dropped"`
	// 导入产生的事件可能超出事件队列被丢弃，前端据此补齐列表。
	IDs []string `json:"ids"`
}

// ImportSessionArchive 逐条解析档案，全部校验成功后追加会话；文件中的 ID 不参与入库。
// 导入只还原离线记录，不发起请求，也不改变实时流量统计。
func (s *Service) ImportSessionArchive(reader io.Reader) (SessionImportResult, error) {
	stage := &importStage{
		valueLimit:     maxSessionImportValueBytes,
		memoryLimit:    maxSessionImportMemoryBytes,
		cache:          s.bodyCache,
		spillThreshold: s.cfg.get().LargeBodyKiB * 1024,
		flowCap:        s.sessions.capacity(),
		wsCap:          s.ws.cap,
		biz:            s.biz.Load(),
	}
	if err := stage.read(reader); err != nil {
		stage.discard()
		return SessionImportResult{}, err
	}

	result := SessionImportResult{
		HTTP:      len(stage.flows),
		WebSocket: len(stage.sockets),
		Imported:  len(stage.flows) + len(stage.sockets),
		Dropped:   stage.dropped,
		IDs:       make([]string, 0, len(stage.flows)+len(stage.sockets)),
	}
	for _, f := range stage.flows {
		s.ImportFlowUpdated(f)
		result.IDs = append(result.IDs, f.ID)
	}
	for _, ws := range stage.sockets {
		s.ImportWSSession(ws, nil)
		result.IDs = append(result.IDs, ws.ID)
	}
	return result, nil
}

// importStage 暂存已校验的会话，档案整体通过后才入库。
// 只保留存储容量以内的最新记录：更早的记录入库后也会被立即淘汰。
type importStage struct {
	valueLimit  int64
	memoryLimit int64
	// cache 为 nil 时响应体全部留在内存。
	cache          *bodycache.Cache
	spillThreshold int64
	flowCap        int
	wsCap          int
	biz            *bizMatcher

	flows   []*flow.Flow
	sockets []*flow.WSSession
	dropped int
	// memory 与 spilled 是暂存中记录的内存正文与落盘正文字节数。
	memory  int64
	spilled int64
}

func (st *importStage) read(reader io.Reader) error {
	d := newHARDecoder(reader, st.valueLimit)
	var version string
	hasEntries := false
	readLogField := func(key string) error {
		switch key {
		case "version":
			return d.decode(&version)
		case "entries":
			if hasEntries {
				return errHARShape
			}
			hasEntries = true
			return st.readEntries(d)
		}
		return d.skip()
	}
	err := d.object(func(key string) error {
		if key != "log" {
			return d.skip()
		}
		return d.object(readLogField)
	})
	if err != nil {
		return err
	}
	if _, err := d.token(); !errors.Is(err, io.EOF) {
		return errors.New("HAR 格式错误: 档案末尾存在多余内容")
	}
	if !hasEntries || (version != "" && version != "1.2") {
		return errHARShape
	}
	return nil
}

func (st *importStage) readEntries(d *harDecoder) error {
	if err := d.expect('['); err != nil {
		return err
	}
	for n := 1; d.more(); n++ {
		var entry HAREntry
		if err := d.decode(&entry); err != nil {
			return fmt.Errorf("第 %d 条会话: %w", n, err)
		}
		f, ws, err := importArchiveEntry(entry)
		if err != nil {
			return fmt.Errorf("第 %d 条会话: %w", n, err)
		}
		if ws != nil {
			st.addSocket(ws)
		} else if err := st.addFlow(f); err != nil {
			return err
		}
		if st.memory > st.memoryLimit {
			return errImportMemoryTooLarge
		}
	}
	_, err := d.token()
	return err
}

func (st *importStage) addSocket(ws *flow.WSSession) {
	if len(st.sockets) == st.wsCap {
		st.memory -= wsMemory(st.sockets[0])
		st.sockets[0] = nil
		st.sockets = st.sockets[1:]
		st.dropped++
	}
	st.sockets = append(st.sockets, ws)
	st.memory += wsMemory(ws)
}

func (st *importStage) addFlow(f *flow.Flow) error {
	// 先腾出最早的记录再落盘：容量外的记录不占缓存容量。
	if len(st.flows) == st.flowCap {
		st.release(st.flows[0])
		st.flows[0] = nil
		st.flows = st.flows[1:]
		st.dropped++
	}
	// 落盘之后 Body 即清空,须在此之前提取。
	f.Biz = st.biz.extract(f)
	if err := st.spill(f); err != nil {
		return err
	}
	st.flows = append(st.flows, f)
	st.memory += flowMemory(f)
	return nil
}

func flowMemory(f *flow.Flow) int64 {
	n := int64(len(f.Request.Body))
	if f.Response != nil {
		n += int64(len(f.Response.Body))
	}
	return n
}

func wsMemory(ws *flow.WSSession) int64 {
	var n int64
	for i := range ws.Messages {
		n += int64(len(ws.Messages[i].Data))
	}
	return n
}

// spill 把达到旁路阈值的响应体移到落盘缓存；缓存不可用或写入失败时正文留在内存。
func (st *importStage) spill(f *flow.Flow) error {
	res := f.Response
	if st.cache == nil || res == nil {
		return nil
	}
	size := int64(len(res.Body))
	if size == 0 || size < st.spillThreshold {
		return nil
	}
	// importArchiveEntry 把档案声明的更大尺寸记在 BodyLen 里；落盘会用实际字节数覆盖它，这类正文留在内存。
	if res.BodyLen() > size {
		return nil
	}
	// 写入前拒绝：落盘总量超过缓存容量时，缓存会淘汰最早的副本，其中包括其他会话的正文。
	if st.spilled+size > st.cache.Budget() {
		return errImportCacheTooLarge
	}
	entry := st.cache.Create(f.ID)
	_, _ = entry.Write(res.Body)
	if path, written := entry.Commit(); path != "" {
		res.Body = nil
		res.SetPassthroughBody(path, written)
		st.spilled += written
	}
	return nil
}

// release 把离开暂存的 HTTP 会话从用量中扣除，并回收它的落盘副本。
func (st *importStage) release(f *flow.Flow) {
	st.memory -= flowMemory(f)
	if f.Response == nil {
		return
	}
	if path, size := f.Response.BodyFile(); path != "" {
		st.cache.Remove(path)
		st.spilled -= size
	}
}

// discard 回收失败导入已落盘的副本。
func (st *importStage) discard() {
	for _, f := range st.flows {
		st.release(f)
	}
}

// harDecoder 按 token 遍历档案骨架，只把单个值整体解码进内存。
type harDecoder struct {
	dec        *json.Decoder
	source     *valueLimitReader
	valueLimit int64
}

func newHARDecoder(reader io.Reader, valueLimit int64) *harDecoder {
	source := &valueLimitReader{r: reader}
	return &harDecoder{dec: json.NewDecoder(source), source: source, valueLimit: valueLimit}
}

// resetQuota 在每个值之前重置额度；解码器已预读的字节属于这个值，从额度中扣除。
func (d *harDecoder) resetQuota() {
	buffered := int64(d.dec.Buffered().(*bytes.Reader).Len())
	d.source.remaining = d.valueLimit - buffered
}

func (d *harDecoder) token() (json.Token, error) {
	d.resetQuota()
	tok, err := d.dec.Token()
	return tok, harSyntaxError(err)
}

func (d *harDecoder) more() bool {
	d.resetQuota()
	return d.dec.More()
}

func (d *harDecoder) decode(v any) error {
	d.resetQuota()
	return harSyntaxError(d.dec.Decode(v))
}

func (d *harDecoder) skip() error {
	var discard json.RawMessage
	return d.decode(&discard)
}

// expect 消费一个 token，它必须是给定的定界符。
func (d *harDecoder) expect(delim json.Delim) error {
	tok, err := d.token()
	if err != nil {
		return err
	}
	if tok != delim {
		return errHARShape
	}
	return nil
}

// object 对每个键调用 visit；visit 必须恰好消费该键的值。
func (d *harDecoder) object(visit func(key string) error) error {
	if err := d.expect('{'); err != nil {
		return err
	}
	for d.more() {
		key, err := d.token()
		if err != nil {
			return err
		}
		name, ok := key.(string)
		if !ok {
			return errHARShape
		}
		if err := visit(name); err != nil {
			return err
		}
	}
	_, err := d.token()
	return err
}

func harSyntaxError(err error) error {
	if err == nil || errors.Is(err, ErrSessionImportTooLarge) {
		return err
	}
	return fmt.Errorf("HAR 格式错误: %w", err)
}

// valueLimitReader 限制解码单个值期间从来源读取的字节数；额度由 harDecoder.resetQuota 设定。
type valueLimitReader struct {
	r         io.Reader
	remaining int64
}

func (l *valueLimitReader) Read(p []byte) (int, error) {
	if l.remaining <= 0 {
		return 0, errImportValueTooLarge
	}
	if int64(len(p)) > l.remaining {
		p = p[:l.remaining]
	}
	n, err := l.r.Read(p)
	l.remaining -= int64(n)
	return n, err
}

func importArchiveEntry(entry HAREntry) (*flow.Flow, *flow.WSSession, error) {
	u, err := url.Parse(entry.Request.URL)
	validURL := err == nil && u.Host != "" &&
		(u.Scheme == "http" || u.Scheme == "https" || u.Scheme == "ws" || u.Scheme == "wss")
	if !validURL {
		return nil, nil, errors.New("请求 URL 必须包含 HTTP(S) 或 WS(S) 协议和主机")
	}
	if entry.Request.Method == "" || strings.ContainsAny(entry.Request.Method, " \t\r\n") {
		return nil, nil, errors.New("请求方法无效")
	}
	if entry.Time < 0 || entry.Time > 1e12 || math.IsNaN(entry.Time) || math.IsInf(entry.Time, 0) {
		return nil, nil, errors.New("耗时无效")
	}
	if entry.Response.Status < 0 || entry.Response.Status > 999 {
		return nil, nil, errors.New("响应状态码无效")
	}
	if entry.Request.BodySize < -1 {
		return nil, nil, errors.New("请求正文大小无效")
	}
	if entry.StartedDateTime.IsZero() {
		return nil, nil, errors.New("缺少请求开始时间")
	}
	reqHeaders, reqRaw, err := importArchiveHeaders(entry.Request.Headers)
	if err != nil {
		return nil, nil, err
	}
	resHeaders, resRaw, err := importArchiveHeaders(entry.Response.Headers)
	if err != nil {
		return nil, nil, err
	}
	var body []byte
	if post := entry.Request.PostData; post != nil {
		if body, err = importArchivePostData(post); err != nil {
			return nil, nil, err
		}
	}
	responseBody, err := importArchiveText(entry.Response.Content.Text, entry.Response.Content.Encoding)
	if err != nil {
		return nil, nil, err
	}
	if u.Scheme == "ws" || u.Scheme == "wss" || entry.WebSocketMessages != nil {
		ws, err := importArchiveWebSocket(entry, flow.NewID())
		return nil, ws, err
	}

	if reqHeaders.Get("Content-Type") == "" && entry.Request.PostData != nil {
		reqHeaders.Set("Content-Type", entry.Request.PostData.MimeType)
	}
	if resHeaders.Get("Content-Type") == "" && entry.Response.Content.MimeType != "" {
		resHeaders.Set("Content-Type", entry.Response.Content.MimeType)
	}
	if resHeaders.Get("Location") == "" && entry.Response.RedirectURL != "" {
		resHeaders.Set("Location", entry.Response.RedirectURL)
	}
	proto := entry.Request.HTTPVersion
	if proto == "" {
		proto = "HTTP/1.1"
	}
	host := reqHeaders.Get("Host")
	if host == "" {
		host = u.Host
	}
	f := flow.New(u.Scheme)
	f.Request = &flow.Request{
		Method: entry.Request.Method,
		URL:    entry.Request.URL,
		Host:   host,
		// 与实时抓包一致只取路径、不含查询串：规则的 url_path 条件比对的就是它。
		Path:       u.Path,
		Proto:      proto,
		Header:     reqHeaders,
		RawHeaders: reqRaw,
		Body:       body,
		ClientIP:   entry.ClientIP,
	}
	if p := entry.Process; p != nil && p.Name != "" {
		f.SetProcess(&flow.ProcessInfo{PID: p.PID, Name: p.Name, Path: p.Path})
	}
	// HAR 的 bodySize 是线缆字节数，正文缺失或压缩时不能从 identity 正文推算。
	if entry.Request.BodySize != 0 {
		f.Metadata[harRequestBodySizeKey] = entry.Request.BodySize
	}
	f.State, f.Error = flow.StateCompleted, entry.Error
	if entry.Response.Status == 0 || entry.Error != "" {
		f.State = flow.StateErrored
	}
	ttfb := min(entry.Time, max(0, entry.Timings.Send)+max(0, entry.Timings.Wait))
	f.Timing = flow.Timing{
		RequestAt:   entry.StartedDateTime,
		ResponseAt:  entry.StartedDateTime.Add(time.Duration(ttfb * float64(time.Millisecond))),
		CompletedAt: entry.StartedDateTime.Add(time.Duration(entry.Time * float64(time.Millisecond))),
		TTFBMs:      int64(ttfb),
		DurationMs:  int64(entry.Time),
	}
	if entry.Response.Status > 0 {
		f.Response = &flow.Response{
			Status:     entry.Response.Status,
			StatusText: entry.Response.StatusText,
			Proto:      entry.Response.HTTPVersion,
			Header:     resHeaders,
			RawHeaders: resRaw,
			Body:       responseBody,
		}
		if entry.Response.Content.Size > int64(len(responseBody)) {
			// HAR 可只含正文元信息；保留已知大小，空路径表示没有正文副本。
			f.Response.SetPassthroughBody("", entry.Response.Content.Size)
		}
	}
	return f, nil, nil
}

func importArchivePostData(post *HARPostData) ([]byte, error) {
	if post.Text != nil || len(post.Params) == 0 {
		return importArchiveText(post.Text, post.Encoding)
	}
	mediaType, _, err := mime.ParseMediaType(post.MimeType)
	if err != nil || mediaType != "application/x-www-form-urlencoded" {
		return nil, fmt.Errorf("无法从 %q 的表单参数还原原始正文", post.MimeType)
	}
	var body strings.Builder
	for i, param := range post.Params {
		if param.FileName != "" || param.ContentType != "" {
			return nil, errors.New("表单参数包含文件信息，来源 HAR 未保留原始正文")
		}
		if i > 0 {
			body.WriteByte('&')
		}
		body.WriteString(url.QueryEscape(param.Name))
		body.WriteByte('=')
		body.WriteString(url.QueryEscape(param.Value))
	}
	return []byte(body.String()), nil
}

func importArchiveHeaders(pairs []HARPair) (http.Header, [][2]string, error) {
	h := make(http.Header)
	raw := make([][2]string, 0, len(pairs))
	for _, p := range pairs {
		value := p.Value
		if p.ValueBase64 != "" {
			decoded, err := base64.StdEncoding.DecodeString(p.ValueBase64)
			if err != nil {
				return nil, nil, errors.New("头部 base64 编码无效")
			}
			value = string(decoded)
		}
		h.Add(p.Name, value)
		raw = append(raw, [2]string{p.Name, value})
	}
	if err := flow.ValidateHeaderPairs(raw); err != nil {
		return nil, nil, err
	}
	return h, raw, nil
}

func importArchiveText(text *string, encoding string) ([]byte, error) {
	if text == nil {
		return nil, nil
	}
	switch encoding {
	case "":
		return []byte(*text), nil
	case "base64":
		data, err := base64.StdEncoding.DecodeString(*text)
		if err != nil {
			return nil, errors.New("正文 base64 编码无效")
		}
		return data, nil
	default:
		return nil, fmt.Errorf("不支持的正文编码 %q", encoding)
	}
}

func importArchiveWebSocket(entry HAREntry, id string) (*flow.WSSession, error) {
	end := entry.StartedDateTime.Add(time.Duration(entry.Time * float64(time.Millisecond)))
	rawURL := entry.Request.URL
	if strings.HasPrefix(rawURL, "https://") {
		rawURL = "wss://" + strings.TrimPrefix(rawURL, "https://")
	}
	if strings.HasPrefix(rawURL, "http://") {
		rawURL = "ws://" + strings.TrimPrefix(rawURL, "http://")
	}
	ws := &flow.WSSession{
		ID:        id,
		URL:       rawURL,
		Status:    "closed",
		StartTime: entry.StartedDateTime,
		EndTime:   &end,
		Messages:  []flow.WSMessage{},
	}
	for _, m := range entry.WebSocketMessages {
		kind := ""
		switch m.Opcode {
		case 1:
			kind = flow.WSText
		case 2:
			kind = flow.WSBinary
		case 8:
			kind = flow.WSClose
		case 9:
			kind = flow.WSPing
		case 10:
			kind = flow.WSPong
		default:
			return nil, errors.New("WebSocket opcode 无效")
		}
		if m.Type != "send" && m.Type != "receive" {
			return nil, errors.New("WebSocket 消息方向无效")
		}
		if m.Time < 0 || m.Time > 1e11 || math.IsNaN(m.Time) || math.IsInf(m.Time, 0) {
			return nil, errors.New("WebSocket 消息时间无效")
		}
		encoding := m.Encoding
		// Chromium 的二进制消息按 base64 保存，但不附带编码字段。
		if m.Opcode == 2 && encoding == "" {
			encoding = "base64"
		}
		body, err := importArchiveText(&m.Data, encoding)
		if err != nil {
			return nil, err
		}
		payload, size := flow.RetainPayload(body)
		direction := flow.WSServerToClient
		if m.Type == "send" {
			direction = flow.WSClientToServer
		}
		message := flow.WSMessage{
			ID:        flow.NewID(),
			FlowID:    id,
			URL:       ws.URL,
			Type:      kind,
			Direction: direction,
			Data:      payload,
			Timestamp: time.UnixMicro(int64(m.Time * 1e6)),
			Size:      max(m.Size, size),
		}
		ws.Messages = flow.TrimWSMessages(append(ws.Messages, message))
		ws.MessageCount++
		ws.TotalSize += message.PayloadSize()
	}
	return ws, nil
}
