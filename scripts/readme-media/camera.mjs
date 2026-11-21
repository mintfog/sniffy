// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0

import assert from 'node:assert/strict'
import { performance } from 'node:perf_hooks'
import { setTimeout as sleep } from 'node:timers/promises'

// 由 addInitScript 序列化后注入每个页面和 iframe 执行，只能使用浏览器全局对象，不能引用本模块的导入。
export function installCamera() {
  if (window !== top) return
  const pointer = document.createElement('div')
  pointer.style.cssText = 'position:fixed;left:0;top:0;z-index:2147483647;pointer-events:none;display:none'
  pointer.innerHTML = [
    '<svg width="25" height="30" viewBox="0 0 25 30" style="filter:drop-shadow(0 1px 2px #000a)">',
    '<path d="M3 2l17 14-8 .3-3 7z" fill="#fff" stroke="#171821" stroke-width="1.5" stroke-linejoin="round"/>',
    '</svg>',
  ].join('')
  window.readmePointer = { x: 24, y: 24 }
  window.drawReadmePointer = (x, y, clicked) => {
    if (!pointer.isConnected) document.documentElement.append(pointer)
    window.readmePointer = { x, y }
    pointer.style.display = 'block'
    pointer.style.transform = 'translate(' + (x - 3) + 'px,' + (y - 2) + 'px)'
    if (!clicked) return
    const ring = document.createElement('div')
    ring.style.cssText = 'position:absolute;top:-17px;left:-16px;width:38px;height:38px;border:2px solid #66e2ba;border-radius:50%;background:#66e2ba40'
    pointer.prepend(ring)
    ring.animate([{ opacity: 1, scale: '.55' }, { opacity: 0, scale: '1.1' }], 500).onfinish = () => ring.remove()
  }
  document.addEventListener('DOMContentLoaded', () => {
    document.documentElement.style.overflow = 'clip'
    document.body.style.transformOrigin = '0 0'
  })
  const draw = (event) => window.drawReadmePointer(event.clientX, event.clientY, event.type === 'pointerdown')
  addEventListener('pointermove', draw, true)
  addEventListener('pointerdown', draw, true)
}

export async function focusCamera(page, locator, { scale = 1.7, x = .5, y = .5, duration = 460 } = {}) {
  const target = locator && await locator.evaluate((element, { x, y }) => {
    const rect = element.getBoundingClientRect()
    const matrix = new DOMMatrix(getComputedStyle(document.body).transform)
    return { x: (rect.left + rect.width * x - matrix.e) / matrix.a, y: (rect.top + rect.height * y - matrix.f) / matrix.d }
  }, { x, y })
  await page.evaluate(async ({ scale, target, duration }) => {
    const body = document.body
    const current = getComputedStyle(body).transform
    const clamp = (point, size) => Math.min(0, Math.max(size * (1 - scale), size / 2 - point * scale))
    const x = target ? clamp(target.x, innerWidth) : 0
    const y = target ? clamp(target.y, innerHeight) : 0
    const next = target ? 'translate(' + x + 'px,' + y + 'px) scale(' + scale + ')' : 'none'
    const finish = new DOMMatrix(next)
    const start = new DOMMatrix(current)
    if (Math.abs(start.e - finish.e) < 4 && Math.abs(start.f - finish.f) < 4 && start.a === finish.a) return
    body.style.transform = next
    await body.animate([{ transform: current }, { transform: next }], {
      duration, easing: 'cubic-bezier(.45, 0, .25, 1)',
    }).finished
  }, { target, scale, duration })
}

async function screenPoint(locator) {
  return locator.evaluate((element) => {
    const rect = element.getBoundingClientRect()
    const x = Math.round((Math.max(0, rect.left) + Math.min(innerWidth, rect.right)) / 2)
    const y = Math.round((Math.max(0, rect.top) + Math.min(innerHeight, rect.bottom)) / 2)
    return { x, y, hits: element.contains(document.elementFromPoint(x, y)) }
  })
}

async function movePointer(page, target, duration = 450) {
  const origin = await page.evaluate(() => window.readmePointer)
  const steps = Math.ceil(duration / 37.5)
  const started = performance.now()
  for (let step = 1; step <= steps; step++) {
    await sleep(Math.max(0, started + duration * step / steps - performance.now()))
    const t = step / steps
    const progress = t * t * (3 - 2 * t)
    const point = await target()
    await page.mouse.move(origin.x + (point.x - origin.x) * progress, origin.y + (point.y - origin.y) * progress)
  }
}

export async function parkPointer(page, restore) {
  const point = await page.evaluate(() => ({ x: innerWidth - 36, y: innerHeight - 38 }))
  await Promise.all([restore(), movePointer(page, () => point)])
  const isBlank = await page.evaluate(({ x, y }) => {
    const hit = document.elementFromPoint(x, y)
    return hit?.closest('[data-find-region="response"], [data-find-region="body"]') &&
      !hit.closest('button, a, input, [role="option"], tr, [class*="hover:"]')
  }, point)
  assert.ok(isBlank, '结尾指针未落在详情空白处')
}

export async function click(page, locator, { pan, after, around } = {}) {
  await locator.waitFor()
  const motion = pan?.()
  void motion?.catch(() => {})
  const initial = await screenPoint(locator)
  if (!motion) assert.ok(initial.hits, '镜头内点击未命中：' + locator)
  if (around) {
    const menu = await around.boundingBox()
    assert.ok(menu, '动作菜单不可见')
    const origin = await page.evaluate(() => window.readmePointer)
    // 从目标所在列的外侧进入，避免沿途激活其他动作。
    const x = initial.x < menu.x + menu.width / 2 ? menu.x - 6 : menu.x + menu.width + 6
    await movePointer(page, () => ({ x, y: origin.y }), 150)
    await movePointer(page, () => ({ x, y: initial.y }), 200)
    await movePointer(page, () => initial, 100)
  } else {
    await movePointer(page, motion ? () => screenPoint(locator) : () => initial)
  }
  await motion
  const point = motion ? await screenPoint(locator) : initial
  assert.ok(point.hits, '镜头内点击未命中：' + locator)
  if (motion) await page.mouse.move(point.x, point.y)
  await page.mouse.click(point.x, point.y)
  const pointer = await page.evaluate(() => window.readmePointer)
  assert.ok(Math.abs(pointer.x - point.x) < 2 && Math.abs(pointer.y - point.y) < 2, '指针和点击坐标偏离')
  await Promise.all([sleep(600), after?.()])
}

export async function typeText(page, value) {
  for (const character of value) {
    await page.keyboard.insertText(character)
    await sleep(70)
  }
}
