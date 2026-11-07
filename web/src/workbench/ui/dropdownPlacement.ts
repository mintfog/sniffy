export const VIEWPORT_MARGIN = 8
const GAP = 4

/**
 * 下拉浮层的纵向位置：默认贴在锚点下方；下方放不下且上方更宽裕时翻到上方。
 * maxHeight 不超过 limit，也不超过所在一侧的可用空间。
 */
export function placeDropdown(anchor: DOMRect, contentHeight: number, limit: number) {
  const below = window.innerHeight - anchor.bottom - VIEWPORT_MARGIN - GAP
  const above = anchor.top - VIEWPORT_MARGIN - GAP
  const height = Math.min(contentHeight, limit)
  const upwards = below < height && above > below
  const maxHeight = Math.max(0, Math.min(limit, upwards ? above : below))
  const top = upwards ? anchor.top - Math.min(height, maxHeight) - GAP : anchor.bottom + GAP
  return { top, maxHeight }
}
