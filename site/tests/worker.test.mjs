import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { test } from "node:test";
import worker from "../worker.ts";

const release = JSON.parse(
  await readFile(new URL("../src/data/release.json", import.meta.url), "utf8"),
);

test("官网清单跟随 R2 更新，不需要重新部署静态页面", async (t) => {
  t.mock.method(globalThis, "fetch", async (url, init) => {
    assert.equal(url, "https://cdn.gosniffy.com/release.json");
    assert.equal(init.redirect, "manual");
    return Response.json({ ...release, version: "1.2.3" });
  });
  const env = {
    RELEASE_MANIFEST_URL: "https://cdn.gosniffy.com/release.json",
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
    RELEASE_MANIFEST_URL: "https://cdn.gosniffy.com/release.json",
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
