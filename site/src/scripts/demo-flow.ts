export interface DemoResult {
  content: string;
  status: string;
  state?: "success" | "neutral";
}

export function createDemoPlayer(form: HTMLFormElement) {
  const button = form.querySelector<HTMLButtonElement>(".demo-run")!;
  const placeholder = form.querySelector<HTMLElement>(
    "[data-demo-placeholder]",
  )!;
  const output = form.querySelector<HTMLElement>("[data-demo-output]")!;
  const status = form.querySelector<HTMLElement>("[data-demo-status]")!;
  let revision = 0;

  function reset() {
    revision++;
    form.classList.remove("is-running");
    delete form.dataset.state;
    form.setAttribute("aria-busy", "false");
    button.disabled = false;
    placeholder.hidden = false;
    output.hidden = true;
    status.textContent = form.dataset.pending!;
  }

  async function run(evaluate: () => DemoResult | Promise<DemoResult>) {
    reset();
    const current = revision;
    form.classList.add("is-running");
    form.setAttribute("aria-busy", "true");
    button.disabled = true;
    status.textContent = form.dataset.running!;
    try {
      const result = await evaluate();
      const delay = window.matchMedia("(prefers-reduced-motion: reduce)")
        .matches
        ? 0
        : 420;
      await new Promise((resolve) => window.setTimeout(resolve, delay));
      if (current !== revision) return;
      placeholder.hidden = true;
      output.textContent = result.content;
      output.hidden = false;
      status.textContent = result.status;
      form.dataset.state = result.state ?? "success";
    } catch {
      if (current !== revision) return;
      status.textContent = form.dataset.failed!;
      form.dataset.state = "error";
    } finally {
      if (current === revision) {
        form.classList.remove("is-running");
        form.setAttribute("aria-busy", "false");
        button.disabled = false;
      }
    }
  }

  return { reset, run };
}
