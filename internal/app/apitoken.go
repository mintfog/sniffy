// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package app

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/mintfog/sniffy/internal/platform"
)

// API token 独立存储，避免随 config.json 暴露。
const apiTokenFileName = "api_token"

const apiTokenEnv = "SNIFFY_API_TOKEN"

// LoadAPIToken 优先读取环境变量，其次读取配置目录下的 token 文件。
func LoadAPIToken() string {
	if t := strings.TrimSpace(os.Getenv(apiTokenEnv)); t != "" {
		return t
	}
	dir, err := platform.ConfigDir()
	if err != nil {
		return ""
	}
	return loadAPITokenFile(dir)
}

func loadAPITokenFile(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, apiTokenFileName))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// EnsureTokenSecrecy 收紧磁盘 token 的权限，并轮换可能已暴露的凭证。
// 使用环境变量时不修改磁盘，并返回空 token。
func EnsureTokenSecrecy() (token string, rotated bool, err error) {
	if strings.TrimSpace(os.Getenv(apiTokenEnv)) != "" {
		return "", false, nil
	}
	dir, err := platform.ConfigDir()
	if err != nil {
		return "", false, nil
	}
	return ensureTokenSecrecy(dir)
}

func ensureTokenSecrecy(dir string) (token string, rotated bool, err error) {
	path := filepath.Join(dir, apiTokenFileName)
	lockErr := withTokenDirLock(dir, func() error {
		if loadAPITokenFile(dir) == "" {
			return nil
		}
		wide, e := filePermTooWide(path)
		if e != nil {
			return e
		}
		if wide {
			_, r, e := rotateIfWide(dir, path)
			if e != nil {
				return e
			}
			rotated = r
		} else if e := secureFilePerm(path); e != nil {
			return e
		}
		token = loadAPITokenFile(dir)
		return nil
	})
	if lockErr != nil {
		return "", false, lockErr
	}
	return token, rotated, nil
}

// withTokenDirLock 串行化跨进程的 token 文件修改。
func withTokenDirLock(dir string, fn func() error) error {
	unlock, err := lockTokenDir(dir)
	if err != nil {
		return fmt.Errorf("获取 token 锁: %w", err)
	}
	defer func() { _ = unlock() }()
	return fn()
}

// EnsureAPIToken 返回现有 token，或生成并原子写入新 token。
func EnsureAPIToken() (token, path string, err error) {
	if t := strings.TrimSpace(os.Getenv(apiTokenEnv)); t != "" {
		return t, "", nil
	}
	dir, err := platform.ConfigDir()
	if err != nil {
		return "", "", err
	}
	lockErr := withTokenDirLock(dir, func() error {
		t, p, e := ensureAPITokenFile(dir)
		token, path = t, p
		return e
	})
	if lockErr != nil {
		return "", "", lockErr
	}
	return token, path, nil
}

func ensureAPITokenFile(dir string) (token, path string, err error) {
	path = filepath.Join(dir, apiTokenFileName)
	if t := loadAPITokenFile(dir); t != "" {
		if err := secureFilePerm(path); err != nil {
			return "", "", fmt.Errorf("收紧已有 token 文件权限: %w", err)
		}
		return t, path, nil
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("生成随机 token: %w", err)
	}
	token = hex.EncodeToString(buf)
	tmpName, err := writeTempToken(dir, token)
	if err != nil {
		return "", "", err
	}
	// Link 避免覆盖其他进程并发创建的 token。
	if err := os.Link(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		if os.IsExist(err) {
			t := loadAPITokenFile(dir)
			if t == "" {
				return "", "", fmt.Errorf("token 文件 %s 已存在但为空,请删除后重试", path)
			}
			if err := secureFilePerm(path); err != nil {
				return "", "", fmt.Errorf("收紧已有 token 文件权限: %w", err)
			}
			return t, path, nil
		}
		// 不支持硬链接时回退到原子重命名。
		tmpName2, werr := writeTempToken(dir, token)
		if werr != nil {
			return "", "", werr
		}
		if rerr := os.Rename(tmpName2, path); rerr != nil {
			_ = os.Remove(tmpName2)
			return "", "", fmt.Errorf("发布 token 文件: %w", rerr)
		}
		if err := secureFilePerm(path); err != nil {
			return "", "", fmt.Errorf("收紧 token 文件权限: %w", err)
		}
		return token, path, nil
	}
	_ = os.Remove(tmpName)
	if err := secureFilePerm(path); err != nil {
		return "", "", fmt.Errorf("收紧 token 文件权限: %w", err)
	}
	return token, path, nil
}

// rotateIfWide 在权限过宽时原子轮换 token；调用方须持有目录锁。
func rotateIfWide(dir, path string) (token string, rotated bool, err error) {
	wide, err := filePermTooWide(path)
	if err != nil || !wide {
		return "", false, nil
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", false, fmt.Errorf("生成随机 token: %w", err)
	}
	token = hex.EncodeToString(buf)
	tmpName, err := writeTempToken(dir, token)
	if err != nil {
		return "", false, err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return "", false, fmt.Errorf("轮换 token 文件: %w", err)
	}
	if err := secureFilePerm(path); err != nil {
		return "", false, fmt.Errorf("收紧轮换后 token 文件权限: %w", err)
	}
	return token, true, nil
}

func writeTempToken(dir, token string) (string, error) {
	tmp, err := os.CreateTemp(dir, apiTokenFileName+"-*.tmp")
	if err != nil {
		return "", fmt.Errorf("创建临时 token 文件: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.WriteString(token + "\n"); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return "", fmt.Errorf("写入 token 文件: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return "", fmt.Errorf("关闭 token 文件: %w", err)
	}
	return tmpName, nil
}

// IsLoopbackHost 判断 host 是否为 localhost 或回环 IP 地址。
func IsLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
