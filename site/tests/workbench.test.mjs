import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { test } from "node:test";
import { createDemoSessions } from "../src/data/workbench.ts";

const zh = JSON.parse(
  await readFile(
    new URL("../src/i18n/locales/zh.json", import.meta.url),
    "utf8",
  ),
);
const en = JSON.parse(
  await readFile(
    new URL("../src/i18n/locales/en.json", import.meta.url),
    "utf8",
  ),
);

const expectedChips = {
  protocol: ["https", "http", "ws"],
  version: ["h1", "h2"],
  content: [
    "json",
    "xml",
    "text",
    "html",
    "js",
    "css",
    "image",
    "media",
    "binary",
  ],
  status: ["1xx", "2xx", "3xx", "4xx", "5xx", "err"],
};

test("工作台预览会话覆盖全部筛选芯片并携带进程与插件执行记录", () => {
  for (const copy of [zh.workbench, en.workbench]) {
    const details = createDemoSessions(copy);
    assert.ok(details.length >= 14);
    for (const [group, chips] of Object.entries(expectedChips)) {
      for (const chip of chips) {
        assert.ok(
          details.some((item) => item.filters[group].includes(chip)),
          `筛选维度 ${group}:${chip} 缺少匹配示例`,
        );
      }
    }
    assert.ok(details.some((item) => Boolean(item.session.process)));
    assert.ok(details.some((item) => item.executionRecords.length > 0));
  }
});
