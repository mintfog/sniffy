/*
 * 配图放在 src/assets/cert-guide/，含系统文字的截图按 `<步骤 id>.<语言>.<扩展名>` 命名。
 * 仅含命令、路径等语言无关内容的截图按 `<步骤 id>.<扩展名>` 命名，由各语言复用。
 * 优先使用与界面语言一致的截图，缺少对应语言及通用截图时保留文字说明。
 */
const files = import.meta.glob<string>(
  '/src/assets/cert-guide/*.{png,webp,jpg,jpeg,svg}',
  {
    eager: true,
    query: '?url',
    import: 'default',
  }
)

const images = new Map<string, string>()
for (const [path, url] of Object.entries(files)) {
  const name = path.slice(path.lastIndexOf('/') + 1)
  images.set(name.slice(0, name.lastIndexOf('.')), url)
}

export function resolveGuideImage(
  id: string,
  lang: string
): string | undefined {
  return images.get(`${id}.${lang}`) ?? images.get(id)
}
