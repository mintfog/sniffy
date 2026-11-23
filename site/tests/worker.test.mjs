import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { test } from "node:test";
import worker from "../worker.ts";

const release = JSON.parse(
  await readFile(new URL("./fixtures/release.json", import.meta.url), "utf8"),
);

test("官网清单跟随 R2 更新，不需要重新部署静态页面", async (t) => {
  t.mock.method(globalThis, "fetch", async (url, init) => {
    assert.equal(url, "https://cdn.gosniffy.com/release.json");
    assert.equal(init.redirect, "manual");
    return Response.json({ ...release, version: "1.2.3" });
  });
  const env = {
    ASSETS: { fetch: () => assert.fail("应读取 R2") },
  };
  const response = await worker.fetch(
    new Request("https://gosniffy.com/release.json"),
    env,
  );
  assert.equal((await response.json()).version, "1.2.3");
  assert.equal(response.headers.get("cache-control"), "no-store");
  assert.equal(
    await (
      await worker.fetch(
        new Request("https://gosniffy.com/release.json", { method: "HEAD" }),
        env,
      )
    ).text(),
    "",
  );
  assert.equal(
    (
      await worker.fetch(
        new Request("https://gosniffy.com/release.json", { method: "POST" }),
        env,
      )
    ).status,
    405,
  );
});

test("R2 故障或错误清单回退静态资源，其他网页按原路径提供", async (t) => {
  t.mock.method(console, "warn", () => {});
  const fallback = () => Response.json(release);
  const env = {
    ASSETS: { fetch: fallback },
  };
  t.mock.method(globalThis, "fetch", async () =>
    Response.json({ error: "bad" }),
  );
  const response = await worker.fetch(
    new Request("https://gosniffy.com/release.json"),
    env,
  );
  assert.deepEqual(await response.json(), release);
  assert.deepEqual(
    await (
      await worker.fetch(new Request("https://gosniffy.com/docs/"), env)
    ).json(),
    release,
  );
});

function useEdgeCache(t) {
  const store = new Map();
  const before = Object.getOwnPropertyDescriptor(globalThis, "caches");
  Object.defineProperty(globalThis, "caches", {
    configurable: true,
    writable: true,
    value: {
      default: {
        match: async (key) => store.get(key.url)?.clone(),
        put: async (key, response) => void store.set(key.url, response.clone()),
      },
    },
  });
  t.after(() =>
    before
      ? Object.defineProperty(globalThis, "caches", before)
      : delete globalThis.caches,
  );
  return store;
}

const starsRequest = (init) =>
  new Request("https://gosniffy.com/stars.json", init);

test("Star 数在边缘缓存一小时内只向 GitHub 请求一次", async (t) => {
  useEdgeCache(t);
  let calls = 0;
  t.mock.method(globalThis, "fetch", async (url, init) => {
    calls++;
    assert.equal(url, "https://api.github.com/repos/mintfog/sniffy");
    assert.equal(init.headers.Authorization, "Bearer t0ken");
    return Response.json({ stargazers_count: 153 });
  });
  const env = {
    ASSETS: { fetch: () => assert.fail("不应读取静态资源") },
    GITHUB_TOKEN: "t0ken",
  };

  const response = await worker.fetch(starsRequest(), env);
  assert.deepEqual(await response.json(), { count: 153 });
  assert.equal(response.headers.get("cache-control"), "public, max-age=1800");
  assert.equal(await (await worker.fetch(starsRequest(), env)).text(), '{"count":153}');
  assert.equal(
    await (await worker.fetch(starsRequest({ method: "HEAD" }), env)).text(),
    "",
  );
  assert.equal(calls, 1);
  assert.equal(
    (await worker.fetch(starsRequest({ method: "POST" }), env)).status,
    405,
  );
});

test("GitHub 不可用时发过期的缓存值，没有缓存时返回 503", async (t) => {
  t.mock.method(console, "warn", () => {});
  const store = useEdgeCache(t);
  t.mock.method(globalThis, "fetch", async () =>
    new Response("rate limited", { status: 403 }),
  );
  const env = { ASSETS: { fetch: () => assert.fail("不应读取静态资源") } };

  const missing = await worker.fetch(starsRequest(), env);
  assert.equal(missing.status, 503);
  assert.equal(missing.headers.get("cache-control"), "no-store");

  store.set(
    "https://gosniffy.com/stars.json",
    Response.json({ count: 120, fetchedAt: Date.now() - 2 * 60 * 60 * 1000 }),
  );
  assert.deepEqual(await (await worker.fetch(starsRequest(), env)).json(), {
    count: 120,
  });
});
