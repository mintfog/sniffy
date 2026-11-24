// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0

import assert from 'node:assert/strict'
import { execFileSync, spawn } from 'node:child_process'
import { once } from 'node:events'
import { copyFile, mkdir, mkdtemp, readFile, rm, stat, writeFile } from 'node:fs/promises'
import { createConnection, createServer } from 'node:net'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { performance } from 'node:perf_hooks'
import { setTimeout as sleep } from 'node:timers/promises'
import { fileURLToPath } from 'node:url'
import { chromium } from 'playwright'
import sharp from 'sharp'
import { click, focusCamera, installCamera, parkPointer, typeText } from './camera.mjs'
import { startDemo } from './demo-service.mjs'
import { chatPath, createTraffic, orderPath, quotePath, seedCount, stockPath } from './traffic.mjs'

const repo = fileURLToPath(new URL('../../', import.meta.url))
const output = fileURLToPath(new URL('out/', import.meta.url))
// CDN 边缘和 GitHub 图片代理都会缓存同名对象，每批素材换用新的版本前缀，README 图片地址随之更新。
const mediaPrefix = 'readme/v1'
const heroViewport = { width: 1440, height: 900 }
const videoViewport = { width: 1152, height: 720 }
const proxyPort = 8080
const row = 'div[class~="group/row"]'
const ffmpeg = process.env.FFMPEG || 'ffmpeg'
const language = [
  { dir: 'zh-CN', lang: 'zh-Hans', locale: 'zh-CN', rule: '模拟缺货' },
  { dir: 'en', lang: 'en', locale: 'en-US', rule: 'Simulate out of stock' },
]

async function freePort() {
  const listener = createServer()
  await once(listener.listen(0, '127.0.0.1'), 'listening')
  const { port } = listener.address()
  await new Promise((done) => listener.close(done))
  return port
}

async function openWorkbench(binary, demo, state) {
  const browserPort = await freePort()
  const url = 'http://127.0.0.1:' + browserPort
  // Wails 的 WAILS_SERVER_* 环境变量优先于 ServerOptions，显式设置以免继承的同名变量改掉监听地址。
  const child = spawn(binary, [
    '-state', state, '-upstream', demo.upstream, '-origin', demo.origin,
    '-proxy-port', String(proxyPort), '-browser-port', String(browserPort),
  ], { cwd: repo, env: { ...process.env, WAILS_SERVER_HOST: '127.0.0.1', WAILS_SERVER_PORT: String(browserPort) } })
  let log = ''
  child.stdout.on('data', (data) => { log = (log + data).slice(-8000) })
  child.stderr.on('data', (data) => { log = (log + data).slice(-8000) })
  const exit = once(child, 'exit')
  const stop = async () => {
    child.kill()
    await exit
  }
  try {
    for (let attempt = 0; attempt < 60 && child.exitCode === null; attempt++) {
      const ready = await fetch(url, { signal: AbortSignal.timeout(500) }).catch(() => null)
      if (ready?.ok) {
        await ready.arrayBuffer()
        return {
          url, proxy: 'http://127.0.0.1:' + proxyPort, proxyPort, upstream: demo.upstream,
          ca: await readFile(join(state, 'sniffy-ca.pem')), stop,
        }
      }
      await sleep(100)
    }
    throw new Error('Wails 演示工作台无法启动\n' + log)
  } catch (error) {
    await stop()
    throw error
  }
}

function escapeRegex(value) {
  return [...value].map((character) => '.*+?^${}()|[]\\'.includes(character) ? '\\' + character : character).join('')
}

