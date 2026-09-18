import { defineConfig } from "astro/config";
import starlight from "@astrojs/starlight";

export default defineConfig({
  site: "https://gosniffy.com",
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
          label: "开始使用",
          translations: { en: "Getting started" },
          items: [
            {
              label: "安装",
              slug: "docs/install",
              translations: { en: "Installation" },
            },
            {
              label: "桌面版快速开始",
              slug: "docs/desktop",
              translations: { en: "Desktop quick start" },
            },
            {
              label: "HTTPS 解密与根证书",
              slug: "docs/https",
              translations: { en: "HTTPS and the root certificate" },
            },
          ],
        },
        {
          label: "抓包",
          translations: { en: "Capturing traffic" },
          items: [
            {
              label: "接入流量",
              slug: "docs/capture",
              translations: { en: "Routing traffic" },
            },
            {
              label: "移动设备与远程客户端",
              slug: "docs/mobile",
              translations: { en: "Phones and remote clients" },
            },
            {
              label: "工作台导览",
              slug: "docs/workbench",
              translations: { en: "The workbench" },
            },
          ],
        },
        {
          label: "改写与调试",
          translations: { en: "Editing and debugging" },
          items: [
            {
              label: "请求构造器",
              slug: "docs/compose",
              translations: { en: "Request composer" },
            },
            {
              label: "重写规则",
              slug: "docs/rules",
              translations: { en: "Rewrite rules" },
            },
            {
              label: "断点",
              slug: "docs/breakpoints",
              translations: { en: "Breakpoints" },
            },
            {
              label: "JavaScript 插件",
              slug: "docs/plugins",
              translations: { en: "JavaScript plugins" },
            },
          ],
        },
        {
          label: "部署",
          translations: { en: "Deployment" },
          items: [
            {
              label: "无界面版",
              slug: "docs/headless",
              translations: { en: "Headless service" },
            },
          ],
        },
        {
          label: "管理 API",
          translations: { en: "Management API" },
          items: [
            {
              label: "概览",
              slug: "docs/api",
              translations: { en: "Overview" },
            },
            {
              label: "认证与安全",
              slug: "docs/api/auth",
              translations: { en: "Authentication and security" },
            },
            {
              label: "约定与错误",
              slug: "docs/api/conventions",
              translations: { en: "Conventions and errors" },
            },
            {
              label: "状态、统计与录制",
              slug: "docs/api/status",
              translations: { en: "Status, statistics, recording" },
            },
            {
              label: "配置",
              slug: "docs/api/config",
              translations: { en: "Configuration" },
            },
            {
              label: "会话",
              slug: "docs/api/sessions",
              translations: { en: "Sessions" },
            },
            {
              label: "WebSocket 与流式会话",
              slug: "docs/api/streams",
              translations: { en: "WebSocket and streaming sessions" },
            },
            {
              label: "请求构造器",
              slug: "docs/api/compose",
              translations: { en: "Request composer" },
            },
            {
              label: "重写规则",
              slug: "docs/api/rules",
              translations: { en: "Rewrite rules" },
            },
            {
              label: "断点",
              slug: "docs/api/breakpoints",
              translations: { en: "Breakpoints" },
            },
            {
              label: "插件",
              slug: "docs/api/plugins",
              translations: { en: "Plugins" },
            },
            {
              label: "证书",
              slug: "docs/api/certificates",
              translations: { en: "Certificates" },
            },
            {
              label: "导出",
              slug: "docs/api/export",
              translations: { en: "Export" },
            },
            {
              label: "实时事件",
              slug: "docs/api/events",
              translations: { en: "Live events" },
            },
          ],
        },
        {
          label: "参考",
          translations: { en: "Reference" },
          items: [
            {
              label: "配置参考",
              slug: "docs/configuration",
              translations: { en: "Configuration reference" },
            },
            {
              label: "疑难排查",
              slug: "docs/troubleshooting",
              translations: { en: "Troubleshooting" },
            },
          ],
        },
      ],
    }),
  ],
});
