// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0

import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { existsSync, mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { tmpdir } from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('../..', import.meta.url));
const require = createRequire(path.join(root, 'web/package.json'));
const { parseDocument } = require('yaml');
const document = parseDocument(readFileSync(path.join(root, '.github/workflows/build.yml'), 'utf8'));
assert.deepEqual(document.errors, []);
const workflow = document.toJS();
const steps = workflow.jobs.desktop.steps;
const packageStep = steps.find((step) => step.name === '生成安装包');
const installerUpload = steps.find((step) => step.with?.path === 'dist/installers/*');
const bash = process.env.BASH_PATH || (
  process.platform === 'win32' && existsSync('C:/Program Files/Git/bin/bash.exe')
    ? 'C:/Program Files/Git/bin/bash.exe'
    : 'bash'
);

// Git for Windows 启动器会前置工具目录，mock 路径需在 Shell 内设置。
const mockSetup = `
mock_bin="$INSTALLER_TEST_MOCK_BIN"
if command -v cygpath >/dev/null 2>&1; then mock_bin="$(cygpath -u "$mock_bin")"; fi
export PATH="$mock_bin:$PATH"
`;

function runBash(args, options = {}) {
  const result = spawnSync(bash, args, { cwd: root, encoding: 'utf8', ...options });
  assert.ifError(result.error);
  return result;
}

