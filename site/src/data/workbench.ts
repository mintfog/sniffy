import type { WorkbenchCopy } from "../i18n/types";

export type JsonValue =
  null | boolean | number | string | JsonValue[] | { [key: string]: JsonValue };

// 与桌面应用 ContentKind 对应，决定列表图标与「内容」组筛选。
export type Kind =
  | "json"
  | "xml"
  | "text"
  | "html"
  | "js"
  | "css"
  | "image"
  | "video"
  | "binary"
  | "ws"
  | "other";

type NoteKey = keyof WorkbenchCopy["notes"];
type ExecutionKey = keyof WorkbenchCopy["executions"];

interface Session {
  index: number;
  /** null 表示请求未拿到响应，列表里显示 ERR。 */
  status: number | null;
  method: string;
  scheme: "https" | "http" | "wss";
  protocol: "HTTP/1.1" | "HTTP/2.0";
  host: string;
  target: string;
  kind: Kind;
  duration: string;
  startedAt: string;
  contentType: string;
  clientIP?: string;
  process?: string;
  /** 仅无正文样例（媒体/图片/标记）需要声明；带 body 的会话按序列化结果实算。 */
  bytes?: number;
  body?: JsonValue;
  textBody?: string;
  note?: NoteKey;
  reqBody?: JsonValue;
  cookie?: [string, string];
  extraReqHeaders?: [string, string][];
  executions?: ExecutionKey[];
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
    duration: "42ms",
    startedAt: "12:16:03.528",
    contentType: "application/json; charset=utf-8",
    process: "shop-desktop",
    cookie: ["session", "a1f3c8d2"],
    extraReqHeaders: [["Authorization", "Bearer sniffy-demo-token"]],
    executions: ["pluginToken"],
    body: {
      code: 0,
      message: "success",
      data: {
        id: 1,
        username: "sniffy",
        role: "admin",
        status: "active",
        groups: ["dev", "qa"],
        permissions: {
          capture: true,
          rules: true,
          plugins: true,
        },
      },
    },
  },
  {
    index: 22,
    status: 201,
    method: "POST",
    scheme: "https",
    protocol: "HTTP/2.0",
    host: "api.example.com",
    target: "/v1/orders",
    kind: "json",
    duration: "84ms",
    startedAt: "12:16:04.115",
    contentType: "application/json; charset=utf-8",
    process: "shop-desktop",
    extraReqHeaders: [
      ["X-Timestamp", "1763967364"],
      ["X-Signature", "9c4e17b02fa86d1c35b90e47a12f68d0"],
    ],
    executions: ["pluginSign"],
    reqBody: { sku: "SNF-01", quantity: 2, amount: 19900 },
    body: {
      code: 0,
      data: { orderId: "ord_20931", status: "created", total: 19900 },
    },
  },
  {
    index: 23,
    status: 101,
    method: "WS",
    scheme: "wss",
    protocol: "HTTP/1.1",
    host: "live.example.com",
    target: "/v1/orders/events?channel=ord_20931",
    kind: "ws",
    duration: "1.8s",
    startedAt: "12:16:04.240",
    contentType: "websocket",
    process: "shop-desktop",
    bytes: 612,
    note: "switchingProtocols",
  },
  {
    index: 24,
    status: 200,
    method: "POST",
    scheme: "https",
    protocol: "HTTP/2.0",
    host: "ai.example.com",
    target: "/v1/chat/completions",
    kind: "text",
    duration: "618ms",
    startedAt: "12:16:04.602",
    contentType: "text/event-stream; charset=utf-8",
    process: "shop-desktop",
    reqBody: { model: "demo-chat", stream: true },
    textBody: [
      'event: message\ndata: {"id":"cmpl-1","delta":{"content":"Order ord_20931 "}}',
      'event: message\ndata: {"id":"cmpl-1","delta":{"content":"is ready for dispatch."}}',
      "event: done\ndata: [DONE]",
    ].join("\n\n"),
  },
  {
    index: 25,
    status: 200,
    method: "POST",
    scheme: "https",
    protocol: "HTTP/2.0",
    host: "rpc.example.com",
    target: "/inventory.v1.StockService/GetStock",
    kind: "binary",
    duration: "19ms",
    startedAt: "12:16:05.014",
    contentType: "application/grpc+proto",
    process: "inventory-cli",
    bytes: 186,
    note: "grpcPayload",
  },
  {
    index: 26,
    status: 404,
    method: "GET",
    scheme: "https",
    protocol: "HTTP/2.0",
    host: "api.example.com",
    target: "/v1/coupons/SPRING15",
    kind: "json",
    duration: "37ms",
    startedAt: "12:16:05.330",
    contentType: "application/json; charset=utf-8",
    process: "shop-desktop",
    body: {
      code: 40401,
      error: "coupon_not_found",
      message: "Coupon SPRING15 has expired",
    },
  },
  {
    index: 27,
    status: 503,
    method: "POST",
    scheme: "https",
    protocol: "HTTP/2.0",
    host: "api.example.com",
    target: "/v1/checkout",
    kind: "json",
    duration: "3ms",
    startedAt: "12:16:05.692",
    contentType: "application/json; charset=utf-8",
    clientIP: "192.168.1.42",
    executions: ["ruleMock"],
    reqBody: { orderId: "ord_20931", shipping: "express" },
    body: {
      error: "service_unavailable",
      retryAfter: 5,
      mocked: true,
    },
  },
  {
    index: 28,
    status: 304,
    method: "GET",
    scheme: "https",
    protocol: "HTTP/2.0",
    host: "api.example.com",
    target: "/v1/announcements?page=1&page_size=20",
    kind: "json",
    duration: "24ms",
    startedAt: "12:16:05.910",
    contentType: "application/json; charset=utf-8",
    process: "Chrome",
    extraReqHeaders: [["If-None-Match", '"W/8a1f-202611"']],
    bytes: 0,
    note: "notModified",
  },
  {
    index: 29,
    status: null,
    method: "GET",
    scheme: "https",
    protocol: "HTTP/1.1",
    host: "telemetry.example.net",
    target: "/collect?id=9f2c1b",
    kind: "other",
    duration: "—",
    startedAt: "12:16:06.171",
    contentType: "—",
    process: "Chrome",
    note: "connectionReset",
  },
  {
    index: 30,
    status: 200,
    method: "GET",
    scheme: "https",
    protocol: "HTTP/2.0",
    host: "app.example.com",
    target: "/dashboard",
    kind: "html",
    duration: "65ms",
    startedAt: "12:16:06.412",
    contentType: "text/html; charset=utf-8",
    process: "Chrome",
    bytes: 18432,
    note: "markup",
  },
  {
    index: 31,
    status: 200,
    method: "GET",
    scheme: "https",
    protocol: "HTTP/2.0",
    host: "cdn.example.com",
    target: "/assets/app.8f3c1a.js",
    kind: "js",
    duration: "31ms",
    startedAt: "12:16:06.508",
    contentType: "text/javascript; charset=utf-8",
    process: "Chrome",
    bytes: 46280,
    note: "markup",
  },
  {
    index: 32,
    status: 200,
    method: "GET",
    scheme: "https",
    protocol: "HTTP/2.0",
    host: "cdn.example.com",
    target: "/assets/theme.4b19d0.css",
    kind: "css",
    duration: "18ms",
    startedAt: "12:16:06.544",
    contentType: "text/css; charset=utf-8",
    process: "Chrome",
    bytes: 9420,
    note: "markup",
  },
  {
    index: 33,
    status: 206,
    method: "GET",
    scheme: "https",
    protocol: "HTTP/2.0",
    host: "cdn.example.com",
    target: "/media/intro.mp4",
    kind: "video",
    duration: "452ms",
    startedAt: "12:16:06.804",
    contentType: "video/mp4",
    process: "Chrome",
    bytes: 1468006,
    note: "binary",
  },
  {
    index: 34,
    status: 200,
    method: "GET",
    scheme: "https",
    protocol: "HTTP/2.0",
    host: "cdn.example.com",
    target: "/assets/logo.png",
    kind: "image",
    duration: "59ms",
    startedAt: "12:16:07.019",
    contentType: "image/png",
    process: "Chrome",
    bytes: 12698,
    note: "image",
  },
  {
    index: 35,
    status: 200,
    method: "GET",
    scheme: "http",
    protocol: "HTTP/1.1",
    host: "static.example.org",
    target: "/legacy/config.xml",
    kind: "xml",
    duration: "96ms",
    startedAt: "12:16:07.245",
    contentType: "application/xml",
    process: "curl",
    bytes: 834,
    note: "markup",
  },
  {
    index: 36,
    status: 200,
    method: "GET",
    scheme: "https",
    protocol: "HTTP/2.0",
    host: "api.example.com",
    target: "/v1/stock?sku=SNF-01",
    kind: "json",
    duration: "29ms",
    startedAt: "12:16:07.512",
    contentType: "application/json; charset=utf-8",
    process: "curl",
    body: {
      code: 0,
      data: { sku: "SNF-01", available: 12, warehouse: "sh-east-1" },
    },
  },
];

