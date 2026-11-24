// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0

import { execFileSync } from 'node:child_process'
import { randomInt } from 'node:crypto'
import { once } from 'node:events'
import { mkdtemp, readFile, rm } from 'node:fs/promises'
import { createServer } from 'node:http'
import { createSecureServer } from 'node:http2'
import { connect } from 'node:net'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { setTimeout as sleep } from 'node:timers/promises'
import { fileURLToPath } from 'node:url'
import sharp from 'sharp'
import { WebSocketServer } from 'ws'

const loopback = '127.0.0.1'
const demoHosts = ['api', 'cdn', 'live', 'ai', 'inventory'].map((name) => name + '.shop.test')
const product = { sku: 'TB-04', name: 'Trail Bottle', price: 24, currency: 'USD' }
const order = {
  id: 'ORD-1042',
  status: 'confirmed',
  customer: { id: 'CUS-208', tier: 'standard' },
  items: [{ ...product, quantity: 2 }],
  shipping: { method: 'standard', estimate: '3–5 days' },
  total: 48,
  currency: 'USD',
}
const completion = {
  en: 'Your order is packed and will ship tomorrow.',
  'zh-CN': '你的订单已打包，预计明天发货。',
}

function json(res, status, body) {
  res.writeHead(status, { 'Content-Type': 'application/json', 'Cache-Control': 'no-store' })
  res.end(JSON.stringify(body))
}

async function readJSON(req) {
  let body = ''
  for await (const chunk of req) body += chunk
  return JSON.parse(body || '{}')
}

