import { parseRelease } from "./src/lib/downloads.ts";

interface Env {
  ASSETS: { fetch(request: Request): Promise<Response> };
  RELEASE_MANIFEST_URL: string;
}

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    if (new URL(request.url).pathname !== "/release.json") {
      return env.ASSETS.fetch(request);
    }
    if (request.method !== "GET" && request.method !== "HEAD") {
      return new Response(null, {
        status: 405,
        headers: { Allow: "GET, HEAD" },
      });
    }
    try {
      const response = await fetch(env.RELEASE_MANIFEST_URL, {
        signal: AbortSignal.timeout(3000),
        redirect: "manual",
        cache: "no-store",
      });
      if (!response.ok) throw new Error("发布清单不可用");
      const text = await response.text();
      if (text.length > 256 * 1024) throw new Error("发布清单过大");
      const release = parseRelease(JSON.parse(text));
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
  },
};
