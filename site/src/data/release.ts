import manifest from "./release.json";

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

/** 构建时嵌入的清单，供静态页面展示和实时清单读取失败时回退。 */
export const release = manifest as ReleaseManifest;
