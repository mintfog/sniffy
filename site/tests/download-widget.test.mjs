import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { test } from "node:test";
import { JSDOM } from "jsdom";
import {
  initDownloadSection,
  initDownloadTable,
} from "../src/lib/download-widget.ts";

const release = JSON.parse(
  await readFile(new URL("../src/data/release.json", import.meta.url), "utf8"),
);
const copy = JSON.parse(
  await readFile(
    new URL("../src/i18n/locales/zh.json", import.meta.url),
    "utf8",
  ),
).download;
const latest = {
  ...release,
  version: "1.2.3",
  assets: release.assets.map((asset) => ({
    ...asset,
    url: asset.url.replace(`/v${release.version}/`, "/v1.2.3/"),
  })),
};
const tick = () => new Promise((done) => setTimeout(done, 0));

function fixture(t, ua = "Windows NT 10.0; Win64; x64") {
  const dom = new JSDOM(
    `<section>
      <span data-release-version></span>
      <div data-download-control>
        <a data-recommended-download href="#download-platforms">
          <span data-download-label></span><span data-recommended-size></span><span data-chip-arrow hidden></span>
        </a>
        <div id="download-menu" data-download-menu hidden>
          <div data-menu-options></div>
        </div>
        <template data-download-option-template>
          <a role="menuitem" tabindex="-1" data-download-choice>
            <span data-option-architecture></span><span data-option-size></span>
          </a>
        </template>
      </div>
      <p data-download-status></p>
      <details data-download-alternatives>
      <summary>其他平台与版本</summary>
      <div data-platform-tabs hidden>
        <button data-platform value="darwin">macOS</button>
        <button data-platform value="windows">Windows</button>
        <button data-platform value="linux">Linux</button>
      </div>
      ${["darwin", "windows", "linux"]
        .map((os) => {
          const groups = ["installer", "binary"]
            .map((kind) => {
              const assets = release.assets.filter(
                (asset) =>
                  asset.os === os &&
                  asset.edition === "desktop" &&
                  asset.kind === kind,
              );
              return `<section data-package-kind="${kind}"><div data-platform-assets>
            ${assets
              .map(
                (asset) => `
              <article data-download-package>
                <a href="${asset.url}" data-release-asset="${asset.name}">
                  <strong data-asset-architecture>${asset.arch}</strong>
                  <span data-download-size></span>
                </a>
                <details><summary>SHA-256</summary><code data-checksum-value>${asset.sha256}</code></details>
              </article>
            `,
              )
              .join("")}
          </div></section>`;
            })
            .join("");
          return `<div data-platform-panel="${os}">${groups}</div>`;
        })
        .join("")}
      </details>
    </section>`,
    { url: "https://gosniffy.com" },
  );
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
  Object.defineProperty(dom.window.navigator, "userAgent", { value: ua });
  const root = dom.window.document.querySelector("section");
  root.dataset.release = JSON.stringify(release);
  root.dataset.copy = JSON.stringify(copy);
  const click = (selector = "[data-recommended-download]") =>
    root
      .querySelector(selector)
      .dispatchEvent(
        new dom.window.MouseEvent("click", { bubbles: true, cancelable: true }),
      );
  const selectPlatform = (os) => click(`[data-platform][value="${os}"]`);
  return { dom, root, selectPlatform, click };
}

test("Mac 主按钮展开芯片菜单，选择后立即下载桌面安装包", async (t) => {
  const { root, click } = fixture(t, "Macintosh; Intel Mac OS X");
  const destinations = [];
  await initDownloadSection(root, {
    navigate: (url) => destinations.push(url),
    fetch: async (_, init) =>
      init.method === "HEAD"
        ? new Response(null, { status: 404 })
        : Response.json(latest),
  });
  const menu = root.querySelector("[data-download-menu]");
  const link = root.querySelector("[data-recommended-download]");
  assert.equal(
    root.querySelector("[data-download-label]").textContent,
    "下载 macOS 版",
  );
  click();
  assert.equal(menu.hidden, false);
  assert.equal(link.getAttribute("aria-expanded"), "true");
  const choices = [...menu.querySelectorAll("[data-download-choice]")];
  assert.equal(choices.length, 2);
  assert.ok(choices.every((choice) => choice.href.endsWith("-installer.dmg")));
  const name = "sniffy-desktop-darwin-arm64-installer.dmg";
  click(`[data-download-menu] [data-release-asset="${name}"]`);
  await tick();
  assert.equal(menu.hidden, true);
  assert.deepEqual(destinations, [
    `https://github.com/mintfog/sniffy/releases/download/v1.2.3/${name}`,
  ]);
});

