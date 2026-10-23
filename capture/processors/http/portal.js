'use strict'

const host = document.getElementById('proxy-host')
const shareURL = document.getElementById('share-url')
const qr = document.getElementById('share-qr')
const copyStatus = document.getElementById('copy-status')

for (const input of document.querySelectorAll('input[name="network"]')) {
  input.addEventListener('change', () => {
    host.textContent = input.value
    if (!shareURL || !qr) return
    const address = input.value.includes(':') ? '[' + input.value + ']' : input.value
    const url = 'http://' + address + ':' + document.body.dataset.port + '/'
    shareURL.href = url
    shareURL.textContent = url
    qr.src = '/sniffy-qr.png?host=' + encodeURIComponent(input.value)
  })
}

for (const button of document.querySelectorAll('[data-copy]')) {
  button.addEventListener('click', async () => {
    const target = document.getElementById(button.dataset.copy)
    const text = target.textContent
    try {
      if (navigator.clipboard && window.isSecureContext) {
        await navigator.clipboard.writeText(text)
      } else {
        // 局域网 HTTP 页面没有 Clipboard API，保留浏览器的选区复制路径。
        const selection = window.getSelection()
        const range = document.createRange()
        range.selectNodeContents(target)
        selection.removeAllRanges()
        selection.addRange(range)
        if (!document.execCommand('copy')) throw new Error('复制失败')
        selection.removeAllRanges()
      }
      copyStatus.textContent = '已复制：' + text
      button.title = '已复制'
    } catch {
      copyStatus.textContent = '复制失败，请选择地址后手动复制。'
      button.title = '请手动复制'
    }
  })
}

// iPadOS 的桌面模式会发送 Macintosh UA，触摸能力用于修正服务器端的默认选项。
if (navigator.maxTouchPoints > 1 && /Macintosh/.test(navigator.userAgent)) {
  document.getElementById('choose-ios').checked = true
}

const install = document.getElementById('install-local')
const installStatus = document.getElementById('install-status')
for (const input of document.querySelectorAll('input[name="platform"]')) {
  input.addEventListener('change', () => {
    installStatus.textContent = ''
  })
}

if (install) {
  install.addEventListener('click', async () => {
    const label = install.querySelector('span')
    install.disabled = true
    label.textContent = '正在打开 Sniffy…'
    installStatus.textContent = ''
    const controller = new AbortController()
    const timeout = window.setTimeout(() => controller.abort(), 10000)
    try {
      const response = await fetch('/sniffy/install-ca', {
        method: 'POST',
        headers: { 'X-Sniffy-Install': install.dataset.installToken },
        credentials: 'omit',
        signal: controller.signal,
      })
      const message = await response.text()
      if (!response.ok) throw new Error(message)
      installStatus.textContent = message.trim()
    } catch (error) {
      installStatus.textContent = error.name === 'AbortError'
        ? '打开超时，请重试或在 Sniffy 中安装证书。'
        : error.message || '无法打开 Sniffy，请重试或下载证书手动安装。'
    } finally {
      window.clearTimeout(timeout)
      install.disabled = false
      label.textContent = '安装到本机'
    }
  })
}

document.querySelector('.help-link').addEventListener('click', () => {
  document.getElementById('connection-help').open = true
})
