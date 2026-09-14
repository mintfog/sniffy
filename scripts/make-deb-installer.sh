#!/usr/bin/env bash
# Copyright 2026 The mintfog Authors
# SPDX-License-Identifier: Apache-2.0
#
# 用已编译的 Linux 桌面二进制生成 DEB 安装包。
# 用法: bash scripts/make-deb-installer.sh --binary <exe> --version <semver|SHA> \
#         --arch <amd64|arm64> --out <deb>
# 依赖: dpkg-dev、binutils、ImageMagick；打包主机须具备目标架构的运行库及 shlibs 信息。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CALLER_PWD="$PWD"
BINARY=""
VERSION=""
ARCH=""
OUT=""

fail() {
  printf '错误: %s\n' "$*" >&2
  exit 1
}

require_value() {
  [[ $# -ge 2 && -n "$2" && "$2" != -* ]] || fail "$1 缺少参数值"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --binary) require_value "$@"; BINARY="$2"; shift 2 ;;
    --version) require_value "$@"; VERSION="$2"; shift 2 ;;
    --arch) require_value "$@"; ARCH="$2"; shift 2 ;;
    --out) require_value "$@"; OUT="$2"; shift 2 ;;
    -h|--help)
      printf '用法: %s --binary <exe> --version <semver|SHA> --arch <amd64|arm64> --out <deb>\n' "$0"
      exit 0
      ;;
    *) fail "未知参数: $1" ;;
  esac
done

[[ -n "$BINARY" ]] || fail "--binary 必填"
[[ -n "$VERSION" ]] || fail "--version 必填"
[[ -n "$OUT" ]] || fail "--out 必填"
case "$ARCH" in
  amd64|arm64) ;;
  *) fail "--arch 必须为 amd64 或 arm64" ;;
esac

for tool in dpkg dpkg-deb dpkg-shlibdeps readelf realpath; do
  command -v "$tool" >/dev/null 2>&1 || fail "未找到 $tool，请安装 dpkg-dev、binutils 和 coreutils"
done
if command -v magick >/dev/null 2>&1; then
  IMAGE_CONVERT=(magick)
elif command -v convert >/dev/null 2>&1; then
  IMAGE_CONVERT=(convert)
else
  fail "未找到 ImageMagick，请安装 imagemagick"
fi

absolute_path() {
  case "$1" in
    /*) printf '%s\n' "$1" ;;
    *) printf '%s/%s\n' "$CALLER_PWD" "$1" ;;
  esac
}
BINARY="$(absolute_path "$BINARY")"
OUT="$(absolute_path "$OUT")"
[[ -f "$BINARY" && -r "$BINARY" ]] || fail "找不到可读取的二进制 $BINARY"
BINARY="$(realpath -- "$BINARY")"
OUT="$(realpath -m -- "$OUT")"
[[ "$BINARY" != "$OUT" ]] || fail "--out 不能覆盖输入二进制"
[[ ! -d "$OUT" ]] || fail "--out 必须指向文件"

VERSION="${VERSION#v}"
SEMVER='^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-([0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*))?(\+([0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*))?$'
if [[ "$VERSION" =~ $SEMVER ]]; then
  DEB_VERSION="${BASH_REMATCH[1]}.${BASH_REMATCH[2]}.${BASH_REMATCH[3]}"
  PRERELEASE="${BASH_REMATCH[5]}"
  BUILD_METADATA="${BASH_REMATCH[8]}"
  if [[ -n "$PRERELEASE" ]]; then
    IFS='.' read -r -a prerelease_parts <<< "$PRERELEASE"
    for part in "${prerelease_parts[@]}"; do
      [[ ! "$part" =~ ^0[0-9]+$ ]] || fail "SemVer 预发布数字标识不能含前导零"
    done
    DEB_VERSION+="~$PRERELEASE"
  fi
  [[ -z "$BUILD_METADATA" ]] || DEB_VERSION+="+$BUILD_METADATA"
  # Debian 将最后一个连字符解释为修订号，补固定修订号以保留 SemVer 标识。
  [[ "$DEB_VERSION" != *-* ]] || DEB_VERSION+="-1"
elif [[ "$VERSION" =~ ^[0-9a-fA-F]{7,40}$ ]]; then
  DEB_VERSION="0.0.0+$VERSION"
else
  fail "--version 必须为 SemVer 或 7 至 40 位提交 SHA"
fi
dpkg --validate-version "$DEB_VERSION"

ELF_HEADER="$(LC_ALL=C readelf --file-header -- "$BINARY")"
ELF_CLASS="$(awk '$1 == "Class:" { print $2 }' <<< "$ELF_HEADER")"
ELF_DATA="$(sed -n 's/^[[:space:]]*Data:[[:space:]]*//p' <<< "$ELF_HEADER")"
ELF_MACHINE="$(sed -n 's/^[[:space:]]*Machine:[[:space:]]*//p' <<< "$ELF_HEADER")"
[[ "$ELF_CLASS" == ELF64 ]] || fail "输入二进制必须为 ELF64"
[[ "$ELF_DATA" == "2's complement, little endian" ]] || fail "输入二进制必须使用小端字节序"
case "$ARCH:$ELF_MACHINE" in
  'amd64:Advanced Micro Devices X86-64'|'arm64:AArch64') ;;
  *) fail "输入二进制架构与 --arch $ARCH 不匹配 ($ELF_MACHINE)" ;;
esac
[[ -r "$ROOT/build/appicon.png" && -r "$ROOT/LICENSE" ]] || fail "缺少 build/appicon.png 或 LICENSE"

