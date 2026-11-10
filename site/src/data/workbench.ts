import type { WorkbenchCopy } from "../i18n/types";

export type JsonValue =
  null | boolean | number | string | JsonValue[] | { [key: string]: JsonValue };

// 与桌面应用 ContentKind 同名，决定列表图标与「内容」组筛选。
type Kind = "json" | "image" | "video" | "text" | "other";
type NoteKey = keyof WorkbenchCopy["notes"];

interface Session {
  index: number;
  /** null 表示请求未拿到响应，列表里显示 ERR。 */
  status: number | null;
  method: string;
  scheme: "https" | "http";
  protocol: "HTTP/1.1" | "HTTP/2.0";
  host: string;
  target: string;
  kind: Kind;
  duration: string;
  startedAt: string;
  contentType: string;
  /** 仅无正文样例（媒体/图片/标记）需要声明；带 body 的会话按序列化结果实算。 */
  bytes?: number;
  body?: JsonValue;
  note?: NoteKey;
  reqBody?: JsonValue;
  cookie?: [string, string];
}

// 示例流量使用保留域名，避免指向真实服务。
const sessions: Session[] = [
  {
    index: 21,
    status: 200,
    method: "GET",
    scheme: "https",
    protocol: "HTTP/2.0",
    host: "api.example.com",
    target: "/v1/auth/me?timezone=Asia%2FShanghai",
    kind: "json",
    duration: "531ms",
    startedAt: "12:16:03.528",
    contentType: "application/json; charset=utf-8",
    cookie: ["session", "a1f3c8d2"],
    body: {
      code: 0,
      message: "success",
      data: {
        id: 1,
        username: "sniffy",
        role: "admin",
        status: "active",
        concurrency: 5,
        groups: ["dev", "qa"],
      },
    },
  },
  {
    index: 22,
    status: 200,
    method: "GET",
    scheme: "https",
    protocol: "HTTP/2.0",
    host: "api.example.com",
    target: "/v1/announcements?page=1&page_size=20",
    kind: "json",
    duration: "683ms",
    startedAt: "12:16:04.102",
    contentType: "application/json; charset=utf-8",
    body: {
      code: 0,
      data: {
        total: 2,
        items: [
          { id: 9, title: "Scheduled maintenance", pinned: true },
          { id: 8, title: "v1.4 released", pinned: false },
        ],
      },
    },
  },
  {
    index: 23,
    status: null,
    method: "GET",
    scheme: "https",
    protocol: "HTTP/1.1",
    host: "telemetry.example.net",
    target: "/collect?id=9f2c1b",
    kind: "other",
    duration: "—",
    startedAt: "12:16:04.771",
    contentType: "—",
    note: "connectionReset",
  },
  {
    index: 24,
    status: 200,
    method: "POST",
    scheme: "https",
    protocol: "HTTP/2.0",
    host: "api.example.com",
    target: "/v1/orders",
    kind: "json",
    duration: "128ms",
    startedAt: "12:16:05.330",
    contentType: "application/json; charset=utf-8",
    reqBody: { sku: "SNF-01", quantity: 2 },
    body: {
      code: 0,
      data: { orderId: "ord_20931", status: "created", total: 48 },
    },
  },
  {
    index: 25,
    status: 206,
    method: "GET",
    scheme: "https",
    protocol: "HTTP/2.0",
    host: "cdn.example.com",
    target: "/media/intro.mp4",
    kind: "video",
    duration: "452ms",
    startedAt: "12:16:06.004",
    contentType: "video/mp4",
    bytes: 1468006,
    note: "binary",
  },
  {
    index: 26,
    status: 200,
    method: "GET",
    scheme: "https",
    protocol: "HTTP/2.0",
    host: "cdn.example.com",
    target: "/assets/logo.png",
    kind: "image",
    duration: "279ms",
    startedAt: "12:16:06.519",
    contentType: "image/png",
    bytes: 12698,
    note: "image",
  },
  {
    index: 27,
    status: 200,
    method: "GET",
    scheme: "http",
    protocol: "HTTP/1.1",
    host: "static.example.org",
    target: "/legacy/config.xml",
    kind: "text",
    duration: "96ms",
    startedAt: "12:16:07.045",
    contentType: "application/xml",
    bytes: 834,
    note: "markup",
  },
];

function requestHeaders(session: Session): [string, string][] {
  const headers: [string, string][] = [
    ["Host", session.host],
    ["User-Agent", "Sniffy-Demo/1.0"],
    ["Accept", session.kind === "json" ? "application/json" : "*/*"],
    ["Accept-Encoding", "gzip, deflate, br"],
    ["Connection", "keep-alive"],
  ];
  if (session.reqBody !== undefined)
    headers.splice(2, 0, ["Content-Type", "application/json"]);
  if (session.cookie)
    headers.push(["Cookie", `${session.cookie[0]}=${session.cookie[1]}`]);
  return headers;
}

