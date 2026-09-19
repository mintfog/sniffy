#!/usr/bin/env bash
# Copyright 2025 The mintfog Authors
# SPDX-License-Identifier: Apache-2.0
#
# sniffy 构建脚本。
#   build.sh headless [os/arch ...]   交叉编译 headless 服务器二进制(纯 Go,无 cgo)
#   build.sh frontend                 构建前端(web -> web/dist)
#   build.sh desktop                  构建桌面二进制(需 -tags desktop + 各平台 webview 依赖)
#
# 桌面制品使用 Wails production 模式；开发调试使用 task dev。
#
# 版本号来源:环境变量 VERSION > git describe > 0.0.0-dev。
# 显式传入 VERSION 时裁剪调试符号；自动推导版本时保留调试符号。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

cmd="${1:-headless}"
shift || true

LDFLAGS_BASE=""
if [ -n "${VERSION:-}" ]; then
  LDFLAGS_BASE="-s -w"
else
  VERSION="$(git describe --tags --always --dirty 2>/dev/null || echo "")"
fi
if [ -z "$VERSION" ]; then
  VERSION="0.0.0-dev"
fi

LDFLAGS_BASE+=" -X github.com/mintfog/sniffy/internal/version.Version=${VERSION}"
echo ">> 版本: ${VERSION}"

build_frontend() {
  echo ">> 构建前端 (web)"
  (cd web && npm install && npm run build)
}

case "$cmd" in
  headless)
    targets=("$@")
    if [ "${#targets[@]}" -eq 0 ]; then
      targets=("$(go env GOOS)/$(go env GOARCH)")
    fi
    mkdir -p dist
    for t in "${targets[@]}"; do
      os="${t%%/*}"; arch="${t##*/}"
      out="dist/sniffy-${os}-${arch}"
      [ "$os" = "windows" ] && out="${out}.exe"
      echo ">> 编译 headless ${os}/${arch} -> ${out}"
      CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath \
        -ldflags "$LDFLAGS_BASE" -o "$out" ./cmd/sniffy
    done
    ;;

  frontend)
    build_frontend
    ;;

  desktop)
    build_frontend
    echo ">> 拷贝前端到桌面 embed 目录"
    rm -rf cmd/sniffy-desktop/dist
    mkdir -p cmd/sniffy-desktop/dist
    cp -r web/dist/* cmd/sniffy-desktop/dist/
    echo ">> 编译桌面二进制 (Wails v3, -tags desktop)"
    mkdir -p dist
    os="$(go env GOOS)"
    suffix=""
    desktop_ldflags="$LDFLAGS_BASE"
    # macOS/Linux 通过 CGO 调用系统 WebView；Windows 使用纯 Go 后端。
    cgo=1
    if [ "$os" = "windows" ]; then
      suffix=".exe"
      desktop_ldflags+=" -H windowsgui"
      cgo=0
      bash scripts/generate-windows-resources.sh "$(go env GOARCH)"
    fi
    CGO_ENABLED="$cgo" go build -tags desktop,production -trimpath \
      -ldflags "$desktop_ldflags" -o "dist/sniffy-desktop${suffix}" ./cmd/sniffy-desktop
    echo ">> 完成。"
    ;;

  *)
    echo "用法: build.sh [headless [os/arch ...] | frontend | desktop]"
    exit 1
    ;;
esac

echo ">> 构建完成。"
