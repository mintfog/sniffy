import type { ReleaseAsset, ReleaseManifest } from "../data/release";
import type { DownloadCopy } from "../i18n/types";
import {
  desktopAssets,
  detectPlatform,
  formatArchitecture,
  formatSize,
  loadRelease,
  resolveDownload,
} from "./downloads.ts";

interface DownloadOptions {
  fetch?: typeof fetch;
  navigate?: (url: string) => void;
}

type DetectedPlatform = ReturnType<typeof detectPlatform>;

async function readBrowserPlatform(
  nav: Navigator,
  onDetected: (device: DetectedPlatform) => void,
) {
  type Hints = {
    platform?: string;
    mobile?: boolean;
    architecture?: string;
    bitness?: string;
  };
  const hints = (
    nav as Navigator & {
      userAgentData?: Hints & {
        getHighEntropyValues?: (keys: string[]) => Promise<Hints>;
      };
    }
  ).userAgentData;
  function applyHints(values: Hints = {}) {
    onDetected(
      detectPlatform({
        userAgent: nav.userAgent,
        maxTouchPoints: nav.maxTouchPoints,
        platform: hints?.platform,
        mobile: hints?.mobile,
        ...values,
      }),
    );
  }
  applyHints(hints);
  if (!hints?.getHighEntropyValues) return;
  let timeout: number | undefined;
  try {
    // 架构查询有时间上限，避免浏览器迟迟不响应导致下载页一直加载。
    const values = await Promise.race([
      hints.getHighEntropyValues(["architecture", "bitness"]),
      new Promise<undefined>((resolve) => {
        timeout = window.setTimeout(resolve, 1500);
      }),
    ]);
    if (values) applyHints(values);
  } catch {
    // 浏览器可能拒绝提供架构信息，此时保留基础平台识别结果。
  } finally {
    window.clearTimeout(timeout);
  }
}

function bindDownloadLinks(
  root: HTMLElement,
  latestRelease: Promise<ReleaseManifest>,
  options: DownloadOptions,
) {
  let activeDownload:
    | { controller: AbortController; link: HTMLAnchorElement }
    | undefined;
  const navigate =
    options.navigate ?? ((url: string) => window.location.assign(url));

  function cancelDownload() {
    if (!activeDownload) return;
    activeDownload.controller.abort();
    activeDownload.link.removeAttribute("aria-busy");
    activeDownload = undefined;
  }

  root.addEventListener("click", async (event) => {
    const target = event.target as Element;
    const link = target.closest<HTMLAnchorElement>("a[data-release-asset]");
    if (
      !link ||
      event.ctrlKey ||
      event.metaKey ||
      event.shiftKey ||
      event.altKey ||
      event.button !== 0
    ) {
      return;
    }
    const name = link.dataset.releaseAsset;
    event.preventDefault();
    cancelDownload();
    const download = { controller: new AbortController(), link };
    activeDownload = download;
    const { signal } = download.controller;
    link.setAttribute("aria-busy", "true");
    try {
      // 只保留点击时的文件名；清单刷新后用同一文件名取得新版本地址。
      const release = await latestRelease;
      if (signal.aborted) return;
      const asset = release.assets.find((item) => item.name === name);
      if (!asset) return;
      const url = await resolveDownload(release, asset, {
        signal,
        fetch: options.fetch,
      });
      if (!signal.aborted) navigate(url);
    } catch (error) {
      if (!signal.aborted) console.error("下载失败", error);
    } finally {
      // 被取消的任务也会进入 finally，链接状态只由当前任务清理。
      if (activeDownload === download) {
        link.removeAttribute("aria-busy");
        activeDownload = undefined;
      }
    }
  });
  return cancelDownload;
}

