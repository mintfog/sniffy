export const demoSecret = "sniffy-demo-secret";
export const defaultAmount = 19900;
export const defaultMockId = "unauthorized";

export function orderBody(amount: number) {
  return JSON.stringify({ amount, currency: "CNY" });
}

export const signingCode = `function onRequest(flow) {
  if (flow.host !== 'api.example.com' ||
      flow.path !== '/api/orders') return;

  const ts = String(time.unix());
  const payload = [flow.method, flow.path, ts,
    crypto.sha256(flow.body || '')].join('\\n');
  const sign = crypto.hmac('sha256', settings.appSecret, payload);

  header.set(flow.headers, 'X-Timestamp', ts);
  header.set(flow.headers, 'X-Signature', sign);
}`;

export const tokenCode = `function onResponse(flow) {
  if (flow.host !== 'api.example.com' ||
      flow.path !== '/api/login' || flow.response.status !== 200) return;
  const token = json.get(flow.response.body, 'data.token');
  if (token) store.set('token', token);
}

function onRequest(flow) {
  if (flow.host !== 'api.example.com' ||
      flow.path !== '/api/profile') return;
  const token = store.get('token');
  if (token) header.set(flow.headers, 'Authorization', 'Bearer ' + token);
}`;

export const mockScenarios = [
  {
    id: "success",
    status: 200,
    reason: "OK",
    body: { name: "Sniffy", plan: "pro", ready: true },
  },
  {
    id: "unauthorized",
    status: 401,
    reason: "Unauthorized",
    body: { error: "session_expired", authenticated: false },
  },
  {
    id: "unavailable",
    status: 503,
    reason: "Service Unavailable",
    body: { error: "service_unavailable", retry: true },
  },
] as const;

export function mockCode(scenario: (typeof mockScenarios)[number]) {
  const body = JSON.stringify(scenario.body);
  return `function onRequest(flow) {
  if (flow.host !== 'api.example.com' ||
      flow.path !== '/api/profile') return;

  mock({
    status: ${scenario.status},
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(${body}),
  });
}`;
}