function requestHeaders(session: Session): [string, string][] {
  if (session.scheme === "wss") {
    return [
      ["Host", session.host],
      ["Connection", "Upgrade"],
      ["Upgrade", "websocket"],
      ["Sec-WebSocket-Version", "13"],
      ["User-Agent", "Sniffy-Demo/1.0"],
    ];
  }
  const headers: [string, string][] = [
    ["Host", session.host],
    ["User-Agent", "Sniffy-Demo/1.0"],
    [
      "Accept",
      session.kind === "json"
        ? "application/json"
        : session.contentType.startsWith("text/event-stream")
          ? "text/event-stream"
          : "*/*",
    ],
    ["Accept-Encoding", "gzip, deflate, br"],
    ["Connection", "keep-alive"],
  ];
  if (session.reqBody !== undefined)
    headers.splice(2, 0, ["Content-Type", "application/json"]);
  if (session.extraReqHeaders) headers.push(...session.extraReqHeaders);
  if (session.cookie)
    headers.push(["Cookie", `${session.cookie[0]}=${session.cookie[1]}`]);
  return headers;
}

function responseHeaders(session: Session, bytes: number): [string, string][] {
  if (session.status === null) return [];
  if (session.status === 101) {
    return [
      ["Upgrade", "websocket"],
      ["Connection", "Upgrade"],
      ["Sec-WebSocket-Accept", "s3pPLMBiTxaQ9kYGzzhZRbK+xOo="],
      ["Server", "sniffy-edge"],
    ];
  }
  const headers: [string, string][] = [
    ["Content-Type", session.contentType],
    ["Server", "nginx"],
    ["Date", "Tue, 24 Nov 2026 04:16:03 GMT"],
    [
      "Cache-Control",
      session.kind === "json" || session.kind === "text"
        ? "no-store"
        : "public, max-age=86400",
    ],
  ];
  if (session.status !== 304 && !session.contentType.startsWith("text/event-stream")) {
    headers.splice(1, 0, ["Content-Length", String(bytes)]);
  }
  if (session.status === 206) headers.push(["Accept-Ranges", "bytes"]);
  if (session.kind === "binary") headers.push(["Grpc-Status", "0"]);
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
  if (status === 101 || status >= 300) return "info";
  return "ok";
}

