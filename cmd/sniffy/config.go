// Copyright 2025 The f-dong Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"net"
	"time"
)

// Config TCP监听器配置
type Config struct {
	// Address 监听地址，默认为 "0.0.0.0"
	Address string `json:"address" yaml:"address"`

	// Port 监听端口，默认为 8080
	Port int `json:"port" yaml:"port"`

	// ReadTimeout 读取超时时间
	ReadTimeout time.Duration `json:"read_timeout" yaml:"read_timeout"`

	// WriteTimeout 写入超时时间
	WriteTimeout time.Duration `json:"write_timeout" yaml:"write_timeout"`

	// MaxConnections 最大连接数，0表示无限制
	MaxConnections int `json:"max_connections" yaml:"max_connections"`

	// BufferSize 缓冲区大小
	BufferSize int `json:"buffer_size" yaml:"buffer_size"`

	// EnableLogging 是否启用日志
	EnableLogging bool `json:"enable_logging" yaml:"enable_logging"`

	// Threads 线程数
	Threads int `json:"threads" yaml:"threads"`
}

// DefaultConfig 返回默认配置
func DefaultConfig() *Config {
	return &Config{
		Address:        "0.0.0.0",
		Port:           8080,
		ReadTimeout:    30 * time.Second,
		WriteTimeout:   30 * time.Second,
		MaxConnections: 0, // 无限制
		BufferSize:     4096,
		EnableLogging:  true,
		Threads:        5, // 默认5个线程
	}
}

// 实现 capture.Config 接口

func (c *Config) GetAddress() string {
	return c.Address
}

func (c *Config) GetPort() int {
	return c.Port
}

func (c *Config) GetBufferSize() int {
	return c.BufferSize
}

func (c *Config) GetReadTimeout() time.Duration {
	return c.ReadTimeout
}

func (c *Config) GetWriteTimeout() time.Duration {
	return c.WriteTimeout
}

func (c *Config) IsLoggingEnabled() bool {
	return c.EnableLogging
}

func (c *Config) GetThreads() int {
	return c.Threads
}

// Validate 验证配置
func (c *Config) Validate() error {
	// 验证地址
	if c.Address == "" {
		c.Address = "0.0.0.0"
	}

	// 验证IP地址格式
	if ip := net.ParseIP(c.Address); ip == nil {
		return fmt.Errorf("invalid IP address: %s", c.Address)
	}

	// 验证端口范围
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("invalid port number: %d (must be between 1-65535)", c.Port)
	}

	// 验证超时时间
	if c.ReadTimeout <= 0 {
		c.ReadTimeout = 30 * time.Second
	}

	if c.WriteTimeout <= 0 {
		c.WriteTimeout = 30 * time.Second
	}

	// 验证缓冲区大小
	if c.BufferSize <= 0 {
		c.BufferSize = 4096
	}

	// 验证最大连接数
	if c.MaxConnections < 0 {
		c.MaxConnections = 0
	}

	return nil
}

// GetListenAddress 获取完整的监听地址
func (c *Config) GetListenAddress() string {
	return fmt.Sprintf("%s:%d", c.Address, c.Port)
}

// Clone 克隆配置
func (c *Config) Clone() *Config {
	return &Config{
		Address:        c.Address,
		Port:           c.Port,
		ReadTimeout:    c.ReadTimeout,
		WriteTimeout:   c.WriteTimeout,
		MaxConnections: c.MaxConnections,
		BufferSize:     c.BufferSize,
		EnableLogging:  c.EnableLogging,
	}
}