export async function initDownloadSection(
  root: HTMLElement,
  options: DownloadOptions = {},
) {
  root.setAttribute("aria-busy", "true");
  let release: ReleaseManifest = JSON.parse(root.dataset.release!);
  const copy: DownloadCopy = JSON.parse(root.dataset.copy!);
  const tabs = root.querySelector<HTMLElement>("[data-platform-tabs]")!;
  const platformButtons = [
    ...tabs.querySelectorAll<HTMLButtonElement>("[data-platform]"),
  ];
  const platformPanels = [
    ...root.querySelectorAll<HTMLElement>("[data-platform-panel]"),
  ];
  const downloadLink = root.querySelector<HTMLAnchorElement>(
    "[data-recommended-download]",
  )!;
  const downloadLabel = root.querySelector<HTMLElement>(
    "[data-download-label]",
  )!;
  const downloadSize = root.querySelector<HTMLElement>(
    "[data-recommended-size]",
  )!;
  const downloadStatus = root.querySelector<HTMLElement>(
    "[data-download-status]",
  )!;
  const versionLabel = root.querySelector<HTMLElement>(
    "[data-release-version]",
  )!;
  const downloadControl = root.querySelector<HTMLElement>(
    "[data-download-control]",
  )!;
  const platformCatalog = root.querySelector<HTMLDetailsElement>(
    "[data-download-alternatives]",
  )!;
  const chipMenu = root.querySelector<HTMLElement>("[data-download-menu]")!;
  const chipArrow = root.querySelector<HTMLElement>("[data-chip-arrow]")!;
  const chipOptionTemplate = root.querySelector<HTMLTemplateElement>(
    "[data-download-option-template]",
  )!;
  // 克隆构建时的卡片以保留 Astro 的样式作用域属性，刷新清单后继续复用。
  const packageTemplate = root
    .querySelector<HTMLElement>("[data-download-package]")!
    .cloneNode(true) as HTMLElement;
  const nav = root.ownerDocument.defaultView!.navigator;
  // 用户开始选择后，迟到的浏览器识别结果不能再切换平台。
  let hasUserSelection = false;
  let detectedPlatform: DetectedPlatform = {};
  let selectedOS = platformButtons[0].value;
  const latestRelease = loadRelease(release, options.fetch);
  const cancelDownload = bindDownloadLinks(root, latestRelease, options);

  function closeMenu(restoreFocus = false) {
    chipMenu.hidden = true;
    if (downloadLink.hasAttribute("aria-haspopup")) {
      downloadLink.setAttribute("aria-expanded", "false");
    }
    if (restoreFocus) downloadLink.focus();
  }

  function openMenu(last = false) {
    if (!downloadLink.hasAttribute("aria-haspopup")) return;
    cancelDownload();
    hasUserSelection = true;
    chipMenu.hidden = false;
    downloadLink.setAttribute("aria-expanded", "true");
    const choices = chipMenu.querySelectorAll<HTMLAnchorElement>(
      "[data-download-choice]",
    );
    choices[last ? choices.length - 1 : 0]?.focus();
  }

  function updateMenu() {
    const focusedElement = root.ownerDocument
      .activeElement as HTMLElement | null;
    const hadMenuFocus = chipMenu.contains(focusedElement);
    const focusedName = focusedElement?.dataset.releaseAsset;
    const choices = desktopAssets(release, "darwin", "installer").map(
      (asset) => {
        const choice = chipOptionTemplate.content.firstElementChild!.cloneNode(
          true,
        ) as HTMLAnchorElement;
        choice.href = asset.url;
        choice.dataset.releaseAsset = asset.name;
        choice.querySelector("[data-option-architecture]")!.textContent =
          formatArchitecture(asset, copy.archNames);
        choice.querySelector("[data-option-size]")!.textContent = formatSize(
          asset.size,
        );
        return choice;
      },
    );
    chipMenu.querySelector("[data-menu-options]")!.replaceChildren(...choices);
    // 替换 DOM 会丢失键盘焦点，用文件名恢复到同一芯片选项。
    if (hadMenuFocus) {
      const choice =
        choices.find((item) => item.dataset.releaseAsset === focusedName) ??
        choices[0];
      choice?.focus();
    }
  }

  function updateRecommendation() {
    const selectedButton = platformButtons.find(
      (button) => button.value === selectedOS,
    )!;
    const downloadText = copy.downloadFor.replace(
      "{platform}",
      selectedButton.textContent!.trim(),
    );
    // macOS 的 Intel UA 也可能来自 Apple 芯片设备，统一交由用户选择芯片。
    const chooseMacChip =
      selectedOS === "darwin" &&
      (detectedPlatform.os === "darwin" || hasUserSelection);
    chipArrow.hidden = !chooseMacChip;
    if (chooseMacChip) {
      downloadLink.href = "#download-platforms";
      delete downloadLink.dataset.releaseAsset;
      downloadLink.setAttribute("role", "button");
      downloadLink.setAttribute("aria-haspopup", "menu");
      downloadLink.setAttribute("aria-controls", chipMenu.id);
      downloadLink.setAttribute("aria-expanded", String(!chipMenu.hidden));
      downloadLabel.textContent = downloadText;
      downloadStatus.textContent = copy.chooseArchitecture;
      downloadSize.hidden = true;
      return;
    }
    closeMenu();
    for (const attribute of [
      "role",
      "aria-haspopup",
      "aria-controls",
      "aria-expanded",
    ]) {
      downloadLink.removeAttribute(attribute);
    }
    const assets = desktopAssets(release, selectedOS, "installer");
    let asset: ReleaseAsset | undefined;
    // 手动选择可用于下载其他设备的安装包，此时不沿用本机架构。
    if (hasUserSelection || !detectedPlatform.arch) {
      asset = assets.length === 1 ? assets[0] : undefined;
    } else {
      asset = assets.find((item) => item.arch === detectedPlatform.arch);
    }
    downloadSize.hidden = !asset;
    if (!asset) {
      downloadLink.href = "#download-platforms";
      delete downloadLink.dataset.releaseAsset;
      downloadLabel.textContent = copy.choosePlatform;
      downloadStatus.textContent =
        !hasUserSelection && detectedPlatform.arch
          ? copy.unsupported
          : copy.choose;
      return;
    }
    downloadLink.href = asset.url;
    downloadLink.dataset.releaseAsset = asset.name;
    downloadLabel.textContent = downloadText;
    downloadSize.textContent = formatSize(asset.size);
    downloadStatus.textContent = `${copy.packageKinds.installer} · ${formatArchitecture(asset, copy.archNames)}`;
  }

  function showPlatform() {
    platformButtons.forEach((button, index) => {
      const selected = button.value === selectedOS;
      button.setAttribute("aria-selected", String(selected));
      button.tabIndex = selected ? 0 : -1;
      if (selected) tabs.style.setProperty("--platform-index", String(index));
    });
    for (const panel of platformPanels) {
      panel.hidden = panel.dataset.platformPanel !== selectedOS;
    }
    updateRecommendation();
  }

  function createPackageCard(asset: ReleaseAsset) {
    const card = packageTemplate.cloneNode(true) as HTMLElement;
    const anchor = card.querySelector<HTMLAnchorElement>(
      "[data-release-asset]",
    )!;
    const architecture = formatArchitecture(asset, copy.archNames);
    anchor.href = asset.url;
    anchor.dataset.releaseAsset = asset.name;
    anchor.setAttribute(
      "aria-label",
      `${copy.packageKinds[asset.kind]} · ${architecture}`,
    );
    card.querySelector("[data-asset-architecture]")!.textContent = architecture;
    card.querySelector("[data-download-size]")!.textContent = formatSize(
      asset.size,
    );
    card.querySelector("[data-checksum-value]")!.textContent = asset.sha256;
    return card;
  }

  downloadLink.addEventListener("click", (event) => {
    if (downloadLink.hasAttribute("aria-haspopup")) {
      event.preventDefault();
      if (chipMenu.hidden) openMenu();
      else closeMenu();
    } else if (!downloadLink.dataset.releaseAsset) {
      event.preventDefault();
      hasUserSelection = true;
      platformCatalog.open = true;
      platformButtons.find((button) => button.value === selectedOS)!.focus();
    }
  });
  downloadControl.addEventListener("keydown", (event) => {
    if (
      chipMenu.contains(event.target as Node) ||
      !downloadLink.hasAttribute("aria-haspopup")
    ) {
      return;
    }
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault();
      openMenu(event.key === "ArrowUp");
    } else if (event.key === " " && event.target === downloadLink) {
      event.preventDefault();
      openMenu();
    }
  });
  chipMenu.addEventListener("keydown", (event) => {
    const choices = [
      ...chipMenu.querySelectorAll<HTMLAnchorElement>("[data-download-choice]"),
    ];
    const index = choices.indexOf(
      root.ownerDocument.activeElement as HTMLAnchorElement,
    );
    let next: number;
    switch (event.key) {
      case "ArrowDown":
        next = (index + 1) % choices.length;
        break;
      case "ArrowUp":
        next = (index + choices.length - 1) % choices.length;
        break;
      case "Home":
        next = 0;
        break;
      case "End":
        next = choices.length - 1;
        break;
      case "Escape":
        event.preventDefault();
        closeMenu(true);
        return;
      default:
        return;
    }
    event.preventDefault();
    choices[next]?.focus();
  });
  root.ownerDocument.addEventListener("pointerdown", (event) => {
    if (!downloadControl.contains(event.target as Node)) closeMenu();
  });
  downloadControl.addEventListener("focusout", (event) => {
    if (
      event.relatedTarget &&
      !downloadControl.contains(event.relatedTarget as Node)
    ) {
      closeMenu();
    }
  });

  for (const button of platformButtons) {
    button.addEventListener("click", () => {
      cancelDownload();
      closeMenu();
      hasUserSelection = true;
      selectedOS = button.value;
      showPlatform();
    });
    button.addEventListener("keydown", (event) => {
      const index = platformButtons.indexOf(button);
      let next: number;
      switch (event.key) {
        case "ArrowRight":
          next = (index + 1) % platformButtons.length;
          break;
        case "ArrowLeft":
          next = (index + platformButtons.length - 1) % platformButtons.length;
          break;
        case "Home":
          next = 0;
          break;
        case "End":
          next = platformButtons.length - 1;
          break;
        default:
          return;
      }
      event.preventDefault();
      platformButtons[next].click();
      platformButtons[next].focus();
    });
  }
  root.addEventListener("click", (event) => {
    const target = event.target as Element;
    const anchor = target.closest<HTMLAnchorElement>("a[data-release-asset]");
    if (!anchor) return;
    hasUserSelection = true;
    if (chipMenu.contains(anchor)) closeMenu(true);
  });
  tabs.hidden = false;
  updateMenu();

  const platformReady = readBrowserPlatform(nav, (detected) => {
    if (hasUserSelection) return;
    detectedPlatform = detected;
    selectedOS = detectedPlatform.os ?? platformButtons[0].value;
    showPlatform();
  });

  [release] = await Promise.all([latestRelease, platformReady]);
  versionLabel.textContent = `v${release.version}`;
  updateMenu();
  for (const panel of platformPanels) {
    const assets = desktopAssets(release, panel.dataset.platformPanel);
    for (const group of panel.querySelectorAll<HTMLElement>(
      "[data-package-kind]",
    )) {
      const matching = assets.filter(
        (asset) => asset.kind === group.dataset.packageKind,
      );
      group.hidden = matching.length === 0;
      group
        .querySelector("[data-platform-assets]")!
        .replaceChildren(...matching.map(createPackageCard));
    }
  }
  updateRecommendation();
  root.removeAttribute("data-loading");
  root.setAttribute("aria-busy", "false");
}