test("Mac 芯片菜单支持键盘选择、Escape 和外部点击关闭", async (t) => {
  const { root, dom, click } = fixture(t, "Macintosh; Intel Mac OS X");
  await initDownloadSection(root, { fetch: async () => Response.json(latest) });
  const menu = root.querySelector("[data-download-menu]");
  const link = root.querySelector("[data-recommended-download]");
  const press = (key) =>
    dom.window.document.activeElement.dispatchEvent(
      new dom.window.KeyboardEvent("keydown", {
        key,
        bubbles: true,
        cancelable: true,
      }),
    );
  link.focus();
  press("ArrowDown");
  assert.equal(menu.hidden, false);
  const choices = [...menu.querySelectorAll("[data-download-choice]")];
  assert.equal(dom.window.document.activeElement, choices[0]);
  press("ArrowUp");
  assert.equal(dom.window.document.activeElement, choices[1]);
  press("Home");
  assert.equal(dom.window.document.activeElement, choices[0]);
  press("End");
  assert.equal(dom.window.document.activeElement, choices[1]);
  press("Escape");
  assert.equal(menu.hidden, true);
  assert.equal(dom.window.document.activeElement, link);
  click();
  root.dispatchEvent(new dom.window.Event("pointerdown", { bubbles: true }));
  assert.equal(menu.hidden, true);
  assert.equal(link.getAttribute("aria-expanded"), "false");
});

test("清单刷新保留下拉焦点，点击芯片后使用新版本下载", async (t) => {
  const { root, dom, click } = fixture(t, "Macintosh; Intel Mac OS X");
  const hints = Promise.withResolvers();
  const manifest = Promise.withResolvers();
  const destinations = [];
  Object.defineProperty(dom.window.navigator, "userAgentData", {
    value: { platform: "macOS", getHighEntropyValues: () => hints.promise },
  });
  const ready = initDownloadSection(root, {
    navigate: (url) => destinations.push(url),
    fetch: async (_, init) =>
      init.method === "HEAD"
        ? new Response(null, { status: 404 })
        : manifest.promise,
  });
  click();
  const name = "sniffy-desktop-darwin-amd64-installer.dmg";
  root
    .querySelector(`[data-download-choice][data-release-asset="${name}"]`)
    .focus();
  hints.resolve({ architecture: "arm", bitness: "64" });
  manifest.resolve(Response.json(latest));
  await ready;
  await tick();
  assert.equal(root.querySelector("[data-download-menu]").hidden, false);
  assert.equal(dom.window.document.activeElement.dataset.releaseAsset, name);
  click(`[data-download-choice][data-release-asset="${name}"]`);
  await tick();
  assert.deepEqual(destinations, [
    `https://github.com/mintfog/sniffy/releases/download/v1.2.3/${name}`,
  ]);
});

