// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/mintfog/sniffy/internal/bodycache"
)

func harEntryJSON(url string, body []byte) string {
	return fmt.Sprintf(`{"startedDateTime":"2026-09-17T10:00:00Z","time":1,
  "request":{"method":"GET","url":%q,"httpVersion":"HTTP/1.1","headers":[],"cookies":[],"queryString":[],"headersSize":-1,"bodySize":0},
  "response":{"status":200,"statusText":"OK","httpVersion":"HTTP/1.1","headers":[],"cookies":[],
    "content":{"size":%d,"mimeType":"application/octet-stream","text":%q,"encoding":"base64"},"redirectURL":"","headersSize":-1,"bodySize":-1},
  "cache":{},"timings":{"send":0,"wait":1,"receive":0}}`, url, len(body), base64.StdEncoding.EncodeToString(body))
}

func harDocument(entries ...string) string {
	return `{"log":{"entries":[` + strings.Join(entries, ",") + `],"version":"1.2"}}`
}

func newCachedService(t *testing.T) (*Service, *bodycache.Cache) {
	t.Helper()
	cache, err := bodycache.New(t.TempDir(), 0)
	if err != nil {
		t.Fatal(err)
	}
	svc := New(nil, nil, "", "")
	svc.SetBodyCache(cache)
	return svc, cache
}

func TestImportArchiveSpillsLargeBodies(t *testing.T) {
	t.Parallel()
	svc, cache := newCachedService(t)
	large := bytes.Repeat([]byte{0x00, 0xff, 0x7f}, int(defaultLargeBodyKiB*1024)/3+1)
	small := []byte("small")
	document := harDocument(harEntryJSON("https://a.test/large", large), harEntryJSON("https://a.test/small", small))
	result, err := svc.ImportSessionArchive(strings.NewReader(document))
	if err != nil || result.Imported != 2 || result.Dropped != 0 {
		t.Fatalf("导入结果 = %+v, %v", result, err)
	}

	spilled, _ := svc.RawFlow(result.IDs[0])
	path, size := spilled.Response.BodyFile()
	if path == "" || size != int64(len(large)) || spilled.Response.Body != nil {
		t.Fatalf("大正文应落盘: path=%q size=%d body=%d", path, size, len(spilled.Response.Body))
	}
	if stored, err := os.ReadFile(path); err != nil || !bytes.Equal(stored, large) {
		t.Fatalf("落盘正文不一致: %v", err)
	}
	inline, _ := svc.RawFlow(result.IDs[1])
	if path, _ := inline.Response.BodyFile(); path != "" || !bytes.Equal(inline.Response.Body, small) {
		t.Fatal("小正文应留在内存")
	}

	var exported bytes.Buffer
	if _, err := svc.WriteSessionArchive(context.Background(), &exported, result.IDs, fullBodies); err != nil {
		t.Fatal(err)
	}
	target := New(nil, nil, "", "")
	again, err := target.ImportSessionArchive(&exported)
	if err != nil {
		t.Fatal(err)
	}
	restored, _ := target.RawFlow(again.IDs[0])
	if !bytes.Equal(restored.Response.Body, large) {
		t.Fatal("落盘正文导出后未还原")
	}

	svc.DeleteSession(result.IDs[0])
	if cache.Total() != 0 {
		t.Fatalf("会话删除后缓存仍占用 %d 字节", cache.Total())
	}
}

func TestImportArchiveFailureReleasesSpilledBodies(t *testing.T) {
	t.Parallel()
	svc, cache := newCachedService(t)
	large := make([]byte, defaultLargeBodyKiB*1024)
	document := harDocument(harEntryJSON("https://a.test/large", large), harEntryJSON("/relative", nil))
	if _, err := svc.ImportSessionArchive(strings.NewReader(document)); err == nil || !strings.Contains(err.Error(), "第 2 条会话") {
		t.Fatalf("应报告非法条目的位置: %v", err)
	}
	if len(svc.SessionIDs()) != 0 || cache.Total() != 0 {
		t.Fatalf("失败导入留下了记录或缓存: %d 字节", cache.Total())
	}
}

func TestImportArchiveKeepsNewestWithinCapacity(t *testing.T) {
	t.Parallel()
	svc, cache := newCachedService(t)
	svc.sessions.setCap(2)
	large := make([]byte, defaultLargeBodyKiB*1024)
	document := harDocument(
		harEntryJSON("https://a.test/1", large),
		harEntryJSON("https://a.test/2", large),
		harEntryJSON("https://a.test/3", large),
	)
	result, err := svc.ImportSessionArchive(strings.NewReader(document))
	if err != nil || result.Imported != 2 || result.HTTP != 2 || result.Dropped != 1 || len(result.IDs) != 2 {
		t.Fatalf("导入结果 = %+v, %v", result, err)
	}
	first, _ := svc.RawFlow(result.IDs[0])
	if first.Request.URL != "https://a.test/2" {
		t.Fatalf("应保留最新的记录，首条为 %s", first.Request.URL)
	}
	if cache.Total() != int64(2*len(large)) {
		t.Fatalf("缓存占用 %d 字节，期望两份正文", cache.Total())
	}
}

func TestImportArchiveValueLimit(t *testing.T) {
	t.Parallel()
	document := harDocument(harEntryJSON("https://a.test/1", nil), harEntryJSON("https://a.test/2", make([]byte, 64<<10)))
	stage := &importStage{valueLimit: 16 << 10, memoryLimit: 1 << 20, flowCap: 10, wsCap: 10}
	if err := stage.read(strings.NewReader(document)); !errors.Is(err, ErrSessionImportTooLarge) {
		t.Fatalf("超限条目应被拒绝: %v", err)
	}
	stage = &importStage{valueLimit: 128 << 10, memoryLimit: 1 << 20, flowCap: 10, wsCap: 10}
	if err := stage.read(strings.NewReader(document)); err != nil || len(stage.flows) != 2 {
		t.Fatalf("额度按单个值计算，总量超出不应失败: %v", err)
	}
}

