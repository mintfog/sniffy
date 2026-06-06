// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package core

import (
	"net/http"

	"github.com/mintfog/sniffy/ca"
	"github.com/mintfog/sniffy/capture/types"
)

// Option 以函数式选项方式配置 Engine。
type Option func(*Engine)

// WithCA 注入自定义 CA(默认自动创建自签名 CA)。
func WithCA(c ca.CA) Option {
	return func(e *Engine) { e.ca = c }
}

// WithUpstreamClient 注入自定义上游 HTTP 客户端。
func WithUpstreamClient(c *http.Client) Option {
	return func(e *Engine) { e.upstream = c }
}

// WithLogger 注入日志器。
func WithLogger(l types.Logger) Option {
	return func(e *Engine) { e.logger = l }
}
