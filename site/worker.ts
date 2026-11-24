import { fetchRelease } from "./src/lib/downloads.ts";
import { fetchStarCount } from "./src/lib/github-stars.ts";

interface Env {
  ASSETS: { fetch(request: Request): Promise<Response> };
  /** 只读公开仓库的 GitHub token，未配置时按共享出口 IP 的未认证额度请求。 */
  GITHUB_TOKEN?: string;
}

interface StarsEntry {
  count: number;
  fetchedAt: number;
}

const STARS_REFRESH_MS = 60 * 60 * 1000;
/** 边缘缓存保留远长于刷新周期，GitHub 不可用时仍有旧值可发。 */
const STARS_KEEP_SECONDS = 7 * 24 * 60 * 60;
const STARS_BROWSER_MAX_AGE = 30 * 60;

async function releaseResponse(request: Request, env: Env): Promise<Response> {
  try {
    const release = await fetchRelease();
    return new Response(
      request.method === "HEAD" ? null : JSON.stringify(release),
      {
        headers: {
          "Content-Type": "application/json; charset=utf-8",
          "Cache-Control": "no-store",
          "Access-Control-Allow-Origin": "*",
        },
      },
    );
  } catch (error) {
    console.warn(
      "读取 R2 发布清单失败，使用静态清单",
      error instanceof Error ? error.message : String(error),
    );
    // R2 暂不可用时，静态资源里的清单仍指向已发布的版本目录。
    return env.ASSETS.fetch(request);
  }
}

async function starsResponse(request: Request, env: Env): Promise<Response> {
  const cache = (caches as CacheStorage & { default: Cache }).default;
  const key = new Request(new URL("/stars.json", request.url));
  const cached = await cache.match(key);
  let entry = cached ? ((await cached.json()) as StarsEntry) : null;

  if (!entry || Date.now() - entry.fetchedAt >= STARS_REFRESH_MS) {
    try {
      const count = await fetchStarCount({ token: env.GITHUB_TOKEN });
      entry = { count, fetchedAt: Date.now() };
      await cache.put(
        key,
        new Response(JSON.stringify(entry), {
          headers: { "Cache-Control": `max-age=${STARS_KEEP_SECONDS}` },
        }),
      );
    } catch (error) {
      console.warn(
        "读取 GitHub Star 数失败",
        error instanceof Error ? error.message : String(error),
      );
    }
  }

  if (!entry) {
    return new Response(null, {
      status: 503,
      headers: { "Cache-Control": "no-store" },
    });
  }
  return new Response(
    request.method === "HEAD" ? null : JSON.stringify({ count: entry.count }),
    {
      headers: {
        "Content-Type": "application/json; charset=utf-8",
        "Cache-Control": `public, max-age=${STARS_BROWSER_MAX_AGE}`,
      },
    },
  );
}

const routes: Record<string, (request: Request, env: Env) => Promise<Response>> = {
  "/release.json": releaseResponse,
  "/stars.json": starsResponse,
};

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    const route = routes[new URL(request.url).pathname];
    if (!route) return env.ASSETS.fetch(request);
    if (request.method !== "GET" && request.method !== "HEAD") {
      return new Response(null, {
        status: 405,
        headers: { Allow: "GET, HEAD" },
      });
    }
    return route(request, env);
  },
};
