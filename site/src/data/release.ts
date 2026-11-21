import { loadBuildRelease } from "../lib/downloads";
import fallback from "./release.json";

/** desktop 为桌面版，headless 为无界面版。 */
export type ReleaseEdition = "desktop" | "headless";

/** installer 为安装包，binary 为免安装可执行文件。 */
export type ReleaseKind = "installer" | "binary";

export interface ReleaseAsset {
  /** 使用 GOOS 命名，macOS 对应 darwin。 */
  os: string;
  /** 使用 GOARCH 命名；universal 表示适用于该系统的所有架构。 */
  arch: string;
  edition: ReleaseEdition;
  kind: ReleaseKind;
  name: string;
  /** 文件的 HTTPS 直链。 */
  url: string;
  /** 文件大小，单位为字节。 */
  size: number;
  /** 小写十六进制 SHA256。 */
  sha256: string;
}

export interface ReleaseManifest {
  version: string;
  /** 日期或 RFC3339 时间字符串。 */
  publishedAt: string;
  /** 更新说明页；为空表示该版本没有单独的说明页。 */
  notesUrl: string;
  assets: ReleaseAsset[];
}

/**
 * 构建时读取的最新清单，供静态页面展示和实时清单读取失败时回退；astro dev 直接使用仓库清单，
 * 离线开发不必等待网络超时。
 * 只能在构建期（.astro 与静态端点）以值导入；浏览器与 worker 代码只导入类型。
 */
export const release: ReleaseManifest = import.meta.env.DEV
  ? (fallback as ReleaseManifest)
  : await loadBuildRelease(fallback as ReleaseManifest, {
      strict: process.env.WORKERS_CI === "1",
    });
