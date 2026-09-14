#!/usr/bin/env bash
# Copyright 2026 The mintfog Authors
# SPDX-License-Identifier: Apache-2.0

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
SCRIPT="$ROOT/scripts/make-dmg-installer.sh"
TEST_DIR="$(mktemp -d "${TMPDIR:-/tmp}/sniffy-dmg-tests.XXXXXX")"
trap 'rm -rf -- "$TEST_DIR"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

fail() {
  printf '测试失败: %s\n' "$1" >&2
  exit 1
}

expect_failure() {
  if "$@" > "$TEST_DIR/failure.log" 2>&1; then
    fail "命令应失败: $*"
  fi
}

assert_line() {
  grep -Fqx -- "$1" "$2" || fail "缺少预期内容: $1"
}

assert_clean() {
  local path
  for path in "$1"/.sniffy-dmg.*; do
    [ ! -e "$path" ] || fail "临时目录未清理: $path"
  done
}

MOCK_BIN="$TEST_DIR/mock tools"
mkdir -p "$MOCK_BIN"
export DMG_TEST_LOG="$TEST_DIR/tools.log"
export DMG_TEST_CAPTURE="$TEST_DIR/capture-semver"
export DMG_TEST_CHMOD="$(command -v chmod)"
export DMG_TEST_LN="$(command -v ln)"
export DMG_TEST_READLINK="$(command -v readlink)"
export PATH="$MOCK_BIN:$PATH"

# 原生工具只模拟参数契约与失败；真实签名、图标和镜像由 macOS 构建验证。
printf '%s\n' '#!/usr/bin/env bash' 'set -euo pipefail' '
printf "%s:%s\n" "${0##*/}" "$*" >> "$DMG_TEST_LOG"
case "${0##*/}" in
  uname) printf "%s\n" "${DMG_TEST_OS:-Darwin}" ;;
  chmod) "$DMG_TEST_CHMOD" "$@" ;;
  ln)
    [ "$1" = -s ] && [ "$2" = /Applications ]
    case "$OSTYPE" in
      msys*) printf "%s\n" "$2" > "$3" ;;
      *) "$DMG_TEST_LN" "$@" ;;
    esac
    ;;
  readlink)
    case "$OSTYPE" in
      msys*) cat "$1" ;;
      *) "$DMG_TEST_READLINK" "$@" ;;
    esac
    ;;
  sips)
    [ "$1" = -z ] && [ "$5" = --out ]
    cp "$4" "$6"
    ;;
  iconutil)
    [ "$1" = -c ] && [ "$2" = icns ] && [ "$4" = -o ]
    cp "$3/icon_16x16.png" "$5"
    ;;
  plutil)
    case "$1" in
      -create) [ "$2" = xml1 ]; printf "属性列表参数\n" > "$3" ;;
      -insert) [ "$#" -eq 5 ]; printf "%s %s %s\n" "$2" "$3" "$4" >> "$5" ;;
      -lint) [ -s "$2" ] ;;
      *) exit 90 ;;
    esac
    ;;
  codesign)
    app="${!#}"
    case "$OSTYPE" in
      msys*) ;;
      *) [ -x "$app/Contents/MacOS/Sniffy" ] ;;
    esac
    [ -s "$app/Contents/Resources/appicon.icns" ]
    if [ "$1" = --force ] && [ "${DMG_TEST_FAIL:-}" = signature ]; then exit 92; fi
    ;;
  hdiutil)
    case "$1" in
      create)
        [ "$2" = -volname ] && [ "$3" = Sniffy ] && [ "$4" = -srcfolder ]
        [ "$6" = -fs ] && [ "$7" = HFS+ ] && [ "$8" = -format ] && [ "$9" = UDZO ]
        mkdir -p "$DMG_TEST_CAPTURE/contents"
        cp -R "$5/." "$DMG_TEST_CAPTURE/contents/"
        printf "模拟 DMG\n" > "${!#}"
        ;;
      verify)
        [ -s "$2" ]
        [ "${DMG_TEST_FAIL:-}" != image ] || exit 91
        ;;
      *) exit 90 ;;
    esac
    ;;
  *) exit 90 ;;
esac
' > "$MOCK_BIN/native-tool"
chmod 755 "$MOCK_BIN/native-tool"
for tool in uname chmod ln readlink sips iconutil plutil codesign hdiutil; do
  cp "$MOCK_BIN/native-tool" "$MOCK_BIN/$tool"
done

bash "$SCRIPT" --help > "$TEST_DIR/help.log"
expect_failure bash "$SCRIPT" --binary
expect_failure bash "$SCRIPT" --binary --version 1.2.3 --out output.dmg
expect_failure bash "$SCRIPT" --binary missing --out output.dmg
expect_failure bash "$SCRIPT" --binary missing --version 1.2.3
expect_failure bash "$SCRIPT" --binary missing --version 1.2.3 --arch x86 --out output.dmg

