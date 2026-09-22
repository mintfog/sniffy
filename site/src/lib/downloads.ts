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
      !/^sniffy-[a-z0-9.-]+$/.test(asset.name) ||
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
  return release.assets.filter(
    (asset) =>
      asset.edition === "desktop" &&
      (!kind || asset.kind === kind) &&
      (!os || asset.os === os),
  );
}

export function formatSize(bytes: number): string {
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}

export function formatArchitecture(
  asset: ReleaseAsset,
  names: Record<string, string>,
): string {
  return names[`${asset.os}-${asset.arch}`] ?? names[asset.arch] ?? asset.arch;
}

export function githubDownload(
  release: ReleaseManifest,
  asset: ReleaseAsset,
): string {
  return `https://github.com/mintfog/sniffy/releases/download/v${release.version}/${asset.name}`;
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
