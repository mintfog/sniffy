/*
 * 自包含 QR 码生成器（字节模式，离线、无依赖）。
 *
 * 基于 Nayuki「QR Code generator library」算法的精简移植（公有领域 / MIT）：
 * 仅保留字节模式编码 + 自动版本选择 + 纠错等级（含等级提升）+ 掩码评分。
 * 返回布尔矩阵（true=黑模块），交由界面渲染为 SVG。
 */

export type Ecc = 'L' | 'M' | 'Q' | 'H'

const ECC_FORMAT_BITS: Record<Ecc, number> = { L: 1, M: 0, Q: 3, H: 2 }
const ECC_ORDER: Ecc[] = ['L', 'M', 'Q', 'H'] // 纠错强度递增的索引顺序

// 每块纠错码字数 [eccIndex][version]
const ECC_CODEWORDS_PER_BLOCK: number[][] = [
  [-1, 7, 10, 15, 20, 26, 18, 20, 24, 30, 18, 20, 24, 26, 30, 22, 24, 28, 30, 28, 28, 28, 28, 30, 30, 26, 28, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30],
  [-1, 10, 16, 26, 18, 24, 16, 18, 22, 22, 26, 30, 22, 22, 24, 24, 28, 28, 26, 26, 26, 26, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28],
  [-1, 13, 22, 18, 26, 18, 24, 18, 22, 20, 24, 28, 26, 24, 20, 30, 24, 28, 28, 26, 30, 28, 30, 30, 30, 30, 28, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30],
  [-1, 17, 28, 22, 16, 22, 28, 26, 26, 24, 28, 24, 28, 22, 24, 24, 30, 28, 28, 26, 28, 30, 24, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30],
]

// 每版纠错块数 [eccIndex][version]
const NUM_ERROR_CORRECTION_BLOCKS: number[][] = [
  [-1, 1, 1, 1, 1, 1, 2, 2, 2, 2, 4, 4, 4, 4, 4, 6, 6, 6, 6, 7, 8, 8, 9, 9, 10, 12, 12, 12, 13, 14, 15, 16, 17, 18, 19, 19, 20, 21, 22, 24, 25],
  [-1, 1, 1, 1, 2, 2, 4, 4, 4, 5, 5, 5, 8, 9, 9, 10, 10, 11, 13, 14, 16, 17, 17, 18, 20, 21, 23, 25, 26, 28, 29, 31, 33, 35, 37, 38, 40, 43, 45, 47, 49],
  [-1, 1, 1, 2, 2, 4, 4, 6, 6, 8, 8, 8, 10, 12, 16, 12, 17, 16, 18, 21, 20, 23, 23, 25, 27, 29, 34, 34, 35, 38, 40, 43, 45, 48, 51, 53, 56, 59, 62, 65, 68],
  [-1, 1, 1, 2, 4, 4, 4, 5, 6, 8, 8, 11, 11, 16, 16, 18, 16, 19, 21, 25, 25, 25, 34, 30, 32, 35, 37, 40, 42, 45, 48, 51, 54, 57, 60, 63, 66, 70, 74, 77, 81],
]

const MIN_VERSION = 1
const MAX_VERSION = 40
const PENALTY_N1 = 3
const PENALTY_N2 = 3
const PENALTY_N3 = 40
const PENALTY_N4 = 10

class QrCode {
  readonly size: number
  private readonly modules: boolean[][]
  private readonly isFunction: boolean[][]