function statusReason(status: number | null): string {
  switch (status) {
    case null:
      return "";
    case 101:
      return "Switching Protocols";
    case 201:
      return "Created";
    case 206:
      return "Partial Content";
    case 304:
      return "Not Modified";
    case 404:
      return "Not Found";
    case 503:
      return "Service Unavailable";
    default:
      return "OK";
  }
}

// 快捷筛选与应用相同：每个维度把会话归入若干芯片 key，组内单选、组间按「与」叠加。
function filterKeys(session: Session): Record<FilterGroup, string[]> {
  const content: Record<Kind, string[]> = {
    json: ["json"],
    xml: ["xml"],
    text: ["text"],
    html: ["html"],
    js: ["js"],
    css: ["css"],
    image: ["image"],
    video: ["media"],
    binary: ["binary"],
    ws: [],
    other: [],
  };
  const status = session.status;
  const statusKeys =
    status === null
      ? ["err"]
      : [`${Math.floor(status / 100)}xx`, ...(status >= 400 ? ["err"] : [])];
  const protocolKeys =
    session.scheme === "wss" ? ["https", "ws"] : [session.scheme];
  return {
    protocol: protocolKeys,
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
    const statusText = statusReason(session.status);
    const [path, search = ""] = session.target.split("?");
    const params = [...new URLSearchParams(search)];

    const requestBody =
      session.reqBody === undefined ? "" : JSON.stringify(session.reqBody);
    const compactBody =
      session.body !== undefined
        ? JSON.stringify(session.body)
        : (session.textBody ?? "");
    const bytes =
      compactBody !== ""
        ? new TextEncoder().encode(compactBody).length
        : (session.bytes ?? 0);

    const reqHeaders = requestHeaders(session);
    const resHeaders = responseHeaders(session, bytes);
    const reqMethod = session.scheme === "wss" ? "GET" : session.method;
    const requestHead = [
      `${reqMethod} ${session.target} ${session.protocol}`,
      ...reqHeaders.map(([key, value]) => `${key}: ${value}`),
    ].join("\n");
    const responseHead = [
      `${session.protocol} ${session.status} ${statusText}`,
      ...resHeaders.map(([key, value]) => `${key}: ${value}`),
    ].join("\n");
    const executionRecords = (session.executions ?? []).map(
      (key) => copy.executions[key],
    );

    return {
      session,
      failed,
      isEncrypted: session.scheme === "https" || session.scheme === "wss",
      tone: statusTone(session.status),
      path,
      filters: filterKeys(session),
      params,
      requestHeaders: reqHeaders,
      responseHeaders: resHeaders,
      executionRecords,
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
        executionCount: executionRecords.length,
        responseHeaderCount: resHeaders.length,
      },
    };
  });
}

// 摘要经 data-sessions 传入浏览器，字段类型从构建期结果推导。
export type SessionSummary = ReturnType<
  typeof createDemoSessions
>[number]["summary"];
