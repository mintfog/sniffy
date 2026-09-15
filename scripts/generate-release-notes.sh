#!/usr/bin/env bash
# Copyright 2026 The mintfog Authors
# SPDX-License-Identifier: Apache-2.0

set -euo pipefail

tag="${1:-}"
output="${2:-}"

if [ -z "$tag" ] || [ -z "$output" ]; then
  echo "用法: generate-release-notes.sh <tag> <输出文件>" >&2
  exit 2
fi

tag_type="$(git cat-file -t "refs/tags/$tag" 2>/dev/null || true)"
if [ "$tag_type" != "tag" ] && [ "$tag_type" != "commit" ]; then
  echo "::error::找不到发布标签 $tag" >&2
  exit 1
fi

tag_notes=""
if [ "$tag_type" = "tag" ]; then
  tag_notes="$(git for-each-ref --format='%(contents:body)' "refs/tags/$tag")"
fi

repository="${GITHUB_REPOSITORY:-mintfog/sniffy}"

strip_prefix() {
  local subject="$1"
  local prefix='^(新增|增加|添加|支持|实现|修复|优化|完善|改进|升级|更新|调整|重构|精简|收紧|补全|维护|构建|文档|测试|feat|fix|perf|refactor|ci|build|docs|test|chore)(\([^()]+\))?[:：][[:blank:]]*(.*)$'

  if [[ "$subject" =~ $prefix ]]; then
    subject="${BASH_REMATCH[3]}"
  fi
  printf '%s' "$subject"
}

write_section() {
  local title="$1" item
  shift
  [ "$#" -gt 0 ] || return

  printf '## %s\n\n' "$title"
  for item in "$@"; do
    printf -- '- %s\n' "$item"
  done
  printf '\n'
}

generate_from_commits() {
  local previous_tag range subjects subject description total
  local feature_count=0 improvement_count=0 fix_count=0 other_count=0
  local -a features improvements fixes others

  # 从当前提交查找才能保留同提交标签和合并提交的全部父链。
  previous_tag="$(git describe --tags --match 'v*' --exclude "$tag" --abbrev=0 "refs/tags/$tag^{commit}" 2>/dev/null || true)"
  range="refs/tags/$tag"
  if [ -n "$previous_tag" ]; then
    range="refs/tags/$previous_tag..refs/tags/$tag"
  fi
  subjects="$(git log --no-merges --format='%s' "$range" --)"

  features=()
  improvements=()
  fixes=()
  others=()

  while IFS= read -r subject; do
    [ -n "$subject" ] || continue
    case "$subject" in
      新增*|增加*|添加*|支持*|实现*|feat:*|feat\(*)
        description="$(strip_prefix "$subject")"
        features+=("$description")
        feature_count=$((feature_count + 1))
        ;;
      修复*|fix:*|fix\(*)
        description="$(strip_prefix "$subject")"
        fixes+=("$description")
        fix_count=$((fix_count + 1))
        ;;
      优化*|完善*|改进*|升级*|更新*|调整*|重构*|精简*|收紧*|补全*|perf:*|perf\(*|refactor:*|refactor\(*)
        description="$(strip_prefix "$subject")"
        improvements+=("$description")
        improvement_count=$((improvement_count + 1))
        ;;
      维护*|构建*|文档*|测试*|ci:*|ci\(*|build:*|build\(*|docs:*|docs\(*|test:*|test\(*|chore:*|chore\(*)
        description="$(strip_prefix "$subject")"
        others+=("$description")
        other_count=$((other_count + 1))
        ;;
      *)
        others+=("$subject")
        other_count=$((other_count + 1))
        ;;
    esac
  done <<< "$subjects"

  total=$((feature_count + improvement_count + fix_count + other_count))
  if [ "$total" -eq 0 ]; then
    printf '%s\n' '本版本未包含新的代码提交。'
    return
  fi

  if [ -n "$previous_tag" ]; then
    printf '本版本汇总了 `%s` 之后的 %d 项变更。\n\n' "$previous_tag" "$total"
  else
    printf '本版本汇总了项目当前的 %d 项变更。\n\n' "$total"
  fi

  if [ "$feature_count" -gt 0 ]; then
    write_section '新增功能' "${features[@]}"
  fi
  if [ "$improvement_count" -gt 0 ]; then
    write_section '优化改进' "${improvements[@]}"
  fi
  if [ "$fix_count" -gt 0 ]; then
    write_section '问题修复' "${fixes[@]}"
  fi
  if [ "$other_count" -gt 0 ]; then
    write_section '其他更新' "${others[@]}"
  fi
}

{
  printf '%s\n\n' '> 跨平台抓包 / 代理工具，通过 JavaScript 插件实时修改请求与响应、Mock 和断点调试。'
  if [ -n "${tag_notes//[[:space:]]/}" ]; then
    printf '%s\n\n' "$tag_notes"
  else
    generate_from_commits
    printf '\n'
  fi
  printf '%s\n\n' '---'
  printf '%s\n\n' '## 下载安装'
  printf '%s\n\n' '请从下方 **Assets** 选择对应平台的安装包或可执行文件：'
  printf '%s\n' '- macOS：下载对应架构的 `sniffy-desktop-darwin-*-installer.dmg`'
  printf '%s\n' '- Windows：下载 `sniffy-desktop-windows-amd64-installer.exe`'
  printf '%s\n' '- Linux：下载 `sniffy-desktop-linux-amd64-installer.deb`'
  printf '%s\n\n' '- Headless：下载对应系统与架构的 `sniffy-*` 可执行文件'
  printf '%s\n\n' '发布附件的 SHA-256 校验值见 `SHA256SUMS`。'
  printf '%s\n\n' '## 相关文档'
  printf -- '- [项目仓库](https://github.com/%s)\n' "$repository"
  printf -- '- [使用与构建说明](https://github.com/%s/blob/%s/README.md)\n' "$repository" "$tag"
  printf -- '- [插件辅助 API](https://github.com/%s/blob/%s/docs/plugins-helpers.md)\n' "$repository" "$tag"
} > "$output"