test("Windows 和 Linux 主按钮直接下载桌面安装包", async (t) => {
  for (const [os, ua, extension] of [
    ["windows", "Windows NT 10.0; Win64; x64", "exe"],
    ["linux", "Linux x86_64", "deb"],
  ]) {
    await t.test(os, async (t) => {
      const { root, click } = fixture(t, ua);
      const destinations = [];
      await initDownloadSection(root, {
        navigate: (url) => destinations.push(url),
        fetch: async (_, init) =>
          init.method === "HEAD"
            ? new Response(null, { status: 404 })
            : Response.json(latest),
      });
      const link = root.querySelector("[data-recommended-download]");
      assert.equal(
        root.querySelector("[data-download-alternatives]").open,
        false,
      );
      assert.equal(link.hasAttribute("aria-haspopup"), false);
      assert.equal(root.querySelector("[data-chip-arrow]").hidden, true);
      click();
      await tick();
      assert.equal(
        root.querySelector("[data-download-alternatives]").open,
        false,
      );
      assert.equal(root.querySelector("[data-download-menu]").hidden, true);
      assert.deepEqual(destinations, [
        `https://github.com/mintfog/sniffy/releases/download/v1.2.3/sniffy-desktop-${os}-amd64-installer.${extension}`,
      ]);
    });
  }
});

test("下方免安装版的下载选择不改变主按钮的安装包", async (t) => {
  const { root, click } = fixture(t);
  const manifest = Promise.withResolvers();
  const destinations = [];
  const ready = initDownloadSection(root, {
    navigate: (url) => destinations.push(url),
    fetch: async (_, init) =>
      init.method === "HEAD"
        ? new Response(null, { status: 404 })
        : manifest.promise,
  });
  click('[data-platform-panel="windows"] [data-package-kind="binary"] a');
  manifest.resolve(Response.json(latest));
  await ready;
  await tick();
  assert.match(
    root.querySelector("[data-recommended-download]").href,
    /windows-amd64-installer\.exe$/,
  );
  assert.deepEqual(destinations, [
    "https://github.com/mintfog/sniffy/releases/download/v1.2.3/sniffy-desktop-windows-amd64.exe",
  ]);
});

test("Windows 自动选择 x64，清单刷新后从 R2 下载新版本", async (t) => {
  const { root, click } = fixture(t);
  const destinations = [];
  await initDownloadSection(root, {
    navigate: (url) => destinations.push(url),
    fetch: async (url, init) => {
      if (init.method !== "HEAD") return Response.json(latest);
      const asset = latest.assets.find((item) => item.url === url);
      return new Response(null, {
        headers: { "Content-Length": String(asset.size) },
      });
    },
  });
  assert.equal(
    root.querySelector('[data-platform][aria-selected="true"]').value,
    "windows",
  );
  assert.match(
    root.querySelector("[data-recommended-download]").href,
    /windows-amd64/,
  );
  assert.match(
    root.querySelector("[data-release-version]").textContent,
    /v1.2.3/,
  );
  click();
  await tick();
  assert.deepEqual(destinations, [
    latest.assets.find((a) => a.os === "windows" && a.kind === "installer").url,
  ]);
});

test("平台内分别展示安装版和免安装版，并刷新各自的下载信息", async (t) => {
  const { root } = fixture(t);
  const current = {
    ...latest,
    assets: latest.assets.map((asset) => ({
      ...asset,
      size: asset.size + 1024 * 1024,
      sha256: "a".repeat(64),
    })),
  };
  await initDownloadSection(root, {
    fetch: async () => Response.json(current),
  });
  const links = root.querySelectorAll(
    "[data-platform-panel] a[data-release-asset]",
  );
  assert.equal(links.length, 8);
  for (const link of links) {
    const asset = current.assets.find(
      (item) => item.name === link.dataset.releaseAsset,
    );
    assert.equal(asset.edition, "desktop");
    assert.equal(link.href, asset.url);
    assert.equal(
      link.closest("[data-package-kind]").dataset.packageKind,
      asset.kind,
    );
    assert.equal(
      link.closest("[data-platform-panel]").dataset.platformPanel,
      asset.os,
    );
    assert.equal(
      link.querySelector("[data-download-size]").textContent,
      `${(asset.size / 1024 / 1024).toFixed(1)} MB`,
    );
    assert.equal(
      link
        .closest("[data-download-package]")
        .querySelector("[data-checksum-value]").textContent,
      asset.sha256,
    );
  }
  const recommended = root.querySelector("[data-recommended-download]");
  assert.match(recommended.href, /windows-amd64-installer\.exe$/);
  assert.match(
    root.querySelector("[data-download-status]").textContent,
    /安装版/,
  );
});

