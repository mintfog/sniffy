// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0

package release

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/mintfog/sniffy/internal/notices"
)

// WintunVersion 是随 Windows 产物分发的 wintun.dll 版本，第三方声明里的条目须与它一致。
const WintunVersion = "0.14.1"

// 许可只覆盖 wintun.net/builds 官方 zip 里原样的 wintun.dll：DLL 一律从校验过摘要的官方 zip 里取出。
var (
	wintunURL      = "https://www.wintun.net/builds/wintun-" + WintunVersion + ".zip"
	wintunSHA256   = "07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51"
	wintunCacheDir = func() (string, error) {
		dir, err := os.UserCacheDir()
		return filepath.Join(dir, "sniffy-build"), err
	}
)

const maxWintunSize = 8 << 20

// WintunDLL 返回官方发布包里指定架构的 wintun.dll。
// 校验过的发布包缓存在用户缓存目录，缓存缺失或摘要不符时重新下载。
func WintunDLL(ctx context.Context, client *http.Client, arch string) ([]byte, error) {
	if arch != "amd64" && arch != "arm64" {
		return nil, fmt.Errorf("wintun 不支持的架构：%s", arch)
	}
	archive, err := wintunArchive(ctx, client)
	if err != nil {
		return nil, err
	}
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, fmt.Errorf("读取 wintun 发布包：%w", err)
	}
	name := "wintun/bin/" + arch + "/wintun.dll"
	file, err := reader.Open(name)
	if err != nil {
		return nil, fmt.Errorf("wintun 发布包里没有 %s", name)
	}
	defer file.Close()
	return readLimited(file, maxWintunSize, name)
}

func wintunArchive(ctx context.Context, client *http.Client) ([]byte, error) {
	cache := ""
	if dir, err := wintunCacheDir(); err == nil {
		cache = filepath.Join(dir, path.Base(wintunURL))
		if data, err := os.ReadFile(cache); err == nil && checksum(data) == wintunSHA256 {
			return data, nil
		}
	}
	data, err := fetch(ctx, client, wintunURL)
	if err != nil {
		return nil, fmt.Errorf("下载 wintun %s：%w", WintunVersion, err)
	}
	if sum := checksum(data); sum != wintunSHA256 {
		return nil, fmt.Errorf("wintun 发布包摘要不符：期望 %s，实际 %s", wintunSHA256, sum)
	}
	if cache != "" {
		// 缓存只省下次下载，写不进去不影响本次打包。
		_ = WriteFile(cache, data)
	}
	return data, nil
}

func fetch(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s 返回 %s", url, response.Status)
	}
	return readLimited(response.Body, maxWintunSize, url)
}

func readLimited(reader io.Reader, limit int64, name string) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s 超过 %d 字节", name, limit)
	}
	return data, nil
}

// WindowsPortable 生成 Windows 免安装 zip：可执行文件、同目录加载的 wintun.dll 与第三方声明都放在根目录。
func WindowsPortable(ctx context.Context, client *http.Client, arch, binary, exeName string) ([]byte, error) {
	if strings.ContainsAny(exeName, `/\`) || !strings.HasSuffix(strings.ToLower(exeName), ".exe") {
		return nil, fmt.Errorf("可执行文件名必须是不含目录的 .exe 文件名：%s", exeName)
	}
	info, err := os.Stat(binary)
	if err != nil {
		return nil, err
	}
	exe, err := readArtifact(binary)
	if err != nil {
		return nil, err
	}
	dll, err := WintunDLL(ctx, client, arch)
	if err != nil {
		return nil, err
	}
	// 各条目统一取可执行文件的修改时间，同一输入得到相同的 zip。
	modified := info.ModTime()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	entries := []struct {
		name string
		data []byte
	}{
		{exeName, exe},
		{"wintun.dll", dll},
		{"THIRD_PARTY_NOTICES.txt", []byte(notices.Text)},
	}
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Deflate, Modified: modified}
		file, err := writer.CreateHeader(header)
		if err != nil {
			return nil, err
		}
		if _, err := file.Write(entry.data); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}