  constructor(
    readonly version: number,
    readonly ecl: Ecc,
    dataCodewords: number[],
    msk: number,
  ) {
    if (version < MIN_VERSION || version > MAX_VERSION) throw new Error('版本超出范围')
    this.size = version * 4 + 17
    const row: boolean[] = Array(this.size).fill(false)
    this.modules = Array.from({ length: this.size }, () => row.slice())
    this.isFunction = Array.from({ length: this.size }, () => row.slice())

    this.drawFunctionPatterns()
    const allCodewords = this.addEccAndInterleave(dataCodewords)
    this.drawCodewords(allCodewords)

    if (msk < 0) {
      let minPenalty = Infinity
      let bestMask = 0
      for (let i = 0; i < 8; i++) {
        this.applyMask(i)
        this.drawFormatBits(i)
        const penalty = this.getPenaltyScore()
        if (penalty < minPenalty) {
          bestMask = i
          minPenalty = penalty
        }
        this.applyMask(i) // 撤销
      }
      msk = bestMask
    }
    this.applyMask(msk)
    this.drawFormatBits(msk)
  }

  getModule(x: number, y: number): boolean {
    return x >= 0 && x < this.size && y >= 0 && y < this.size && this.modules[y][x]
  }

  getMatrix(): boolean[][] {
    return this.modules
  }

  /* ── 功能图形 ── */

  private drawFunctionPatterns(): void {
    for (let i = 0; i < this.size; i++) {
      this.setFunctionModule(6, i, i % 2 === 0)
      this.setFunctionModule(i, 6, i % 2 === 0)
    }
    this.drawFinderPattern(3, 3)
    this.drawFinderPattern(this.size - 4, 3)
    this.drawFinderPattern(3, this.size - 4)

    const alignPatPos = this.getAlignmentPatternPositions()
    const numAlign = alignPatPos.length
    for (let i = 0; i < numAlign; i++) {
      for (let j = 0; j < numAlign; j++) {
        if (
          !(
            (i === 0 && j === 0) ||
            (i === 0 && j === numAlign - 1) ||
            (i === numAlign - 1 && j === 0)
          )
        ) {
          this.drawAlignmentPattern(alignPatPos[i], alignPatPos[j])
        }
      }
    }

    this.drawFormatBits(0)
    this.drawVersion()
  }

  private drawFormatBits(mask: number): void {
    const data = (ECC_FORMAT_BITS[this.ecl] << 3) | mask
    let rem = data
    for (let i = 0; i < 10; i++) rem = (rem << 1) ^ ((rem >>> 9) * 0x537)
    const bits = ((data << 10) | rem) ^ 0x5412
    for (let i = 0; i <= 5; i++) this.setFunctionModule(8, i, getBit(bits, i))
    this.setFunctionModule(8, 7, getBit(bits, 6))
    this.setFunctionModule(8, 8, getBit(bits, 7))
    this.setFunctionModule(7, 8, getBit(bits, 8))
    for (let i = 9; i < 15; i++) this.setFunctionModule(14 - i, 8, getBit(bits, i))
    for (let i = 0; i < 8; i++) this.setFunctionModule(this.size - 1 - i, 8, getBit(bits, i))
    for (let i = 8; i < 15; i++) this.setFunctionModule(8, this.size - 15 + i, getBit(bits, i))
    this.setFunctionModule(8, this.size - 8, true)
  }

  private drawVersion(): void {
    if (this.version < 7) return
    let rem = this.version
    for (let i = 0; i < 12; i++) rem = (rem << 1) ^ ((rem >>> 11) * 0x1f25)
    const bits = (this.version << 12) | rem
    for (let i = 0; i < 18; i++) {
      const bit = getBit(bits, i)
      const a = this.size - 11 + (i % 3)
      const b = Math.floor(i / 3)
      this.setFunctionModule(a, b, bit)
      this.setFunctionModule(b, a, bit)
    }
  }

  private drawFinderPattern(x: number, y: number): void {
    for (let dy = -4; dy <= 4; dy++) {
      for (let dx = -4; dx <= 4; dx++) {
        const dist = Math.max(Math.abs(dx), Math.abs(dy))
        const xx = x + dx
        const yy = y + dy
        if (xx >= 0 && xx < this.size && yy >= 0 && yy < this.size) {
          this.setFunctionModule(xx, yy, dist !== 2 && dist !== 4)
        }
      }
    }
  }

