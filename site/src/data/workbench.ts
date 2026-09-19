import type { WorkbenchCopy } from "../i18n/types";

type JsonValue =
  null | boolean | number | string | JsonValue[] | { [key: string]: JsonValue };

type Kind = "json" | "image" | "media" | "markup";
type NoteKey = keyof WorkbenchCopy["notes"];

interface Session {
  index: number;
  /** null 表示请求未拿到响应，列表里显示 ERR。 */
  status: number | null;
  method: string;
  scheme: "https" | "http";
  host: string;
  target: string;
  kind: Kind;
  duration: string;
  clientPort: number;
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
    host: "api.example.com",
    target: "/v1/auth/me?timezone=Asia%2FShanghai",
    kind: "json",
    duration: "531ms",
    clientPort: 51601,
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
    host: "api.example.com",
    target: "/v1/announcements?page=1&page_size=20",
    kind: "json",
    duration: "683ms",
    clientPort: 51604,
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
    host: "telemetry.example.net",
    target: "/collect?id=9f2c1b",
    kind: "markup",
    duration: "–",
    clientPort: 51607,
    startedAt: "12:16:04.771",
    contentType: "–",
    note: "connectionReset",
  },
  {
    index: 24,
    status: 200,
    method: "POST",
    scheme: "https",
    host: "api.example.com",
    target: "/v1/orders",
    kind: "json",
    duration: "128ms",
    clientPort: 51612,
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
    host: "cdn.example.com",
    target: "/media/intro.mp4",
    kind: "media",
    duration: "452ms",
    clientPort: 51618,
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
    host: "cdn.example.com",
    target: "/assets/logo.png",
    kind: "image",
    duration: "279ms",
    clientPort: 51620,
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
    host: "static.example.org",
    target: "/legacy/config.xml",
    kind: "markup",
    duration: "96ms",
    clientPort: 51623,
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

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}

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

type Token = {
  text: string;
  kind: "key" | "string" | "number" | "literal" | "punct";
};
type JsonLine = { indent: number; open: boolean; tokens: Token[] };

function jsonLines(
  value: JsonValue,
  indent = 0,
  key?: string,
  trailing = "",
): JsonLine[] {
  const lead: Token[] =
    key !== undefined
      ? [
          { text: JSON.stringify(key), kind: "key" },
          { text: ": ", kind: "punct" },
        ]
      : [];

  if (Array.isArray(value) || (value && typeof value === "object")) {
    const entries: [string | undefined, JsonValue][] = Array.isArray(value)
      ? value.map((item) => [undefined, item])
      : Object.entries(value);
    const [open, close] = Array.isArray(value) ? ["[", "]"] : ["{", "}"];
    if (entries.length === 0) {
      return [
        {
          indent,
          open: false,
          tokens: [...lead, { text: open + close + trailing, kind: "punct" }],
        },
      ];
    }
    const lines: JsonLine[] = [
      { indent, open: true, tokens: [...lead, { text: open, kind: "punct" }] },
    ];
    entries.forEach(([childKey, child], i) => {
      lines.push(
        ...jsonLines(
          child,
          indent + 1,
          childKey,
          i === entries.length - 1 ? "" : ",",
        ),
      );
    });
    lines.push({
      indent,
      open: false,
      tokens: [{ text: close + trailing, kind: "punct" }],
    });
    return lines;
  }

  const kind: Token["kind"] =
    typeof value === "string"
      ? "string"
      : typeof value === "number"
        ? "number"
        : "literal";
  const text = JSON.stringify(value);
  return [
    { indent, open: false, tokens: [...lead, { text: text + trailing, kind }] },
  ];
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
    const query = search ? `?${search}` : "";
    const params = [...new URLSearchParams(search)];
    const filters = ["all", session.scheme];
    if (session.kind === "json") filters.push("json");
    if (session.kind === "image") filters.push("images");
    if (failed) filters.push("errors");

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
      `${session.method} ${session.target} HTTP/1.1`,
      ...reqHeaders.map(([key, value]) => `${key}: ${value}`),
    ].join("\n");
    const responseHead = [
      `HTTP/1.1 ${session.status} ${statusText}`,
      ...resHeaders.map(([key, value]) => `${key}: ${value}`),
    ].join("\n");

    return {
      session,
      failed,
      statusText,
      path,
      query,
      filters,
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
      tree: session.body === undefined ? [] : jsonLines(session.body),
      compactBody,
      size: bytes === 0 ? "" : formatBytes(bytes),
      hex: hexDump(compactBody),
      summary: {
        index: session.index,
        method: session.method,
        status: failed ? "ERR" : String(session.status),
        url: `${session.scheme}://${session.host}${path}`,
        query,
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
