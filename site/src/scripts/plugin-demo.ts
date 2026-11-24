import { demoSecret, orderBody, mockScenarios } from "../data/plugin-demo";
import { copyText } from "../lib/clipboard";
import { createDemoPlayer, type DemoResult } from "./demo-flow";

const root = document.querySelector<HTMLElement>("[data-plugin-demo]")!;
const buttons = [
  ...root.querySelectorAll<HTMLButtonElement>("[data-scenario]"),
];
const panels = [...root.querySelectorAll<HTMLElement>("[data-scenario-panel]")];
const announce = root.querySelector<HTMLElement>("[data-copy-status]")!;
const forms = [...root.querySelectorAll<HTMLFormElement>("[data-demo-flow]")];
const amount = root.querySelector<HTMLInputElement>("[data-amount]")!;
const token = root.querySelector<HTMLInputElement>("[data-token-value]")!;
const mockSelect = root.querySelector<HTMLSelectElement>("[data-mock-status]")!;
const orderPreview = root.querySelector<HTMLElement>("[data-order-body]")!;
const loginPreview = root.querySelector<HTMLElement>("[data-login-body]")!;
const storedToken = root.querySelector<HTMLElement>("[data-stored-token]")!;
const mockSources = root.querySelectorAll<HTMLElement>("[data-mock-code]");
const copyButtons = [
  ...root.querySelectorAll<HTMLButtonElement>("[data-copy-code]"),
];
let copyRevision = 0;
let copyTimer: number | undefined;

function resetCopy() {
  copyRevision++;
  window.clearTimeout(copyTimer);
  copyButtons.forEach((button) => {
    button.querySelector("span")!.textContent = button.dataset.idle!;
  });
  announce.textContent = "";
}

function hex(bytes: ArrayBuffer) {
  return Array.from(new Uint8Array(bytes), (byte) =>
    byte.toString(16).padStart(2, "0"),
  ).join("");
}

async function signRequest(): Promise<string> {
  const body = orderBody(amount.valueAsNumber);
  const timestamp = String(Math.floor(Date.now() / 1000));
  const encoder = new TextEncoder();
  const bodyHash = await crypto.subtle.digest("SHA-256", encoder.encode(body));
  const payload = ["POST", "/api/orders", timestamp, hex(bodyHash)].join("\n");
  const key = await crypto.subtle.importKey(
    "raw",
    encoder.encode(demoSecret),
    { name: "HMAC", hash: "SHA-256" },
    false,
    ["sign"],
  );
  const signature = await crypto.subtle.sign(
    "HMAC",
    key,
    encoder.encode(payload),
  );
  return `X-Timestamp: ${timestamp}\n\nX-Signature:\n${hex(signature)}`;
}

function relayToken(): string {
  const savedToken = token.value;
  storedToken.textContent = savedToken;
  return `[onResponse] POST /api/login → store.set('token', '${savedToken}')\n\n[onRequest] GET /api/profile\nHost: api.example.com\nAuthorization: Bearer ${savedToken}`;
}

const demos = forms.map((form) => {
  const player = createDemoPlayer(form);
  const id = form.dataset.demoFlow!;
  async function evaluate(): Promise<DemoResult> {
    const status = form.dataset.scriptDone!;
    if (id === "signing") return { content: await signRequest(), status };
    if (id === "token") return { content: relayToken(), status };
    const mock = mockScenarios.find((item) => item.id === mockSelect.value)!;
    return {
      content: `HTTP/1.1 ${mock.status} ${mock.reason}\nContent-Type: application/json\n\n${JSON.stringify(mock.body, null, 2)}`,
      status,
    };
  }
  function run(options?: { immediate?: boolean }) {
    if (form.checkValidity()) void player.run(evaluate, options);
  }
  form.addEventListener("input", player.reset);
  form.addEventListener("submit", (event) => {
    event.preventDefault();
    run();
  });
  return { id, run, reset: player.reset };
});

function activateScenario(button: HTMLButtonElement) {
  resetCopy();
  const id = button.dataset.scenario!;
  buttons.forEach((item) => {
    const selected = item === button;
    item.setAttribute("aria-selected", String(selected));
    item.tabIndex = selected ? 0 : -1;
  });
  panels.forEach((panel) => {
    panel.hidden = panel.dataset.scenarioPanel !== id;
  });
  demos.forEach((demo) => demo.reset());
  demos.find((demo) => demo.id === id)!.run();
}

buttons.forEach((button, index) => {
  button.addEventListener("click", () => activateScenario(button));
  button.addEventListener("keydown", (event) => {
    let next: number | undefined;
    if (event.key === "ArrowRight") next = (index + 1) % buttons.length;
    else if (event.key === "ArrowLeft")
      next = (index - 1 + buttons.length) % buttons.length;
    else if (event.key === "Home") next = 0;
    else if (event.key === "End") next = buttons.length - 1;
    if (next === undefined) return;
    event.preventDefault();
    buttons[next].focus();
    activateScenario(buttons[next]);
  });
});

amount.addEventListener("input", () => {
  orderPreview.textContent = amount.validity.valid
    ? orderBody(amount.valueAsNumber)
    : "—";
});
token.addEventListener("input", () => {
  loginPreview.textContent = JSON.stringify({ data: { token: token.value } });
});
mockSelect.addEventListener("change", () => {
  resetCopy();
  mockSources.forEach((code) => {
    code.hidden = code.dataset.mockCode !== mockSelect.value;
  });
  demos.find((demo) => demo.id === "mock")!.run();
});

for (const button of copyButtons) {
  button.addEventListener("click", async () => {
    const source = button
      .closest<HTMLElement>("[data-script-source]")!
      .querySelector<HTMLElement>(".source-code:not([hidden]) pre")!;
    const label = button.querySelector("span")!;
    resetCopy();
    const revision = copyRevision;
    const copied = await copyText(source);
    if (revision !== copyRevision) return;
    label.textContent = copied ? button.dataset.done! : button.dataset.failed!;
    announce.textContent = copied
      ? button.dataset.messageDone!
      : button.dataset.messageFailed!;
    copyTimer = window.setTimeout(() => {
      label.textContent = button.dataset.idle!;
    }, 2500);
  });
}

if (typeof crypto !== "undefined" && crypto.subtle) {
  demos[0]?.run({ immediate: true });
}