CALLER_DIR="$TEST_DIR/caller with spaces"
mkdir -p "$CALLER_DIR/input files"
BINARY="$CALLER_DIR/input files/Sniffy input"
printf '原始桌面二进制\n' > "$BINARY"
chmod 644 "$BINARY"
cp "$BINARY" "$TEST_DIR/original-binary"
if ! (
  cd "$CALLER_DIR"
  bash "$SCRIPT" --binary 'input files/Sniffy input' \
    --version 'v1.2.3-beta.1+build.42' --arch arm64 --out 'output files/install image.dmg'
); then
  cat "$DMG_TEST_LOG" >&2
  fail '带空格路径的包装失败'
fi
CONTENTS="$DMG_TEST_CAPTURE/contents"
PLIST="$CONTENTS/Sniffy.app/Contents/Info.plist"
assert_line 'CFBundleIdentifier -string com.mintfog.sniffy' "$PLIST"
assert_line 'CFBundleExecutable -string Sniffy' "$PLIST"
assert_line 'CFBundleIconFile -string appicon.icns' "$PLIST"
assert_line 'LSMinimumSystemVersion -string 12.0.0' "$PLIST"
assert_line 'CFBundleShortVersionString -string 1.2.3' "$PLIST"
assert_line 'CFBundleVersion -string 1.2.3' "$PLIST"
assert_line 'SniffyBuildVersion -string v1.2.3-beta.1+build.42' "$PLIST"
assert_line 'SniffyBuildArchitecture -string arm64' "$PLIST"
[ "$(readlink "$CONTENTS/Applications")" = /Applications ] || fail 'Applications 链接不正确'
cmp "$BINARY" "$CONTENTS/Sniffy.app/Contents/MacOS/Sniffy" || fail '应用包二进制内容不一致'
cmp "$ROOT/LICENSE" "$CONTENTS/Sniffy.app/Contents/Resources/LICENSE" || fail '许可证内容不一致'
[ -s "$CALLER_DIR/output files/install image.dmg" ] || fail '相对路径的镜像未生成'
[ "$(grep -c '^sips:' "$DMG_TEST_LOG")" -eq 10 ] || fail '图标尺寸数量不正确'
grep -F 'chmod:755 ' "$DMG_TEST_LOG" >/dev/null || fail '未设置应用可执行权限'
grep -F 'codesign:--force --sign - --timestamp=none ' "$DMG_TEST_LOG" >/dev/null || fail '未执行 ad-hoc 签名'
grep -F 'codesign:--verify --strict --verbose=2 ' "$DMG_TEST_LOG" >/dev/null || fail '未校验应用签名'
grep -F 'hdiutil:verify ' "$DMG_TEST_LOG" >/dev/null || fail '未校验镜像'
assert_clean "$CALLER_DIR/output files"

export DMG_TEST_CAPTURE="$TEST_DIR/capture-sha"
bash "$SCRIPT" --binary "$BINARY" --version abcdef0123456789 --arch amd64 --out "$CALLER_DIR/sha.dmg"
PLIST="$DMG_TEST_CAPTURE/contents/Sniffy.app/Contents/Info.plist"
assert_line 'CFBundleShortVersionString -string 0.0.0' "$PLIST"
assert_line 'CFBundleVersion -string 0.0.0' "$PLIST"
assert_line 'SniffyBuildVersion -string abcdef0123456789' "$PLIST"
assert_line 'SniffyBuildArchitecture -string amd64' "$PLIST"

export DMG_TEST_CAPTURE="$TEST_DIR/capture-default"
bash "$SCRIPT" --binary "$BINARY" --version 1.0.0 --out "$CALLER_DIR/default.dmg"
assert_line 'CFBundleVersion -string 1.0.0' "$DMG_TEST_CAPTURE/contents/Sniffy.app/Contents/Info.plist"

printf '已有镜像\n' > "$CALLER_DIR/existing.dmg"
expect_failure env DMG_TEST_FAIL=image bash "$SCRIPT" --binary "$BINARY" --version 1.2.3 --out "$CALLER_DIR/existing.dmg"
assert_line '已有镜像' "$CALLER_DIR/existing.dmg"
expect_failure env DMG_TEST_FAIL=signature bash "$SCRIPT" --binary "$BINARY" --version 1.2.3 --out "$CALLER_DIR/existing.dmg"
assert_line '已有镜像' "$CALLER_DIR/existing.dmg"
expect_failure env DMG_TEST_OS=Linux bash "$SCRIPT" --binary "$BINARY" --version 1.2.3 --out "$CALLER_DIR/existing.dmg"

cp "$BINARY" "$CALLER_DIR/input files/original.dmg"
expect_failure bash "$SCRIPT" --binary "$CALLER_DIR/input files/original.dmg" --version 1.2.3 --out "$CALLER_DIR/input files/original.dmg"
cmp "$TEST_DIR/original-binary" "$CALLER_DIR/input files/original.dmg" || fail '原始二进制被覆盖'
cmp "$TEST_DIR/original-binary" "$BINARY" || fail '原始二进制被修改'
assert_clean "$CALLER_DIR"

printf 'DMG 包装脚本模拟测试通过\n'