test("免安装版下载失败时回退到同版本、同架构的桌面可执行文件", async (t) => {
  for (const [os, arch] of [
    ["darwin", "arm64"],
    ["windows", "amd64"],
    ["linux", "amd64"],
  ]) {
    await t.test(os, async (t) => {
      const { root, click, selectPlatform } = fixture(t);
      const destinations = [];
      await initDownloadSection(root, {
        navigate: (url) => destinations.push(url),
        fetch: async (_, init) =>
          init.method === "HEAD"
            ? new Response(null, { status: 404 })
            : Response.json(latest),
      });
      selectPlatform(os);
      const name = `sniffy-desktop-${os}-${arch}${os === "windows" ? ".exe" : ""}`;
      click(`[data-platform-panel="${os}"] [data-release-asset="${name}"]`);
      await tick();
      assert.deepEqual(destinations, [
        `https://github.com/mintfog/sniffy/releases/download/v1.2.3/${name}`,
      ]);
    });
  }
});

test("下载探测期间选择免安装版会取消安装版下载", async (t) => {
  const { root, click } = fixture(t);
  const requests = [];
  const destinations = [];
  await initDownloadSection(root, {
    navigate: (url) => destinations.push(url),
    fetch: async (url, init) => {
      if (init.method !== "HEAD") return Response.json(latest);
      return new Promise((resolve, reject) => {
        requests.push({ url, signal: init.signal, resolve });
        init.signal.addEventListener(
          "abort",
          () => reject(init.signal.reason),
          { once: true },
        );
      });
    },
  });
  click();
  await tick();
  click('[data-platform-panel="windows"] [data-package-kind="binary"] a');
  await tick();
  assert.equal(requests[0].signal.aborted, true);
  assert.equal(requests[1].signal.aborted, false);
  const portable = latest.assets.find(
    (asset) => asset.name === "sniffy-desktop-windows-amd64.exe",
  );
  requests[1].resolve(
    new Response(null, {
      headers: { "Content-Length": String(portable.size) },
    }),
  );
  await tick();
  assert.deepEqual(destinations, [portable.url]);
  assert.equal(root.querySelector("[aria-busy]"), null);
});

test("浏览器仅提供基础取消 API 时仍能刷新清单并下载", async (t) => {
  for (const [target, name] of [
    [AbortSignal, "any"],
    [AbortSignal, "timeout"],
    [AbortSignal.prototype, "throwIfAborted"],
  ]) {
    const original = Object.getOwnPropertyDescriptor(target, name);
    Object.defineProperty(target, name, {
      configurable: true,
      value: undefined,
    });
    t.after(() => Object.defineProperty(target, name, original));
  }
  const { root, click } = fixture(t);
  const destinations = [];
  const asset = latest.assets.find(
    (item) => item.os === "windows" && item.kind === "installer",
  );
  await initDownloadSection(root, {
    navigate: (url) => destinations.push(url),
    fetch: async (_, init) => {
      if (init.method !== "HEAD") return Response.json(latest);
      return new Response(null, {
        headers: { "Content-Length": String(asset.size) },
      });
    },
  });
  click();
  await tick();
  assert.deepEqual(destinations, [asset.url]);
});

