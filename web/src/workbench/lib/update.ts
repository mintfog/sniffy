import { useCallback, useEffect, useMemo, useState } from 'react'
import { Events } from '@wailsio/runtime'
import { Bridge, type UpdateState } from '@/lib/bridge'
import { APP_VERSION } from './links'
import { newerUpdateState, syncUpdateState } from './updateSync'

/** 浏览器演示使用的初始状态。 */
const OFFLINE: UpdateState = {
  // 比后端任何快照都旧，第一份到达的快照必定被采纳。
  revision: -1,
  status: 'idle',
  current: APP_VERSION,
  notify: false,
  autoCheck: false,
  installAction: 'reveal',
}

export interface UpdateActions {
  check: () => Promise<UpdateState | undefined>
  download: () => void
  cancel: () => void
  skip: () => void
  unskip: () => void
  reveal: () => void
  install: () => void
  setAutoCheck: (enabled: boolean) => void
}

/** 各窗口订阅后端快照，并定时回查以补偿丢失的事件。 */
export function useUpdate(): {
  state: UpdateState
  actions: UpdateActions
  installError: string
  installing: boolean
} {
  const [state, setState] = useState<UpdateState>(OFFLINE)
  // 启动安装失败时仍可重试使用已下载的文件。
  const [installError, setInstallError] = useState('')
  // 暂存新应用包可能耗时数秒,期间禁用安装按钮防止重复提交。只有原地替换成功后应用必然退出,
  // 按钮保持禁用;Windows 安装器允许用户放弃更新,交出后同样恢复按钮。
  const [installing, setInstalling] = useState(false)

  const accept = useCallback((s: UpdateState) => {
    setState((prev) => newerUpdateState(prev, s))
  }, [])

  useEffect(() => {
    let active = true
    const apply = (s: UpdateState) => {
      if (active) accept(s)
    }
    Bridge.getUpdateState().then(apply).catch(() => {})
    const off = Events.On('update_state', (e: { data?: UpdateState }) => {
      if (e.data) apply(e.data)
    })
    return () => {
      active = false
      off()
    }
  }, [accept])

  const status = state.status
  useEffect(() => syncUpdateState(status, Bridge.getUpdateState, accept), [status, accept])

  const run = useCallback(
    (request: Promise<UpdateState>) =>
      request
        .then((next) => {
          accept(next)
          return next
        })
        .catch(() => undefined),
    [accept],
  )

  const installAction = state.installAction
  const actions = useMemo<UpdateActions>(
    () => ({
      check: () => run(Bridge.checkUpdate()),
      download: () => {
        setInstallError('')
        run(Bridge.downloadUpdate())
      },
      cancel: () => run(Bridge.cancelUpdateDownload()),
      skip: () => run(Bridge.skipUpdateVersion()),
      unskip: () => run(Bridge.clearSkippedUpdateVersion()),
      reveal: () => {
        Bridge.revealUpdateDownload().catch(() => {})
      },
      install: () => {
        setInstallError('')
        setInstalling(true)
        Bridge.installUpdate()
          .then(() => {
            if (installAction !== 'replace') setInstalling(false)
          })
          .catch((e: unknown) => {
            setInstallError(e instanceof Error ? e.message : String(e))
            setInstalling(false)
          })
      },
      setAutoCheck: (enabled) => run(Bridge.setUpdateAutoCheck(enabled)),
    }),
    [run, installAction],
  )

  return { state, actions, installError, installing }
}

type InstallableAction = Exclude<UpdateState['installAction'], 'reveal'>

/** 「安装」按钮的文案；reveal 只提供打开所在文件夹。 */
export const INSTALL_LABEL_KEYS: Record<InstallableAction, string> = {
  run: 'about.update.install',
  open: 'about.update.openImage',
  replace: 'about.update.installRestart',
}

/** 会关闭应用的安装动作需要事先说明后续流程。 */
export const INSTALL_HINT_KEYS: Partial<Record<InstallableAction, string>> = {
  run: 'about.update.installHint',
  replace: 'about.update.replaceHint',
}

/** 下载期间 total 由后端保证为正数。 */
export function downloadPercent(state: UpdateState): number {
  return Math.round(((state.downloaded ?? 0) / state.total!) * 100)
}

export function formatMB(bytes: number): string {
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`
}
