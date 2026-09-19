#!/usr/bin/env bash
# Copyright 2026 The mintfog Authors
# SPDX-License-Identifier: Apache-2.0
#
# 从一批发布产物生成官网的发布清单(site/src/data/release.json)。
#
#   scripts/release-manifest.sh --version v1.2.3 [--dir dist] [--out <path>]
#                               [--base <url>] [--notes <url>] [--published YYYY-MM-DD]
#
# 清单是「应用内更新提醒」与官网下载区共用的数据源:客户端按它判断有没有新版、
# 下载哪个包、校验值是多少。产物本身传到对象存储,清单随官网部署。
#
# 体积与 SHA256 从本批产物计算,避免使用与产物不匹配的 SHA256SUMS。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

VERSION=""
DIR="dist"
OUT="site/src/data/release.json"
BASE="https://cdn.gosniffy.com/releases"
NOTES_URL=""
PUBLISHED=""

while [ $# -gt 0 ]; do
  case "$1" in
    --version) VERSION="$2"; shift 2 ;;
    --dir) DIR="$2"; shift 2 ;;
    --out) OUT="$2"; shift 2 ;;
    --base) BASE="$2"; shift 2 ;;
    --notes) NOTES_URL="$2"; shift 2 ;;
    --published) PUBLISHED="$2"; shift 2 ;;
    -h|--help)
      sed -n '4,12p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
      exit 0 ;;
    *) echo "未知参数: $1" >&2; exit 1 ;;
  esac
done

if [ -z "$VERSION" ]; then
  VERSION="$(git describe --tags --abbrev=0 2>/dev/null || true)"
fi
if [ -z "$VERSION" ]; then
  echo "错误: 无法确定版本号,请用 --version 指定" >&2
  exit 1
fi
if [ ! -d "$DIR" ]; then
  echo "错误: 产物目录不存在: $DIR" >&2
  exit 1
fi

# 清单版本号不带 v 前缀,对象存储的目录带 v 前缀以便与 git tag 对齐。
TAG="$VERSION"
[ "${TAG#v}" = "$TAG" ] && TAG="v$TAG"
PLAIN="${TAG#v}"

sha_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | cut -d' ' -f1
  else
    shasum -a 256 "$1" | cut -d' ' -f1
  fi
}

size_of() {
  # stat 的参数在 GNU 与 BSD 下不同,wc 到处都一样。
  wc -c < "$1" | tr -d ' '
}

# classify 从文件名解析出 os / arch / 形态 / 包类型,输出以空格分隔;不认识的文件输出空串。
#   sniffy-<os>-<arch>[.exe]                      无界面版可执行文件
#   sniffy-desktop-<os>-<arch>[.exe]              桌面版免安装可执行文件
#   sniffy-desktop-<os>-<arch>-installer.<ext>    桌面版安装包
classify() {
  local name="$1" edition="headless" rest kind="binary" os arch
  case "$name" in
    SHA256SUMS|*.txt|*.md|*.json) return ;;
    sniffy-desktop-*) edition="desktop"; rest="${name#sniffy-desktop-}" ;;
    sniffy-*) rest="${name#sniffy-}" ;;
    *) return ;;
  esac
  rest="${rest%.exe}"
  case "$rest" in
    *-installer.*) kind="installer"; rest="${rest%-installer.*}" ;;
    *-installer) kind="installer"; rest="${rest%-installer}" ;;
  esac
  os="${rest%%-*}"
  arch="${rest#*-}"
  # 解析不出两段就不是发布产物(如中间文件),跳过而不是硬塞进清单。
  [ -n "$os" ] && [ -n "$arch" ] && [ "$os" != "$arch" ] || return
  printf '%s %s %s %s' "$os" "$arch" "$edition" "$kind"
}

# 安装包排在免安装二进制前面:客户端取同平台的第一条,默认该给普通用户安装包。
sort_key() {
  case "$1" in
    installer) printf '0' ;;
    *) printf '1' ;;
  esac
}

rows=""
for path in "$DIR"/*; do
  [ -f "$path" ] || continue
  name="$(basename "$path")"
  meta="$(classify "$name")" || true
  [ -n "$meta" ] || continue
  # shellcheck disable=SC2086
  set -- $meta
  os="$1"; arch="$2"; edition="$3"; kind="$4"

  rows+="$(printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' \
    "$os" "$arch" "$(sort_key "$kind")" "$edition" "$kind" "$name" "$(size_of "$path")" "$(sha_of "$path")")"
  rows+=$'\n'
done

if [ -z "$rows" ]; then
  echo "错误: $DIR 下没有可识别的发布产物" >&2
  exit 1
fi

mkdir -p "$(dirname "$OUT")"
{
  printf '{\n'
  printf '  "version": "%s",\n' "$PLAIN"
  printf '  "publishedAt": "%s",\n' "${PUBLISHED:-$(date -u +%Y-%m-%d)}"
  printf '  "notesUrl": "%s",\n' "$NOTES_URL"
  printf '  "assets": [\n'
  first=1
  # 按 os / arch / 包类型排序,让同一份产物集合每次都生成逐字节相同的清单。
  printf '%s' "$rows" | LC_ALL=C sort -t$'\t' -k1,1 -k2,2 -k3,3 -k6,6 | while IFS=$'\t' read -r os arch _ edition kind name size sha; do
    [ -n "$name" ] || continue
    [ $first -eq 1 ] || printf ',\n'
    first=0
    printf '    {\n'
    printf '      "os": "%s",\n' "$os"
    printf '      "arch": "%s",\n' "$arch"
    printf '      "edition": "%s",\n' "$edition"
    printf '      "kind": "%s",\n' "$kind"
    printf '      "name": "%s",\n' "$name"
    printf '      "url": "%s/%s/%s",\n' "$BASE" "$TAG" "$name"
    printf '      "size": %s,\n' "$size"
    printf '      "sha256": "%s"\n' "$sha"
    printf '    }'
  done
  printf '\n  ]\n}\n'
} > "$OUT"

echo ">> 已写入 $OUT ($PLAIN,$(grep -c '"name"' "$OUT") 个产物)"
echo ">> 记得把 $DIR 下的产物传到 $BASE/$TAG/,再部署官网。"
