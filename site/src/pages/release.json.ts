import type { APIRoute } from "astro";
import { release } from "../data/release";

/**
 * 客户端清单与官网下载区共用数据。
 * 静态部署的缓存头由 public/_headers 设置，保持短缓存以便及时发现新版本。
 */
export const GET: APIRoute = () =>
  new Response(`${JSON.stringify(release, null, 2)}\n`, {
    headers: { "content-type": "application/json; charset=utf-8" },
  });
