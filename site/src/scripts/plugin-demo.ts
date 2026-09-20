import { demoSecret, orderBody, mockScenarios } from "../data/plugin-demo";
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
  return `GET /api/profile\nHost: api.example.com\n\nAuthorization:\nBearer ${savedToken}`;
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
  function run() {
    if (form.checkValidity()) void player.run(evaluate);
  }
  form.addEventListener("input", player.reset);
  form.addEventListener("submit", (event) => {
    event.preventDefault();
    run();
  });
  return { id, run, reset: player.reset };
});

for (const button of buttons) {
  button.addEventListener("click", () => {
    resetCopy();
    const id = button.dataset.scenario!;
    buttons.forEach((item) =>
      item.setAttribute("aria-pressed", String(item === button)),
    );
    panels.forEach((panel) => {
      panel.hidden = panel.dataset.scenarioPanel !== id;
    });
    demos.forEach((demo) => demo.reset());
    demos.find((demo) => demo.id === id)!.run();
  });
}

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
    try {
      await navigator.clipboard.writeText(source.textContent!);
      if (revision !== copyRevision) return;
      label.textContent = button.dataset.done!;
      announce.textContent = button.dataset.messageDone!;
    } catch {
      if (revision !== copyRevision) return;
      label.textContent = button.dataset.failed!;
      announce.textContent = button.dataset.messageFailed!;
      const range = document.createRange();
      range.selectNodeContents(source);
      const selection = window.getSelection();
      selection?.removeAllRanges();
      selection?.addRange(range);
    }
    copyTimer = window.setTimeout(() => {
      label.textContent = button.dataset.idle!;
    }, 2500);
  });
}
