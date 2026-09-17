export type Lang = "zh" | "en";

export const languages = {
  zh: { label: "简体中文", short: "中文", htmlLang: "zh-CN", base: "" },
  en: { label: "English", short: "EN", htmlLang: "en", base: "/en" },
} as const satisfies Record<
  Lang,
  { label: string; short: string; htmlLang: string; base: string }
>;

/** 站内链接统一经此拼接：中文在根路径，英文在 /en 下。 */
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
    copyExample: string;
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
  mobileNav: { features: string; plugins: string; docs: string; github: string };
  hero: {
    /** 主标题的第二段带主色，不是任意分行，故不用 Paragraph。 */
    title: { lead: string; highlight: string };
    description: Paragraph;
    download: string;
    readDocs: string;
    license: string;
    previewLeft: string;
    previewRight: string;
    footnote: string;
    footnoteMono: string;
  };
  protocols: { intro: string; items: string[] };
  features: {
    eyebrow: string;
    title: string;
    aside: Paragraph;
    cards: { title: string; text: string; tags: string[] }[];
  };
  plugins: {
    eyebrow: string;
    chip: string;
    title: Paragraph;
    description: Paragraph;
    benefits: string[];
    link: string;
    fileName: string;
    outputLabel: string;
    outputCode: string;
    outputNote: string;
    copy: string;
    copied: string;
    copyManually: string;
  };
  editions: {
    eyebrow: string;
    title: string;
    aside: Paragraph;
    desktop: { title: string; text: Paragraph; link: string };
    headless: { title: string; text: Paragraph; link: string };
  };
  download: {
    eyebrow: string;
    title: Paragraph;
    description: Paragraph;
    allReleases: string;
    action: string;
    options: { name: string; detail: string }[];
    headlessPrompt: string;
    headlessLink: string;
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

/**
 * 工作台演示照搬应用本体的信息架构与用词，改动前请对照 web/src/i18n/locales，
 * 官网另造一套说法会让首屏和用户装到的软件对不上。
 */
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
  notes: { binary: string; image: string; markup: string; connectionReset: string };
  emptyBody: string;
  emptyCookies: string;
  statusBar: {
    capturing: string;
    showing: string;
    selected: string;
    live: string;
  };
  empty: string;
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
  workbench: WorkbenchCopy;
}
