// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package plugin

import (
	"fmt"
	"os"
)

// Manager 导出方法的错误分三类,传输层据此选状态码:
//   - 插件 id 未知:满足 errors.Is(err, os.ErrNotExist)
//   - 调用方输入有问题(非法 id/入口、id 冲突、源码编译不过):满足 InvalidInput() bool
//   - 其余是磁盘或运行时故障
//
// 因此导出方法里文件系统调用的错误一律用 %v 展平后再返回:原样透出会让 ENOENT 满足
// errors.Is(os.ErrNotExist),磁盘故障被读成「插件不存在」。loadOne/loadManifest 不受此约束,
// 它们的错误只进 failed 表当展示文本,从不参与分类。

// notFoundError 是「插件 id 未知」。Unwrap 到 os.ErrNotExist 让传输层照常按 404 分类,
// Error() 则给出可读文案——裸 os.ErrNotExist 的 "file does not exist" 作为 API 响应体没有意义。
type notFoundError struct{ id string }

func (e *notFoundError) Error() string { return fmt.Sprintf("插件不存在: %s", e.id) }
func (e *notFoundError) Unwrap() error { return os.ErrNotExist }

func notFound(id string) error { return &notFoundError{id: id} }

// inputError 标记调用方输入引起的失败。
type inputError struct{ err error }

func (e *inputError) Error() string      { return e.err.Error() }
func (e *inputError) Unwrap() error      { return e.err }
func (e *inputError) InvalidInput() bool { return true }

func badInput(err error) error { return &inputError{err: err} }
