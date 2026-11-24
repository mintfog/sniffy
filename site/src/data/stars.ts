import { fetchStarCount, formatStarCount } from "../lib/github-stars";

async function loadBuildStarCount(): Promise<number | null> {
  try {
    return await fetchStarCount({
      token: process.env.GITHUB_TOKEN,
      timeoutMs: 10_000,
    });
  } catch (error) {
    console.warn(
      "构建期读取 GitHub Star 数失败，徽标留待浏览器补全",
      error instanceof Error ? error.message : String(error),
    );
    return null;
  }
}

/**
 * 构建时写进静态 HTML 的 Star 数文本，空串表示徽标先隐藏；浏览器再从 /stars.json 刷新。
 * astro dev 不请求网络，与发布清单一致。只能在构建期（.astro）导入。
 */
const count = import.meta.env.DEV ? null : await loadBuildStarCount();
export const buildStarLabel = count === null ? "" : formatStarCount(count);
