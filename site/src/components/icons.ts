import { readFileSync } from "node:fs";
import { createRequire } from "node:module";

const require = createRequire(import.meta.url);

// 与桌面应用使用同一套 Lucide 图标，保持演示界面一致。
const lucideNames = {
  arrow: "arrow-right",
  download: "download",
  activity: "activity",
  code: "code-xml",
  sliders: "sliders-horizontal",
  globe: "globe",
  terminal: "terminal",
  monitor: "monitor",
  check: "check",
  search: "search",
  copy: "copy",
  menu: "menu",
  chevron: "chevron-right",
  external: "external-link",
  bolt: "zap",
  book: "book-open",
  shuffle: "shuffle",
  clock: "clock",
  puzzle: "puzzle",
  gauge: "gauge",
  shield: "shield-check",
  settings: "settings",
  lock: "lock",
  pause: "pause",
  trash: "trash-2",
  caret: "chevron-down",
  braces: "braces",
  file: "file-text",
  image: "image",
  close: "x",
  sun: "sun",
  pencil: "pencil",
  media: "video",
} as const;

/** lucide 不收品牌标志，这几个保留自绘路径。 */
const brandPaths = {
  github:
    "M9 19c-4.3 1.3-4.3-2.2-6-2.7m12 5v-3.9c0-1.1-.4-1.9-1-2.4 3.3-.4 6.8-1.6 6.8-7.4 0-1.6-.5-2.9-1.5-3.9.2-.4.7-1.8-.1-3.7 0 0-1.3-.4-4.2 1.5a14.4 14.4 0 0 0-7.5 0C4.6-.4 3.3 0 3.3 0c-.8 1.9-.3 3.3-.1 3.7A5.5 5.5 0 0 0 1.7 7.6c0 5.8 3.5 7 6.8 7.4-.5.4-.9 1.2-1 2.3V21",
  apple:
    "M16 2c0 2-1 3-3 4 0-2 1-3 3-4ZM18 12c0-2 1-3 2-4-2-3-5-2-7-1-2-1-5-2-7 0-3 3-1 9 1 12 2 3 3 1 6 1s4 2 6-1l2-4c-2 0-3-2-3-3Z",
  windows: "M3 5l8-1v7H3zm10-1 8-1v8h-8zM3 13h8v7l-8-1zm10 0h8v8l-8-1z",
  linux:
    "M9 5a3 3 0 0 1 6 0v4l4 7-2 4h-4l-1-1-1 1H7l-2-4 4-7ZM10 7h.01M14 7h.01m-4 3 2 1 2-1M9 14l-2 6m8-6 2 6",
} as const;

export type IconName = keyof typeof lucideNames | keyof typeof brandPaths;

function lucideBody(file: string): string {
  return readFileSync(
    require.resolve(`lucide-static/icons/${file}.svg`),
    "utf8",
  )
    .replace(/^[\s\S]*?<svg[^>]*>/, "")
    .replace(/<\/svg>\s*$/, "")
    .trim();
}

export const iconBodies = {
  ...Object.fromEntries(
    Object.entries(lucideNames).map(([key, file]) => [key, lucideBody(file)]),
  ),
  ...Object.fromEntries(
    Object.entries(brandPaths).map(([key, d]) => [key, `<path d="${d}" />`]),
  ),
} as Record<IconName, string>;
