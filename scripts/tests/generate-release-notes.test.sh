#!/usr/bin/env bash
# Copyright 2026 The mintfog Authors
# SPDX-License-Identifier: Apache-2.0

set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
script="$root/scripts/generate-release-notes.sh"
workspace="$(mktemp -d)"
trap 'rm -rf "$workspace"' EXIT

init_repo() {
  git -C "$1" init -q -b main
  git -C "$1" config user.name "Sniffy Test"
  git -C "$1" config user.email "sniffy@example.invalid"
  git -C "$1" config commit.gpgsign false
  git -C "$1" config tag.gpgsign false
}

assert_line() {
  if ! grep -Fxq -- "$2" "$1"; then
    printf '发布说明缺少预期内容：%s\n' "$2" >&2
    cat "$1" >&2
    exit 1
  fi
}

init_repo "$workspace"
git -C "$workspace" commit --allow-empty -q -m "初始化"
git -C "$workspace" tag v1.2.2

(
  cd "$workspace"
  bash "$script" v1.2.2 "$workspace/first-release.md"
)
grep -Fq '本版本汇总了项目当前的 1 项变更。' "$workspace/first-release.md"
grep -Fq -- '- 初始化' "$workspace/first-release.md"

git -C "$workspace" commit --allow-empty -q -m "新增：支持自动发布说明"
git -C "$workspace" tag checkpoint
git -C "$workspace" commit --allow-empty -q -m "优化：统一中文格式"
git -C "$workspace" commit --allow-empty -q -m "修复：保留轻量标签发布方式"
git -C "$workspace" commit --allow-empty -q -m "维护：整理发布附件名称"
git -C "$workspace" tag v1.2.3

(
  cd "$workspace"
  GITHUB_REPOSITORY="mintfog/sniffy" bash "$script" v1.2.3 "$workspace/automatic.md"
)

grep -Fq '本版本汇总了 `v1.2.2` 之后的 4 项变更。' "$workspace/automatic.md"
grep -Fq '## 新增功能' "$workspace/automatic.md"
grep -Fq -- '- 支持自动发布说明' "$workspace/automatic.md"
grep -Fq '## 优化改进' "$workspace/automatic.md"
grep -Fq '## 问题修复' "$workspace/automatic.md"
grep -Fq '## 其他更新' "$workspace/automatic.md"
grep -Fq -- '- 整理发布附件名称' "$workspace/automatic.md"
grep -Fq 'blob/v1.2.3/README.md' "$workspace/automatic.md"

git -C "$workspace" commit --allow-empty -q -m "修复：这条提交由手写正文覆盖"
git -C "$workspace" tag -a v1.2.4 --cleanup=verbatim \
  -m "Sniffy 1.2.4" \
  -m $'集中完善桌面发布体验。\n\n## 优化改进\n\n- 使用人工整理的版本说明'

(
  cd "$workspace"
  bash "$script" v1.2.4 "$workspace/manual.md"
)

grep -Fq '集中完善桌面发布体验。' "$workspace/manual.md"
grep -Fq -- '- 使用人工整理的版本说明' "$workspace/manual.md"
if grep -Fq '这条提交由手写正文覆盖' "$workspace/manual.md"; then
  echo "annotated tag 正文应覆盖自动生成内容" >&2
  exit 1
fi

git -C "$workspace" commit --allow-empty -q -m "修复：无正文标签自动回退"
git -C "$workspace" tag -a v1.2.5 -m "Sniffy 1.2.5"
(
  cd "$workspace"
  bash "$script" v1.2.5 "$workspace/empty-body.md"
)
grep -Fq -- '- 无正文标签自动回退' "$workspace/empty-body.md"

while IFS='|' read -r subject expected; do
  git -C "$workspace" commit --allow-empty -q -m "$subject"
  printf -- '- %s\n' "$expected" >> "$workspace/expected-prefixes.md"
done <<'CASES'
feat: 支持配置：代理地址|支持配置：代理地址
修复 http://localhost:8080 代理连接|修复 http://localhost:8080 代理连接
新增：支持 http://localhost:8080 调试|支持 http://localhost:8080 调试
fix(proxy): 修复连接：http://localhost:8080|修复连接：http://localhost:8080
修复详情查找(Ctrl+F)作用域:按光标所在区域查找|修复详情查找(Ctrl+F)作用域:按光标所在区域查找
优化: 配置入口：代理设置|配置入口：代理设置
refactor(api): 整理响应处理|整理响应处理
CASES
git -C "$workspace" tag v1.2.6
(
  cd "$workspace"
  bash "$script" v1.2.6 "$workspace/prefixes.md"
)
while IFS= read -r expected; do
  if ! grep -Fxq -- "$expected" "$workspace/prefixes.md"; then
    printf '发布说明缺少预期条目：%s\n' "$expected" >&2
    cat "$workspace/prefixes.md" >&2
    exit 1
  fi
