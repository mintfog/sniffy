import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { test } from "node:test";
import {
  detectPlatform,
  desktopAssets,
  fetchRelease,
  githubDownload,
  loadBuildRelease,
  loadRelease,
  pageRelease,
  parseRelease,
  resolveDownload,
} from "../src/lib/downloads.ts";

const release = JSON.parse(
  await readFile(new URL("./fixtures/release.json", import.meta.url), "utf8"),
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

test("Linux 免安装列表优先展示 AppImage，并保持清单顺序", () => {
  const binary = desktopAssets(release, "linux", "binary")[0];
  const appimage = {
    ...binary,
    name: "sniffy-desktop-linux-amd64.AppImage",
    url: "https://cdn.gosniffy.com/releases/v1.2.3/sniffy-desktop-linux-amd64.AppImage",
  };
  const manifest = parseRelease({
    ...release,
    assets: [...release.assets, appimage],
  });
  assert.deepEqual(desktopAssets(manifest, "linux", "binary"), [
    appimage,
    binary,
  ]);
  assert.equal(manifest.assets.at(-1), appimage);
});

test("下载列表不展示供应用内更新使用的 macOS 应用归档", () => {
  const binary = release.assets.find(
    (item) => item.os === "darwin" && item.edition === "desktop",
  );
  const archive = {
    ...binary,
    kind: "binary",
    name: `sniffy-desktop-darwin-${binary.arch}.app.tar.gz`,
    url: `https://cdn.gosniffy.com/releases/v1.2.3/sniffy-desktop-darwin-${binary.arch}.app.tar.gz`,
  };
  const manifest = parseRelease({
    ...release,
    assets: [...release.assets, archive],
  });
  assert.deepEqual(desktopAssets(manifest), desktopAssets(release));
  assert.equal(manifest.assets.at(-1), archive);
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

test("读取 R2 清单时拒绝非 2xx、超出上限和格式无效的响应", async () => {
  const requested = [];
  const remote = await fetchRelease({
    request: async (url, init) => {
      requested.push([url, init.redirect]);
      return Response.json({ ...release, notes: "## 正文" });
    },
  });
  assert.deepEqual(requested, [["https://cdn.gosniffy.com/release.json", "manual"]]);
  assert.equal(remote.notes, "## 正文");
  await assert.rejects(
    fetchRelease({ request: async () => new Response("", { status: 503 }) }),
    /HTTP 503/,
  );
  await assert.rejects(
    fetchRelease({
      request: async () =>
        Response.json({ ...release, notes: "x".repeat(256 * 1024) }),
    }),
    /过大/,
  );
  await assert.rejects(
    fetchRelease({ request: async () => Response.json({ version: "bad" }) }),
    /格式无效/,
  );
});

test("本地构建读取失败时沿用仓库清单，只尝试一次", async (t) => {
  t.mock.method(console, "warn", () => {});
  let calls = 0;
  const result = await loadBuildRelease(release, {
    request: async () => {
      calls++;
      throw new Error("offline");
    },
  });
  assert.equal(result, release);
  assert.equal(calls, 1);
});

test("正式构建重试后成功则使用最新清单，始终失败则中止构建", async () => {
  let calls = 0;
  const latest = await loadBuildRelease(release, {
    strict: true,
    retryDelayMs: 0,
    request: async () =>
      ++calls < 3
        ? new Response("", { status: 502 })
        : Response.json({ ...release, version: "1.2.3" }),
  });
  assert.equal(latest.version, "1.2.3");
  assert.equal(calls, 3);

  calls = 0;
  await assert.rejects(
    loadBuildRelease(release, {
      strict: true,
      retryDelayMs: 0,
      request: async () => {
        calls++;
        throw new Error("offline");
      },
    }),
    /读取发布清单失败：offline/,
  );
  assert.equal(calls, 3);
});

test("页面嵌入的清单不带更新说明正文", () => {
  assert.deepEqual(pageRelease({ ...release, notes: "## 正文" }), release);
});
