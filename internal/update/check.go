// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/mintfog/sniffy/internal/version"
)

// DefaultFeeds 按官网、对象存储的顺序提供发布清单。
var DefaultFeeds = []string{
	"https://gosniffy.com/release.json",
	"https://cdn.gosniffy.com/release.json",
}

// FeedEnv 用逗号分隔的 https 地址覆盖清单源,供自建分发渠道使用。
const FeedEnv = "SNIFFY_UPDATE_FEED"

const maxManifestBytes = 256 << 10

// 每个清单源单独计时,以便主源超时后仍能尝试备用源。
const manifestTimeout = 10 * time.Second

// Checker 按序尝试各清单源,并负责比较版本与下载安装包。
type Checker struct {
	Feeds   []string
	Current string
	Client  *http.Client
}

// Result 是一次检查的结论。
type Result struct {
	Manifest Manifest
	// Feed 是实际取到清单的源,便于排查主源/兜底源哪一条生效。
	Feed string
	// Available 表示线上版本严格新于当前版本。
	Available bool
	// Dev 表示当前是未注入版本号的开发构建,此时不做新旧判定。
	Dev bool
	// Asset 是匹配当前平台与形态的产物;HasAsset 为假时只能引导用户去官网自取。
	Asset    Asset
	HasAsset bool
}

// NewChecker 以默认源构造检查器;设置了 FeedEnv 时以环境变量为准。
func NewChecker(current string) *Checker {
	return &Checker{Feeds: feeds(), Current: current, Client: newClient()}
}

func feeds() []string {
	custom := parseFeedList(os.Getenv(FeedEnv))
	if len(custom) > 0 {
		return custom
	}
	return append([]string(nil), DefaultFeeds...)
}

// 清单包含下载地址和摘要,必须通过 HTTPS 获取以防两者同时被篡改。
func parseFeedList(raw string) []string {
	var out []string
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		u, err := url.Parse(item)
		if err != nil || u.Host == "" || u.Scheme != "https" {
			continue
		}
		out = append(out, item)
	}
	return out
}

// 清单请求由 context 限时;安装包下载仅限制握手和响应头等待,允许慢速传输。
func newClient() *http.Client {
	tr := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          4,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	return &http.Client{Transport: tr}
}

func IsDevBuild(v string) bool {
	v = strings.TrimSpace(v)
	return v == "" || v == version.DevVersion
}

// Check 取回清单并与当前版本比较。所有源都失败时返回错误。
func (c *Checker) Check(ctx context.Context) (Result, error) {
	m, feed, err := c.fetch(ctx)
	if err != nil {
		return Result{}, err
	}
	res := Result{Manifest: m, Feed: feed, Dev: IsDevBuild(c.Current)}
	res.Available = !res.Dev && IsNewer(m.Version, c.Current)
	res.Asset, res.HasAsset = m.AssetFor(runtime.GOOS, runtime.GOARCH, Edition)
	return res, nil
}

func (c *Checker) fetch(ctx context.Context) (Manifest, string, error) {
	list := c.Feeds
	if len(list) == 0 {
		list = feeds()
	}
	var errs []error
	for _, feed := range list {
		m, err := c.fetchOne(ctx, feed)
		if err == nil {
			return m, feed, nil
		}
		errs = append(errs, fmt.Errorf("%s: %w", feed, err))
		if ctx.Err() != nil {
			break
		}
	}
	if len(errs) == 0 {
		return Manifest{}, "", ErrNoManifest
	}
	return Manifest{}, "", errors.Join(append([]error{ErrNoManifest}, errs...)...)
}

func (c *Checker) fetchOne(ctx context.Context, feed string) (Manifest, error) {
	ctx, cancel := context.WithTimeout(ctx, manifestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, feed, nil)
	if err != nil {
		return Manifest{}, err
	}
	req.Header.Set("Accept", "application/json")
	// 清单与官网静态资源共用缓存,每次检查要求重新验证版本。
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("User-Agent", c.userAgent())

	resp, err := c.client().Do(req)
	if err != nil {
		return Manifest{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Manifest{}, fmt.Errorf("清单源返回 %s", resp.Status)
	}

	var m Manifest
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxManifestBytes)).Decode(&m); err != nil {
		return Manifest{}, fmt.Errorf("解析清单失败: %w", err)
	}
	if err := m.Validate(); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

func (c *Checker) client() *http.Client {
	if c.Client != nil {
		return c.Client
	}
	c.Client = newClient()
	return c.Client
}

func (c *Checker) userAgent() string {
	cur := c.Current
	if cur == "" {
		cur = version.DevVersion
	}
	return fmt.Sprintf("sniffy/%s (%s; %s/%s)", cur, Edition, runtime.GOOS, runtime.GOARCH)
}