func TestImportArchiveRejectsMalformedStructure(t *testing.T) {
	t.Parallel()
	for _, document := range []string{
		"",
		"[]",
		`{"log":[]}`,
		`{"log":{"entries":{}}}`,
		`{"log":{"entries":[],"entries":[]}}`,
		`{"log":{"entries":[]}}}`,
		`{"log":{"entries":[`,
		`{"log":{"version":1.2,"entries":[]}}`,
	} {
		svc := New(nil, nil, "", "")
		if _, err := svc.ImportSessionArchive(strings.NewReader(document)); err == nil {
			t.Errorf("应拒绝 %q", document)
		}
	}
}

func TestImportArchiveRejectsBodiesBeyondCacheBudget(t *testing.T) {
	t.Parallel()
	large := make([]byte, defaultLargeBodyKiB*1024)
	cache, err := bodycache.New(t.TempDir(), int64(2*len(large)))
	if err != nil {
		t.Fatal(err)
	}
	svc := New(nil, nil, "", "")
	svc.SetBodyCache(cache)
	document := harDocument(
		harEntryJSON("https://a.test/1", large),
		harEntryJSON("https://a.test/2", large),
		harEntryJSON("https://a.test/3", large),
	)
	if _, err := svc.ImportSessionArchive(strings.NewReader(document)); !errors.Is(err, ErrSessionImportTooLarge) {
		t.Fatalf("落盘总量超过缓存容量应被拒绝: %v", err)
	}
	if len(svc.SessionIDs()) != 0 || cache.Total() != 0 {
		t.Fatalf("失败导入留下了记录或缓存: %d 字节", cache.Total())
	}

	svc.sessions.setCap(2)
	result, err := svc.ImportSessionArchive(strings.NewReader(document))
	if err != nil || result.Imported != 2 || result.Dropped != 1 {
		t.Fatalf("容量外的记录不占缓存容量: %+v, %v", result, err)
	}
	for _, id := range result.IDs {
		f, _ := svc.RawFlow(id)
		path, _ := f.Response.BodyFile()
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("入库会话的落盘正文缺失: %v", err)
		}
	}
}

func TestImportArchiveMemoryLimit(t *testing.T) {
	t.Parallel()
	body := make([]byte, 4<<10)
	document := harDocument(
		harEntryJSON("https://a.test/1", body),
		harEntryJSON("https://a.test/2", body),
		harEntryJSON("https://a.test/3", body),
	)
	newStage := func(flowCap int) *importStage {
		return &importStage{valueLimit: 1 << 20, memoryLimit: 10 << 10, spillThreshold: 1 << 20, flowCap: flowCap, wsCap: 10}
	}
	if err := newStage(10).read(strings.NewReader(document)); !errors.Is(err, ErrSessionImportTooLarge) {
		t.Fatalf("内存正文超限应被拒绝: %v", err)
	}
	stage := newStage(2)
	if err := stage.read(strings.NewReader(document)); err != nil || stage.memory != int64(2*len(body)) {
		t.Fatalf("容量外的记录不占内存额度: memory=%d, %v", stage.memory, err)
	}
}

func TestImportArchiveKeepsDeclaredSizeOfTruncatedBody(t *testing.T) {
	t.Parallel()
	svc, cache := newCachedService(t)
	partial := make([]byte, defaultLargeBodyKiB*1024)
	declared := int64(len(partial)) * 4
	entry := strings.Replace(harEntryJSON("https://a.test/partial", partial),
		fmt.Sprintf(`"content":{"size":%d`, len(partial)), fmt.Sprintf(`"content":{"size":%d`, declared), 1)
	result, err := svc.ImportSessionArchive(strings.NewReader(harDocument(entry)))
	if err != nil {
		t.Fatal(err)
	}
	f, _ := svc.RawFlow(result.IDs[0])
	if f.Response.BodyLen() != declared || !bytes.Equal(f.Response.Body, partial) || cache.Total() != 0 {
		t.Fatalf("截断正文应留在内存并保留声明大小: len=%d cache=%d", f.Response.BodyLen(), cache.Total())
	}
}

func TestImportArchiveOverBudgetKeepsExistingBodies(t *testing.T) {
	t.Parallel()
	unit := int(defaultLargeBodyKiB * 1024)
	cache, err := bodycache.New(t.TempDir(), int64(2*unit))
	if err != nil {
		t.Fatal(err)
	}
	svc := New(nil, nil, "", "")
	svc.SetBodyCache(cache)
	existing := cache.Create("existing")
	_, _ = existing.Write(make([]byte, unit))
	path, _ := existing.Commit()

	document := harDocument(
		harEntryJSON("https://a.test/1", make([]byte, unit)),
		harEntryJSON("https://a.test/2", make([]byte, unit+unit/2)),
	)
	if _, err := svc.ImportSessionArchive(strings.NewReader(document)); !errors.Is(err, ErrSessionImportTooLarge) {
		t.Fatalf("落盘总量超过缓存容量应被拒绝: %v", err)
	}
	if _, err := os.Stat(path); err != nil || cache.Total() != int64(unit) {
		t.Fatalf("被拒绝的导入不应淘汰已有副本: total=%d, %v", cache.Total(), err)
	}
}
