// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0

package release

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mintfog/sniffy/internal/update"
	"github.com/stretchr/testify/require"
)

type memoryStore struct {
	objects  map[string][]byte
	writes   []string
	reads    []string
	metadata map[string]Metadata
	corrupt  string
}

func newMemoryStore() *memoryStore {
	return &memoryStore{
		objects:  make(map[string][]byte),
		metadata: make(map[string]Metadata),
	}
}

func (store *memoryStore) Get(_ context.Context, key string) ([]byte, error) {
	store.reads = append(store.reads, key)
	data, exists := store.objects[key]
	if !exists {
		return nil, fs.ErrNotExist
	}
	return bytes.Clone(data), nil
}

func (store *memoryStore) Stat(_ context.Context, key string) (ObjectInfo, error) {
	data, exists := store.objects[key]
	if !exists {
		return ObjectInfo{}, fs.ErrNotExist
	}
	return objectInfo(data), nil
}

func (store *memoryStore) Put(_ context.Context, key string, data []byte, metadata Metadata) error {
	if key == store.corrupt {
		data = []byte("损坏")
	}
	store.objects[key] = bytes.Clone(data)
	store.metadata[key] = metadata
	store.writes = append(store.writes, key)
	return nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return fn(request) }

func testPublisher(store *memoryStore) Publisher {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		data, exists := store.objects[strings.TrimPrefix(request.URL.Path, "/")]
		status := http.StatusOK
		if !exists {
			status = http.StatusNotFound
		}
		return &http.Response{
			StatusCode:    status,
			ContentLength: int64(len(data)),
			Header:        http.Header{"Access-Control-Allow-Origin": {"*"}},
			Body:          http.NoBody,
		}, nil
	})
	return Publisher{Store: store, PublicClient: &http.Client{Transport: transport}}
}

func TestPublishLifecycleAndIdempotency(t *testing.T) {
	directory, manifest := artifactFixture(t, "1.2.3")
	store := newMemoryStore()
	publisher := testPublisher(store)
	_, err := publisher.Promote(t.Context(), manifest)
	require.Error(t, err)
	require.NoError(t, publisher.Stage(t.Context(), directory, manifest))
	require.NotContains(t, store.objects, "release.json")
	require.Equal(t, "releases/v1.2.3/release.json", store.writes[len(store.writes)-1])
	promoted, err := publisher.Promote(t.Context(), manifest)
	require.NoError(t, err)
	require.True(t, promoted)
	require.Equal(t, "release.json", store.writes[len(store.writes)-1])
	require.Equal(t, "public, max-age=60", store.metadata["release.json"].CacheControl)
	asset := manifest.Assets[0]
	metadata := store.metadata["releases/v1.2.3/"+asset.Name]
	require.Equal(t, asset.Name, metadata.Filename)
	require.Equal(t, immutableCacheControl, metadata.CacheControl)
	count := len(store.writes)
	require.NoError(t, publisher.Stage(t.Context(), directory, manifest))
	for _, asset := range manifest.Assets {
		require.NotContains(t, store.reads, "releases/v1.2.3/"+asset.Name, "制品比对只读对象头")
	}
	promoted, err = publisher.Promote(t.Context(), manifest)
	require.NoError(t, err)
	require.False(t, promoted)
	require.Len(t, store.writes, count)
	decoded, err := ParseManifest(store.objects["release.json"])
	require.NoError(t, err)
	require.NoError(t, decoded.Validate())
	require.Equal(t, manifest, decoded)

	require.NoError(t, os.WriteFile(filepath.Join(directory, asset.Name), []byte("另一批制品"), 0600))
	manifest, err = GenerateManifest(ManifestOptions{Directory: directory, Version: "1.2.3", PublishedAt: manifest.PublishedAt})
	require.NoError(t, err)
	require.ErrorContains(t, publisher.Stage(t.Context(), directory, manifest), "拒绝覆盖")
	require.Len(t, store.writes, count)
}

func TestLocalMismatchDoesNotWrite(t *testing.T) {
	directory, manifest := artifactFixture(t, "1.2.3")
	store := newMemoryStore()
	lastAsset := manifest.Assets[len(manifest.Assets)-1]
	path := filepath.Join(directory, lastAsset.Name)
	require.NoError(t, os.WriteFile(path, []byte("更改"), 0600))
	publisher := testPublisher(store)
	err := publisher.Stage(t.Context(), directory, manifest)
	require.ErrorContains(t, err, "本地制品与清单不一致")
	require.Empty(t, store.writes)
}