test("连续点击同一链接时，忙碌状态保持到最后一次探测完成", async (t) => {
  const { root, click } = fixture(t);
  const destinations = [];
  const requests = [];
  const asset = latest.assets.find(
    (item) => item.os === "windows" && item.kind === "installer",
  );
  await initDownloadSection(root, {
    navigate: (url) => destinations.push(url),
    fetch: async (_, init) => {
      if (init.method !== "HEAD") return Response.json(latest);
      const pending = Promise.withResolvers();
      init.signal.addEventListener(
        "abort",
        () => pending.reject(init.signal.reason),
        {
          once: true,
        },
      );
      requests.push({ ...pending, signal: init.signal });
      return pending.promise;
    },
  });
  const link = root.querySelector("[data-recommended-download]");
  click();
  await tick();
  click();
  await tick();
  assert.equal(requests.length, 2);
  assert.equal(requests[0].signal.aborted, true);
  assert.equal(requests[1].signal.aborted, false);
  assert.equal(link.getAttribute("aria-busy"), "true");
  assert.deepEqual(destinations, []);

  requests[1].resolve(
    new Response(null, {
      headers: { "Content-Length": String(asset.size) },
    }),
  );
  await tick();
  assert.deepEqual(destinations, [asset.url]);
  assert.equal(link.hasAttribute("aria-busy"), false);
});

test("跳转异常会记录诊断并清理忙碌状态", async (t) => {
  const { root, click } = fixture(t);
  const error = new Error("跳转失败");
  const log = t.mock.method(console, "error", () => {});
  await initDownloadSection(root, {
    fetch: async (_, init) => {
      if (init.method !== "HEAD") return Response.json(latest);
      return new Response(null, { status: 404 });
    },
    navigate: () => {
      throw error;
    },
  });
  click();
  await tick();
  assert.equal(log.mock.callCount(), 1);
  assert.deepEqual(log.mock.calls[0].arguments, ["下载失败", error]);
  assert.equal(
    root.querySelector("[data-recommended-download]").hasAttribute("aria-busy"),
    false,
  );
});

test("Mac 芯片由用户选择，迟到的设备信息和清单保留这次下载选择", async (t) => {
  const { root, dom, click } = fixture(t, "Macintosh; Intel Mac OS X");
  const hints = Promise.withResolvers();
  const manifest = Promise.withResolvers();
  const destinations = [];
  Object.defineProperty(dom.window.navigator, "userAgentData", {
    value: { platform: "macOS", getHighEntropyValues: () => hints.promise },
  });
  const ready = initDownloadSection(root, {
    navigate: (url) => destinations.push(url),
    fetch: async (url, init) => {
      if (init.method !== "HEAD") return manifest.promise;
      const asset = latest.assets.find((item) => item.url === url);
      return new Response(null, {
        headers: { "Content-Length": String(asset.size) },
      });
    },
  });
  assert.equal(
    root.querySelector("[data-recommended-download]").dataset.releaseAsset,
    undefined,
  );
  assert.equal(
    root.querySelector('[data-platform-panel="darwin"]').hidden,
    false,
  );
  click(
    '[data-platform-panel="darwin"] [data-release-asset="sniffy-desktop-darwin-amd64-installer.dmg"]',
  );
  hints.resolve({ architecture: "arm", bitness: "64" });
  manifest.resolve(Response.json(latest));
  await ready;
  await tick();
  assert.deepEqual(destinations, [
    latest.assets.find(
      (asset) => asset.name === "sniffy-desktop-darwin-amd64-installer.dmg",
    ).url,
  ]);
});

test("移动设备不预选桌面包，手动选平台后可下载，R2 失败时使用对应版本 GitHub", async (t) => {
  const { root, selectPlatform, click } = fixture(t, "iPhone");
  const destinations = [];
  await initDownloadSection(root, {
    navigate: (url) => destinations.push(url),
    fetch: async (_, init) =>
      init.method === "HEAD"
        ? new Response(null, { status: 404 })
        : Response.json(latest),
  });
  assert.equal(
    root.querySelector("[data-recommended-download]").dataset.releaseAsset,
    undefined,
  );
  click();
  assert.equal(destinations.length, 0);
  selectPlatform("linux");
  click();
  await tick();
  assert.deepEqual(destinations, [
    "https://github.com/mintfog/sniffy/releases/download/v1.2.3/sniffy-desktop-linux-amd64-installer.deb",
  ]);
});

