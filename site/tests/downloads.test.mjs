import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { test } from "node:test";
import {
  detectPlatform,
  desktopAssets,
  githubDownload,
  loadRelease,
  parseRelease,
  resolveDownload,
} from "../src/lib/downloads.ts";

const release = JSON.parse(
  await readFile(new URL("../src/data/release.json", import.meta.url), "utf8"),
);
const asset = desktopAssets(release, "windows")[0];

test("识别桌面平台和明确的架构，移动设备及未知 Mac 芯片留给用户选择", () => {
  assert.deepEqual(
    detectPlatform({ userAgent: "Windows NT 10.0; Win64; x64" }),
    { os: "windows", arch: "amd64" },
  );
  assert.deepEqual(detectPlatform({ userAgent: "Linux aarch64" }), {
    os: "linux",
    arch: "arm64",
  });
  assert.deepEqual(detectPlatform({ userAgent: "Macintosh; Intel Mac OS X" }), {
    os: "darwin",
  });
  assert.deepEqual(
    detectPlatform({
      userAgent: "Macintosh; Intel Mac OS X",
      architecture: "arm",
      bitness: "64",
    }),
    { os: "darwin", arch: "arm64" },
  );
  assert.deepEqual(
    detectPlatform({
      userAgent: "Windows NT",
      architecture: "x86",
      bitness: "32",
    }),
    { os: "windows", arch: "unsupported" },
  );
  for (const userAgent of ["iPhone", "Android Linux", "iPad"])
    assert.deepEqual(detectPlatform({ userAgent }), {});
  assert.deepEqual(
    detectPlatform({ userAgent: "Macintosh", maxTouchPoints: 5 }),
    {},
  );
});

test("读取客户端共用清单并校验下载地址、大小、摘要和重复制品", () => {
  assert.equal(parseRelease(release).version, release.version);
  assert.equal(desktopAssets(release).length, 8);
  assert.equal(desktopAssets(release, undefined, "installer").length, 4);
  assert.equal(desktopAssets(release, undefined, "binary").length, 4);
  for (const change of [
    { url: "javascript:alert(1)" },
    { url: "https://user:pass@example.com/file" },
    { size: 0 },
    { sha256: "bad" },
    { name: "../file" },
  ]) {
    assert.throws(() =>
      parseRelease({ ...release, assets: [{ ...asset, ...change }] }),
    );
  }
  assert.throws(() => parseRelease({ ...release, assets: [asset, asset] }));
  assert.throws(() => parseRelease({ version: "v1.2.3", files: {} }));
});

test("R2 文件大小匹配时直连下载，失败时回退同版本 GitHub 附件", async () => {
  assert.equal(
    await resolveDownload(release, asset, {
      fetch: async (url, init) => {
        assert.equal(url, asset.url);
        assert.equal(init.method, "HEAD");
        assert.equal(init.redirect, "error");
        return new Response(null, {
          headers: { "content-length": String(asset.size) },
        });
      },
    }),
    asset.url,
  );
  for (const response of [
    new Response(null, { status: 404 }),
    new Response(null, { headers: { "content-length": "1" } }),
  ]) {
    assert.equal(
      await resolveDownload(release, asset, { fetch: async () => response }),
      githubDownload(release, asset),
    );
  }
  assert.match(
    githubDownload(release, asset),
    new RegExp(`/download/v${release.version}/`),
  );
  assert.equal(
    await resolveDownload(release, asset, {
      fetch: async () => {
        throw new TypeError("CORS");
      },
    }),
    githubDownload(release, asset),
  );
});

test("探测超时会回退，用户改选会取消旧下载", async () => {
  const pending = async (_, { signal }) =>
    new Promise((_, reject) => {
      signal.addEventListener("abort", () => reject(signal.reason), {
        once: true,
      });
    });
  assert.equal(
    await resolveDownload(release, asset, { timeoutMs: 10, fetch: pending }),
    githubDownload(release, asset),
  );
  const controller = new AbortController();
  const promise = resolveDownload(release, asset, {
    signal: controller.signal,
    fetch: pending,
  });
  controller.abort();
  await assert.rejects(promise, { name: "AbortError" });

  await assert.rejects(
    resolveDownload(release, asset, {
      signal: controller.signal,
      fetch: () => assert.fail("已取消的下载不应发起探测"),
    }),
    { name: "AbortError" },
  );
});

test("刷新最新清单，网络或数据错误时保留构建时的下载入口", async () => {
  const latest = { ...release, version: "1.2.3" };
  assert.equal(
    (await loadRelease(release, async () => Response.json(latest))).version,
    "1.2.3",
  );
  assert.equal(
    await loadRelease(release, async () => Response.json({})),
    release,
  );
  assert.equal(
    await loadRelease(release, async () => {
      throw new Error("离线");
    }),
    release,
  );
});
