export interface DemoResult {
  content: string;
  status: string;
  state?: "success" | "neutral";
}

export function createDemoPlayer(
  container: HTMLElement,
  renderOutput = (element: HTMLElement, content: string) => {
    element.textContent = content;
  },
) {
  const button = container.querySelector<HTMLButtonElement>(".demo-run");
  const placeholder = container.querySelector<HTMLElement>(
    "[data-demo-placeholder]",
  )!;
  const output = container.querySelector<HTMLElement>("[data-demo-output]")!;
  const status = container.querySelector<HTMLElement>("[data-demo-status]")!;
  // 输入变化或重新运行会使旧结果失效，异步完成时仅更新最新一轮的界面。
  let revision = 0;

  function reset() {
    revision++;
    container.classList.remove("is-running");
    delete container.dataset.state;
    container.setAttribute("aria-busy", "false");
    if (button) button.disabled = false;
    placeholder.hidden = false;
    output.hidden = true;
    status.textContent = container.dataset.pending!;
  }

  async function run(evaluate: () => DemoResult | Promise<DemoResult>) {
    reset();
    const currentRevision = revision;
    container.classList.add("is-running");
    container.setAttribute("aria-busy", "true");
    if (button) button.disabled = true;
    status.textContent = container.dataset.running!;
    try {
      const result = await evaluate();
      const delay = window.matchMedia("(prefers-reduced-motion: reduce)")
        .matches
        ? 0
        : 420;
      await new Promise((resolve) => window.setTimeout(resolve, delay));
      if (currentRevision !== revision) return;
      placeholder.hidden = true;
      renderOutput(output, result.content);
      output.hidden = false;
      status.textContent = result.status;
      container.dataset.state = result.state ?? "success";
    } catch {
      if (currentRevision !== revision) return;
      status.textContent = container.dataset.failed!;
      container.dataset.state = "error";
    } finally {
      if (currentRevision === revision) {
        container.classList.remove("is-running");
        container.setAttribute("aria-busy", "false");
        if (button) button.disabled = false;
      }
    }
  }

  return { reset, run };
}
