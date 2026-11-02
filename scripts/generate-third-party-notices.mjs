// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// 生成前准备 Go 模块缓存及 web/site 的 npm 依赖；前端清单需按发布产物核对。
// 运行 node scripts/generate-third-party-notices.mjs 更新声明，使用 --check 核对生成结果。

import { createHash } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { existsSync, readFileSync, readdirSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('../', import.meta.url));
const output = path.join(root, 'internal/notices/THIRD_PARTY_NOTICES.txt');
const metadata = path.join(root, 'licenses/notices-lock.json');
const inputs = [
  'go.mod',
  'web/package-lock.json',
  'site/package-lock.json',
  'licenses/components.json',
  'licenses/upstream.json',
  'LICENSE',
  'licenses/upstream/svelte.txt',
  'scripts/generate-third-party-notices.mjs',
];
const read = (file) => readFileSync(file, 'utf8').replaceAll('\r\n', '\n');
const hash = (data) => createHash('sha256').update(data).digest('hex');

// 快速检查用于构建入口；完整检查还会逐项重读依赖包中的原始许可证。
if (process.argv.includes('--check-inputs')) {
  const saved = JSON.parse(read(metadata));
  for (const input of inputs) {
    if (saved.inputs[input] !== hash(read(path.join(root, input)))) {
      throw new Error(`${input} 已更新，请重新生成第三方声明`);
    }
  }
  if (saved.notice !== hash(read(output))) throw new Error('第三方声明已变更，请重新生成并核对声明');
  process.exit(0);
}

const overrides = JSON.parse(readFileSync(path.join(root, 'licenses/upstream.json'), 'utf8'));

function licenseFiles(directory, relative = '', recursive = true) {
  const files = [];
  for (const entry of readdirSync(path.join(directory, relative), { withFileTypes: true })) {
    if (['node_modules', '.git', 'testdata'].includes(entry.name)) continue;
    const name = path.posix.join(relative, entry.name);
    if (entry.isDirectory()) {
      if (recursive) files.push(...licenseFiles(directory, name));
      continue;
    }
    const licenseName = /(?:^|[-_])(licen[sc]e|copying|notice|patents|copyright|authors)|^ThirdPartyNotice/i.test(entry.name);
    const sourceFile = /\.(?:[cm]?[jt]sx?|astro)$/i.test(entry.name);
    if (entry.isFile() && licenseName && !sourceFile) files.push(name);
  }
  return files.sort();
}

const sections = [];
function add(name, source, directory, files = licenseFiles(directory)) {
  if (!files.length) throw new Error(`${name} 缺少许可证文件，请核对上游并添加 licenses/upstream.json 条目`);
  const body = files.map((file) => `--- ${path.basename(file)} ---\n${read(path.join(directory, file)).trim()}`).join('\n\n');
  sections.push(`${'='.repeat(78)}\n${name}\nSource: ${source}\n\n${body}`);
}

const goMod = JSON.parse(execFileSync('go', ['mod', 'edit', '-json'], { cwd: root, encoding: 'utf8' }));
if (goMod.Replace?.length) throw new Error('请先核对 go.mod replace 对许可证来源的影响');
const goEnv = JSON.parse(execFileSync('go', ['env', '-json', 'GOMODCACHE', 'GOROOT'], { cwd: root, encoding: 'utf8' }));
const runtimeModules = new Map();
for (const target of ['linux/amd64', 'linux/arm64', 'darwin/amd64', 'darwin/arm64', 'windows/amd64', 'windows/arm64']) {
  const [os, arch] = target.split('/');
  const modules = execFileSync('go', ['list', '-deps', '-tags', 'desktop,production', '-f', '{{with .Module}}{{.Path}}{{end}}\t{{.Dir}}', './cmd/sniffy', './cmd/sniffy-desktop'], {
    cwd: root,
    encoding: 'utf8',
    env: { ...process.env, GOOS: os, GOARCH: arch, CGO_ENABLED: os === 'windows' ? '0' : '1' },
  });
  for (const line of modules.split('\n')) {
    const [name, directory] = line.split('\t');
    if (!name) continue;
    if (!runtimeModules.has(name)) runtimeModules.set(name, new Set());
    runtimeModules.get(name).add(directory);
  }
}
const lru = goMod.Require.find((module) => module.Path === 'github.com/hashicorp/golang-lru/v2');
add('Sniffy — Apache-2.0', 'https://github.com/mintfog/sniffy', root, ['LICENSE']);
add('Go standard library', 'https://go.dev/dl/', goEnv.GOROOT, ['LICENSE', ...licenseFiles(path.join(goEnv.GOROOT, 'src/vendor')).map((file) => `src/vendor/${file}`)]);

