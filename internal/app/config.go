// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package app

import (
	"time"

	"github.com/mintfog/sniffy/internal/platform"
	"github.com/mintfog/sniffy/internal/service"
)

// Config 是一个可复用的 types.Config 实现,供 headless 与桌面入口共用。
type Config struct {
	Address       string
	Port          int
	ReadTimeout   time.Duration
	WriteTimeout  time.Duration
	BufferSize    int
	EnableLogging bool
	Threads       int
}

// DefaultConfig 返回默认配置。
func DefaultConfig() *Config {
	return &Config{
		Address:       "0.0.0.0",
		Port:          8080,
		ReadTimeout:   30 * time.Second,
		WriteTimeout:  30 * time.Second,
		BufferSize:    4096,
		EnableLogging: true,
		Threads:       5,
	}
}

// ResolveListen 以 defHost/defPort 为默认值,套用持久化 config.json 中保存的
// 监听地址与端口(仅采纳有效字段),返回最终生效值。这使 UI 中修改的端口在
// 重启后依然生效;headless 模式下命令行显式参数应由调用方在此之后覆盖。
func ResolveListen(defHost string, defPort int) (string, int) {
	host, port := defHost, defPort
	dir, err := platform.ConfigDir()
	if err != nil {
		return host, port
	}
	saved, ok := service.LoadSaved(dir)
	if !ok {
		return host, port
	}
	if saved.Host != "" {
		host = saved.Host
	}
	if saved.Port >= 1 && saved.Port <= 65535 {
		port = saved.Port
	}
	return host, port
}

func (c *Config) GetAddress() string             { return c.Address }
func (c *Config) GetPort() int                   { return c.Port }
func (c *Config) GetBufferSize() int             { return c.BufferSize }
func (c *Config) GetReadTimeout() time.Duration  { return c.ReadTimeout }
func (c *Config) GetWriteTimeout() time.Duration { return c.WriteTimeout }
func (c *Config) IsLoggingEnabled() bool         { return c.EnableLogging }
func (c *Config) GetThreads() int                { return c.Threads }
