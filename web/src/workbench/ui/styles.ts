import type { CSSProperties } from 'react'
import type { MarkColor } from '../lib/types'

// 棋盘格背景:透明 PNG/SVG 在其上更易辨认真实形状。半透明灰在明暗主题下都可见。
const CHECKER = 'rgba(127,127,127,0.16)'
export const checkerStyle: CSSProperties = {
  backgroundImage: `linear-gradient(45deg, ${CHECKER} 25%, transparent 25%), linear-gradient(-45deg, ${CHECKER} 25%, transparent 25%), linear-gradient(45deg, transparent 75%, ${CHECKER} 75%), linear-gradient(-45deg, transparent 75%, ${CHECKER} 75%)`,
  backgroundSize: '16px 16px',
  backgroundPosition: '0 0, 0 8px, 8px -8px, -8px 0',
}

/** 高亮标记的颜色值，用于内联 style（色条、描边）。 */
export const markColor = (mark: MarkColor) => `rgb(var(--c-mark-${mark}))`
