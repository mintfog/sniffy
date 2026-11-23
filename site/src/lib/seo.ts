import { repository } from "../data/repository";
import { languages, localePath, siteCopy, type Lang } from "../i18n/ui";

export function ogImage(lang: Lang, site: URL) {
  return {
    url: new URL(`/og/${lang}.jpg`, site).href,
    width: 1200,
    height: 630,
    alt: siteCopy[lang].home.meta.ogImageAlt,
  };
}

/** Starlight 的路由语言是 BCP 47 标签（zh-CN / en），映射回站点的语言键。 */
export function langFromTag(tag: string): Lang {
  return tag.toLowerCase().startsWith("en") ? "en" : "zh";
}

export function softwareJsonLd(lang: Lang, site: URL): string {
  const meta = siteCopy[lang].home.meta;
  const home = new URL(localePath(lang, "/"), site).href;
  const data = {
    "@context": "https://schema.org",
    "@graph": [
      {
        "@type": "WebSite",
        "@id": `${home}#website`,
        url: home,
        name: "Sniffy",
        inLanguage: languages[lang].htmlLang,
      },
      {
        "@type": "SoftwareApplication",
        "@id": `${home}#software`,
        name: "Sniffy",
        url: home,
        description: meta.description,
        applicationCategory: "DeveloperApplication",
        operatingSystem: "macOS, Windows, Linux",
        downloadUrl: new URL(localePath(lang, "/download/"), site).href,
        image: ogImage(lang, site).url,
        license: "https://www.apache.org/licenses/LICENSE-2.0",
        isAccessibleForFree: true,
        offers: { "@type": "Offer", price: "0", priceCurrency: "USD" },
        sameAs: [repository],
      },
    ],
  };
  // 内联进 <script> 时 "</" 会提前闭合标签，统一转义尖括号。
  return JSON.stringify(data).replace(/</g, "\\u003c");
}
