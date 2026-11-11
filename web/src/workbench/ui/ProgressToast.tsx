interface ProgressToastProps {
  title: string
  /** 0–100。 */
  percent: number
  detail: string
}

/** 右下角的进度浮层：用于不可中断的后台长操作，不阻塞界面，结束后由调用方卸载。 */
export function ProgressToast({ title, percent, detail }: ProgressToastProps) {
  return (
    <div
      className="fixed bottom-10 right-4 z-40 w-80 rounded-lg border border-line bg-popover px-4 py-3 shadow-dialog"
      role="status"
      aria-label={title}
    >
      <div className="mb-1.5 flex items-center justify-between text-[13px] leading-[19px]">
        <span className="font-semibold text-fg">{title}</span>
        <span className="tabular-nums text-fg-muted">{detail}</span>
      </div>
      <div
        className="h-1.5 w-full overflow-hidden rounded-full bg-elevated"
        role="progressbar"
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={percent}
      >
        <div className="h-full bg-accent transition-[width]" style={{ width: `${percent}%` }} />
      </div>
    </div>
  )
}
