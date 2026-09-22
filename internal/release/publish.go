// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0

package release

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"time"

	"github.com/mintfog/sniffy/internal/update"
	"golang.org/x/mod/semver"
)

const immutableCacheControl = "public, max-age=31536000, immutable"

// Publisher 分别上传版本文件和更新最新清单，两个阶段之间由工作流发布 GitHub 附件。
// 同一桶的发布须串行执行；工作流通过 r2-publish 并发组保证，本地调用由调用方协调。
type Publisher struct {
	Store Store
	// PublicClient 为 nil 时使用默认 HTTP 传输；公开地址校验始终禁用重定向。
	PublicClient *http.Client
}

// Stage 校验全部本地制品后上传；版本清单是上传及公开地址校验完成的标记。
func (publisher Publisher) Stage(ctx context.Context, directory string, manifest update.Manifest) error {
	if err := ValidateManifest(manifest); err != nil {
		return err
	}
	prefix := "releases/v" + manifest.Version
	// 上传使用校验过的同一份内容，全部制品通过校验后才写入 R2。
	contents := make(map[string][]byte, len(manifest.Assets))
	for _, asset := range manifest.Assets {
		location, _ := url.Parse(asset.URL)
		expectedPath := "/" + prefix + "/" + asset.Name
		if location.Path != expectedPath || location.RawQuery != "" || location.ForceQuery || location.Fragment != "" {
			return fmt.Errorf("下载地址与 R2 版本目录不一致：%s", asset.Name)
		}
		data, err := readArtifact(filepath.Join(directory, asset.Name))
		if err != nil {
			return err
		}
		if int64(len(data)) != asset.Size || checksum(data) != asset.SHA256 {
			return fmt.Errorf("本地制品与清单不一致：%s", asset.Name)
		}
		contents[asset.Name] = data
	}
	if err := publisher.Store.EnsureCORS(ctx); err != nil {
		return err
	}
	for _, asset := range manifest.Assets {
		metadata := Metadata{
			ContentType:  "application/octet-stream",
			CacheControl: immutableCacheControl,
			Filename:     asset.Name,
		}
		if err := putImmutable(ctx, publisher.Store, prefix+"/"+asset.Name, contents[asset.Name], metadata); err != nil {
			return err
		}
		if err := verifyPublicAsset(ctx, publisher.PublicClient, asset); err != nil {
			return err
		}
	}
	metadata := Metadata{ContentType: "text/plain; charset=utf-8", CacheControl: immutableCacheControl}
	if err := putImmutable(ctx, publisher.Store, prefix+"/SHA256SUMS", Checksums(manifest), metadata); err != nil {
		return err
	}
	return putManifest(ctx, publisher.Store, prefix+"/release.json", manifest, true)
}

// Promote 在版本文件完成后更新最新清单；旧版本和正式版之后的预发布只保留版本目录。
// 返回值表示本次是否已更新最新清单。
func (publisher Publisher) Promote(ctx context.Context, manifest update.Manifest) (bool, error) {
	if err := ValidateManifest(manifest); err != nil {
		return false, err
	}
	staged, err := getManifest(ctx, publisher.Store, "releases/v"+manifest.Version+"/release.json")
	if err != nil {
		return false, fmt.Errorf("读取版本完成标记：%w", err)
	}
	if !sameManifest(staged, manifest) {
		return false, fmt.Errorf("该版本尚未完成 R2 上传校验")
	}
	previous, err := getManifest(ctx, publisher.Store, "release.json")
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	if err == nil {
		if semver.Compare("v"+manifest.Version, "v"+previous.Version) <= 0 {
			return false, nil
		}
		// 首个正式版发布前，官网可以跟随预发布版本；之后仅更新正式版。
		if semver.Prerelease("v"+previous.Version) == "" && semver.Prerelease("v"+manifest.Version) != "" {
			return false, nil
		}
	}
	if err := putManifest(ctx, publisher.Store, "release.json", manifest, false); err != nil {
		return false, err
	}
	return true, nil
}

func putImmutable(ctx context.Context, store Store, key string, data []byte, metadata Metadata) error {
	existing, err := store.Get(ctx, key)
	if err == nil {
		if !bytes.Equal(existing, data) {
			return fmt.Errorf("已发布的文件内容不同，拒绝覆盖：%s", key)
		}
		return nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("拒绝覆盖 %s：%w", key, err)
	}
	if err := store.Put(ctx, key, data, metadata); err != nil {
		return err
	}
	uploaded, err := store.Get(ctx, key)
	if err != nil {
		return fmt.Errorf("上传后校验失败 %s：%w", key, err)
	}
	if !bytes.Equal(uploaded, data) {
		return fmt.Errorf("上传后校验失败：%s", key)
	}
	return nil
}

func getManifest(ctx context.Context, store Store, key string) (update.Manifest, error) {
	data, err := store.Get(ctx, key)
	if err != nil {
		return update.Manifest{}, err
	}
	manifest, err := ParseManifest(data)
	if err != nil {
		return manifest, fmt.Errorf("读取清单 %s：%w", key, err)
	}
	return manifest, nil
}

func sameManifest(a, b update.Manifest) bool {
	return a.Version == b.Version &&
		a.PublishedAt == b.PublishedAt &&
		a.NotesURL == b.NotesURL &&
		slices.Equal(a.Assets, b.Assets)
}

func putManifest(ctx context.Context, store Store, key string, manifest update.Manifest, immutable bool) error {
	cache := "public, max-age=60"
	if immutable {
		cache = immutableCacheControl
		previous, err := getManifest(ctx, store, key)
		if err == nil {
			if !sameManifest(previous, manifest) {
				return fmt.Errorf("已发布的清单内容不同，拒绝覆盖：%s", key)
			}
			return nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	data, err := EncodeManifest(manifest)
	if err != nil {
		return err
	}
	metadata := Metadata{ContentType: "application/json; charset=utf-8", CacheControl: cache}
	if err := store.Put(ctx, key, data, metadata); err != nil {
		return err
	}
	uploaded, err := getManifest(ctx, store, key)
	if err != nil {
		return err
	}
	if !sameManifest(uploaded, manifest) {
		return fmt.Errorf("发布清单上传后校验失败：%s", key)
	}
	return nil
}

func verifyPublicAsset(ctx context.Context, client *http.Client, asset update.Asset) error {
	if client == nil {
		client = &http.Client{}
	}
	requestClient := *client
	requestClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	const maxAttempts = 5
	var lastErr error
	for attempt := range maxAttempts {
		lastErr = checkPublicAsset(ctx, &requestClient, asset)
		if lastErr == nil {
			return nil
		}
		if attempt == maxAttempts-1 {
			break
		}
		timer := time.NewTimer(time.Duration(attempt+1) * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return lastErr
}

func checkPublicAsset(ctx context.Context, client *http.Client, asset update.Asset) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodHead, asset.URL, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Origin", "https://gosniffy.com")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	origin := response.Header.Get("Access-Control-Allow-Origin")
	validStatus := response.StatusCode >= 200 && response.StatusCode < 300
	validOrigin := origin == "*" || origin == "https://gosniffy.com"
	if !validStatus || !validOrigin || response.ContentLength != asset.Size {
		return fmt.Errorf("下载域名的文件或 CORS 校验失败：%s（HTTP %d）", asset.Name, response.StatusCode)
	}
	return nil
}
