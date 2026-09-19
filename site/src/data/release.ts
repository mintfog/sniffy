import manifest from "./release.json";

/** desktop = 桌面版,headless = 无界面版可执行文件。 */
export type ReleaseEdition = "desktop" | "headless";

/** installer = 安装包,binary = 免安装可执行文件。 */
export type ReleaseKind = "installer" | "binary";

export interface ReleaseAsset {
  /** GOOS,如 darwin / windows / linux。 */
  os: string;
  /** GOARCH,如 amd64 / arm64。 */
  arch: string;
  edition: ReleaseEdition;
  kind: ReleaseKind;
  name: string;
  /** 对象存储上的 https 直链。 */
  url: string;
  size: number;
  /** 小写十六进制 SHA256。 */
  sha256: string;
}

export interface ReleaseManifest {
  version: string;
  /** 日期或 RFC3339 时间字符串。 */
  publishedAt: string;
  /** 更新说明页;为空表示该版本没有单独的说明页。 */
  notesUrl: string;
  assets: ReleaseAsset[];
}

/** 由 scripts/release-manifest.sh 生成，官网下载区与客户端共用。 */
export const release = manifest as ReleaseManifest;

/** 取指定系统的桌面安装包，保留清单顺序。 */
export function desktopDownloads(os: string): ReleaseAsset[] {
  return release.assets.filter(
    (a) => a.os === os && a.edition === "desktop" && a.kind === "installer",
  );
}

/** 取全部无界面版产物，保留清单顺序。 */
export function headlessDownloads(): ReleaseAsset[] {
  return release.assets.filter((a) => a.edition === "headless");
}

export function formatSize(bytes: number): string {
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}