async function browserContext(browser, instance, locale, videoDir) {
  const context = await browser.newContext({
    locale, viewport: videoDir ? videoViewport : heroViewport, colorScheme: 'dark',
    ...(videoDir ? { recordVideo: { dir: videoDir, size: videoViewport } } : { deviceScaleFactor: 2 }),
  })
  // 原生 macOS 窗口把三个控制按钮叠在前端为其预留的位置。
  await context.addInitScript(({ port, upstreamAddr }) => {
    if (!localStorage.getItem('sniffy-prefs')) {
      localStorage.setItem('sniffy-prefs', JSON.stringify({
        version: 5,
        state: { port, upstream: true, upstreamAddr, systemProxy: false, autoSystemProxy: false },
      }))
    }
    document.addEventListener('DOMContentLoaded', () => {
      const style = document.createElement('style')
      style.textContent = [
        '.wb-mac-titlebar { position: relative; }',
        '.wb-mac-titlebar::before { content: ""; position: absolute; inset: 0 auto 0 0;',
        'width: 72px; pointer-events: none; background:',
        'radial-gradient(circle at 20px 50%, #ff5f57 5.5px, transparent 6.5px),',
        'radial-gradient(circle at 40px 50%, #febc2e 5.5px, transparent 6.5px),',
        'radial-gradient(circle at 60px 50%, #28c840 5.5px, transparent 6.5px); }',
      ].join(' ')
      document.head.append(style)
    })
  }, { port: String(instance.proxyPort), upstreamAddr: 'http://' + instance.upstream })
  await context.route('**/wails/runtime', async (route) => {
    const request = route.request()
    const method = request.method() === 'POST' && request.postDataJSON()?.args?.methodName
    // 代理地址栏统一展示回环地址，画面不带采集机器的网络信息。
    if (typeof method === 'string' && method.endsWith('.GetLANIPs')) await route.fulfill({ json: [] })
    else await route.continue()
  })
  return context
}

async function load(page, url, query = '') {
  await page.goto(url + '/?theme=dark' + query)
  await page.locator('.wb-root, .wb-portal').first().waitFor()
  await page.evaluate(() => document.fonts.ready.then(() => true))
}

const trafficRow = (page, path) => page.locator(row).filter({
  has: page.getByText(path, { exact: true }),
}).last()

async function hero(browser, instance, lang, t, directory) {
  const context = await browserContext(browser, instance, lang.locale)
  try {
    const page = await context.newPage()
    await load(page, instance.url)
    await trafficRow(page, orderPath).click()
    await page.getByRole('button', { name: new RegExp('^' + escapeRegex(t.detail.req.tab.headers)) }).first().click()
    await page.getByText('ORD-1042', { exact: false }).last().waitFor()
    await page.getByText('Shop Desktop/2.4', { exact: true }).waitFor()
    await page.getByText('req-demo-1042', { exact: true }).waitFor()
    assert.equal(await page.locator(row).count(), seedCount)
    assert.equal(await page.getByTitle(t.traffic.modified, { exact: true }).count(), 0)
    await page.mouse.move(0, heroViewport.height - 2)
    const png = await page.screenshot({ animations: 'disabled' })
    const file = join(directory, 'hero.png')
    await sharp(png).png({ compressionLevel: 9 }).toFile(file)
    if ((await stat(file)).size > 1_000_000) {
      await sharp(png).png({ palette: true, quality: 95, dither: 0 }).toFile(file)
    }
    assert.ok((await stat(file)).size <= 1_000_000, '主图超过 1 MB')
  } finally {
    await context.close()
  }
}

function requestTab(page, name) {
  return page.locator('[data-find-region="request"]')
    .getByRole('button', { name: new RegExp('^' + escapeRegex(name)) })
}

const trafficButton = (page) => page.locator('nav').first().getByRole('button').first()

