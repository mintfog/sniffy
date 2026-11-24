import type {
  ReleaseAsset,
  ReleaseKind,
  ReleaseManifest,
} from "../data/release";

export type Platform = "darwin" | "windows" | "linux";

export interface Device {
  userAgent: string;
  platform?: string;
  mobile?: boolean;
  maxTouchPoints?: number;
  architecture?: string;
  bitness?: string;
}

export function detectPlatform(device: Device): {
  os?: Platform;
  arch?: string;
} {
  const ua = device.userAgent;
  // iPadOS 的桌面模式使用 Macintosh UA，需要结合触控点数识别。
  if (
    device.mobile ||
    /Android|iPhone|iPad|iPod/i.test(ua) ||
    (/Macintosh/i.test(ua) && (device.maxTouchPoints ?? 0) > 1)
  ) {
    return {};
  }
  const platform = device.platform || ua;
  let os: Platform | undefined;
  if (/Windows/i.test(platform)) {
    os = "windows";
  } else if (/macOS|Macintosh|Mac OS X/i.test(platform)) {
    os = "darwin";
  } else if (/Linux/i.test(platform)) {
    os = "linux";
  }
  if (!os) return {};
  if (device.bitness === "32") return { os, arch: "unsupported" };
  if (device.architecture === "arm" && device.bitness === "64") {
    return { os, arch: "arm64" };
  }
  if (device.architecture === "x86" && device.bitness === "64") {
    return { os, arch: "amd64" };
  }
  // Apple 芯片上的浏览器也会报告 Intel，普通 UA 不能确定 macOS 架构。
  if (os === "darwin") return { os };
  if (/aarch64|arm64/i.test(ua)) return { os, arch: "arm64" };
  if (/x86_64|amd64|Win64|WOW64/i.test(ua)) return { os, arch: "amd64" };
  if (/i[3-6]86|armv7/i.test(ua)) return { os, arch: "unsupported" };
  return { os };
}

function isHTTPSURL(value: unknown): value is string {
  if (typeof value !== "string") return false;
  try {
    const url = new URL(value);
    return url.protocol === "https:" && !url.username && !url.password;
  } catch {
    return false;
  }
}

export function parseRelease(value: unknown): ReleaseManifest {
  const release = value as Partial<ReleaseManifest> | null;
  if (
    !release ||
    typeof release.version !== "string" ||
    !/^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$/.test(
      release.version,
    ) ||
    typeof release.publishedAt !== "string" ||
    typeof release.notesUrl !== "string" ||
    !Array.isArray(release.assets) ||
    release.assets.length === 0
  ) {
    throw new Error("发布清单格式无效");
  }
  const names = new Set<string>();
  for (const asset of release.assets) {
    if (
      !asset ||
      !["darwin", "windows", "linux"].includes(asset.os) ||
      !["amd64", "arm64", "universal"].includes(asset.arch) ||
      !["desktop", "headless"].includes(asset.edition) ||
      !["installer", "binary"].includes(asset.kind) ||
      typeof asset.name !== "string" ||
      !/^sniffy-[a-z0-9.-]+(?:\.AppImage)?$/.test(asset.name) ||
      names.has(asset.name) ||
      !isHTTPSURL(asset.url) ||
      !Number.isSafeInteger(asset.size) ||
      asset.size <= 0 ||
      typeof asset.sha256 !== "string" ||
      !/^[a-f0-9]{64}$/.test(asset.sha256)
    ) {
      throw new Error("发布清单的制品信息无效");
    }
    names.add(asset.name);
  }
  return release as ReleaseManifest;
}

export function desktopAssets(
  release: ReleaseManifest,
  os?: string,
  kind?: ReleaseKind,
): ReleaseAsset[] {
  const assets = release.assets.filter(
    (asset) =>
      asset.edition === "desktop" &&
      !isAppArchive(asset) &&
      (!kind || asset.kind === kind) &&
      (!os || asset.os === os),
  );
  return os === "linux" && kind === "binary"
    ? assets.sort((a, b) => Number(isAppImage(b)) - Number(isAppImage(a)))
    : assets;
}

// 应用归档只供客户端应用内更新整包替换，不作为下载项。
function isAppArchive(asset: ReleaseAsset): boolean {
  return asset.kind === "binary" && asset.name.endsWith(".app.tar.gz");
}

export function isAppImage(asset: ReleaseAsset): boolean {
  return (
    asset.os === "linux" &&
    asset.edition === "desktop" &&
    asset.kind === "binary" &&
    asset.name.endsWith(".AppImage")
  );
}

export function formatSize(bytes: number): string {
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}