export async function initDownloadTable(
  root: HTMLElement,
  options: DownloadOptions = {},
) {
  const initialRelease: ReleaseManifest = JSON.parse(root.dataset.release!);
  const labels: { os: Record<string, string>; arch: Record<string, string> } =
    JSON.parse(root.dataset.labels!);
  const latestRelease = loadRelease(initialRelease, options.fetch);
  bindDownloadLinks(root, latestRelease, options);
  const release = await latestRelease;
  const assets =
    root.dataset.edition === "desktop"
      ? desktopAssets(release, undefined, "installer")
      : release.assets.filter((asset) => asset.edition === "headless");
  const rows = assets.map((asset) => {
    const row = document.createElement("tr");
    row.insertCell().textContent = labels.os[asset.os] ?? asset.os;
    row.insertCell().textContent = formatArchitecture(asset, labels.arch);

    const anchor = document.createElement("a");
    anchor.href = asset.url;
    anchor.dataset.releaseAsset = asset.name;
    anchor.title = `SHA256: ${asset.sha256}`;
    const code = document.createElement("code");
    code.textContent = asset.name;
    anchor.append(code);
    row.insertCell().append(anchor);

    row.insertCell().textContent = formatSize(asset.size);
    return row;
  });
  root.querySelector("tbody")!.replaceChildren(...rows);
  root.querySelector("[data-release-version]")!.textContent = release.version;
  root.querySelector("[data-release-date]")!.textContent = release.publishedAt;
}
