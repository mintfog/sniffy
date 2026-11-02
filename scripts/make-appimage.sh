#!/usr/bin/env bash
# Copyright 2026 The mintfog Authors
# SPDX-License-Identifier: Apache-2.0
#
# 依赖与 go.mod 相同版本的 wails3、GTK 4 / WebKitGTK 6.0 开发包及 linuxdeploy 运行工具。
# Wails 生成器会下载 linuxdeploy，打包须在目标架构的 Linux 主机执行。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BINARY=""
VERSION=""
ARCH=""
OUT=""

fail() {
  printf '错误: %s\n' "$*" >&2
  exit 1
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --binary|--version|--arch|--out)
      [[ $# -ge 2 && -n "$2" && "$2" != -* ]] || fail "$1 缺少参数值"
      case "$1" in
        --binary) BINARY="$2" ;;
        --version) VERSION="${2#v}" ;;
        --arch) ARCH="$2" ;;
        --out) OUT="$2" ;;
      esac
      shift 2
      ;;
    -h|--help)
      printf '用法: %s --binary <exe> --version <ver> --arch <amd64|arm64> --out <AppImage>\n' "$0"
      exit 0
      ;;
    *) fail "未知参数: $1" ;;
  esac
done

[[ -n "$BINARY" && -n "$VERSION" && -n "$ARCH" && -n "$OUT" ]] || fail "--binary、--version、--arch、--out 必填"
[[ "$(uname -s)" == Linux ]] || fail "AppImage 需要在 Linux 主机打包"
case "$ARCH:$(uname -m)" in
  amd64:x86_64) APPIMAGE_ARCH=x86_64; ELF_MACHINE='Advanced Micro Devices X86-64' ;;
  arm64:aarch64) APPIMAGE_ARCH=aarch64; ELF_MACHINE=AArch64 ;;
  *) fail "--arch 必须为与打包主机一致的 amd64 或 arm64" ;;
esac
for tool in wails3 readelf realpath; do
  command -v "$tool" >/dev/null 2>&1 || fail "未找到 $tool"
done
[[ -f "$BINARY" && -r "$BINARY" ]] || fail "找不到可读取的二进制 $BINARY"
BINARY="$(realpath -- "$BINARY")"
OUT="$(realpath -m -- "$OUT")"
[[ "$BINARY" != "$OUT" ]] || fail "--out 不能覆盖输入二进制"
[[ ! -d "$OUT" ]] || fail "--out 必须指向文件"
ELF_HEADER="$(LC_ALL=C readelf --file-header -- "$BINARY")"
grep -Eq 'Class:[[:space:]]+ELF64$' <<< "$ELF_HEADER" || fail "输入二进制必须为 ELF64"
grep -Eq "Data:[[:space:]]+2's complement, little endian$" <<< "$ELF_HEADER" || fail "输入二进制必须使用小端字节序"
grep -Eq "Machine:[[:space:]]+$ELF_MACHINE$" <<< "$ELF_HEADER" || fail "输入二进制架构与 --arch $ARCH 不匹配"

TEMP_DIR="$(mktemp -d -t sniffy-appimage.XXXXXX)"
PUBLISH_TEMP=""
cleanup() {
  rm -rf -- "$TEMP_DIR"
  [[ -z "$PUBLISH_TEMP" ]] || rm -f -- "$PUBLISH_TEMP"
}
trap cleanup EXIT
install -m 0755 "$BINARY" "$TEMP_DIR/sniffy"
install -m 0644 "$ROOT/build/appicon.png" "$TEMP_DIR/sniffy.png"
cat > "$TEMP_DIR/sniffy.desktop" <<'DESKTOP_EOF'
[Desktop Entry]
Type=Application
Name=Sniffy
Exec=sniffy
Icon=sniffy
Terminal=false
Categories=Development;Network;
StartupNotify=true
DESKTOP_EOF

# linuxdeploy 的 AppImage 输出插件也需要解包运行，以兼容未挂载 FUSE 的构建环境。
APPIMAGE_EXTRACT_AND_RUN=1 VERSION="$VERSION" wails3 generate appimage \
  -binary "$TEMP_DIR/sniffy" -icon "$TEMP_DIR/sniffy.png" \
  -desktopfile "$TEMP_DIR/sniffy.desktop" \
  -builddir "$TEMP_DIR/build" -outputdir "$TEMP_DIR"
PACKAGE="$TEMP_DIR/sniffy-${APPIMAGE_ARCH}.AppImage"
[[ -s "$PACKAGE" ]] || fail "AppImage 未生成"

OUT_DIR="$(dirname "$OUT")"
mkdir -p "$OUT_DIR"
PUBLISH_TEMP="$(mktemp "$OUT_DIR/.sniffy-appimage.XXXXXX")"
install -m 0755 "$PACKAGE" "$PUBLISH_TEMP"
mv -f -- "$PUBLISH_TEMP" "$OUT"
PUBLISH_TEMP=""
printf '>> 完成: %s\n' "$OUT"
