import { formatStarCount, parseStarCount } from "../lib/github-stars.ts";

/** 静态 HTML 已带构建时的数字，这里只把它刷新到 worker 边缘缓存的值；失败就保留原样。 */
export async function refreshGitHubStars() {
  const targets = document.querySelectorAll<HTMLElement>("[data-github-stars]");
  if (targets.length === 0) return;

  let count: number;
  try {
    const response = await fetch("/stars.json", {
      credentials: "omit",
      signal: AbortSignal.timeout(4000),
    });
    if (!response.ok) return;
    count = parseStarCount(((await response.json()) as { count?: unknown }).count);
  } catch {
    return;
  }

  const formatted = formatStarCount(count);
  targets.forEach((el) => {
    el.textContent = formatted;
    el.hidden = false;
  });
}

if (typeof document !== "undefined") {
  void refreshGitHubStars();
}
