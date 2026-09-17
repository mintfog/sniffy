import { defineConfig } from "astro/config";
import starlight from "@astrojs/starlight";

export default defineConfig({
  // :where 不增加选择器权重，让组件与共享样式按选择器本身的权重覆盖。
  scopedStyleStrategy: "where",
  integrations: [
    starlight({
      title: { "zh-CN": "sniffy", en: "sniffy" },
      logo: { src: "./public/sniffy-mark.png" },
      favicon: "/sniffy-mark.png",
      defaultLocale: "root",
      locales: {
        root: { label: "简体中文", lang: "zh-CN" },
        en: { label: "English", lang: "en" },
      },
      social: [
        {
          icon: "github",
          label: "GitHub",
          href: "https://github.com/mintfog/sniffy",
        },
      ],
      customCss: ["./src/styles/docs.css"],
      sidebar: [
        {
          label: "文档首页",
          slug: "docs",
          translations: { en: "Documentation" },
        },
        {
          label: "桌面版快速开始",
          slug: "docs/desktop",
          translations: { en: "Desktop quick start" },
        },
        {
          label: "无界面版与 API 接入",
          slug: "docs/headless",
          translations: { en: "Headless and API access" },
        },
      ],
    }),
  ],
});
