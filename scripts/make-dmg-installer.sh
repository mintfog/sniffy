#!/usr/bin/env bash
# Copyright 2026 The mintfog Authors
# SPDX-License-Identifier: Apache-2.0

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
CALLER_PWD="$(pwd -P)"
BINARY=""
VERSION=""
ARCH=""
OUT=""

fail() {
  printf '错误: %s\n' "$1" >&2
  exit 1
}

usage() {
  printf '用法: %s --binary <macOS 二进制> --version <版本或提交 SHA> [--arch amd64|arm64] --out <DMG 路径>\n' "$0"
}

require_value() {
  [ "$#" -ge 2 ] || fail "$1 缺少参数值"
  case "$2" in
    "" | --*) fail "$1 缺少参数值" ;;
  esac
}

absolute_path() {
  case "$1" in
    /*) printf '%s\n' "$1" ;;
    *) printf '%s/%s\n' "$CALLER_PWD" "$1" ;;
  esac
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --binary) require_value "$@"; BINARY="$2"; shift 2 ;;
    --version) require_value "$@"; VERSION="$2"; shift 2 ;;
    --arch) require_value "$@"; ARCH="$2"; shift 2 ;;
    --out) require_value "$@"; OUT="$2"; shift 2 ;;
    -h | --help) usage; exit 0 ;;
    *) fail "未知参数: $1" ;;
  esac
done

[ -n "$BINARY" ] || fail '--binary 必填，须指向已编译的 macOS 桌面二进制'
[ -n "$VERSION" ] || fail '--version 必填'
[ -n "$OUT" ] || fail '--out 必填'
case "$ARCH" in
  "" | amd64 | arm64) ;;
  *) fail '--arch 仅支持 amd64 或 arm64' ;;
esac
case "$OUT" in
  *.dmg) ;;
  *) fail '--out 须使用 .dmg 扩展名' ;;
esac

BINARY="$(absolute_path "$BINARY")"
OUT="$(absolute_path "$OUT")"
[ -f "$BINARY" ] || fail "找不到二进制: $BINARY"
[ -f "$ROOT/build/appicon.png" ] || fail '找不到应用图标: build/appicon.png'
[ -f "$ROOT/build/darwin/dmg-background.png" ] || fail '找不到安装引导背景: build/darwin/dmg-background.png'
[ -f "$ROOT/build/darwin/dmg-background@2x.png" ] || fail '找不到 Retina 安装引导背景: build/darwin/dmg-background@2x.png'
[ -f "$ROOT/LICENSE" ] || fail '找不到许可证: LICENSE'
[ "$(uname -s)" = Darwin ] || fail 'DMG 打包须在 macOS 上运行'
for tool in sips iconutil plutil codesign hdiutil; do
  command -v "$tool" >/dev/null 2>&1 || fail "找不到 macOS 打包工具: $tool"
done
command -v dmgbuild >/dev/null 2>&1 || fail '找不到 dmgbuild，请在 Python 虚拟环境中安装 build/darwin/dmg-requirements.txt'

mkdir -p "$(dirname "$OUT")"
OUT_DIR="$(cd "$(dirname "$OUT")" && pwd -P)"
OUT="$OUT_DIR/$(basename "$OUT")"
[ ! -d "$OUT" ] || fail "输出路径是目录: $OUT"
[ ! "$BINARY" -ef "$OUT" ] || fail '输出路径不能覆盖原始二进制'

# CFBundleVersion 的数字段最多为 4、2、2 位；完整构建版本另存为元数据。
BUNDLE_VERSION="0.0.0"
SEMVER_PATTERN='^v?(0|[1-9][0-9]{0,3})\.(0|[1-9][0-9]?)\.(0|[1-9][0-9]?)(-[0-9A-Za-z][0-9A-Za-z.-]*)?(\+[0-9A-Za-z][0-9A-Za-z.-]*)?$'
if [[ "$VERSION" =~ $SEMVER_PATTERN ]]; then
  BUNDLE_VERSION="${BASH_REMATCH[1]}.${BASH_REMATCH[2]}.${BASH_REMATCH[3]}"
fi

WORK_DIR="$(mktemp -d "$OUT_DIR/.sniffy-dmg.XXXXXX")"
trap 'rm -rf -- "$WORK_DIR"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

APP="$WORK_DIR/Sniffy.app"
RESOURCES="$APP/Contents/Resources"
ICONSET="$WORK_DIR/appicon.iconset"
INFO_PLIST="$APP/Contents/Info.plist"
IMAGE="$WORK_DIR/Sniffy.dmg"
mkdir -p "$APP/Contents/MacOS" "$RESOURCES" "$ICONSET"
cp "$BINARY" "$APP/Contents/MacOS/Sniffy"
chmod 755 "$APP/Contents/MacOS/Sniffy"
cp "$ROOT/LICENSE" "$RESOURCES/LICENSE"

for size in 16 32 128 256 512; do
  sips -z "$size" "$size" "$ROOT/build/appicon.png" \
    --out "$ICONSET/icon_${size}x${size}.png" >/dev/null
  double_size=$((size * 2))
  sips -z "$double_size" "$double_size" "$ROOT/build/appicon.png" \
    --out "$ICONSET/icon_${size}x${size}@2x.png" >/dev/null
done
iconutil -c icns "$ICONSET" -o "$RESOURCES/appicon.icns"

plutil -create xml1 "$INFO_PLIST"
plutil -insert CFBundleName -string Sniffy "$INFO_PLIST"
plutil -insert CFBundleDisplayName -string Sniffy "$INFO_PLIST"
plutil -insert CFBundleExecutable -string Sniffy "$INFO_PLIST"
plutil -insert CFBundleIdentifier -string com.mintfog.sniffy "$INFO_PLIST"
plutil -insert CFBundlePackageType -string APPL "$INFO_PLIST"
plutil -insert CFBundleInfoDictionaryVersion -string 6.0 "$INFO_PLIST"
plutil -insert CFBundleIconFile -string appicon.icns "$INFO_PLIST"
plutil -insert CFBundleShortVersionString -string "$BUNDLE_VERSION" "$INFO_PLIST"
plutil -insert CFBundleVersion -string "$BUNDLE_VERSION" "$INFO_PLIST"
plutil -insert NSHighResolutionCapable -bool true "$INFO_PLIST"
plutil -insert LSMinimumSystemVersion -string 12.0.0 "$INFO_PLIST"
plutil -insert LSApplicationCategoryType -string public.app-category.developer-tools "$INFO_PLIST"
plutil -insert SniffyBuildVersion -string "$VERSION" "$INFO_PLIST"
if [ -n "$ARCH" ]; then
  plutil -insert SniffyBuildArchitecture -string "$ARCH" "$INFO_PLIST"
fi
plutil -lint "$INFO_PLIST"
printf 'APPL????' > "$APP/Contents/PkgInfo"

codesign --force --sign - --timestamp=none "$APP"
codesign --verify --strict --verbose=2 "$APP"
# dmgbuild 直接写入 Finder 布局，供 CI 在后台完成打包。
dmgbuild -s "$ROOT/build/darwin/dmg-settings.py" \
  -D "app=$APP" -D "background=$ROOT/build/darwin/dmg-background.png" \
  'Sniffy Installer' "$IMAGE"
[ -s "$IMAGE" ] || fail 'DMG 文件未生成或为空'
hdiutil verify "$IMAGE"
mv -f "$IMAGE" "$OUT"

printf '>> 已生成 macOS %s 安装镜像: %s (构建版本 %s，应用版本 %s)\n' \
  "${ARCH:-未指定架构}" "$OUT" "$VERSION" "$BUNDLE_VERSION"
