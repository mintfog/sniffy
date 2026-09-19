// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// 限制进度事件频率,避免挤占抓包事件总线。
const progressInterval = 200 * time.Millisecond

// Progress 是一次下载的进度快照。
type Progress struct {
	Done  int64
	Total int64
}

// Download 返回校验通过后的落盘路径;中断或校验失败时清理临时文件。
func (c *Checker) Download(ctx context.Context, a Asset, destDir string, onProgress func(Progress)) (string, error) {
	if err := a.validate(); err != nil {
		return "", err
	}
	name, err := safeAssetName(a.Name)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		return "", fmt.Errorf("创建下载目录失败: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", c.userAgent())
	resp, err := c.client().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("下载 %s 返回 %s", name, resp.Status)
	}

	tmp, err := os.CreateTemp(destDir, name+".*.part")
	if err != nil {
		return "", fmt.Errorf("创建临时文件失败: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		tmp.Close()
		_ = os.Remove(tmpPath)
	}()

	written, sum, err := copyWithProgress(tmp, resp.Body, a.Size, onProgress)
	if err != nil {
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		return "", fmt.Errorf("写入安装包失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("写入安装包失败: %w", err)
	}
	if written != a.Size {
		return "", fmt.Errorf("安装包大小与清单不符: 期望 %d 字节,实际 %d 字节", a.Size, written)
	}
	if !strings.EqualFold(sum, a.SHA256) {
		return "", fmt.Errorf("安装包校验失败: 期望 SHA256 %s,实际 %s", strings.ToLower(a.SHA256), sum)
	}
	// CreateTemp 默认权限为 0600,免安装二进制在校验通过后补上执行位。
	if a.Kind == KindBinary {
		if err := os.Chmod(tmpPath, 0o755); err != nil {
			return "", fmt.Errorf("设置可执行权限失败: %w", err)
		}
	}

	dest := filepath.Join(destDir, name)
	if err := os.Rename(tmpPath, dest); err != nil {
		return "", fmt.Errorf("保存安装包失败: %w", err)
	}
	if onProgress != nil {
		onProgress(Progress{Done: written, Total: a.Size})
	}
	return dest, nil
}

func copyWithProgress(dst io.Writer, src io.Reader, size int64, onProgress func(Progress)) (int64, string, error) {
	// 多读一个字节以区分正常结束与超出清单大小。
	reader := io.LimitReader(src, size+1)

	hash := sha256.New()
	buf := make([]byte, 128<<10)
	var written int64
	last := time.Now()
	for {
		n, readErr := reader.Read(buf)
		if n > 0 {
			if _, err := dst.Write(buf[:n]); err != nil {
				return written, "", fmt.Errorf("写入安装包失败: %w", err)
			}
			hash.Write(buf[:n])
			written += int64(n)
			if written > size {
				return written, "", errors.New("安装包超出清单登记的体积")
			}
			if onProgress != nil && time.Since(last) >= progressInterval {
				last = time.Now()
				onProgress(Progress{Done: written, Total: size})
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return written, "", readErr
		}
	}
	return written, hex.EncodeToString(hash.Sum(nil)), nil
}

// 清单来自远端,文件名必须限制在下载目录内。
func safeAssetName(name string) (string, error) {
	clean := strings.TrimSpace(name)
	if clean == "" || clean != filepath.Base(clean) || clean == "." || clean == ".." ||
		strings.ContainsAny(clean, `/\`) {
		return "", fmt.Errorf("update: 产物文件名 %q 不合法", name)
	}
	return clean, nil
}
