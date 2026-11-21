// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0

import assert from 'node:assert/strict'
import { once } from 'node:events'
import { readFile } from 'node:fs/promises'
import http from 'node:http'
import https from 'node:https'
import http2 from 'node:http2'
import tls from 'node:tls'
import { fileURLToPath } from 'node:url'
import { HttpsProxyAgent } from 'https-proxy-agent'
import { WebSocket } from 'ws'
import { grpcFrame } from './demo-service.mjs'

export const orderPath = '/v1/orders/ORD-1042'
export const quotePath = '/v1/checkout/quote'
export const stockPath = '/v1/stock/TB-04'
export const chatPath = '/v1/chat/completions'
export const seedCount = 26
const grpcPath = '/shop.inventory.v1.Inventory/GetStock'

export function createTraffic(proxy, ca, locale = 'en') {
  const proxyURL = new URL(proxy)
  const agent = new HttpsProxyAgent(proxyURL)
  const headers = {
    'User-Agent': 'Shop Desktop/2.4',
    'Accept-Language': locale,
    'X-Client-Version': '2.4.1',
  }

  async function request(path, { host = 'api', method = 'GET', body, accept = 'application/json', extraHeaders } = {}) {
    const payload = body === undefined ? undefined : JSON.stringify(body, null, 2)
    return new Promise((resolve, reject) => {
      const req = https.request('https://' + host + '.shop.test' + path, {
        method, agent, ca,
        headers: { ...headers, Accept: accept, ...extraHeaders, ...(payload && {
          'Content-Type': 'application/json',
          'Content-Length': Buffer.byteLength(payload),
        }) },
      }, async (res) => {
        try {
          const chunks = []
          for await (const chunk of res) chunks.push(chunk)
          resolve({ status: res.statusCode, body: Buffer.concat(chunks) })
        } catch (error) {
          reject(error)
        }
      })
      req.once('error', reject)
      req.setTimeout(25000, () => req.destroy(new Error('演示请求超时：' + path)))
      req.end(payload)
    })
  }

  async function websocket() {
    const socket = new WebSocket('wss://live.shop.test/v1/orders/events', {
      agent, ca, headers, handshakeTimeout: 5000,
    })
    try {
      const first = once(socket, 'message')
      assert.equal(JSON.parse(String((await first)[0])).type, 'order.updated')
      const reply = once(socket, 'message')
      socket.send(JSON.stringify({ action: 'subscribe', topic: 'orders' }))
      assert.equal(JSON.parse(String((await reply)[0])).type, 'subscription.ready')
    } finally {
      const closed = once(socket, 'close')
      socket.close()
      await closed
    }
  }

  async function grpc() {
    const host = 'inventory.shop.test'
    const tunnel = http.request(proxyURL, {
      method: 'CONNECT', path: host + ':443', headers: { Host: host + ':443' },
    })
    tunnel.setTimeout(5000, () => tunnel.destroy(new Error('gRPC 隧道连接超时')))
    const connected = once(tunnel, 'connect')
    tunnel.end()
    const [response, socket, head] = await connected
    if (response.statusCode !== 200) {
      socket.destroy()
      throw new Error('gRPC 隧道连接失败：' + response.statusCode)
    }
    if (head.length) socket.unshift(head)
    const client = http2.connect('https://' + host, {
      createConnection: () => tls.connect({ socket, ca, servername: host, ALPNProtocols: ['h2'] }),
    })
    try {
      await once(client, 'connect')
      const stream = client.request({
        ':method': 'POST', ':path': grpcPath, 'content-type': 'application/grpc', te: 'trailers',
      })
      stream.setTimeout(5000, () => stream.destroy(new Error('gRPC 请求超时')))
      let status
      stream.on('trailers', (trailers) => { status = trailers['grpc-status'] })
      stream.end(grpcFrame('TB-04'))
      const chunks = []
      for await (const chunk of stream) chunks.push(chunk)
      assert.equal(status, '0')
      assert.deepEqual(Buffer.concat(chunks), grpcFrame('TB-04 in stock: 18'))
    } finally {
      client.destroy()
    }
  }

  const chat = () => request(chatPath, {
    host: 'ai', method: 'POST', accept: 'text/event-stream',
    body: {
      model: 'shop-assistant', stream: true,
      messages: [{ role: 'user', content: locale === 'zh-CN' ? '我的订单什么时候发货？' : 'When will my order ship?' }],
    },
  })
  const quote = () => request(quotePath, {
    method: 'POST',
    body: {
      cartId: 'CART-316',
      items: [{ sku: 'TB-04', quantity: 2 }],
      address: { city: 'Port Alder', postalCode: '10040' },
      shipping: 'standard',
    },
  })
  const stock = () => request(stockPath)

  async function seed() {
    const requests = [
      ['/v1/products?category=outdoors'],
      ['/products/trail-bottle.png', { host: 'cdn', accept: 'image/png' }],
      ['/v1/products/TB-04'],
      ['/v1/users/CUS-208'],
      ['/v1/cart'],
      ['/v1/cart/items', { method: 'POST', body: { sku: 'TB-04', quantity: 2 } }],
      ['/v1/shipping/options'],
      ['/v1/search?q=trail+bottle'],
      ['/v1/coupons/SPRING15', {}, 404],
      ['/v1/orders', { method: 'POST', body: { cartId: 'CART-316' } }, 201],
      ['/v1/orders?limit=10'],
      ['/products/daypack.png', { host: 'cdn', accept: 'image/png' }],
      ['/v1/products?category=travel'],
      [stockPath],
      ['/v1/shipping/options?region=west'],
      ['/v1/orders/ORD-1042/receipt'],
      ['/v1/warehouses'],
      ['/v1/payment/methods'],
      ['/v1/catalog/categories'],
      ['/v1/products?sort=price'],
      ['/v1/orders/ORD-1042/tracking'],
      ['/v1/search?q=daypack'],
    ]
    for (const [path, options, status = 200] of requests) {
      assert.equal((await request(path, options)).status, status, path)
    }
    await websocket()
    await grpc()
    const stream = await chat()
    assert.equal(stream.status, 200)
    assert.match(stream.body.toString(), /data: \[DONE\]/)
    assert.equal((await request(orderPath, { extraHeaders: {
      'Accept-Encoding': 'gzip, deflate, br',
      'X-Request-Id': 'req-demo-1042',
    } })).status, 200)
  }

  return { seed, chat, quote, stock }
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const [, , root, proxy = 'http://127.0.0.1:8080', scenario = 'seed', locale = 'en'] = process.argv
  if (!root || !['seed', 'chat', 'quote', 'stock'].includes(scenario)) {
    throw new Error('用法：npm run traffic -- <Sniffy 根证书 PEM> [代理地址] [seed|chat|quote|stock] [en|zh-CN]')
  }
  const traffic = createTraffic(proxy, await readFile(root), locale)
  const result = await traffic[scenario]()
  console.log(result ? result.body.toString() : '演示流量已发送')
}
