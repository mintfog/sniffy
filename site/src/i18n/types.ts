export type Lang = "zh" | "en";

export const languages = {
  zh: {
    label: "简体中文",
    short: "中文",
    htmlLang: "zh-CN",
    ogLocale: "zh_CN",
    base: "",
  },
  en: {
    label: "English",
    short: "EN",
    htmlLang: "en",
    ogLocale: "en_US",
    base: "/en",
  },
} as const satisfies Record<
  Lang,
  {
    label: string;
    short: string;
    htmlLang: string;
    ogLocale: string;
    base: string;
  }
>;

/** path 以 / 开头；中文位于根路径，其他语言使用各自的路径前缀。 */
export function localePath(lang: Lang, path: string): string {
  return `${languages[lang].base}${path}`;
}

export function otherLang(lang: Lang): Lang {
  return lang === "zh" ? "en" : "zh";
}

/** 多行文案以数组表达，由调用处按行渲染；行数可随语言不同。 */
export type Paragraph = string[];

export type FeatureKey =
  | "capture"
  | "rules"
  | "breakpoints"
  | "plugins"
  | "compose"
  | "har";

export type DemoKey = "sse" | "breakpoint" | "mock";

export interface HomeCopy {
  meta: {
    title: string;
    description: string;
    ogTitle: string;
    ogDescription: string;
    ogImageAlt: string;
  };
  a11y: {
    skipLink: string;
    brandHome: string;
    mainNav: string;
    mobileNav: string;
    openMenu: string;
    closeMenu: string;
    switchLang: string;
    copyDone: string;
    copyFailed: string;
  };
  nav: {
    features: string;
    demos: string;
    plugins: string;
    deploy: string;
    docs: string;
    star: string;
    download: string;
  };
  mobileNav: {
    features: string;
    demos: string;
    plugins: string;
    deploy: string;
    docs: string;
    github: string;
  };
  hero: {
    metaLine: string;
    titleBefore: string;
    titleRotate: string[];
    titleAfter: string;
    description: Paragraph;
    download: string;
    defaultSub: string;
    optionPrefix: string;
    windowsName: string;
    moreDownloads: string;
    allDownloads: string;
    allDownloadsSub: string;
    protocolsLabel: string;
    protocols: string[];
    footnote: string;
  };
  features: {
    title: string;
    description: string;
    cards: Record<
      FeatureKey,
      {
        title: string;
        text: string;
        link: string;
      }
    >;
  };
  demos: {
    title: string;
    description: string;
    choose: string;
    loading: string;
    play: string;
    replay: string;
    items: Record<
      DemoKey,
      {
        title: string;
        summary: string;
        endpoint: string;
        alt: string;
        link: string;
      }
    >;
  };
  plugins: {
    title: string;
    description: string;
    choose: string;
    link: string;
    copy: string;
    copied: string;
    copyManually: string;
    input: string;
    output: string;
    run: string;
    ready: string;
    pending: string;
    running: string;
    failed: string;
    footnote: string;
    scenarios: Record<
      "signing" | "mock" | "token",
      { label: string; note: string }
    >;
    signing: {
      amount: string;
      title: string;
      secret: string;
      done: string;
    };
    mock: {
      title: string;
      status: string;
      done: string;
      scenarios: Record<"success" | "unauthorized" | "unavailable", string>;
    };
    token: {
      response: string;
      value: string;
      title: string;
      stored: string;
      done: string;
    };
  };
  editions: {
    title: string;
    description: string;
    desktop: {
      title: string;
      tag: string;
      text: string;
      points: string[];
      matrix: {
        macos: string;
        windows: string;
        linux: string;
      };
      cta: string;
      link: string;
    };
    headless: {
      title: string;
      tag: string;
      text: string;
      points: string[];
      link: string;
      apiLink: string;
    };
    copyCommand: string;
    commandCopied: string;
    commandCopyFailed: string;
  };
  oss: {
    title: string;
    description: string;
    downloadCta: string;
    starCta: string;
    issuesCta: string;
    sourceTitle: string;
    sourceNote: string;
    copySource: string;
    sourceCopied: string;
    sourceCopyFailed: string;
  };
  footer: {
    tagline: string;
    columns: {
      product: string;
      docs: string;
      community: string;
    };
    links: {
      download: string;
      desktop: string;
      headless: string;
      releases: string;
      quickStart: string;
      https: string;
      rules: string;
      plugins: string;
      api: string;
      github: string;
      issues: string;
      license: string;
      thirdParty: string;
    };
    copyright: string;
    licenseText: string;
  };
}

export interface DownloadCopy {
  meta: Omit<HomeCopy["meta"], "ogImageAlt">;
  loading: string;
  title: Paragraph;
  description: Paragraph;
  installGuide: string;
  downloadFor: string;
  otherPlatforms: string;
  previousVersions: string;
  packageKinds: { installer: string; binary: string };
  binaryFormat: string;
  appImageUnavailable: string;
  recommended: string;
  options: Record<
    "macos" | "windows" | "linux",
    {
      name: string;
      detail: string;
      installerDetail: string;
      binaryDetail: string;
    }
  >;
  /** 优先使用 "<os>-<arch>" 的名称，其次使用 arch，缺省时显示架构标识。 */
  archNames: Record<string, string>;
  platform: string;
  choosePlatform: string;
  chooseArchitecture: string;
  choose: string;
  unsupported: string;
  headlessLink: string;
}

/** 工作台文案与 web/src/i18n/locales 中的应用用词保持一致。 */
export interface WorkbenchCopy {
  demoLabel: string;
  connected: string;
  listening: string;
  networks: string;
  systemProxyOn: string;
  pause: string;
  filters: {
    all: string;
    text: string;
    images: string;
    media: string;
    binary: string;
    errors: string;
  };
  columns: {
    status: string;
    index: string;
    method: string;
    url: string;
    duration: string;
  };
  reqTabs: {
    overview: string;
    params: string;
    headers: string;
    body: string;
    cookies: string;
    raw: string;
    executions: string;
  };
  resTabs: { body: string; headers: string; cookies: string; raw: string };
  overview: {
    state: string;
    method: string;
    scheme: string;
    clientIP: string;
    contentType: string;
    process: string;
    startedAt: string;
    duration: string;
    size: string;
  };
  stateDone: string;
  stateError: string;
  views: { tree: string; raw: string; hex: string };
  json: { items: string; keys: string };
  notes: {
    binary: string;
    image: string;
    markup: string;
    connectionReset: string;
    switchingProtocols: string;
    notModified: string;
    grpcPayload: string;
  };
  executions: Record<
    "pluginSign" | "ruleMock" | "pluginToken",
    {
      kind: string;
      name: string;
      detail: string;
    }
  >;
  emptyBody: string;
  emptyCookies: string;
  emptyExecutions: string;
  statusBar: {
    capturing: string;
    showing: string;
    selected: string;
    live: string;
  };
  empty: string;
  emptyDetail: string;
  a11y: {
    sessionList: string;
    filters: string;
    requestTabs: string;
    responseTabs: string;
    bodyViews: string;
    switchToLight: string;
    switchToDark: string;
  };
}

export interface SiteCopy {
  home: HomeCopy;
  download: DownloadCopy;
  workbench: WorkbenchCopy;
}