  private drawAlignmentPattern(x: number, y: number): void {
    for (let dy = -2; dy <= 2; dy++) {
      for (let dx = -2; dx <= 2; dx++) {
        this.setFunctionModule(x + dx, y + dy, Math.max(Math.abs(dx), Math.abs(dy)) !== 1)
      }
    }
  }

  private setFunctionModule(x: number, y: number, isDark: boolean): void {
    this.modules[y][x] = isDark
    this.isFunction[y][x] = true
  }

  /* ── 纠错 + 交织 ── */

  private addEccAndInterleave(data: number[]): number[] {
    const ver = this.version
    const eccIdx = ECC_ORDER.indexOf(this.ecl)
    const numBlocks = NUM_ERROR_CORRECTION_BLOCKS[eccIdx][ver]
    const blockEccLen = ECC_CODEWORDS_PER_BLOCK[eccIdx][ver]
    const rawCodewords = Math.floor(getNumRawDataModules(ver) / 8)
    const numShortBlocks = numBlocks - (rawCodewords % numBlocks)
    const shortBlockLen = Math.floor(rawCodewords / numBlocks)

    const blocks: number[][] = []
    const rsDiv = reedSolomonComputeDivisor(blockEccLen)
    for (let i = 0, k = 0; i < numBlocks; i++) {
      const datLen = shortBlockLen - blockEccLen + (i < numShortBlocks ? 0 : 1)
      const dat = data.slice(k, k + datLen)
      k += datLen
      const ecc = reedSolomonComputeRemainder(dat, rsDiv)
      if (i < numShortBlocks) dat.push(0)
      blocks.push(dat.concat(ecc))
    }

    const result: number[] = []
    for (let i = 0; i < blocks[0].length; i++) {
      blocks.forEach((block, j) => {
        // 短块在数据区少一个字节（i !== shortBlockLen - blockEccLen 时跳过 padding 列）
        if (i !== shortBlockLen - blockEccLen || j >= numShortBlocks) {
          result.push(block[i])
        }
      })
    }
    return result
  }

  private drawCodewords(data: number[]): void {
    let i = 0
    for (let right = this.size - 1; right >= 1; right -= 2) {
      if (right === 6) right = 5
      for (let vert = 0; vert < this.size; vert++) {
        for (let j = 0; j < 2; j++) {
          const x = right - j
          const upward = ((right + 1) & 2) === 0
          const y = upward ? this.size - 1 - vert : vert
          if (!this.isFunction[y][x] && i < data.length * 8) {
            this.modules[y][x] = getBit(data[i >>> 3], 7 - (i & 7))
            i++
          }
        }
      }
    }
  }

  private applyMask(mask: number): void {
    for (let y = 0; y < this.size; y++) {
      for (let x = 0; x < this.size; x++) {
        let invert: boolean
        switch (mask) {
          case 0: invert = (x + y) % 2 === 0; break
          case 1: invert = y % 2 === 0; break
          case 2: invert = x % 3 === 0; break
          case 3: invert = (x + y) % 3 === 0; break
          case 4: invert = (Math.floor(x / 3) + Math.floor(y / 2)) % 2 === 0; break
          case 5: invert = ((x * y) % 2) + ((x * y) % 3) === 0; break
          case 6: invert = (((x * y) % 2) + ((x * y) % 3)) % 2 === 0; break
          case 7: invert = (((x + y) % 2) + ((x * y) % 3)) % 2 === 0; break
          default: throw new Error('掩码非法')
        }
        if (!this.isFunction[y][x] && invert) this.modules[y][x] = !this.modules[y][x]
      }
    }
  }

