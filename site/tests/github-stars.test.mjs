import assert from "node:assert/strict";
import { test } from "node:test";
import { JSDOM } from "jsdom";
import { fetchStarCount, formatStarCount } from "../src/lib/github-stars.ts";
import { refreshGitHubStars } from "../src/scripts/github-stars.ts";

function useDom(t, html) {
  const dom = new JSDOM(html, { url: "https://gosniffy.com" });
  for (const name of ["window", "document"]) {
    const before = Object.getOwnPropertyDescriptor(globalThis, name);
    Object.defineProperty(globalThis, name, {
      configurable: true,
      writable: true,
      value: dom.window[name],
    });
    t.after(() =>
      before
        ? Object.defineProperty(globalThis, name, before)
        : delete globalThis[name],
    );
  }
  t.after(() => dom.window.close());
  return dom.window.document;
}

test("formatStarCount 格式化千级以上与千级以下 Star 数量", () => {
  assert.equal(formatStarCount(42), "42");
  assert.equal(formatStarCount(999), "999");
  assert.equal(formatStarCount(1000), "1.0k");
  assert.equal(formatStarCount(1530), "1.5k");
  assert.equal(formatStarCount(12400), "12k");
});

test("fetchStarCount 带 User-Agent，配置 token 时带认证头", async () => {
  const seen = [];
  const request = async (url, init) => {
    seen.push(init.headers);
    assert.equal(url, "https://api.github.com/repos/mintfog/sniffy");
    return Response.json({ stargazers_count: 153 });
  };
  assert.equal(await fetchStarCount({ request }), 153);
  assert.equal(await fetchStarCount({ request, token: "t0ken" }), 153);
  assert.equal(seen[0]["User-Agent"], "gosniffy.com");
  assert.equal(seen[0].Authorization, undefined);
  assert.equal(seen[1].Authorization, "Bearer t0ken");

  await assert.rejects(
    fetchStarCount({
      request: async () => new Response("rate limited", { status: 403 }),
    }),
  );
  await assert.rejects(
    fetchStarCount({ request: async () => Response.json({}) }),
  );
});

test("refreshGitHubStars 用同源端点刷新构建时的数字", async (t) => {
  const document = useDom(
    t,
    `<span data-github-stars>120</span><span data-github-stars hidden></span>`,
  );
  t.mock.method(globalThis, "fetch", async (url, init) => {
    assert.equal(url, "/stars.json");
    assert.equal(init.credentials, "omit");
    return Response.json({ count: 1530 });
  });

  await refreshGitHubStars();
  for (const el of document.querySelectorAll("[data-github-stars]")) {
    assert.equal(el.hidden, false);
    assert.equal(el.textContent, "1.5k");
  }
});

test("端点失败或返回无效值时保留构建时的状态", async (t) => {
  const document = useDom(
    t,
    `<span data-github-stars>120</span><span data-github-stars hidden></span>`,
  );
  const responses = [
    () => Promise.reject(new Error("network error")),
    () => Promise.resolve(new Response(null, { status: 503 })),
    () => Promise.resolve(Response.json({ count: "lots" })),
  ];
  for (const respond of responses) {
    t.mock.method(globalThis, "fetch", respond);
    await refreshGitHubStars();
    const [built, hidden] = document.querySelectorAll("[data-github-stars]");
    assert.equal(built.textContent, "120");
    assert.equal(hidden.hidden, true);
    assert.equal(hidden.textContent, "");
  }
});
