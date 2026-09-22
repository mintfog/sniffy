export type Lang = "zh" | "en";

export const languages = {
  zh: { label: "简体中文", short: "中文", htmlLang: "zh-CN", base: "" },
  en: { label: "English", short: "EN", htmlLang: "en", base: "/en" },
} as const satisfies Record<
  Lang,
  { label: string; short: string; htmlLang: string; base: string }
>;

/** path 以 / 开头；中文位于根路径，其他语言使用各自的路径前缀。 */
export function localePath(lang: Lang, path: string): string {
  return `${languages[lang].base}${path}`;
}

export function otherLang(lang: Lang): Lang {
  return lang === "zh" ? "en" : "zh";
}

/** 多行文案以数组表达，由 Lines 组件插入 <br>；行数可随语言不同。 */
export type Paragraph = string[];

export interface HomeCopy {
  meta: {
    title: string;
    description: string;
    ogTitle: string;
    ogDescription: string;
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
    plugins: string;
    docs: string;
    github: string;
    download: string;
  };
  mobileNav: {
    features: string;
    plugins: string;
    docs: string;
    github: string;
  };
  hero: {
    eyebrow: string;
    /** highlight 使用主题色强调。 */
    title: { lead: string; highlight: string };
    description: Paragraph;
    download: string;
    readDocs: string;
    license: string;
    previewLeft: string;
    previewRight: string;
    footnote: string;
    footnoteMono: string;
    trace: {
      label: string;
      incoming: string;
      outgoing: string;
      explore: string;
    };
  };
  protocols: { intro: string; items: string[] };
  features: {
    eyebrow: string;
    title: string;
    aside: Paragraph;
    cards: Record<
      "capture" | "debug" | "plugins",
      {
        label: string;
        title: string;
        text: string;
        tags: string[];
        link: string;
      }
    >;
  };
  automation: {
    eyebrow: string;
    title: Paragraph;
    description: string;
    choose: string;
    rules: string;
    scripts: string;
    live: string;
    output: string;
    ready: string;
    pending: string;
    running: string;
    failed: string;
    local: string;
  };
  rules: {
    choose: string;
    link: string;
    sample: string;
    other: string;
    request: string;
    match: string;
    enabled: string;
    configuration: string;
    preview: string;
    before: string;
    after: string;
    forwarded: string;
    host: string;
    path: string;
    equals: string;
    startsWith: string;
    all: string;
    then: string;
    matched: string;
    missed: string;
    disabled: string;
    pass: string;
    fail: string;
    actions: Record<
      "redirect" | "header" | "mock",
      { label: string; caption: string; effect: string; note: string }
    >;
  };
  plugins: {
    choose: string;
    link: string;
    input: string;
    run: string;
    copy: string;
    copied: string;
    copyManually: string;
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
    eyebrow: string;
    title: Paragraph;
    copyCommand: string;
    commandCopied: string;
    commandCopyFailed: string;
    aside: Paragraph;
    desktop: { title: string; text: Paragraph; link: string };
    headless: { title: string; text: Paragraph; link: string };
  };
  footer: {
    tagline: string;
    docs: string;
    issues: string;
    github: string;
    copyright: string;
    license: string;
    mono: string;
  };
}

export interface DownloadCopy {
  meta: HomeCopy["meta"];
  title: Paragraph;
  description: Paragraph;
  installGuide: string;
  downloadFor: string;
  otherPlatforms: string;
  packageKinds: { installer: string; binary: string };
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
  menus: string[];
  connected: string;
  listening: string;
  networks: string;
  systemProxyOff: string;
  pause: string;
  filters: {
    all: string;
    ws: string;
    json: string;
    images: string;
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
  };
  resTabs: { body: string; headers: string; cookies: string; raw: string };
  overview: {
    state: string;
    method: string;
    scheme: string;
    statusCode: string;
    clientIP: string;
    contentType: string;
    process: string;
    startedAt: string;
    duration: string;
  };
  stateDone: string;
  stateError: string;
  views: { tree: string; raw: string; hex: string };
  notes: {
    binary: string;
    image: string;
    markup: string;
    connectionReset: string;
  };
  emptyBody: string;
  emptyCookies: string;
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
  };
}

export interface SiteCopy {
  home: HomeCopy;
  download: DownloadCopy;
  workbench: WorkbenchCopy;
}
