#!/usr/bin/env bash
# Copyright 2026 The mintfog Authors
# SPDX-License-Identifier: Apache-2.0
#
# 将已构建的桌面二进制打包为 Windows NSIS、macOS DMG 或 Linux DEB。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TARGET_OS=""
ARCH=""
BINARY=""
VERSION=""
OUT=""

usage() {
  echo "用法: $0 --os <windows|darwin|linux> --arch <amd64|arm64> --binary <path> --version <ver> --out <path>"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --os|--arch|--binary|--version|--out)
      if [[ $# -lt 2 || -z "$2" || "$2" == --* ]]; then
        echo "错误: $1 需要参数值" >&2
        exit 1
      fi
      case "$1" in
        --os) TARGET_OS="$2" ;;
        --arch) ARCH="$2" ;;
        --binary) BINARY="$2" ;;
        --version) VERSION="${2#v}" ;;
        --out) OUT="$2" ;;
      esac
      shift 2
      ;;
    -h|--help) usage; exit 0 ;;
    *) echo "错误: 未知参数 $1" >&2; usage >&2; exit 1 ;;
  esac
done

if [[ -z "$TARGET_OS" || -z "$ARCH" || -z "$BINARY" || -z "$VERSION" || -z "$OUT" ]]; then
  echo "错误: --os、--arch、--binary、--version、--out 必填" >&2
  usage >&2
  exit 1
fi

case "$ARCH" in
  amd64|arm64) ;;
  *) echo "错误: 不支持的架构 $ARCH" >&2; exit 1 ;;
esac

case "$TARGET_OS" in
  windows)
    exec bash "$ROOT/scripts/make-nsis-installer.sh" \
      --binary "$BINARY" --version "$VERSION" --out "$OUT"
    ;;
  darwin)
    exec bash "$ROOT/scripts/make-dmg-installer.sh" \
      --binary "$BINARY" --version "$VERSION" --arch "$ARCH" --out "$OUT"
    ;;
  linux)
    exec bash "$ROOT/scripts/make-deb-installer.sh" \
      --binary "$BINARY" --version "$VERSION" --arch "$ARCH" --out "$OUT"
    ;;
  *) echo "错误: 不支持的平台 $TARGET_OS" >&2; exit 1 ;;
esac