export function formatAssetLabel(
  asset: ReleaseAsset,
  names: Record<string, string>,
  binaryFormat?: string,
): string {
  const architecture =
    names[`${asset.os}-${asset.arch}`] ?? names[asset.arch] ?? asset.arch;
  if (isAppImage(asset)) return `${architecture} · AppImage`;
  if (
    binaryFormat &&
    asset.os === "linux" &&
    asset.edition === "desktop" &&
    asset.kind === "binary"
  ) {
    return `${architecture} · ${binaryFormat}`;
  }
  return architecture;
}

export function githubDownload(
  release: ReleaseManifest,
  asset: ReleaseAsset,
): string {
  return `https://github.com/mintfog/sniffy/releases/download/v${release.version}/${asset.name}`;
}

/** 页面 data-release 去掉远端清单里供应用内更新使用的更新说明正文，它不该进静态 HTML。 */
export const pageRelease = ({
  version,
  publishedAt,
  notesUrl,
  assets,
}: ReleaseManifest): ReleaseManifest => ({ version, publishedAt, notesUrl, assets });

const RELEASE_MANIFEST_URL = "https://cdn.gosniffy.com/release.json";

const MAX_MANIFEST_LENGTH = 256 * 1024;

/** 读取 R2 上的最新发布清单，worker 与构建期共用同一套校验。 */
export async function fetchRelease({
  timeoutMs = 3000,
  request = fetch,
}: { timeoutMs?: number; request?: typeof fetch } = {}): Promise<ReleaseManifest> {
  const response = await request(RELEASE_MANIFEST_URL, {
    signal: AbortSignal.timeout(timeoutMs),
    redirect: "manual",
    cache: "no-store",
  });
  if (!response.ok) throw new Error(`发布清单不可用：HTTP ${response.status}`);
  const text = await response.text();
  if (text.length > MAX_MANIFEST_LENGTH) throw new Error("发布清单过大");
  return parseRelease(JSON.parse(text));
}

/**
 * 构建期读取最新清单，下载页的静态 HTML 与 /release.json 静态兜底都由它生成。
 * strict 用于正式构建：沿用仓库清单会让线上静态页退回旧版本，因此重试仍失败时中止构建，
 * 线上保留上一次部署；本地构建则回退到仓库清单。
 */
export async function loadBuildRelease(
  fallback: ReleaseManifest,
  {
    strict = false,
    retryDelayMs = 2000,
    request = fetch,
  }: {
    strict?: boolean;
    retryDelayMs?: number;
    request?: typeof fetch;
  } = {},
): Promise<ReleaseManifest> {
  const attempts = strict ? 3 : 1;
  let failure: unknown;
  for (let attempt = 1; attempt <= attempts; attempt++) {
    try {
      return await fetchRelease({ timeoutMs: 10_000, request });
    } catch (error) {
      failure = error;
    }
    if (attempt < attempts) {
      await new Promise((done) => setTimeout(done, retryDelayMs * attempt));
    }
  }
  const reason = failure instanceof Error ? failure.message : String(failure);
  if (strict) throw new Error(`读取发布清单失败：${reason}`);
  console.warn(`读取发布清单失败，沿用仓库中的 v${fallback.version}：${reason}`);
  return fallback;
}

export async function loadRelease(
  fallback: ReleaseManifest,
  request: typeof fetch = fetch,
): Promise<ReleaseManifest> {
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), 4000);
  try {
    const response = await request("/release.json", {
      signal: controller.signal,
      cache: "no-store",
      credentials: "omit",
    });
    if (!response.ok) return fallback;
    return parseRelease(await response.json());
  } catch {
    return fallback;
  } finally {
    clearTimeout(timeout);
  }
}

export async function resolveDownload(
  release: ReleaseManifest,
  asset: ReleaseAsset,
  options: {
    signal?: AbortSignal;
    timeoutMs?: number;
    fetch?: typeof fetch;
  } = {},
): Promise<string> {
  const { signal, timeoutMs = 2500, fetch: request = fetch } = options;
  if (signal?.aborted) throw signal.reason;

  const controller = new AbortController();
  const cancel = () => controller.abort();
  signal?.addEventListener("abort", cancel, { once: true });
  const timeout = setTimeout(cancel, timeoutMs);
  let response: Response;
  try {
    response = await request(asset.url, {
      method: "HEAD",
      signal: controller.signal,
      mode: "cors",
      cache: "no-store",
      credentials: "omit",
      redirect: "error",
      referrerPolicy: "no-referrer",
    });
  } catch {
    // 用户改选会终止本次下载；超时和网络错误则交给 GitHub 下载源。
    if (signal?.aborted) throw signal.reason;
    return githubDownload(release, asset);
  } finally {
    clearTimeout(timeout);
    signal?.removeEventListener("abort", cancel);
  }
  if (signal?.aborted) throw signal.reason;
  const matchesSize =
    Number(response.headers.get("content-length")) === asset.size;
  if (response.ok && matchesSize) return asset.url;
  return githubDownload(release, asset);
}