async function chat(req, res) {
  const { stream } = await readJSON(req)
  const text = completion[req.headers['accept-language']] ?? completion.en
  if (!stream) {
    json(res, 200, {
      id: 'chatcmpl-demo', object: 'chat.completion', model: 'shop-assistant',
      choices: [{ index: 0, message: { role: 'assistant', content: text }, finish_reason: 'stop' }],
    })
    return
  }
  res.writeHead(200, { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-cache' })
  res.flushHeaders()
  const chunk = (delta, finishReason = null) => {
    res.write('data: ' + JSON.stringify({
      choices: [{ index: 0, delta, finish_reason: finishReason }],
      id: 'chatcmpl-demo', object: 'chat.completion.chunk', created: 1760000000,
      model: 'shop-assistant',
    }) + '\n\n')
  }
  chunk({ role: 'assistant', content: '' })
  await sleep(600)
  for (const character of text) {
    if (res.destroyed) return
    chunk({ content: character })
    await sleep(3620 / text.length)
  }
  chunk({}, 'stop')
  res.end('data: [DONE]\n\n')
}

export function grpcFrame(value) {
  const text = Buffer.from(value)
  // 演示 RPC 的消息只有一个 string 字段，长度小于 128 字节。
  const message = Buffer.concat([Buffer.from([0x0a, text.length]), text])
  const frame = Buffer.alloc(5 + message.length)
  frame.writeUInt32BE(message.length, 1)
  message.copy(frame, 5)
  return frame
}

async function inventory(req, res) {
  for await (const _ of req) {
    // 等待请求流结束再发送库存快照。
  }
  res.writeHead(200, { 'Content-Type': 'application/grpc', Trailer: 'grpc-status' })
  res.write(grpcFrame('TB-04 in stock: 18'))
  res.addTrailers({ 'grpc-status': '0' })
  res.end()
}

async function respond(req, res, productImage) {
  const path = new URL(req.url, 'https://api.shop.test').pathname
  await sleep(path.endsWith('.png') ? randomInt(30, 81) : randomInt(20, 201))
  switch (path) {
    case '/v1/chat/completions':
      await chat(req, res)
      return
    case '/shop.inventory.v1.Inventory/GetStock':
      await inventory(req, res)
      return
    case '/products/trail-bottle.png':
    case '/products/daypack.png':
      res.writeHead(200, { 'Content-Type': 'image/png' })
      res.end(productImage)
      return
    case '/v1/orders/ORD-1042':
      json(res, 200, order)
      return
    case '/v1/checkout/quote': {
      const { shipping = 'standard' } = await readJSON(req)
      const shippingFee = shipping === 'express' ? 8 : 0
      json(res, 200, { shipping, shippingFee, total: 48 + shippingFee, currency: 'USD' })
      return
    }
    case '/v1/stock/TB-04':
      json(res, 200, { sku: product.sku, available: 18, source: 'warehouse' })
      return
    case '/v1/orders':
      json(res, req.method === 'POST' ? 201 : 200, { orders: [order] })
      return
    case '/v1/products':
    case '/v1/products/TB-04':
      json(res, 200, { products: [product, { sku: 'DP-12', name: 'Daypack', price: 59, currency: 'USD' }] })
      return
    case '/v1/users/CUS-208':
      json(res, 200, { id: 'CUS-208', displayName: 'Demo Shopper', tier: 'standard' })
      return
    case '/v1/cart':
    case '/v1/cart/items':
      json(res, 200, { id: 'CART-316', items: order.items, subtotal: 48, currency: 'USD' })
      return
    case '/v1/shipping/options':
      json(res, 200, { options: [{ method: 'standard', fee: 0 }, { method: 'express', fee: 8 }] })
      return
    case '/v1/warehouses':
      json(res, 200, { warehouses: [{ id: 'WH-08', region: 'west' }, { id: 'WH-12', region: 'east' }] })
      return
    case '/v1/payment/methods':
      json(res, 200, { methods: ['card', 'wallet'] })
      return
    case '/v1/catalog/categories':
      json(res, 200, { categories: ['outdoors', 'travel', 'accessories'] })
      return
    case '/v1/orders/ORD-1042/tracking':
      json(res, 200, { orderId: order.id, status: 'label_created' })
      return
    case '/v1/search':
      json(res, 200, { results: [product] })
      return
    case '/v1/coupons/SPRING15':
      json(res, 404, { error: { code: 'coupon_expired', message: 'This offer has ended.' } })
      return
    case '/v1/orders/ORD-1042/receipt':
      json(res, 200, { orderId: order.id, receiptId: 'RCT-901', amount: 48, currency: 'USD' })
      return
    default:
      json(res, 404, { error: { code: 'not_found', message: 'Resource not found.' } })
  }
}

export async function startDemo(upstreamPort = 18443, originPort = 18444) {
  const certDir = await mkdtemp(join(tmpdir(), 'sniffy-demo-cert-'))
  const keyPath = join(certDir, 'server.key')
  const certPath = join(certDir, 'server.pem')
  const certificate = await (async () => {
    try {
      execFileSync('openssl', [
        'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '1',
        '-subj', '/CN=api.shop.test', '-addext', 'subjectAltName=DNS:*.shop.test',
        '-keyout', keyPath, '-out', certPath,
      ], { stdio: 'pipe' })
      return { key: await readFile(keyPath), cert: await readFile(certPath) }
    } finally {
      await rm(certDir, { recursive: true, force: true })
    }
  })()
  const productImage = await sharp({ create: {
    width: 480, height: 360, channels: 3, background: '#397d77',
  } }).png().toBuffer()
  const origin = createSecureServer({ ...certificate, allowHTTP1: true }, (req, res) => {
    respond(req, res, productImage).catch((error) => {
      console.error('演示服务处理请求失败：' + req.url, error)
      res.destroy(error)
    })
  })
  const sockets = new WebSocketServer({ server: origin, path: '/v1/orders/events' })
  sockets.on('connection', async (socket) => {
    await sleep(randomInt(20, 201))
    if (socket.readyState !== socket.OPEN) return
    socket.send(JSON.stringify({ type: 'order.updated', orderId: order.id, status: 'packed' }))
    socket.on('message', () => {
      socket.send(JSON.stringify({ type: 'subscription.ready', topic: 'orders' }))
    })
  })
  const upstream = createServer((_req, res) => res.writeHead(405).end())
  const connections = new Set()
  upstream.on('connection', (socket) => {
    connections.add(socket)
    socket.once('close', () => connections.delete(socket))
  })
  // CONNECT 保留 Sniffy 捕获的目标域名，只有上游实际拨号落在回环地址。
  upstream.on('connect', (req, client, head) => {
    if (!demoHosts.some((host) => req.url === host + ':443')) {
      client.end('HTTP/1.1 403 Forbidden\r\n\r\n')
      return
    }
    const target = connect(origin.address().port, loopback, () => {
      client.write('HTTP/1.1 200 Connection Established\r\n\r\n')
      if (head.length) target.write(head)
      client.pipe(target).pipe(client)
    })
    target.on('error', () => client.destroy())
    client.on('error', () => target.destroy())
    client.on('close', () => target.destroy())
  })
  const close = () => {
    for (const socket of connections) socket.destroy()
    for (const socket of sockets.clients) socket.terminate()
    upstream.close()
    origin.close()
  }
  try {
    await once(origin.listen(originPort, loopback), 'listening')
    await once(upstream.listen(upstreamPort, loopback), 'listening')
    return {
      upstream: loopback + ':' + upstream.address().port,
      origin: loopback + ':' + origin.address().port,
      close,
    }
  } catch (error) {
    close()
    throw error
  }
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const demo = await startDemo(Number(process.argv[2] ?? 18443), Number(process.argv[3] ?? 18444))
  console.log('演示 HTTPS 源站 ' + demo.origin + '，上游代理 ' + demo.upstream)
  process.once('SIGINT', demo.close)
  process.once('SIGTERM', demo.close)
}
