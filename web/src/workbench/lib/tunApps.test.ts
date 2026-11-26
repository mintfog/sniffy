import test from 'node:test'
import assert from 'node:assert/strict'
import { MAX_TUN_APP_BYTES, MAX_TUN_APPS, parseTunApps } from './tunApps.ts'

test('按行拆分，去掉首尾空白，跳过空行与重复行，程序名里可以有空格', () => {
  assert.deepEqual(parseTunApps(' Google Chrome \r\n\ncurl\n\t\ncurl\n/Applications/Postman.app', 'mac'), {
    list: ['Google Chrome', 'curl', '/Applications/Postman.app'],
  })
  assert.deepEqual(parseTunApps('  \n', 'linux'), { list: [] })
})

test('Windows 认盘符、UNC 与设备路径，斜杠开头而不带盘符的不是绝对路径', () => {
  const paths = ['C:\\Program Files\\Google\\Chrome', 'd:/tools/app.exe', '\\\\server\\share\\app.exe', '//server/share/app.exe', '\\\\?\\C:\\app.exe', 'chrome.exe']
  assert.deepEqual(parseTunApps(paths.join('\n'), 'windows'), { list: paths })
  for (const value of ['/usr/bin/curl', '/c/Users/me/app.exe', '\\tools\\app.exe', 'C:app.exe', 'tools\\app.exe', 'bin/curl']) {
    assert.deepEqual(parseTunApps(`chrome\n${value}`, 'windows'), { problem: { kind: 'relative', value } }, value)
  }
})

// 以下结论取自 Go 在 Windows 上的 filepath.VolumeName 与 IsAbs（已在 Windows 上核对），前端照它对齐。
test('Windows 上盘符只看第 2 个字符是不是冒号，以两个分隔符开头的都算绝对路径', () => {
  const accepted = ['1:\\x', '#:\\x', '\\\\', '//', '///x', '\\\\\\x', '\\/x', 'é:x', ':x']
  assert.deepEqual(parseTunApps(accepted.join('\n'), 'windows'), { list: accepted })
  for (const value of ['1:x', '#:x', 'a:', 'C:']) {
    assert.deepEqual(parseTunApps(value, 'windows'), { problem: { kind: 'relative', value } }, value)
  }
})

test(`单项最长 ${MAX_TUN_APP_BYTES} 字节（UTF-8），与后端按字节计一致`, () => {
  assert.deepEqual(parseTunApps('a'.repeat(MAX_TUN_APP_BYTES), 'linux').list?.length, 1)
  for (const value of ['a'.repeat(MAX_TUN_APP_BYTES + 1), 'é'.repeat((MAX_TUN_APP_BYTES + 1) / 2)]) {
    assert.equal(parseTunApps(value, 'linux').problem?.kind, 'invalid')
  }
})

test('Windows 以外的平台只认斜杠开头的路径，带反斜杠的是 Windows 的写法', () => {
  for (const platform of ['linux', 'mac'] as const) {
    assert.deepEqual(parseTunApps('/usr/bin/curl\ncurl', platform), { list: ['/usr/bin/curl', 'curl'] })
    for (const value of ['bin/curl', './curl']) {
      assert.deepEqual(parseTunApps(`chrome\n${value}`, platform), { problem: { kind: 'relative', value } }, value)
    }
    for (const value of ['C:\\Tools\\app.exe', '\\\\server\\share\\app.exe', 'a\\b']) {
      assert.deepEqual(parseTunApps(`chrome\n${value}`, platform), { problem: { kind: 'foreign', value } }, value)
    }
  }
})

test('控制字符与 . / .. 不是有效的程序名', () => {
  for (const value of ['a\u0000b', 'a\u007fb', '.', '..']) {
    assert.deepEqual(parseTunApps(value, 'linux'), { problem: { kind: 'invalid', value } }, value)
  }
})

test(`最多 ${MAX_TUN_APPS} 项`, () => {
  const items = Array.from({ length: MAX_TUN_APPS }, (_, i) => `app${i}`)
  assert.equal(parseTunApps(items.join('\n'), 'linux').list?.length, MAX_TUN_APPS)
  assert.deepEqual(parseTunApps([...items, 'one-more'].join('\n'), 'linux'), { problem: { kind: 'tooMany', max: MAX_TUN_APPS } })
})
