// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0

import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { existsSync, mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('../..', import.meta.url));
const entry = path.join(root, 'scripts/generate-windows-resources.sh');
const bash = process.env.BASH_PATH || (
  process.platform === 'win32' && existsSync('C:/Program Files/Git/bin/bash.exe')
    ? 'C:/Program Files/Git/bin/bash.exe'
    : 'bash'
);

function runGenerator(args, exitCode = '0') {
  const directory = mkdtempSync(path.join(tmpdir(), 'sniffy windows resources '));
  try {
    const mockBin = path.join(directory, 'mock bin');
    mkdirSync(mockBin);
    writeFileSync(path.join(mockBin, 'go'), `#!/bin/sh
case "$1" in
  env)
    case "$2" in
      GOARCH) printf 'arm64\\n' ;;
      GOHOSTOS) printf 'linux\\n' ;;
      GOHOSTARCH) printf 'amd64\\n' ;;
      *) exit 1 ;;
    esac ;;
  run)
    printf '%s\\n' "$GOOS" "$GOARCH" "$@"
    exit "$WINDOWS_RESOURCE_TEST_EXIT_CODE" ;;
  *) exit 1 ;;
esac
`, { mode: 0o755 });
    // Git Bash 会前置工具目录，mock 路径需在 Shell 内设置。
    const setup = `
mock_bin="$WINDOWS_RESOURCE_TEST_MOCK_BIN"
if command -v cygpath >/dev/null 2>&1; then mock_bin="$(cygpath -u "$mock_bin")"; fi
export PATH="$mock_bin:$PATH"
exec bash "$1" "\${@:2}"
`;
    const result = spawnSync(bash, ['-e', '-c', setup, 'windows-resource-test', entry, ...args], {
      cwd: directory,
      encoding: 'utf8',
      env: {
        ...process.env,
        GOOS: 'windows',
        GOARCH: 'arm64',
        WINDOWS_RESOURCE_TEST_MOCK_BIN: mockBin,
        WINDOWS_RESOURCE_TEST_EXIT_CODE: exitCode,
      },
    });
    assert.ifError(result.error);
    return result;
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
}

for (const { name, args, arch } of [
  { name: '显式 amd64', args: ['amd64'], arch: 'amd64' },
  { name: '显式 arm64', args: ['arm64'], arch: 'arm64' },
  { name: '默认 Go 目标架构', args: [], arch: 'arm64' },
]) {
  test(`${name}：图标生成器在构建机上运行并传递目标架构`, () => {
    const result = runGenerator(args);
    assert.equal(result.status, 0, result.stderr);
    assert.deepEqual(result.stdout.trim().split(/\r?\n/), [
      'linux', 'amd64', 'run', 'github.com/tc-hib/go-winres@v0.3.3', 'make',
      '--in', 'build/windows/winres.json', '--out', 'cmd/sniffy-desktop/rsrc', '--arch', arch,
    ]);
  });
}

test('图标生成失败保留退出码', () => {
  assert.equal(runGenerator(['amd64'], '27').status, 27);
});

test('图标生成拒绝不支持的架构', () => {
  const result = runGenerator(['386']);
  assert.equal(result.status, 1);
  assert.match(result.stderr, /不支持的 Windows 桌面架构/);
  assert.equal(result.stdout, '');
});

function decodeIconImages(buffer) {
  assert.equal(buffer.readUInt16LE(0), 0, 'ICO 保留字段必须为 0');
  assert.equal(buffer.readUInt16LE(2), 1, 'ICO 类型必须为图标');
  const count = buffer.readUInt16LE(4);
  const directoryEnd = 6 + count * 16;
  assert.ok(count > 0, 'ICO 必须包含图像');
  assert.ok(buffer.length >= directoryEnd, 'ICO 图像目录不完整');
  const images = [];
  for (let index = 0; index < count; index++) {
    const entryOffset = 6 + index * 16;
    const size = buffer.readUInt32LE(entryOffset + 8);
    const offset = buffer.readUInt32LE(entryOffset + 12);
    assert.ok(size > 0 && offset + size <= buffer.length, `ICO 图像 ${index} 的数据范围无效`);
    images.push({
      width: buffer[entryOffset] || 256,
      height: buffer[entryOffset + 1] || 256,
      data: buffer.subarray(offset, offset + size),
    });
  }
  // 资源提取器可能重排 ICO 目录，按尺寸比较实际图像内容。
  return images.sort((a, b) => a.width - b.width || a.height - b.height);
}

const binary = process.env.SNIFFY_WINDOWS_TEST_BINARY;
test('Windows GUI exe 内嵌完整的应用图标', { skip: !binary }, (t) => {
  const expectedMachine = { amd64: 0x8664, arm64: 0xaa64 }[process.env.SNIFFY_WINDOWS_TEST_ARCH];
  assert.ok(expectedMachine, 'SNIFFY_WINDOWS_TEST_ARCH 必须为 amd64 或 arm64');
  const executable = readFileSync(binary);
  const peOffset = executable.readUInt32LE(0x3c);
  const optionalHeaderOffset = peOffset + 24;
  assert.equal(executable.toString('ascii', peOffset, peOffset + 4), 'PE\0\0', 'EXE 必须包含 PE 签名');
  assert.equal(executable.readUInt16LE(peOffset + 4), expectedMachine, 'EXE 架构必须匹配目标架构');
  assert.equal(executable.readUInt16LE(optionalHeaderOffset + 68), 2, 'EXE 子系统必须为 Windows GUI');

  const directory = mkdtempSync(path.join(tmpdir(), 'sniffy embedded windows icon '));
  t.after(() => rmSync(directory, { recursive: true, force: true }));

  const host = spawnSync('go', ['env', 'GOHOSTOS', 'GOHOSTARCH'], { encoding: 'utf8' });
  assert.ifError(host.error);
  assert.equal(host.status, 0, host.stderr);
  const [hostOS, hostArch] = host.stdout.trim().split(/\r?\n/);
  const result = spawnSync('go', [
    'run', 'github.com/tc-hib/go-winres@v0.3.3', 'extract', '--dir', directory, binary,
  ], {
    cwd: root,
    encoding: 'utf8',
    timeout: 60000,
    env: { ...process.env, GOOS: hostOS, GOARCH: hostArch },
  });
  assert.ifError(result.error);
  assert.equal(result.status, 0, result.stderr);

  const resources = JSON.parse(readFileSync(path.join(directory, 'winres.json'), 'utf8'));
  assert.deepEqual(Object.keys(resources.RT_GROUP_ICON), ['#3']);
  const embedded = readFileSync(path.join(directory, resources.RT_GROUP_ICON['#3']['0000']));
  const original = readFileSync(path.join(root, 'build/windows/icon.ico'));
  assert.deepEqual(decodeIconImages(embedded), decodeIconImages(original));
});