async function sse(page, traffic, t, focus) {
  const sessions = page.locator(row).filter({ has: page.getByText(chatPath, { exact: true }) })
  await sessions.first().waitFor()
  const next = sessions.nth(await sessions.count())
  const streamed = traffic.chat()
  await click(page, next)
  const messages = page.locator('[data-find-region="messages"]')
  await focus(messages, { x: .52, y: .4, scale: 1.8 })
  await page.getByText('[DONE]', { exact: true }).last().waitFor()
  const result = await streamed
  assert.equal(result.status, 200)
  assert.match(result.body.toString(), /chat\.completion\.chunk/)
  const events = page.getByTitle(t.detail.ws.received, { exact: true })
  // 倒数第 4 条是回复的最后一个文字，其后依次是句末标点、finish_reason 为 stop 的 chunk 和 [DONE]。
  const event = events.nth(await events.count() - 4)
  await event.scrollIntoViewIfNeeded()
  await click(page, event)
  const body = page.locator('[data-find-region="body"]')
  const nodes = body.locator('div[class~="group/jn"]')
  await focus(nodes.nth(1), { x: .17, y: .5, scale: 1.8 })
  await click(page, nodes.nth(2).locator('span').first())
  await click(page, body.getByText('"delta"', { exact: true }))
  await body.getByText('"content"', { exact: true }).waitFor()
  await sleep(1500)
}

async function breakpoint(page, traffic, t, focus) {
  await click(page, page.getByRole('switch').first())
  const pending = traffic.quote()
  await page.getByText(quotePath, { exact: false }).first().waitFor()
  await sleep(1500)
  await click(page, page.getByRole('button', { name: t.breakpoints.paused.edit, exact: true }))
  await click(page, page.getByRole('button', { name: t.breakpoints.edit.tabBody, exact: true }))
  const editor = page.locator('.cm-content[contenteditable="true"]')
  const line = editor.locator('.cm-line').filter({ hasText: /"shipping": "standard"/ })
  await click(page, line, { pan: () => focus(line, { x: .14, y: .5, scale: 1.75 }) })
  await page.keyboard.press('End')
  await page.keyboard.press('ArrowLeft')
  for (const _ of 'standard') await page.keyboard.press('Shift+ArrowLeft')
  await typeText(page, 'express')
  await editor.locator('.cm-line').filter({ hasText: /"shipping": "express"/ }).waitFor()
  await sleep(1550)
  await click(page, page.getByRole('button', { name: t.breakpoints.paused.resumeEdited, exact: true }), { pan: () => focus() })
  const response = await pending
  assert.equal(response.status, 200)
  assert.deepEqual(JSON.parse(response.body), { shipping: 'express', shippingFee: 8, total: 56, currency: 'USD' })
  await click(page, trafficButton(page))
  await click(page, trafficRow(page, quotePath))
  const fee = page.getByText('"shippingFee"', { exact: true })
  await focus(fee, { scale: 1.8 })
  await sleep(1500)
}

const mockResponse = { sku: 'TB-04', available: 0, source: 'mock' }

async function createMockRule(page, lang, t, focus) {
  const name = page.getByRole('textbox', { name: t.rules.basic.name, exact: true })
  const field = page.getByRole('combobox', { name: t.rules.cond.field, exact: true })
  const path = page.getByRole('textbox', { name: t.rules.cond.matchValue, exact: true })
  const addAction = page.getByRole('button', { name: t.rules.action.add, exact: true })
  const actions = page.getByRole('listbox', { name: t.rules.action.choose, exact: true })
  const mockAction = actions.getByRole('option').filter({ hasText: t.rules.action.mock })
  const body = page.getByRole('textbox', { name: t.rules.param.responseBodyJson, exact: true })
  await click(page, page.getByRole('button', { name: t.rules.newRule, exact: true }))
  await name.press('ControlOrMeta+a')
  await typeText(page, lang.rule)
  await sleep(1000)
  await click(page, field)
  await click(page, page.getByRole('option', { name: 'Path', exact: true }))
  await click(page, path, { pan: () => focus(path, { x: .23, scale: 1.6 }) })
  await typeText(page, stockPath)
  await sleep(1000)
  await click(page, addAction, { pan: () => focus() })
  assert.ok(await actions.getByRole('option').count() > 1, '应展示完整动作菜单')
  await click(page, mockAction, { around: actions })
  await click(page, body, { pan: () => focus(body, { x: .23, scale: 1.6 }) })
  await body.fill(JSON.stringify(mockResponse))
  await Promise.all([page.getByText(t.rules.save.saved, { exact: true }).waitFor(), sleep(1550)])
}