test("下载探测期间改选平台会取消先前跳转", async (t) => {
  const { root, selectPlatform, click } = fixture(t);
  const destinations = [];
  let aborted = false;
  await initDownloadSection(root, {
    navigate: (url) => destinations.push(url),
    fetch: async (_, init) => {
      if (init.method !== "HEAD") return Response.json(latest);
      return new Promise((_, reject) =>
        init.signal.addEventListener(
          "abort",
          () => {
            aborted = true;
            reject(init.signal.reason);
          },
          { once: true },
        ),
      );
    },
  });
  click();
  await tick();
  selectPlatform("linux");
  assert.equal(
    root.querySelector("[data-recommended-download]").hasAttribute("aria-busy"),
    false,
  );
  await tick();
  assert.equal(aborted, true);
  assert.equal(destinations.length, 0);
});

test("清单加载期间点击下载，会等待并使用新版本", async (t) => {
  const { click } = fixture(t);
  const pending = Promise.withResolvers();
  const destinations = [];
  const ready = initDownloadSection(document.querySelector("section"), {
    navigate: (url) => destinations.push(url),
    fetch: async (url, init) => {
      if (init.method !== "HEAD") return pending.promise;
      const asset = latest.assets.find((item) => item.url === url);
      return new Response(null, {
        headers: { "Content-Length": String(asset.size) },
      });
    },
  });
  click();
  assert.equal(destinations.length, 0);
  pending.resolve(Response.json(latest));
  await ready;
  await tick();
  assert.equal(destinations.length, 1);
  assert.match(destinations[0], /\/v1.2.3\//);
});

test("安装文档的下载表更新为当前清单的版本和链接", async (t) => {
  const { root } = fixture(t);
  root.innerHTML =
    "<table><tbody></tbody></table><span data-release-version></span><span data-release-date></span>";
  root.dataset.edition = "desktop";
  root.dataset.labels = JSON.stringify({
    os: { darwin: "macOS", windows: "Windows", linux: "Linux" },
    arch: copy.archNames,
  });
  await initDownloadTable(root, { fetch: async () => Response.json(latest) });
  assert.equal(
    root.querySelector("[data-release-version]").textContent,
    "1.2.3",
  );
  assert.equal(root.querySelectorAll("tbody tr").length, 4);
  for (const link of root.querySelectorAll("a"))
    assert.match(link.href, /\/v1.2.3\//);
});

test("平台标签支持方向键切换，并保留用户选择", async (t) => {
  const { root, dom } = fixture(t);
  await initDownloadSection(root, { fetch: async () => Response.json(latest) });
  const windows = root.querySelector('[data-platform][value="windows"]');
  windows.focus();
  windows.dispatchEvent(
    new dom.window.KeyboardEvent("keydown", {
      key: "ArrowRight",
      bubbles: true,
      cancelable: true,
    }),
  );
  const selected = root.querySelector('[data-platform][aria-selected="true"]');
  assert.equal(selected.value, "linux");
  assert.equal(dom.window.document.activeElement, selected);
  assert.equal(
    root.querySelectorAll('[data-platform][tabindex="0"]').length,
    1,
  );
  assert.equal(
    root.querySelectorAll("[data-platform-panel]:not([hidden])").length,
    1,
  );
  assert.equal(
    root.querySelector('[data-platform-panel="linux"]').hidden,
    false,
  );
});

test("未提供原生安装包时，主按钮展开平台选择并转移焦点", async (t) => {
  const { root, dom, click } = fixture(t, "Windows NT 10.0; ARM64");
  await initDownloadSection(root, { fetch: async () => Response.json(latest) });
  assert.equal(
    root.querySelector("[data-recommended-download]").dataset.releaseAsset,
    undefined,
  );
  assert.equal(
    root.querySelector("[data-download-status]").textContent,
    copy.unsupported,
  );
  const alternatives = root.querySelector("[data-download-alternatives]");
  assert.equal(alternatives.open, false);
  click();
  assert.equal(alternatives.open, true);
  assert.equal(
    dom.window.document.activeElement,
    root.querySelector('[data-platform][value="windows"]'),
  );
});
