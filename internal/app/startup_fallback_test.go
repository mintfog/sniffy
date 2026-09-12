// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package app

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/mintfog/sniffy/internal/bodycache"
	"github.com/mintfog/sniffy/internal/platform"
)

func TestBuildCacheUnavailable(t *testing.T) {
	for _, mode := range []string{"路径被文件占用", "初始化失败"} {
		t.Run(mode, func(t *testing.T) {
			if !appScenarioProcess(t) {
				return
			}
			dir, err := platform.CacheDir()
			if err != nil {
				t.Fatal(err)
			}
			var calls int
			if mode == "路径被文件占用" {
				if err := os.Remove(dir); err != nil {
					t.Fatal(err)
				}
				writeAppFixture(t, dir, "占用缓存目录")
			} else {
				newBodyCache = func(path string, budget int64) (*bodycache.Cache, error) {
					calls++
					if path != dir || budget != bodycache.DefaultBudget {
						t.Errorf("缓存初始化参数错误: %s %d", path, budget)
					}
					return nil, os.ErrPermission
				}
			}
			a := buildRunningApp(t)
			assertBuiltAppCapturesHTTP(t, a, a.Engine.Listener().GetAddress())
			payload := bytes.Repeat([]byte("媒体响应"), 4096)
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "video/mp4")
				_, _ = w.Write(payload)
			}))
			defer origin.Close()
			resp, err := proxyTestClient(t, a).Get(origin.URL)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if err != nil || resp.StatusCode != http.StatusOK || !bytes.Equal(body, payload) {
				t.Fatalf("缓存不可用时媒体转发失败: bytes=%d err=%v", len(body), err)
			}

			if mode == "初始化失败" && calls != 1 {
				t.Fatalf("初始化调用次数 = %d", calls)
			}
		})
	}
}

func TestTokenHardLinkFallbackConcurrent(t *testing.T) {
	if !appScenarioProcess(t) {
		return
	}
	dir, err := platform.ConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	tokenLink = func(old, new string) error {
		calls.Add(1)
		return &os.LinkError{Op: "link", Old: old, New: new, Err: errors.New("文件系统不支持硬链接")}
	}
	type result struct {
		token, path string
		err         error
	}
	results := make(chan result, 16)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			token, path, err := EnsureAPIToken()
			results <- result{token, path, err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	want := LoadAPIToken()
	for r := range results {
		if r.err != nil || len(r.token) != 64 || r.token != want || r.path != filepath.Join(dir, apiTokenFileName) {
			t.Fatalf("并发发布结果不一致: path=%q err=%v", r.path, r.err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("硬链接发布次数=%d", calls.Load())
	}
	wide, err := filePermTooWide(filepath.Join(dir, apiTokenFileName))
	if err != nil || wide {
		t.Fatalf("回退发布权限异常: wide=%v err=%v", wide, err)
	}
	assertNoTempTokens(t, dir)
}

func TestTokenHardLinkFallbackRenameFailure(t *testing.T) {
	if !appScenarioProcess(t) {
		return
	}
	dir, err := platform.ConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	tokenLink = func(old, new string) error {
		calls.Add(1)
		if err := os.Mkdir(new, 0o700); err != nil {
			return err
		}
		return &os.LinkError{Op: "link", Old: old, New: new, Err: errors.New("文件系统不支持硬链接")}
	}

	token, path, err := EnsureAPIToken()
	if err == nil || token != "" || path != "" {
		t.Fatalf("发布失败返回值异常: path=%q err=%v", path, err)
	}
	if !strings.Contains(err.Error(), "发布 token 文件") {
		t.Fatalf("重命名错误上下文缺失: %v", err)
	}
	var renameErr *os.LinkError
	if !errors.As(err, &renameErr) || renameErr.Op != "rename" || calls.Load() != 1 {
		t.Fatalf("未进入重命名回退: calls=%d err=%v", calls.Load(), err)
	}
	assertNoTempTokens(t, dir)
}