async function showMockHit(page, traffic, lang, t, focus) {
  const response = await traffic.stock()
  assert.equal(response.status, 200)
  assert.deepEqual(JSON.parse(response.body), mockResponse)
  const result = page.locator(row).filter({ has: page.getByTitle(t.traffic.modified, { exact: true }) })
    .filter({ has: page.getByText(stockPath, { exact: true }) }).last()
  const responsePane = page.locator('[data-find-region="response"]')
  const mockBody = responsePane.getByText('"mock"', { exact: true })
  await click(page, result, { after: async () => {
    await mockBody.waitFor()
    await focus(responsePane.getByText('"available"', { exact: true }), { scale: 1.6 })
    for (const field of ['sku', 'available', 'source']) {
      const bounds = await responsePane.getByText('"' + field + '"', { exact: true }).boundingBox()
      assert.ok(bounds && bounds.y >= 0 && bounds.y + bounds.height <= videoViewport.height)
    }
  } })
  await sleep(1500)
  const execution = requestTab(page, t.detail.executions.title)
  await click(page, execution, { pan: () => focus(page.locator('[data-find-region="request"]'), { y: .7, scale: 1.6 }) })
  await page.getByText(lang.rule, { exact: true }).waitFor()
  await mockBody.waitFor()
}

async function captureFrames(page, directory, prefix) {
  const cdp = await page.context().newCDPSession(page)
  const frames = []
  const started = performance.now()
  let stopped = false
  const capture = async () => {
    while (!stopped) {
      const at = performance.now()
      const { data } = await cdp.send('Page.captureScreenshot', { format: 'png', optimizeForSpeed: true })
      const file = prefix + '-' + frames.length + '.png'
      await writeFile(join(directory, file), Buffer.from(data, 'base64'))
      frames.push({ file, at })
      await sleep(Math.max(0, started + frames.length * (1000 / 15) - performance.now()))
    }
  }
  const capturing = capture()
  void capturing.catch(() => {})
  return async () => {
    const ended = performance.now()
    stopped = true
    try {
      await capturing
      return frames.map(({ file, at }, index) => ({
        file,
        duration: Math.max(1, (frames[index + 1]?.at ?? ended) - at) / 1000,
      }))
    } finally {
      await cdp.detach()
    }
  }
}

async function crossfade(directory, from, to) {
  const incoming = await sharp(join(directory, to.file)).removeAlpha().png().toBuffer()
  const frames = []
  for (let index = 1; index <= 5; index++) {
    const overlay = await sharp(incoming).ensureAlpha(index / 6).png().toBuffer()
    const file = 'fade-' + index + '.png'
    await sharp(join(directory, from.file)).composite([{ input: overlay }]).removeAlpha().png().toFile(join(directory, file))
    frames.push({ file, duration: .06 })
  }
  return frames
}

async function writeFrameTimeline(directory, frames) {
  const input = join(directory, 'frames.ffconcat')
  const lines = frames.flatMap(({ file, duration }) => [
    "file '" + file + "'",
    'duration ' + duration.toFixed(3),
  ])
  await writeFile(input, ['ffconcat version 1.0', ...lines, "file '" + frames.at(-1).file + "'", ''].join('\n'))
  return input
}