for (const module of goMod.Require.sort((a, b) => a.Path < b.Path ? -1 : 1)) {
  if (!runtimeModules.has(module.Path)) continue;
  const escaped = `${module.Path}@${module.Version}`.replace(/[A-Z]/g, (letter) => `!${letter.toLowerCase()}`);
  const moduleDirectory = path.join(goEnv.GOMODCACHE, escaped);
  const directories = new Set([moduleDirectory]);
  for (const packageDirectory of runtimeModules.get(module.Path)) {
    for (let directory = packageDirectory; directory.startsWith(`${moduleDirectory}${path.sep}`); directory = path.dirname(directory)) {
      directories.add(directory);
    }
  }
  const files = [...directories].flatMap((directory) => licenseFiles(directory, '', false).map((file) => (
    path.relative(moduleDirectory, path.join(directory, file)).split(path.sep).join('/')
  ))).sort();
  add(`${module.Path} ${module.Version}`, `https://pkg.go.dev/${module.Path}@${module.Version}`, moduleDirectory, files);
}

// 清单对应发布包中的前端代码、样式、字体和图标，随前端依赖更新核对。
const components = JSON.parse(read(path.join(root, 'licenses/components.json')));
for (const [workspace, names] of Object.entries(components)) {
  const lock = JSON.parse(read(path.join(root, workspace, 'package-lock.json')));
  for (const component of names.toSorted()) {
    const location = `node_modules/${component}`;
    const pkg = lock.packages[location];
    if (!pkg) throw new Error(`${workspace}: ${component} 未在 lockfile 中找到，请核对发布组件清单`);
    const directory = path.join(root, workspace, location);
    const manifest = JSON.parse(read(path.join(directory, 'package.json')));
    const name = manifest.name;
    if (manifest.version !== pkg.version) throw new Error(`${workspace}/${location} 与 lockfile 版本不一致，请先执行 npm ci`);
    let source = typeof manifest.repository === 'string' ? manifest.repository : manifest.repository?.url;
    source = (source || `https://www.npmjs.com/package/${name}/v/${pkg.version}`).replace(/^git\+/, '').replace(/\.git$/, '');
    if (/^[\w-]+\/[\w.-]+$/.test(source)) source = `https://github.com/${source}`;
    const title = `${name} ${pkg.version} — ${manifest.license || pkg.license}`;
    const override = overrides[name];
    if (!override) {
      add(title, source, directory);
      continue;
    }
    if (override.version !== pkg.version) throw new Error(`${name} 已升级，请重新核对补充许可证的版本与来源`);
    const sourceDirectory = override.goModule
      ? path.join(goEnv.GOMODCACHE, override.goModule)
      : root;
    add(title, `${override.source}${override.note ? `\nNote: ${override.note}` : ''}`, sourceDirectory, override.files);
  }
}

const header = `Sniffy License and Third-Party Notices

Sniffy is licensed under the Apache License 2.0. Third-party components are licensed under their respective licenses.
This file includes copyright notices, full license texts, and source code access information.

MPL-2.0 source code:
The source code for ${lru.Path} ${lru.Version} is available at these version-specific locations:
https://github.com/hashicorp/golang-lru/tree/${lru.Version}
https://proxy.golang.org/${lru.Path}/@v/${lru.Version}.zip
Sniffy uses the upstream version of this module. Its source code is available under MPL-2.0; the full license is included in the corresponding entry below.
`;
const text = `${header}\n${sections.join('\n\n')}\n`;
const state = `${JSON.stringify({
  inputs: Object.fromEntries(inputs.map((input) => [input, hash(read(path.join(root, input)))])),
  notice: hash(text),
}, null, 2)}\n`;
if (process.argv.includes('--check')) {
  if (!existsSync(output) || read(output) !== text || !existsSync(metadata) || read(metadata) !== state) {
    throw new Error('第三方声明需要更新，请运行 node scripts/generate-third-party-notices.mjs');
  }
} else {
  writeFileSync(output, text);
  writeFileSync(metadata, state);
}
console.log(`第三方声明已${process.argv.includes('--check') ? '核对' : '生成'}：${sections.length} 个条目`);