function responseHeaders(session: Session, bytes: number): [string, string][] {
  if (session.status === null) return [];
  const headers: [string, string][] = [
    ["Content-Type", session.contentType],
    ["Content-Length", String(bytes)],
    ["Server", "nginx"],
    ["Date", "Wed, 17 Sep 2026 04:16:03 GMT"],
    [
      "Cache-Control",
      session.kind === "json" ? "no-store" : "public, max-age=86400",
    ],
  ];
  if (session.status === 206) headers.push(["Accept-Ranges", "bytes"]);
  return headers;
}

// 以下格式与桌面应用 lib/format.ts 保持一致。
function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes}B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)}K`;
  return `${(bytes / 1024 / 1024).toFixed(2)}M`;
}

type Tone = "ok" | "info" | "warn" | "danger";

function statusTone(status: number | null): Tone {
  if (status === null || status >= 500) return "danger";
  if (status >= 400) return "warn";
  if (status >= 300) return "info";
  return "ok";
}

// 快捷筛选与应用相同：每个维度把会话归入若干芯片 key，组内单选、组间按「与」叠加。
function filterKeys(session: Session): Record<FilterGroup, string[]> {
  const content: Record<Kind, string[]> = {
    json: ["json"],
    image: ["image"],
    video: ["media"],
    text: session.contentType.includes("xml") ? ["xml"] : ["text"],
    other: [],
  };
  const status = session.status;
  const statusKeys =
    status === null
      ? ["err"]
      : [`${Math.floor(status / 100)}xx`, ...(status >= 400 ? ["err"] : [])];
  return {
    protocol: [session.scheme],
    version: [session.protocol.startsWith("HTTP/2") ? "h2" : "h1"],
    content: content[session.kind],
    status: statusKeys,
  };
}

export type FilterGroup = "protocol" | "version" | "content" | "status";

function hexDump(text: string): string {
  const bytes = new TextEncoder().encode(text);
  const rows: string[] = [];
  for (let offset = 0; offset < bytes.length; offset += 16) {
    const chunk = Array.from(bytes.slice(offset, offset + 16));
    const hex = chunk
      .map((b) => b.toString(16).padStart(2, "0"))
      .join(" ")
      .padEnd(47, " ");
    const ascii = chunk
      .map((b) => (b >= 32 && b < 127 ? String.fromCharCode(b) : "."))
      .join("");
    rows.push(`${offset.toString(16).padStart(8, "0")}  ${hex}  |${ascii}|`);
  }
  return rows.join("\n");
}

export function createDemoSessions(copy: WorkbenchCopy) {
  return sessions.map((session) => {
    const failed = session.status === null;
    const statusText = failed
      ? ""
      : session.status === 206
        ? "Partial Content"
        : "OK";
    const [path, search = ""] = session.target.split("?");
    const params = [...new URLSearchParams(search)];

    const requestBody =
      session.reqBody === undefined ? "" : JSON.stringify(session.reqBody);
    const compactBody =
      session.body === undefined ? "" : JSON.stringify(session.body);
    const bytes =
      session.body === undefined
        ? (session.bytes ?? 0)
        : new TextEncoder().encode(compactBody).length;

    const reqHeaders = requestHeaders(session);
    const resHeaders = responseHeaders(session, bytes);
    const requestHead = [
      `${session.method} ${session.target} ${session.protocol}`,
      ...reqHeaders.map(([key, value]) => `${key}: ${value}`),
    ].join("\n");
    const responseHead = [
      `${session.protocol} ${session.status} ${statusText}`,
      ...resHeaders.map(([key, value]) => `${key}: ${value}`),
    ].join("\n");

    return {
      session,
      failed,
      tone: statusTone(session.status),
      path,
      filters: filterKeys(session),
      params,
      requestHeaders: reqHeaders,
      responseHeaders: resHeaders,
      rawRequest: requestBody
        ? `${requestHead}\n\n${requestBody}`
        : requestHead,
      rawResponse: failed
        ? copy.notes.connectionReset
        : compactBody
          ? `${responseHead}\n\n${compactBody}`
          : responseHead,
      compactBody,
      size: bytes === 0 ? "—" : formatSize(bytes),
      hex: hexDump(compactBody),
      summary: {
        index: session.index,
        method: session.method,
        status: failed ? "ERR" : String(session.status),
        tone: statusTone(session.status),
        hasTree: session.body !== undefined,
        protocol: failed ? "" : session.protocol,
        parameterCount: params.length,
        requestHeaderCount: reqHeaders.length,
        cookieCount: session.cookie ? 1 : 0,
        responseHeaderCount: resHeaders.length,
      },
    };
  });
}

// 摘要经 data-sessions 传入浏览器，字段类型从构建期结果推导。
export type SessionSummary = ReturnType<
  typeof createDemoSessions
>[number]["summary"];