async function record(browser, work, instance, lang, clip, compact = false) {
  const context = await browserContext(browser, instance, lang.locale, work)
  context.setDefaultTimeout(10000)
  await context.addInitScript(installCamera)
  const frameDir = join(work, [lang.dir, clip.name, compact ? 'compact' : 'default'].join('-'))
  let page
  let stopFrames
  try {
    await mkdir(frameDir)
    const scenes = clip.scenes ?? [clip]
    const pages = []
    // 提前加载工作台，切换窗口时可直接开始采集。
    for (const scene of scenes) {
      const window = await context.newPage()
      page = window
      await load(window, instance.url, scene.query)
      await scene.ready?.(window)
      pages.push(window)
    }
    const frames = []
    for (const [index, scene] of scenes.entries()) {
      page = pages[index]
      await page.bringToFront()
      await sleep(220)
      // WebM 保留各窗口的原始录像，GIF 使用无损帧以减小调色后的文件。
      stopFrames = await captureFrames(page, frameDir, 'scene-' + index)
      const focus = (locator, options) => focusCamera(page, locator, compact && locator
        ? { ...options, scale: 1.6, duration: 400 }
        : options)
      await sleep(index === 0 ? 300 : 80)
      await scene.action(page, focus)
      if (index === scenes.length - 1) {
        await parkPointer(page, focus)
        await sleep(2550)
      } else {
        await focus()
        await sleep(120)
      }
      const stop = stopFrames
      stopFrames = null
      const captured = await stop()
      if (frames.length) frames.push(...await crossfade(frameDir, frames.at(-1), captured[0]))
      frames.push(...captured)
    }
    const input = await writeFrameTimeline(frameDir, frames)
    const duration = frames.reduce((total, frame) => total + frame.duration, 0)
    await context.close()
    await clip.cleanup?.()
    return { input, duration, name: clip.name }
  } catch (error) {
    if (process.env.README_KEEP_TEMP && page && !page.isClosed()) {
      await page.screenshot({ path: join(work, lang.dir + '-' + clip.name + '-error.png') })
    }
    throw error
  } finally {
    if (stopFrames) await stopFrames().catch(() => {})
    await context.close()
  }
}

async function gif({ input, duration, name }, directory, rates) {
  const file = join(directory, name + '.gif')
  for (const rate of rates) {
    const filters = [
      'fps=' + rate + ',split[frames][sample]',
      '[sample]palettegen=stats_mode=diff[palette]',
      '[frames][palette]paletteuse=dither=none:diff_mode=rectangle',
    ]
    execFileSync(ffmpeg, [
      '-hide_banner', '-loglevel', 'error', '-y', '-f', 'concat', '-safe', '0', '-i', input,
      '-t', String(duration), '-filter_complex_threads', '2', '-filter_complex', filters.join(';'), '-loop', '0', file,
    ])
    if ((await stat(file)).size <= 5_000_000) return rate
  }
  return 0
}

async function makeClip(browser, work, instance, lang, clip, directory) {
  const normal = await record(browser, work, instance, lang, clip)
  const rate = await gif(normal, directory, [15, 12])
  if (rate) return rate + ' fps，常规模式'

  await clip.reset?.()
  const compact = await record(browser, work, instance, lang, clip, true)
  if (await gif(compact, directory, [12])) return '12 fps，紧凑模式'

  const file = join(directory, clip.name + '.gif')
  execFileSync(process.env.GIFSICLE || 'gifsicle', ['-O3', '--lossy=35', '--batch', file])
  if ((await stat(file)).size > 5_000_000) throw new Error(clip.name + ' 超过 5 MB')
  return '12 fps，紧凑模式，gifsicle 压缩'
}

async function bridgeCall(instance, method, args = []) {
  const response = await fetch(instance.url + '/wails/runtime', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-Wails-Client-Id': 'readme-media' },
    body: JSON.stringify({ object: 0, method: 0, args: {
      'call-id': 'readme-media-' + method,
      methodName: 'github.com/mintfog/sniffy/internal/desktop.Bridge.' + method,
      args,
    } }),
  })
  if (!response.ok) throw new Error(method + ' 失败：' + await response.text())
  const result = await response.text()
  return result ? JSON.parse(result) : null
}

async function checkProxyPort() {
  const occupied = () => new Error('Sniffy 代理端口 ' + proxyPort + ' 已被占用，请释放后重新生成。')
  // macOS 的通配监听可能允许更具体的地址绑定，先检查现有实例是否已经接受连接。
  for (const host of ['127.0.0.1', '::1']) {
    const listening = await new Promise((resolve) => {
      const socket = createConnection({ host, port: proxyPort })
      socket.once('connect', () => { socket.destroy(); resolve(true) })
      socket.once('error', () => resolve(false))
    })
    if (listening) throw occupied()
  }
  const listener = createServer()
  try {
    await once(listener.listen(proxyPort, '127.0.0.1'), 'listening')
  } catch (error) {
    if (error.code === 'EADDRINUSE') throw occupied()
    throw error
  }
  await new Promise((done) => listener.close(done))
}