function withMockBash(run) {
  const directory = mkdtempSync(path.join(tmpdir(), 'sniffy installer tests '));
  try {
    const mockBin = path.join(directory, 'mock bin');
    mkdirSync(mockBin);
    writeFileSync(path.join(mockBin, 'bash'), '#!/bin/sh\nprintf "%s\\n" "$PWD" "$@"\nexit "${INSTALLER_TEST_EXIT_CODE:-0}"\n', { mode: 0o755 });
    run({
      directory,
      env: { ...process.env, INSTALLER_TEST_MOCK_BIN: mockBin },
    });
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
}

test('四个桌面目标均生成独立安装包制品', () => {
  const expected = { 'linux/amd64': 'deb', 'windows/amd64': 'exe', 'darwin/amd64': 'dmg', 'darwin/arm64': 'dmg' };
  const matrix = workflow.jobs.desktop.strategy.matrix.include;
  assert.equal(matrix.length, Object.keys(expected).length);
  for (const target of matrix) assert.equal(target.installer_ext, expected[target.name]);
  assert.equal(packageStep.if, undefined);
  assert.equal(packageStep.shell, 'bash');
  assert.equal(installerUpload.if, undefined);
  assert.equal(installerUpload.with['if-no-files-found'], 'error');
  assert.equal(installerUpload.with.name, 'sniffy-desktop-${{ matrix.goos }}-${{ matrix.goarch }}-installer');
  assert.ok(steps.indexOf(installerUpload) > steps.indexOf(packageStep));
  const portableUpload = steps.find((step) => step.with?.name === 'sniffy-desktop-${{ matrix.goos }}-${{ matrix.goarch }}');
  assert.ok(steps.indexOf(portableUpload) < steps.indexOf(packageStep));
  assert.equal(portableUpload.with.path, 'dist/*');
});

test('Release 收集原始文件与所有安装包', () => {
  const release = workflow.jobs.release;
  assert.deepEqual(release.needs, ['headless', 'desktop']);
  const download = release.steps.find((step) => step.uses?.startsWith('actions/download-artifact@'));
  assert.equal(download.with.pattern, 'sniffy-*');
  assert.equal(download.with['merge-multiple'], true);
  assert.equal(download.with.path, 'release');
  const publish = release.steps.find((step) => step.name === '发布 Release');
  assert.match(publish.run, /gh release upload "\$TAG" release\/\*/);
  assert.match(publish.run, /gh release create "\$TAG" release\/\*/);
});

test('Windows NSIS 编译器经过校验并传入后续 PATH', () => {
  const nsis = steps.find((step) => step.name === '安装 NSIS');
  assert.equal(nsis.if, "runner.os == 'Windows'");
  assert.equal(nsis.shell, 'pwsh');
  assert.match(nsis.run, /& \$makensis -VERSION/);
  assert.equal((nsis.run.match(/exit \$LASTEXITCODE/g) || []).length, 2);
  assert.match(nsis.run, /Add-Content -LiteralPath \$env:GITHUB_PATH/);
  assert.ok(steps.indexOf(nsis) < steps.indexOf(packageStep));
});

for (const target of workflow.jobs.desktop.strategy.matrix.include) {
  test(`${target.name} 工作流传递目标二进制、版本及安装包路径`, () => withMockBash(({ directory, env }) => {
    const result = runBash(['-e', '-c', mockSetup + packageStep.run], {
      cwd: directory,
      env: { ...env, GOOS: target.goos, GOARCH: target.goarch, INSTALLER_EXT: target.installer_ext, VERSION: 'v2.0.0-beta.1' },
    });
    assert.equal(result.status, 0, result.stderr);
    const args = result.stdout.trim().split(/\r?\n/).slice(1);
    const binary = `dist/sniffy-desktop-${target.goos}-${target.goarch}${target.goos === 'windows' ? '.exe' : ''}`;
    assert.deepEqual(args, [
      'scripts/make-desktop-installer.sh', '--os', target.goos, '--arch', target.goarch,
      '--binary', binary, '--version', '2.0.0-beta.1',
      '--out', `dist/installers/sniffy-desktop-${target.goos}-${target.goarch}-installer.${target.installer_ext}`,
    ]);
  }));
}

for (const [os, script] of [['windows', 'nsis'], ['darwin', 'dmg'], ['linux', 'deb']]) {
  test(`${os} 共用入口保持调用目录及带空格参数`, () => withMockBash(({ directory, env }) => {
    const result = runBash([
      '-e', '-c', mockSetup + 'source "$1" "${@:2}"', 'installer-test',
      path.join(root, 'scripts/make-desktop-installer.sh'), '--os', os, '--arch', 'amd64',
      '--binary', 'build output/Sniffy', '--version', 'v2.0.0', '--out', 'install output/setup',
    ], { cwd: directory, env });
    assert.equal(result.status, 0, result.stderr);
    const [cwd, entry, ...args] = result.stdout.trim().split(/\r?\n/);
    assert.equal(cwd.split('/').at(-1), path.basename(directory));
    assert.ok(entry.endsWith(`/scripts/make-${script}-installer.sh`));
    const expected = ['--binary', 'build output/Sniffy', '--version', '2.0.0'];
    if (os !== 'windows') expected.push('--arch', 'amd64');
    expected.push('--out', 'install output/setup');
    assert.deepEqual(args, expected);
  }));

  test(`${os} 共用入口保留打包失败退出码`, () => withMockBash(({ directory, env }) => {
    const result = runBash([
      '-e', '-c', mockSetup + 'source "$1" "${@:2}"', 'installer-test',
      path.join(root, 'scripts/make-desktop-installer.sh'), '--os', os, '--arch', 'amd64',
      '--binary', 'Sniffy', '--version', '2.0.0', '--out', 'setup',
    ], { cwd: directory, env: { ...env, INSTALLER_TEST_EXIT_CODE: '27' } });
    assert.equal(result.status, 27, result.stderr);
  }));
}

test('Windows 打包输出校验原始二进制路径', () => {
  const directory = mkdtempSync(path.join(tmpdir(), 'sniffy nsis input tests '));
  try {
    const binary = path.join(directory, 'Sniffy.exe');
    writeFileSync(binary, 'NSIS input fixture');
    const result = runBash([
      path.join(root, 'scripts/make-nsis-installer.sh'), '--binary', binary,
      '--version', '2.0.0', '--out', binary,
    ]);
    assert.equal(result.status, 1, result.stderr);
    assert.match(result.stderr, /--out 不能覆盖输入二进制/);
    assert.equal(readFileSync(binary, 'utf8'), 'NSIS input fixture');
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});

test('共用入口校验必填参数与目标平台', () => {
  const entry = path.join(root, 'scripts/make-desktop-installer.sh');
  const valid = ['--os', 'linux', '--arch', 'amd64', '--binary', 'Sniffy', '--version', '2.0.0', '--out', 'setup'];
  for (let index = 0; index < valid.length; index += 2) {
    const missing = [...valid.slice(0, index), ...valid.slice(index + 2)];
    assert.equal(runBash([entry, ...missing]).status, 1);
    assert.equal(runBash([entry, valid[index]]).status, 1);
  }
  const unsupportedOs = [...valid];
  unsupportedOs[1] = 'unsupported';
  assert.equal(runBash([entry, ...unsupportedOs]).status, 1);
  const unsupportedArch = [...valid];
  unsupportedArch[3] = '386';
  assert.equal(runBash([entry, ...unsupportedArch]).status, 1);
  assert.equal(runBash([entry, '--help']).status, 0);
});
