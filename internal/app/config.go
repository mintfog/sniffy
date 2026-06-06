// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package app

import "time"

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

func (c *Config) GetAddress() string             { return c.Address }
func (c *Config) GetPort() int                   { return c.Port }
func (c *Config) GetBufferSize() int             { return c.BufferSize }
func (c *Config) GetReadTimeout() time.Duration  { return c.ReadTimeout }
func (c *Config) GetWriteTimeout() time.Duration { return c.WriteTimeout }
func (c *Config) IsLoggingEnabled() bool         { return c.EnableLogging }
func (c *Config) GetThreads() int                { return c.Threads }
