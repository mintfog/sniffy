const REPO_API_URL = "https://api.github.com/repos/mintfog/sniffy";

export function formatStarCount(count: number): string {
  if (count >= 1000) {
    return `${(count / 1000).toFixed(count >= 10000 ? 0 : 1)}k`;
  }
  return String(count);
}

/** 0 不算有效值：数字为 0 时徽标保持隐藏，不展示「0」。 */
export function parseStarCount(value: unknown): number {
  if (
    typeof value === "number" &&
    Number.isInteger(value) &&
    value > 0
  ) {
    return value;
  }
  throw new Error("Star 数无效");
}

/**
 * 从 GitHub 读取仓库 Star 数，worker 与构建期共用。
 * GitHub 拒绝没有 User-Agent 的请求，而 worker 的 fetch 默认不带；
 * 未认证额度按出口 IP 计算，worker 的出口 IP 与其他租户共享，所以配了 token 就带上。
 */
export async function fetchStarCount({
  token,
  timeoutMs = 3000,
  request = fetch,
}: {
  token?: string;
  timeoutMs?: number;
  request?: typeof fetch;
} = {}): Promise<number> {
  const headers: Record<string, string> = {
    Accept: "application/vnd.github+json",
    "User-Agent": "gosniffy.com",
    "X-GitHub-Api-Version": "2022-11-28",
  };
  if (token) headers.Authorization = `Bearer ${token}`;
  const response = await request(REPO_API_URL, {
    headers,
    signal: AbortSignal.timeout(timeoutMs),
  });
  if (!response.ok) throw new Error(`GitHub API 不可用：HTTP ${response.status}`);
  const data = (await response.json()) as { stargazers_count?: unknown };
  return parseStarCount(data.stargazers_count);
}
