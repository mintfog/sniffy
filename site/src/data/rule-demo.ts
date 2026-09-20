export const ruleConditions = { host: "api.example.com", path: "/api/" };

export const ruleActions = {
  redirect: {
    type: "redirect",
    parameters: { url: "http://localhost:3000" },
  },
  header: {
    type: "modify_headers",
    parameters: { name: "X-Debug", value: "1" },
  },
  mock: {
    type: "auto_respond",
    parameters: {
      response: {
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ name: "Sniffy", plan: "pro" }),
      },
    },
  },
} as const;

export type RuleAction = keyof typeof ruleActions;

export function evaluateDemoRule(
  request: string,
  options: { enabled: boolean; action: RuleAction },
) {
  const url = new URL(request);
  if (!["http:", "https:"].includes(url.protocol))
    throw new Error("请求地址需要使用 HTTP 或 HTTPS");
  const conditions = [
    url.host.toLowerCase() === ruleConditions.host.toLowerCase(),
    decodeURIComponent(url.pathname)
      .toLowerCase()
      .startsWith(ruleConditions.path.toLowerCase()),
  ];
  const matched = options.enabled && conditions.every(Boolean);
  const original = `GET ${url.href}\nHost: ${url.host}`;
  if (!matched) return { conditions, matched, output: original };

  const action = ruleActions[options.action];
  if (action.type === "auto_respond") {
    const response = action.parameters.response;
    return {
      conditions,
      matched,
      output: `HTTP/1.1 ${response.status} OK\nContent-Type: ${response.contentType}\n\n${response.body}`,
    };
  }
  if (action.type === "modify_headers") {
    return {
      conditions,
      matched,
      output: `${original}\n${action.parameters.name}: ${action.parameters.value}`,
    };
  }
  const target = new URL(action.parameters.url);
  url.protocol = target.protocol;
  url.host = target.host;
  return { conditions, matched, output: `GET ${url.href}\nHost: ${url.host}` };
}
