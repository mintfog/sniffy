import {
  evaluateDemoRule,
  ruleActions,
  ruleConditions,
  type RuleAction,
} from "../data/rule-demo";
import { createDemoPlayer } from "./demo-flow";

const root = document.querySelector<HTMLElement>("[data-rule-demo]")!;
const workspace = root.querySelector<HTMLElement>("[data-demo-flow]")!;
const player = createDemoPlayer(workspace, renderOutput);
const actionButtons =
  root.querySelectorAll<HTMLButtonElement>("[data-rule-action]");
const sampleButtons =
  root.querySelectorAll<HTMLButtonElement>("[data-rule-sample]");
const enabled = root.querySelector<HTMLInputElement>("[data-rule-enabled]")!;
const host = root.querySelector<HTMLElement>("[data-rule-host]")!;
const conditionStates = root.querySelectorAll<HTMLElement>(
  "[data-condition-state]",
);
const labels = workspace.dataset;
const summaries = root.querySelectorAll<HTMLElement>("[data-action-summary]");
const note = workspace.querySelector<HTMLElement>("[data-demo-note]")!;
const effect = root.querySelector<HTMLElement>("[data-rule-effect]")!;
let selectedAction = root.querySelector<HTMLButtonElement>(
  '[data-rule-action][aria-pressed="true"]',
)!;
let requestHost = ruleConditions.host;

function renderOutput(element: HTMLElement, content: string) {
  const [startLine, ...lines] = content.split("\n");
  const heading = document.createElement("div");
  heading.className = "rule-request-url";
  const method = document.createElement("span");
  method.className = "rule-method";
  const destination = document.createElement("code");
  const path = document.createElement("p");
  path.className = "rule-url-path";
  if (startLine.startsWith("GET ")) {
    const url = new URL(startLine.slice(4));
    method.textContent = "GET";
    destination.textContent = url.host;
    destination.classList.toggle("rule-changed", url.host !== requestHost);
    path.textContent = url.href;
  } else {
    method.textContent = "HTTP";
    destination.textContent = startLine.replace("HTTP/1.1 ", "");
    destination.className = "rule-changed";
    path.textContent = ruleActions.mock.parameters.response.contentType;
  }
  heading.append(method, destination);
  const details = document.createElement("pre");
  details.className = "rule-output-details";
  for (const line of lines) {
    const row = document.createElement("span");
    row.textContent = `${line}\n`;
    const changed =
      line.startsWith(`${ruleActions.header.parameters.name}:`) ||
      line.startsWith("{") ||
      (line.startsWith("Host:") && line !== `Host: ${requestHost}`);
    row.classList.toggle("rule-changed-line", changed);
    details.append(row);
  }
  element.replaceChildren(heading, path, details);
}

function run() {
  return player.run(() => {
    const isEnabled = enabled.checked;
    const result = evaluateDemoRule(
      `https://${requestHost}/api/profile?debug=1`,
      {
        enabled: isEnabled,
        action: selectedAction.dataset.ruleAction as RuleAction,
      },
    );
    conditionStates.forEach((state, index) => {
      if (!isEnabled) {
        state.textContent = "—";
        state.dataset.state = "idle";
        return;
      }
      const matched = result.conditions[index];
      state.textContent = matched ? state.dataset.pass! : state.dataset.fail!;
      state.dataset.state = matched ? "pass" : "fail";
    });
    let status = labels.disabled!;
    if (isEnabled) {
      status = result.matched ? labels.matched! : labels.missed!;
    }
    effect.textContent = result.matched
      ? selectedAction.dataset.effect!
      : labels.forwarded!;
    return {
      content: result.output,
      status,
      state: result.matched ? "success" : "neutral",
    };
  });
}

for (const button of actionButtons) {
  button.addEventListener("click", () => {
    selectedAction = button;
    actionButtons.forEach((item) =>
      item.setAttribute("aria-pressed", String(item === button)),
    );
    summaries.forEach((summary) => {
      summary.hidden =
        summary.dataset.actionSummary !== selectedAction.dataset.ruleAction;
    });
    note.textContent = button.dataset.note!;
    void run();
  });
}

for (const button of sampleButtons) {
  button.addEventListener("click", () => {
    requestHost = button.dataset.ruleSample!;
    host.textContent = requestHost;
    sampleButtons.forEach((item) =>
      item.setAttribute("aria-pressed", String(item === button)),
    );
    void run();
  });
}

enabled.addEventListener("change", () => {
  void run();
});
void run();