  private getPenaltyScore(): number {
    let result = 0
    const size = this.size
    // 行
    for (let y = 0; y < size; y++) {
      let runColor = false
      let runX = 0
      const runHistory = [0, 0, 0, 0, 0, 0, 0]
      for (let x = 0; x < size; x++) {
        if (this.modules[y][x] === runColor) {
          runX++
          if (runX === 5) result += PENALTY_N1
          else if (runX > 5) result++
        } else {
          this.finderPenaltyAddHistory(runX, runHistory)
          if (!runColor) result += this.finderPenaltyCountPatterns(runHistory) * PENALTY_N3
          runColor = this.modules[y][x]
          runX = 1
        }
      }
      result += this.finderPenaltyTerminateAndCount(runColor, runX, runHistory) * PENALTY_N3
    }
    // 列
    for (let x = 0; x < size; x++) {
      let runColor = false
      let runY = 0
      const runHistory = [0, 0, 0, 0, 0, 0, 0]
      for (let y = 0; y < size; y++) {
        if (this.modules[y][x] === runColor) {
          runY++
          if (runY === 5) result += PENALTY_N1
          else if (runY > 5) result++
        } else {
          this.finderPenaltyAddHistory(runY, runHistory)
          if (!runColor) result += this.finderPenaltyCountPatterns(runHistory) * PENALTY_N3
          runColor = this.modules[y][x]
          runY = 1
        }
      }
      result += this.finderPenaltyTerminateAndCount(runColor, runY, runHistory) * PENALTY_N3
    }
    // 2x2 同色块
    for (let y = 0; y < size - 1; y++) {
      for (let x = 0; x < size - 1; x++) {
        const c = this.modules[y][x]
        if (c === this.modules[y][x + 1] && c === this.modules[y + 1][x] && c === this.modules[y + 1][x + 1]) {
          result += PENALTY_N2
        }
      }
    }
    // 黑白比例
    let dark = 0
    for (const rowArr of this.modules) for (const cell of rowArr) if (cell) dark++
    const total = size * size
    const k = Math.ceil(Math.abs(dark * 20 - total * 10) / total) - 1
    result += k * PENALTY_N4
    return result
  }

  private finderPenaltyCountPatterns(runHistory: number[]): number {
    const n = runHistory[1]
    const core =
      n > 0 &&
      runHistory[2] === n &&
      runHistory[3] === n * 3 &&
      runHistory[4] === n &&
      runHistory[5] === n
    return (
      (core && runHistory[0] >= n * 4 && runHistory[6] >= n ? 1 : 0) +
      (core && runHistory[6] >= n * 4 && runHistory[0] >= n ? 1 : 0)
    )
  }

  private finderPenaltyTerminateAndCount(currentRunColor: boolean, currentRunLength: number, runHistory: number[]): number {
    if (currentRunColor) {
      this.finderPenaltyAddHistory(currentRunLength, runHistory)
      currentRunLength = 0
    }
    currentRunLength += this.size
    this.finderPenaltyAddHistory(currentRunLength, runHistory)
    return this.finderPenaltyCountPatterns(runHistory)
  }

  private finderPenaltyAddHistory(currentRunLength: number, runHistory: number[]): void {
    if (runHistory[0] === 0) currentRunLength += this.size
    runHistory.pop()
    runHistory.unshift(currentRunLength)
  }

  private getAlignmentPatternPositions(): number[] {
    if (this.version === 1) return []
    const numAlign = Math.floor(this.version / 7) + 2
    const step = this.version === 32 ? 26 : Math.ceil((this.version * 4 + 4) / (numAlign * 2 - 2)) * 2
    const result = [6]
    for (let pos = this.size - 7; result.length < numAlign; pos -= step) result.splice(1, 0, pos)
    return result
  }
}

/* ── 全局帮助函数 ── */

function getBit(x: number, i: number): boolean {
  return ((x >>> i) & 1) !== 0
}

function getNumRawDataModules(ver: number): number {
  let result = (16 * ver + 128) * ver + 64
  if (ver >= 2) {
    const numAlign = Math.floor(ver / 7) + 2
    result -= (25 * numAlign - 10) * numAlign - 55
    if (ver >= 7) result -= 36
  }
  return result
}

