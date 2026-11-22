// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0

package release

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mintfog/sniffy/internal/notices"
	"github.com/stretchr/testify/require"
)

func fakeWintun(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, body := range map[string]string{
		"wintun/LICENSE.txt":          "license",
		"wintun/bin/amd64/wintun.dll": "dll-amd64",
		"wintun/bin/arm64/wintun.dll": "dll-arm64",
	} {
		file, err := writer.Create(name)
		require.NoError(t, err)
		_, err = file.Write([]byte(body))
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	return buffer.Bytes()
}

// serveWintun 让下载指向本地服务，返回请求计数与缓存目录。
func serveWintun(t *testing.T, body []byte, expected []byte) (*atomic.Int32, string) {
	t.Helper()
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write(body)
	}))
	t.Cleanup(server.Close)
	cache := t.TempDir()
	url, sum, cacheDir := wintunURL, wintunSHA256, wintunCacheDir
	t.Cleanup(func() { wintunURL, wintunSHA256, wintunCacheDir = url, sum, cacheDir })
	wintunURL = server.URL + "/builds/wintun-" + WintunVersion + ".zip"
	wintunSHA256 = checksum(expected)
	wintunCacheDir = func() (string, error) { return cache, nil }
	return &hits, cache
}

func TestWintunDLLCachesVerifiedArchive(t *testing.T) {
	archive := fakeWintun(t)
	hits, cache := serveWintun(t, archive, archive)

	dll, err := WintunDLL(t.Context(), http.DefaultClient, "amd64")
	require.NoError(t, err)
	require.Equal(t, "dll-amd64", string(dll))
	dll, err = WintunDLL(t.Context(), http.DefaultClient, "arm64")
	require.NoError(t, err)
	require.Equal(t, "dll-arm64", string(dll))
	require.EqualValues(t, 1, hits.Load())

	cached := filepath.Join(cache, "wintun-"+WintunVersion+".zip")
	require.NoError(t, os.WriteFile(cached, []byte("被改动的缓存"), 0600))
	dll, err = WintunDLL(t.Context(), http.DefaultClient, "amd64")
	require.NoError(t, err)
	require.Equal(t, "dll-amd64", string(dll))
	require.EqualValues(t, 2, hits.Load())
	restored, err := os.ReadFile(cached)
	require.NoError(t, err)
	require.Equal(t, archive, restored)
}

func TestWintunDLLRejectsUnexpectedArchive(t *testing.T) {
	_, cache := serveWintun(t, fakeWintun(t), []byte("官方发布包"))
	_, err := WintunDLL(t.Context(), http.DefaultClient, "amd64")
	require.ErrorContains(t, err, "摘要不符")
	entries, err := os.ReadDir(cache)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestWintunDLLArchitectures(t *testing.T) {
	hits, _ := serveWintun(t, nil, nil)
	for _, arch := range []string{"386", "arm", ""} {
		_, err := WintunDLL(t.Context(), http.DefaultClient, arch)
		require.ErrorContains(t, err, "不支持的架构")
	}
	require.Zero(t, hits.Load())
}

func TestWindowsPortableLayout(t *testing.T) {
	archive := fakeWintun(t)
	serveWintun(t, archive, archive)
	binary := filepath.Join(t.TempDir(), "sniffy-windows-arm64.exe")
	require.NoError(t, os.WriteFile(binary, []byte("exe"), 0600))

	data, err := WindowsPortable(t.Context(), http.DefaultClient, "arm64", binary, "sniffy.exe")
	require.NoError(t, err)
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	require.NoError(t, err)
	contents := map[string]string{}
	var names []string
	for _, file := range reader.File {
		names = append(names, file.Name)
		body, err := file.Open()
		require.NoError(t, err)
		raw, err := io.ReadAll(body)
		require.NoError(t, err)
		require.NoError(t, body.Close())
		contents[file.Name] = string(raw)
	}
	require.Equal(t, []string{"sniffy.exe", "wintun.dll", "THIRD_PARTY_NOTICES.txt"}, names)
	require.Equal(t, "exe", contents["sniffy.exe"])
	require.Equal(t, "dll-arm64", contents["wintun.dll"])
	require.Equal(t, notices.Text, contents["THIRD_PARTY_NOTICES.txt"])

	again, err := WindowsPortable(t.Context(), http.DefaultClient, "arm64", binary, "sniffy.exe")
	require.NoError(t, err)
	require.Equal(t, data, again)

	for _, name := range []string{"bin/sniffy.exe", `bin\sniffy.exe`, "sniffy"} {
		_, err := WindowsPortable(t.Context(), http.DefaultClient, "arm64", binary, name)
		require.Error(t, err, name)
	}
}

func TestNoticesListBundledWintun(t *testing.T) {
	require.True(t, strings.Contains(notices.Text, "\nWintun "+WintunVersion+" "), "第三方声明需登记 wintun %s", WintunVersion)
}
