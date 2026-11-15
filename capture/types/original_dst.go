// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package types

import (
	"context"
	"strings"
)

type originalDstKey struct{}

type originalDst struct {
	authority string
	addr      string
}

// WithOriginalDst 标注 ctx 所属的请求来自透明连接：应用把 authority（host:port）解析成了
// addr（ip:port）并直接连它，没有经过代理握手。
func WithOriginalDst(ctx context.Context, authority, addr string) context.Context {
	return context.WithValue(ctx, originalDstKey{}, originalDst{authority: authority, addr: addr})
}

// IsTransparent 报告 ctx 所属的请求是否来自透明连接。
func IsTransparent(ctx context.Context) bool {
	_, ok := ctx.Value(originalDstKey{}).(originalDst)
	return ok
}

// PinnedDialAddr 返回拨 address 时实际应连的地址。address 就是请求原本的 authority 时换成
// 应用连的原始地址，与应用自己的解析结果保持一致；拨的是别处（被插件改写后的目标）时原样
// 返回。判定只看地址字面，拨上游代理的 ctx 须先经 WithoutDialPin。
func PinnedDialAddr(ctx context.Context, address string) string {
	dst, ok := ctx.Value(originalDstKey{}).(originalDst)
	if !ok || dst.authority == "" || !strings.EqualFold(dst.authority, address) {
		return address
	}
	return dst.addr
}

// WithoutDialPin 返回的 ctx 仍标注请求来自透明连接，但拨任何地址都按原样拨。用于经上游代理
// 发出的请求：这一跳连的是代理，代理地址与请求的 authority 字面相同时也不能换成原始地址，
// 否则应用把 Host 写成代理地址，就能让带着代理凭据的请求发到它自己连的那个地址。
func WithoutDialPin(ctx context.Context) context.Context {
	dst, ok := ctx.Value(originalDstKey{}).(originalDst)
	if !ok || dst.authority == "" {
		return ctx
	}
	return context.WithValue(ctx, originalDstKey{}, originalDst{addr: dst.addr})
}
