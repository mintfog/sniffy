#!/usr/bin/env bash
# Copyright 2026 The mintfog Authors
# SPDX-License-Identifier: Apache-2.0
#
# 通过工具替身验证参数、打包布局和失败路径；真实 DEB 需在 Linux 上验证。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SCRIPT="$ROOT/scripts/make-deb-installer.sh"
TEST_DIR="$(mktemp -d -t sniffy-deb-test.XXXXXX)"
trap 'rm -rf -- "$TEST_DIR"' EXIT
install -d "$TEST_DIR/tools" "$TEST_DIR/caller with spaces/input" "$TEST_DIR/package"

cat > "$TEST_DIR/tool-mock" <<'MOCK_EOF'
#!/usr/bin/env bash
set -euo pipefail
case "$(basename "$0")" in
  dpkg)
    [[ "$1" == --validate-version && $# == 2 ]]
    ;;
  readelf)
    printf '  Class: %s\n' "${MOCK_ELF_CLASS:-ELF64}"
    printf '  Data: %s\n' "${MOCK_ELF_DATA:-"2's complement, little endian"}"
    case "${MOCK_ELF_ARCH:-amd64}" in
      amd64) printf '  Machine: Advanced Micro Devices X86-64\n' ;;
      arm64) printf '  Machine: AArch64\n' ;;
    esac
    ;;
  dpkg-shlibdeps)
    [[ -f debian/control && "$1" == -O && "$2" == -e* ]]
    [[ "$DEB_HOST_ARCH" == "${MOCK_ELF_ARCH:-amd64}" ]]
    [[ -f "${2#-e}" ]]
    [[ "${MOCK_SHLIBS_EXIT:-0}" == 0 ]] || exit "$MOCK_SHLIBS_EXIT"
    printf 'shlibs:Depends=%s\n' "${MOCK_SHLIBS_DEPENDS:-libc6 (>= 2.34), libwebkitgtk-6.0-4 (>= 2.42.0)}"
    ;;
  magick|convert)
    [[ -f "$1" && "$2" == -resize && ( "$3" == 256x256 || "$3" == 512x512 ) ]]
    [[ "${MOCK_CONVERT_EXIT:-0}" == 0 ]] || exit "$MOCK_CONVERT_EXIT"
    cp "$1" "$4"
    ;;
  dpkg-deb)
    [[ "$1" == --root-owner-group && "$2" == --build && $# == 4 ]]
    [[ "${MOCK_DEB_EXIT:-0}" == 0 ]] || exit "$MOCK_DEB_EXIT"
    cp -R "$3/." "$MOCK_PACKAGE/"
    printf 'mock-deb\n' > "$4"
    ;;
esac
MOCK_EOF
chmod 0755 "$TEST_DIR/tool-mock"
for tool in dpkg dpkg-shlibdeps readelf magick convert dpkg-deb; do
  cp "$TEST_DIR/tool-mock" "$TEST_DIR/tools/$tool"
done
export PATH="$TEST_DIR/tools:$PATH"
export MOCK_PACKAGE="$TEST_DIR/package"
cd "$TEST_DIR/caller with spaces"
printf '#!/bin/sh\nexit 0\n' > 'input/sniffy desktop'
cp 'input/sniffy desktop' "$TEST_DIR/original"

assert_line() {
  grep -F -x -- "$2" "$1" >/dev/null || {
    printf '测试失败: %s 缺少 %s\n' "$1" "$2" >&2
    exit 1
  }
}

assert_fails() {
  if bash "$SCRIPT" "$@" > "$TEST_DIR/error.log" 2>&1; then
    printf '测试失败: 命令应退出失败: %s\n' "$*" >&2
    exit 1
  fi
}

ARGS=(--binary 'input/sniffy desktop' --version v2.0.0-beta.1+build.7 --arch amd64 --out 'output/sniffy package.deb')
bash "$SCRIPT" "${ARGS[@]}"
[[ -f 'output/sniffy package.deb' && -x "$MOCK_PACKAGE/usr/bin/sniffy" ]]
cmp 'input/sniffy desktop' "$TEST_DIR/original"
cmp 'input/sniffy desktop' "$MOCK_PACKAGE/usr/bin/sniffy"
cmp "$ROOT/LICENSE" "$MOCK_PACKAGE/usr/share/doc/sniffy/LICENSE"
CONTROL="$MOCK_PACKAGE/DEBIAN/control"
assert_line "$CONTROL" 'Package: sniffy'
assert_line "$CONTROL" 'Version: 2.0.0~beta.1+build.7'
assert_line "$CONTROL" 'Architecture: amd64'
assert_line "$CONTROL" 'Depends: libc6 (>= 2.34), libwebkitgtk-6.0-4 (>= 2.42.0), libgtk-4-1'
assert_line "$MOCK_PACKAGE/usr/share/applications/sniffy.desktop" 'Exec=/usr/bin/sniffy'
assert_line "$MOCK_PACKAGE/usr/share/applications/sniffy.desktop" 'Icon=sniffy'
assert_line "$MOCK_PACKAGE/usr/share/applications/sniffy.desktop" 'Terminal=false'
assert_line "$MOCK_PACKAGE/usr/share/doc/sniffy/copyright" 'License: Apache-2.0'
[[ -f "$MOCK_PACKAGE/usr/share/icons/hicolor/256x256/apps/sniffy.png" ]]
[[ -f "$MOCK_PACKAGE/usr/share/icons/hicolor/512x512/apps/sniffy.png" ]]

MOCK_ELF_ARCH=arm64 bash "$SCRIPT" --binary 'input/sniffy desktop' \
  --version abcdef0123456789 --arch arm64 --out 'output/sha.deb'
assert_line "$CONTROL" 'Version: 0.0.0+abcdef0123456789'
assert_line "$CONTROL" 'Architecture: arm64'

MOCK_SHLIBS_DEPENDS='libgtk-4-1 (>= 4.12), libwebkitgtk-6.0-4 (>= 2.42)' \
  bash "$SCRIPT" --binary 'input/sniffy desktop' --version 1.2.3-beta-test --arch amd64 --out 'output/prerelease.deb'
assert_line "$CONTROL" 'Version: 1.2.3~beta-test-1'
assert_line "$CONTROL" 'Depends: libgtk-4-1 (>= 4.12), libwebkitgtk-6.0-4 (>= 2.42)'

assert_fails --binary
assert_fails --unknown value
assert_fails --version 1.2.3 --arch amd64 --out output/missing.deb
assert_fails --binary 'input/sniffy desktop' --arch amd64 --out output/missing.deb
assert_fails --binary 'input/sniffy desktop' --version 1.2.3 --arch amd64
assert_fails "${ARGS[@]}" --binary missing
assert_fails "${ARGS[@]}" --version 1.2
assert_fails "${ARGS[@]}" --version 1.2.3-01
assert_fails "${ARGS[@]}" --version 01.2.3
assert_fails "${ARGS[@]}" --arch ppc64
assert_fails "${ARGS[@]}" --arch arm64
assert_fails "${ARGS[@]}" --out 'input/sniffy desktop'
MOCK_ELF_CLASS=ELF32 assert_fails "${ARGS[@]}"
MOCK_ELF_DATA="2's complement, big endian" assert_fails "${ARGS[@]}"

cp 'output/sniffy package.deb' "$TEST_DIR/previous.deb"
MOCK_SHLIBS_EXIT=7 assert_fails "${ARGS[@]}"
cmp 'output/sniffy package.deb' "$TEST_DIR/previous.deb"
MOCK_CONVERT_EXIT=8 assert_fails "${ARGS[@]}"
cmp 'output/sniffy package.deb' "$TEST_DIR/previous.deb"
MOCK_DEB_EXIT=9 assert_fails "${ARGS[@]}"
cmp 'output/sniffy package.deb' "$TEST_DIR/previous.deb"
cmp 'input/sniffy desktop' "$TEST_DIR/original"
shopt -s nullglob
temporary_packages=(output/.sniffy-deb.*)
[[ ${#temporary_packages[@]} == 0 ]]

printf 'DEB 打包脚本测试通过。\n'
