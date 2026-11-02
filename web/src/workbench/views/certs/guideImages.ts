/*
 * 配图按 `<步骤 id>.<语言>.<扩展名>` 放在 src/assets/cert-guide/，例如 ios-trust.zh-Hans.png。
 * 截图中的系统文字必须与界面语言一致，缺少对应语言的截图时保留文字说明。
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
  return images.get(`${id}.${lang}`)
}