OUT_DIR="$(dirname "$OUT")"
mkdir -p "$OUT_DIR"
# 包内权限必须能由 chmod 设置，暂存使用 Linux 临时目录以兼容共享盘输出。
TEMP_DIR="$(mktemp -d -t sniffy-deb.XXXXXX)"
PUBLISH_TEMP=""
cleanup() {
  rm -rf -- "$TEMP_DIR"
  [[ -z "$PUBLISH_TEMP" ]] || rm -f -- "$PUBLISH_TEMP"
}
trap cleanup EXIT
PACKAGE_DIR="$TEMP_DIR/package"
install -d "$PACKAGE_DIR/DEBIAN" "$PACKAGE_DIR/usr/bin" \
  "$PACKAGE_DIR/usr/share/applications" "$PACKAGE_DIR/usr/share/doc/sniffy" \
  "$TEMP_DIR/debian"
install -m 0755 "$BINARY" "$PACKAGE_DIR/usr/bin/sniffy"
install -m 0644 "$ROOT/LICENSE" "$PACKAGE_DIR/usr/share/doc/sniffy/LICENSE"

cat > "$TEMP_DIR/debian/control" <<'CONTROL_EOF'
Source: sniffy
Section: net
Priority: optional
Maintainer: The mintfog Authors <mintfog@users.noreply.github.com>

Package: sniffy
Architecture: any
Description: Sniffy 抓包与代理桌面工具
 支持可脚本化的请求与响应处理。
CONTROL_EOF

# 使用目标二进制和主机库的 symbols/shlibs 信息记录最低 ABI 版本。
SHLIBS="$(cd "$TEMP_DIR" && DEB_HOST_ARCH="$ARCH" dpkg-shlibdeps -O -e"$PACKAGE_DIR/usr/bin/sniffy")"
DEPENDS=""
while IFS= read -r line; do
  case "$line" in
    shlibs:Depends=*) DEPENDS="${line#shlibs:Depends=}" ;;
  esac
done <<< "$SHLIBS"
for required in libgtk-4-1 libwebkitgtk-6.0-4; do
  found=false
  IFS=',' read -r -a dependency_parts <<< "$DEPENDS"
  for dependency in "${dependency_parts[@]}"; do
    read -r package_name _ <<< "$dependency"
    if [[ "$package_name" == "$required" && "$dependency" != *'|'* ]]; then
      found=true
      break
    fi
  done
  if [[ "$found" == false ]]; then
    DEPENDS+="${DEPENDS:+, }$required"
  fi
done

cat > "$PACKAGE_DIR/usr/share/applications/sniffy.desktop" <<'DESKTOP_EOF'
[Desktop Entry]
Type=Application
Name=Sniffy
Exec=/usr/bin/sniffy
TryExec=/usr/bin/sniffy
Icon=sniffy
Terminal=false
Categories=Development;Network;
StartupNotify=true
DESKTOP_EOF

for size in 256 512; do
  ICON_DIR="$PACKAGE_DIR/usr/share/icons/hicolor/${size}x${size}/apps"
  install -d "$ICON_DIR"
  "${IMAGE_CONVERT[@]}" "$ROOT/build/appicon.png" -resize "${size}x${size}" "$ICON_DIR/sniffy.png"
  chmod 0644 "$ICON_DIR/sniffy.png"
done

cat > "$PACKAGE_DIR/usr/share/doc/sniffy/copyright" <<'COPYRIGHT_EOF'
Format: https://www.debian.org/doc/packaging-manuals/copyright-format/1.0/
Upstream-Name: Sniffy
Source: https://github.com/mintfog/sniffy

Files: *
Copyright: 2026 The mintfog Authors
License: Apache-2.0
 Apache-2.0 完整文本位于 /usr/share/common-licenses/Apache-2.0。
 本软件包同时附带 /usr/share/doc/sniffy/LICENSE。
COPYRIGHT_EOF

INSTALLED_SIZE="$(du -sk "$PACKAGE_DIR/usr" | awk '{ print $1 }')"
cat > "$PACKAGE_DIR/DEBIAN/control" <<CONTROL_EOF
Package: sniffy
Version: $DEB_VERSION
Section: net
Priority: optional
Architecture: $ARCH
Maintainer: The mintfog Authors <mintfog@users.noreply.github.com>
Installed-Size: $INSTALLED_SIZE
Depends: $DEPENDS
Homepage: https://github.com/mintfog/sniffy
Description: Sniffy 抓包与代理桌面工具
 支持可脚本化的请求与响应处理。
CONTROL_EOF
chmod 0644 "$PACKAGE_DIR/DEBIAN/control" "$PACKAGE_DIR/usr/share/applications/sniffy.desktop" \
  "$PACKAGE_DIR/usr/share/doc/sniffy/copyright"

printf '>> 生成 DEB: %s (版本 %s，架构 %s)\n' "$OUT" "$DEB_VERSION" "$ARCH"
dpkg-deb --root-owner-group --build "$PACKAGE_DIR" "$TEMP_DIR/sniffy.deb"
[[ -f "$TEMP_DIR/sniffy.deb" ]] || fail "安装包未生成"
# 在输出文件系统内原子发布，复制失败时旧制品保持完整。
PUBLISH_TEMP="$(mktemp "$OUT_DIR/.sniffy-deb.XXXXXX")"
install -m 0644 "$TEMP_DIR/sniffy.deb" "$PUBLISH_TEMP"
mv -f -- "$PUBLISH_TEMP" "$OUT"
PUBLISH_TEMP=""
printf '>> 完成: %s\n' "$OUT"
