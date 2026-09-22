#!/usr/bin/env bash
# Copyright 2026 The mintfog Authors
# SPDX-License-Identifier: Apache-2.0

set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
script="$root/scripts/release-manifest.sh"
workspace="$(mktemp -d)"
trap 'rm -rf "$workspace"' EXIT

sha_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | cut -d' ' -f1
  else
    shasum -a 256 "$1" | cut -d' ' -f1
  fi
}

assert_contains() {
  if ! grep -Fq -- "$2" "$1"; then
    printf '发布清单缺少预期内容：%s\n' "$2" >&2
    cat "$1" >&2
    exit 1
  fi
}

dist="$workspace/dist"
mkdir -p "$dist"
printf 'current-headless' > "$dist/sniffy-linux-amd64"
printf 'current-installer' > "$dist/sniffy-desktop-windows-amd64-installer.exe"

# 同目录的 SHA256SUMS 描述的是另一批产物：清单必须按实际文件现算，不能照抄它。
printf 'stale-headless' > "$workspace/old-headless"
printf 'stale-installer' > "$workspace/old-installer"
stale_headless="$(sha_of "$workspace/old-headless")"
stale_installer="$(sha_of "$workspace/old-installer")"
{
  printf '%s  sniffy-linux-amd64\n' "$stale_headless"
  printf '%s  sniffy-desktop-windows-amd64-installer.exe\n' "$stale_installer"
} > "$dist/SHA256SUMS"

out="$workspace/release.json"
bash "$script" --version v1.2.3 --dir "$dist" --out "$out" --published 2026-09-18 >/dev/null

for name in sniffy-linux-amd64 sniffy-desktop-windows-amd64-installer.exe; do
  assert_contains "$out" "\"sha256\": \"$(sha_of "$dist/$name")\""
  assert_contains "$out" "\"size\": $(wc -c < "$dist/$name" | tr -d ' ')"
done
for stale in "$stale_headless" "$stale_installer"; do
  if grep -Fq -- "$stale" "$out"; then
    printf '发布清单沿用了过期的 SHA256SUMS：%s\n' "$stale" >&2
    cat "$out" >&2
    exit 1
  fi
done
if grep -Fq '"name": "SHA256SUMS"' "$out"; then
  printf '校验和文件不该作为产物进入清单\n' >&2
  exit 1
fi
assert_contains "$out" '"kind": "binary"'
assert_contains "$out" '"kind": "installer"'
assert_contains "$out" '"url": "https://cdn.gosniffy.com/releases/v1.2.3/sniffy-linux-amd64"'

for name in sniffy-desktop-windows-amd64.exe sniffy-desktop-darwin-arm64-installer.dmg sniffy-desktop-linux-amd64-installer.deb; do
  printf 'artifact-%s' "$name" > "$dist/$name"
done
printf '说明' > "$dist/README.md"
bash "$script" --version 1.2.3 --dir "$dist" --out "$out" --published 2026-09-18 \
  --base https://downloads.example/releases --notes https://gosniffy.com/notes/1.2.3 >/dev/null

node --experimental-strip-types --input-type=module - "$out" "$dist" "$root" <<'JS'
import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { pathToFileURL } from 'node:url'

const [out, dist, root] = process.argv.slice(2)
const { parseRelease, desktopAssets, detectPlatform } = await import(
  pathToFileURL(join(root, 'site/src/lib/downloads.ts')).href
)
const manifest = parseRelease(JSON.parse(readFileSync(out, 'utf8')))
assert.equal(manifest.version, '1.2.3')
assert.equal(manifest.publishedAt, '2026-09-18')
assert.equal(manifest.notesUrl, 'https://gosniffy.com/notes/1.2.3')
assert.equal(manifest.assets.length, 5)
const device = detectPlatform({ userAgent: 'Windows NT 10.0; Win64; x64' })
const installer = desktopAssets(manifest, device.os).find(asset => asset.arch === device.arch)
assert.equal(installer.name, 'sniffy-desktop-windows-amd64-installer.exe')
assert.deepEqual(manifest.assets.map(a => [a.os, a.arch, a.edition, a.kind]), [
  ['darwin', 'arm64', 'desktop', 'installer'],
  ['linux', 'amd64', 'desktop', 'installer'],
  ['linux', 'amd64', 'headless', 'binary'],
  ['windows', 'amd64', 'desktop', 'installer'],
  ['windows', 'amd64', 'desktop', 'binary'],
])
for (const asset of manifest.assets) {
  const body = readFileSync(join(dist, asset.name))
  assert.equal(asset.size, body.length)
  assert.equal(asset.sha256, createHash('sha256').update(body).digest('hex'))
  assert.equal(asset.url, `https://downloads.example/releases/v1.2.3/${asset.name}`)
}
JS

bash "$script" --version v1.2.3 --dir "$dist" --out "$workspace/repeated.json" --published 2026-09-18 \
  --base https://downloads.example/releases --notes https://gosniffy.com/notes/1.2.3 >/dev/null
cmp "$out" "$workspace/repeated.json"

mkdir "$workspace/empty"
for invalid in "$workspace/empty" "$workspace/missing"; do
  if bash "$script" --version v1.2.3 --dir "$invalid" --out "$out" > "$workspace/error.log" 2>&1; then
    printf '无产物时不应发布清单：%s\n' "$invalid" >&2
    exit 1
  fi
  cmp "$out" "$workspace/repeated.json"
done