func TestCorruptUploadCanBeRetriedAfterRepair(t *testing.T) {
	directory, manifest := artifactFixture(t, "1.2.3")
	store := newMemoryStore()
	store.corrupt = "releases/v1.2.3/" + manifest.Assets[0].Name
	publisher := testPublisher(store)
	require.ErrorContains(t, publisher.Stage(t.Context(), directory, manifest), "上传后校验失败")
	require.NotContains(t, store.objects, "releases/v1.2.3/release.json")
	require.NotContains(t, store.objects, "release.json")
	delete(store.objects, store.corrupt)
	store.corrupt = ""
	require.NoError(t, publisher.Stage(t.Context(), directory, manifest))
	promoted, err := publisher.Promote(t.Context(), manifest)
	require.NoError(t, err)
	require.True(t, promoted)
}

func TestManifestEncodingDoesNotCauseConflict(t *testing.T) {
	directory, manifest := artifactFixture(t, "1.2.3")
	manifest.NotesURL = "https://example.com?a=1&b=2"
	store := newMemoryStore()
	publisher := testPublisher(store)
	require.NoError(t, publisher.Stage(t.Context(), directory, manifest))
	// map 编码会改变键顺序及 HTML 转义，清单内容仍相同。
	var fields map[string]any
	require.NoError(t, json.Unmarshal(store.objects["releases/v1.2.3/release.json"], &fields))
	encoded, err := json.Marshal(fields)
	require.NoError(t, err)
	store.objects["releases/v1.2.3/release.json"] = encoded
	count := len(store.writes)
	require.NoError(t, publisher.Stage(t.Context(), directory, manifest))
	require.Len(t, store.writes, count)
	promoted, err := publisher.Promote(t.Context(), manifest)
	require.NoError(t, err)
	require.True(t, promoted)
	manifest.NotesURL = "https://example.com/changed"
	require.ErrorContains(t, publisher.Stage(t.Context(), directory, manifest), "拒绝覆盖")
	_, err = publisher.Promote(t.Context(), manifest)
	require.Error(t, err)
}

func TestPromotionVersionPolicy(t *testing.T) {
	store := newMemoryStore()
	publisher := testPublisher(store)
	for _, test := range []struct {
		version  string
		promoted bool
	}{
		{"0.1.0-beta.1", true},
		{"0.1.0-beta.2", true},
		{"0.1.0", true},
		{"0.0.9", false},
		{"0.2.0-beta.1", false},
		{"1.0.0", true},
		{"1.0.0+build-linux", false},
	} {
		directory, manifest := artifactFixture(t, test.version)
		require.NoError(t, publisher.Stage(t.Context(), directory, manifest))
		promoted, err := publisher.Promote(t.Context(), manifest)
		require.NoError(t, err)
		require.Equal(t, test.promoted, promoted, test.version)
	}
	latest, err := ParseManifest(store.objects["release.json"])
	require.NoError(t, err)
	require.Equal(t, "1.0.0", latest.Version)
}

func TestPublicFailureLeavesReleaseUnstaged(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		directory, manifest := artifactFixture(t, "1.2.3")
		store := newMemoryStore()
		client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode:    http.StatusOK,
				ContentLength: manifest.Assets[0].Size,
				Header:        http.Header{},
				Body:          http.NoBody,
			}, nil
		})}
		publisher := Publisher{Store: store, PublicClient: client}
		require.ErrorContains(t, publisher.Stage(t.Context(), directory, manifest), "CORS")
		require.NotContains(t, store.objects, "releases/v1.2.3/release.json")
		require.NotContains(t, store.objects, "release.json")
	})
}

func TestPublicProbeRetryTimeoutAndCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		asset := update.Asset{Name: "installer.exe", URL: "https://cdn.example/installer.exe", Size: 123}
		attempts := 0
		client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			attempts++
			require.Equal(t, http.MethodHead, request.Method)
			require.Equal(t, "https://gosniffy.com", request.Header.Get("Origin"))
			if attempts == 1 {
				<-request.Context().Done()
				return nil, request.Context().Err()
			}
			return &http.Response{
				StatusCode:    http.StatusOK,
				ContentLength: asset.Size,
				Header:        http.Header{"Access-Control-Allow-Origin": {"https://gosniffy.com"}},
				Body:          http.NoBody,
			}, nil
		})}
		start := time.Now()
		require.NoError(t, verifyPublicAsset(t.Context(), client, asset))
		require.Equal(t, 2, attempts)
		require.Equal(t, 11*time.Second, time.Since(start))
		ctx, cancel := context.WithCancel(t.Context())
		client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
			cancel()
			return nil, fmt.Errorf("请求取消")
		})
		require.ErrorIs(t, verifyPublicAsset(ctx, client, asset), context.Canceled)
	})
}