function getNumDataCodewords(ver: number, ecl: Ecc): number {
  const eccIdx = ECC_ORDER.indexOf(ecl)
  return (
    Math.floor(getNumRawDataModules(ver) / 8) -
    ECC_CODEWORDS_PER_BLOCK[eccIdx][ver] * NUM_ERROR_CORRECTION_BLOCKS[eccIdx][ver]
  )
}

function reedSolomonComputeDivisor(degree: number): number[] {
  const result: number[] = Array(degree).fill(0)
  result[degree - 1] = 1
  let root = 1
  for (let i = 0; i < degree; i++) {
    for (let j = 0; j < result.length; j++) {
      result[j] = reedSolomonMultiply(result[j], root)
      if (j + 1 < result.length) result[j] ^= result[j + 1]
    }
    root = reedSolomonMultiply(root, 0x02)
  }
  return result
}

function reedSolomonComputeRemainder(data: number[], divisor: number[]): number[] {
  const result: number[] = Array(divisor.length).fill(0)
  for (const b of data) {
    const factor = b ^ (result.shift() as number)
    result.push(0)
    divisor.forEach((coef, i) => (result[i] ^= reedSolomonMultiply(coef, factor)))
  }
  return result
}

function reedSolomonMultiply(x: number, y: number): number {
  let z = 0
  for (let i = 7; i >= 0; i--) {
    z = (z << 1) ^ ((z >>> 7) * 0x11d)
    z ^= ((y >>> i) & 1) * x
  }
  return z & 0xff
}

/* ── 字节模式编码：构造数据码字 ── */

/**
 * 把文本编码为 QR 矩阵。自动选择能容纳的最小版本，并在有余量时提升纠错等级。
 * @returns 布尔矩阵（true=黑），尺寸 = version*4+17
 */
export function encodeQrText(text: string, minEcc: Ecc = 'M'): boolean[][] {
  const data = new TextEncoder().encode(text)
  if (data.length === 0) throw new Error('内容为空')

  // 选版本：字节模式字符计数指示符位宽随版本变化（1–9:8位, 10–26:16位, 27–40:16位）
  let version = MIN_VERSION
  let dataUsedBits = 0
  for (version = MIN_VERSION; ; version++) {
    if (version > MAX_VERSION) throw new Error('内容过长，超出 QR 容量')
    const ccBits = version <= 9 ? 8 : 16
    const usedBits = 4 + ccBits + data.length * 8
    const capacityBits = getNumDataCodewords(version, minEcc) * 8
    if (usedBits <= capacityBits) {
      dataUsedBits = usedBits
      break
    }
  }

  // 提升纠错等级（不增大版本的前提下尽量用更高 ecl）
  let ecl = minEcc
  for (const candidate of ECC_ORDER) {
    if (ECC_ORDER.indexOf(candidate) <= ECC_ORDER.indexOf(minEcc)) continue
    if (dataUsedBits <= getNumDataCodewords(version, candidate) * 8) ecl = candidate
  }

  // 拼比特流
  const ccBits = version <= 9 ? 8 : 16
  const bits: number[] = []
  const append = (val: number, len: number) => {
    for (let i = len - 1; i >= 0; i--) bits.push((val >>> i) & 1)
  }
  append(4, 4) // 模式：字节
  append(data.length, ccBits)
  for (const b of data) append(b, 8)

  const dataCapacityBits = getNumDataCodewords(version, ecl) * 8
  // 终止符
  append(0, Math.min(4, dataCapacityBits - bits.length))
  // 补齐到字节边界
  append(0, (8 - (bits.length % 8)) % 8)
  // 填充字节 0xEC/0x11 交替
  for (let pad = 0xec; bits.length < dataCapacityBits; pad ^= 0xec ^ 0x11) append(pad, 8)

  // 比特 → 码字
  const codewords: number[] = []
  for (let i = 0; i < bits.length; i += 8) {
    let byte = 0
    for (let j = 0; j < 8; j++) byte = (byte << 1) | bits[i + j]
    codewords.push(byte)
  }

  const qr = new QrCode(version, ecl, codewords, -1)
  return qr.getMatrix()
}