async function main() {
  if (!['darwin', 'linux'].includes(process.platform)) throw new Error('generate 目前只支持 macOS 和 Linux。')
  await checkProxyPort()
  const work = await mkdtemp(join(tmpdir(), 'sniffy-readme-'))
  let browser
  let demo
  try {
    const binary = join(work, 'workbench')
    execFileSync(ffmpeg, ['-version'], { stdio: 'ignore' })
    execFileSync('npm', ['--prefix', 'web', 'run', 'build'], { cwd: repo, stdio: 'inherit' })
    execFileSync('go', ['build', '-buildvcs=false', '-tags', 'desktop,server', '-o', binary, './scripts/readme-media/workbench'], {
      cwd: repo, stdio: 'inherit',
    })
    demo = await startDemo(0, 0)
    browser = await chromium.launch({ channel: process.env.README_BROWSER_CHANNEL || undefined })
    for (const lang of language) {
      const directory = join(work, lang.dir)
      await mkdir(directory)
      const t = JSON.parse(await readFile(join(repo, 'web/src/i18n/locales', lang.lang + '.json')))
      const instance = await openWorkbench(binary, demo, join(work, lang.dir + '-profile'))
      try {
        const traffic = createTraffic(instance.proxy, instance.ca, lang.dir)
        await traffic.seed()
        await hero(browser, instance, lang, t, directory)
        console.log(lang.dir + ' 主图采集完成')
        const clips = [
          { name: 'sse-stream', action: (page, focus) => sse(page, traffic, t, focus) },
          {
            name: 'breakpoint', query: '&view=breakpoints',
            action: (page, focus) => breakpoint(page, traffic, t, focus),
            cleanup: () => bridgeCall(instance, 'SetGlobalBreak', [false, false]),
          },
          {
            name: 'mock-rule',
            scenes: [
              {
                query: '&w=rules',
                ready: (page) => page.getByRole('button', { name: t.rules.newRule, exact: true }).waitFor(),
                action: (page, focus) => createMockRule(page, lang, t, focus),
              },
              {
                ready: (page) => page.getByText('Shop Desktop', { exact: true }).first().waitFor(),
                action: (page, focus) => showMockHit(page, traffic, lang, t, focus),
              },
            ],
            reset: async () => {
              for (const rule of await bridgeCall(instance, 'GetRules') ?? []) {
                if (rule.name === lang.rule) await bridgeCall(instance, 'DeleteRule', [rule.id])
              }
            },
          },
        ]
        for (const clip of clips) {
          const encoding = await makeClip(browser, work, instance, lang, clip, directory)
          console.log(lang.dir + ' ' + clip.name + '.gif 导出完成（' + encoding + '）')
        }
      } finally {
        await instance.stop()
      }
    }
    // out/ 与桶根目录逐级对应，先清掉上一批输出，整个目录即可原样上传。
    await rm(output, { recursive: true, force: true })
    for (const { dir } of language) {
      const destination = join(output, mediaPrefix, dir)
      await mkdir(destination, { recursive: true })
      for (const name of ['hero.png', 'sse-stream.gif', 'breakpoint.gif', 'mock-rule.gif']) {
        await copyFile(join(work, dir, name), join(destination, name))
      }
    }
    console.log('R2 上传目录：' + output)
  } finally {
    await browser?.close()
    demo?.close()
    if (process.env.README_KEEP_TEMP) console.log('临时采集目录：' + work)
    else await rm(work, { recursive: true, force: true })
  }
}

if (process.argv[1] === fileURLToPath(import.meta.url)) await main()
