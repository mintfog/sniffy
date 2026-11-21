import { fetchRelease } from "./src/lib/downloads.ts";

interface Env {
  ASSETS: { fetch(request: Request): Promise<Response> };
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
  },
};
