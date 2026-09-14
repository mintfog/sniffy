#!/usr/bin/env bash
# Copyright 2026 The mintfog Authors
# SPDX-License-Identifier: Apache-2.0

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TEST_DIR="$(mktemp -d -t 'sniffy nsis tests-XXXXXX')"
trap 'rm -rf "$TEST_DIR"' EXIT

mkdir -p "$TEST_DIR/build output"
printf 'Sniffy NSIS input fixture\n' > "$TEST_DIR/build output/Sniffy.exe"
cd "$TEST_DIR"

# 使用真实编译器验证模板中的源文件路径，以及调用目录和空格的处理。
bash "$ROOT/scripts/make-nsis-installer.sh" \
  --binary 'build output/Sniffy.exe' \
  --version '2.0.0-beta.1' \
  --out 'install output/Sniffy-setup.exe'

test -s 'install output/Sniffy-setup.exe'
test "$(cat 'build output/Sniffy.exe')" = 'Sniffy NSIS input fixture'
echo 'NSIS 安装包测试通过'