done < "$workspace/expected-prefixes.md"

if (cd "$workspace" && bash "$script" v9.9.9 "$workspace/missing.md") >/dev/null 2>&1; then
  echo "不存在的标签不应生成发布说明" >&2
  exit 1
fi

# 上一版本来自合并提交的第二父链，第一父链可能有更早的标签，也可能没有标签。
for base_tag in present absent; do
  repo="$workspace/merge-$base_tag"
  mkdir "$repo"
  init_repo "$repo"
  git -C "$repo" commit --allow-empty -qm "初始化"
  if [ "$base_tag" = present ]; then
    git -C "$repo" tag v1.0.0
  fi
  git -C "$repo" checkout -qb published
  git -C "$repo" commit --allow-empty -qm "新增：已发布功能"
  git -C "$repo" tag v1.1.0
  git -C "$repo" checkout -q main
  git -C "$repo" commit --allow-empty -qm "修复：本次修复"
  git -C "$repo" merge -q --no-ff published -m "合并发布分支"
  git -C "$repo" tag v1.2.0

  (
    cd "$repo"
    bash "$script" v1.2.0 notes.md
  )
  assert_line "$repo/notes.md" '本版本汇总了 `v1.1.0` 之后的 1 项变更。'
  assert_line "$repo/notes.md" '- 本次修复'
done

# 预发布与正式版可以指向同一提交，覆盖两种标签类型的组合。
for previous_type in lightweight annotated; do
  for current_type in lightweight annotated; do
    repo="$workspace/same-commit-$previous_type-$current_type"
    mkdir "$repo"
    init_repo "$repo"
    git -C "$repo" commit --allow-empty -qm "新增：候选版本功能"
    if [ "$previous_type" = annotated ]; then
      git -C "$repo" tag -a v2.0.0-rc.1 -m "Sniffy 2.0.0-rc.1"
    else
      git -C "$repo" tag v2.0.0-rc.1
    fi
    if [ "$current_type" = annotated ]; then
      git -C "$repo" tag -a v2.0.0 -m "Sniffy 2.0.0"
    else
      git -C "$repo" tag v2.0.0
    fi
    (
      cd "$repo"
      bash "$script" v2.0.0 notes.md
    )
    assert_line "$repo/notes.md" '本版本未包含新的代码提交。'
  done
done

repo="$workspace/reachable-tags"
mkdir "$repo"
init_repo "$repo"
git -C "$repo" commit --allow-empty -qm "初始化"
git -C "$repo" tag -a v3.0.0 -m "Sniffy 3.0.0"
git -C "$repo" checkout -qb future
git -C "$repo" commit --allow-empty -qm "新增：其他分支功能"
git -C "$repo" tag v9.0.0
git -C "$repo" checkout -q main
git -C "$repo" commit --allow-empty -qm "修复：本次修复"
git -C "$repo" tag checkpoint
git -C "$repo" tag v3.1.0
# 同名分支指向其他提交时，发布范围仍由标签确定。
git -C "$repo" branch v3.1.0 future
git -C "$repo" branch v3.0.0 future
(
  cd "$repo"
  bash "$script" v3.1.0 notes.md
)
assert_line "$repo/notes.md" '本版本汇总了 `v3.0.0` 之后的 1 项变更。'
assert_line "$repo/notes.md" '- 本次修复'

mkdir "$workspace/bin"
cat > "$workspace/bin/git" <<'SH'
#!/usr/bin/env bash
if [ "$1" = log ]; then
  echo "模拟提交历史读取失败" >&2
  exit 42
fi
exec "$RELEASE_TEST_GIT" "$@"
SH
chmod +x "$workspace/bin/git"
git_command="$(command -v git)"
if (
  cd "$repo"
  RELEASE_TEST_GIT="$git_command" PATH="$workspace/bin:$PATH" \
    bash "$script" v3.1.0 failed.md
) > "$workspace/failure.log" 2>&1; then
  echo "提交历史读取失败时不应成功生成发布说明" >&2
  exit 1
else
  result=$?
  if [ "$result" -ne 42 ]; then
    printf '提交历史读取失败的退出码为 %s，期望 42\n' "$result" >&2
    cat "$workspace/failure.log" >&2
    exit 1
  fi
fi

echo "发布说明生成测试通过"
