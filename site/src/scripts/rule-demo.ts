import { evaluateDemoRule, type RuleAction } from "../data/rule-demo";
import { createDemoPlayer } from "./demo-flow";

const root = document.querySelector<HTMLElement>("[data-rule-demo]")!;
const form = root.querySelector<HTMLFormElement>("[data-demo-flow]")!;
const player = createDemoPlayer(form);
const actions = [
  ...root.querySelectorAll<HTMLButtonElement>("[data-rule-action]"),
];
const samples = [
  ...root.querySelectorAll<HTMLButtonElement>("[data-rule-sample]"),
];
const enabled = root.querySelector<HTMLInputElement>("[data-rule-enabled]")!;
const host = root.querySelector<HTMLElement>("[data-rule-host]")!;
const states = [
  ...root.querySelectorAll<HTMLElement>("[data-condition-state]"),
];
const labels = root.querySelector<HTMLElement>("[data-rule-labels]")!.dataset;
const summaries = root.querySelectorAll<HTMLElement>("[data-action-summary]");
const note = form.querySelector<HTMLElement>("[data-demo-note]")!;
let action: RuleAction = "redirect";
let requestHost = "api.example.com";

function run() {
  return player.run(() => {
    const isEnabled = enabled.checked;
    const result = evaluateDemoRule(
      `https://${requestHost}/api/profile?debug=1`,
      { enabled: isEnabled, action },
    );
    states.forEach((state, index) => {
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
    return {
      content: result.output,
      status,
      state: result.matched ? "success" : "neutral",
    };
  });
}

for (const button of actions) {
  button.addEventListener("click", () => {
    action = button.dataset.ruleAction as RuleAction;
    actions.forEach((item) =>
      item.setAttribute("aria-pressed", String(item === button)),
    );
    summaries.forEach((summary) => {
      summary.hidden = summary.dataset.actionSummary !== action;
    });
    note.textContent = button.dataset.note!;
    void run();
  });
}

for (const button of samples) {
  button.addEventListener("click", () => {
    requestHost = button.dataset.ruleSample!;
    host.textContent = requestHost;
    samples.forEach((item) =>
      item.setAttribute("aria-pressed", String(item === button)),
    );
    void run();
  });
}

enabled.addEventListener("change", () => {
  void run();
});
form.addEventListener("submit", (event) => {
  event.preventDefault();
  void run();
});
void run();
