// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package types

import (
	"context"
	"testing"
)

func TestPinnedDialAddr(t *testing.T) {
	const dst = "192.0.2.1:443"
	transparent := WithOriginalDst(context.Background(), "example.com:443", dst)

	for _, tc := range []struct {
		name    string
		ctx     context.Context
		address string
		want    string
	}{
		{name: "拨的就是原 authority", ctx: transparent, address: "example.com:443", want: dst},
		{name: "主机名大小写不敏感", ctx: transparent, address: "Example.COM:443", want: dst},
		{name: "拨上游代理", ctx: transparent, address: "127.0.0.1:7890", want: "127.0.0.1:7890"},
		{name: "目标被改写", ctx: transparent, address: "other.test:443", want: "other.test:443"},
		{name: "端口不同", ctx: transparent, address: "example.com:8443", want: "example.com:8443"},
		{name: "代理入站的请求", ctx: context.Background(), address: "example.com:443", want: "example.com:443"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := PinnedDialAddr(tc.ctx, tc.address); got != tc.want {
				t.Fatalf("PinnedDialAddr(%q) = %q，期望 %q", tc.address, got, tc.want)
			}
		})
	}

	if !IsTransparent(transparent) || IsTransparent(context.Background()) {
		t.Fatal("IsTransparent 应只对标注过原始目标的 ctx 为真")
	}
}

// TestWithoutDialPin：经上游代理发出的请求拨的是代理，代理地址与请求的 authority 相同时
// 也连代理本身。
func TestWithoutDialPin(t *testing.T) {
	const authority = "proxy.test:3128"
	transparent := WithOriginalDst(context.Background(), authority, "192.0.2.1:80")

	viaProxy := WithoutDialPin(transparent)
	if got := PinnedDialAddr(viaProxy, authority); got != authority {
		t.Fatalf("PinnedDialAddr(%q) = %q，期望原样返回", authority, got)
	}
	if !IsTransparent(viaProxy) {
		t.Fatal("WithoutDialPin 之后请求仍应标注为来自透明连接")
	}
	if plain := context.Background(); WithoutDialPin(plain) != plain {
		t.Fatal("代理入站的请求应原样返回 ctx")
	}
}
