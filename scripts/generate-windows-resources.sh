#!/usr/bin/env bash
# Copyright 2026 The mintfog Authors
# SPDX-License-Identifier: Apache-2.0

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ARCH="${1:-$(go env GOARCH)}"
case "$ARCH" in
  amd64|arm64) ;;
  *)
    echo "错误: 不支持的 Windows 桌面架构: $ARCH (仅支持 amd64, arm64)" >&2
    exit 1
    ;;
esac
cd "$ROOT"

# go run 须使用构建机架构；--arch 指定生成资源的目标架构。
GOOS="$(go env GOHOSTOS)" GOARCH="$(go env GOHOSTARCH)" \
  go run github.com/tc-hib/go-winres@v0.3.3 make \
    --in build/windows/winres.json \
    --out cmd/sniffy-desktop/rsrc \
    --arch "$ARCH"
