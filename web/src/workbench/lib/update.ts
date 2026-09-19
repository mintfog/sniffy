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
  check: () => void
  download: () => void
  cancel: () => void
  skip: () => void
  unskip: () => void
  reveal: () => void
  install: () => void
  setAutoCheck: (enabled: boolean) => void
}

/** 各窗口订阅后端快照，并定时回查以补偿丢失的事件。 */
export function useUpdate(): { state: UpdateState; actions: UpdateActions; installError: string } {
  const [state, setState] = useState<UpdateState>(OFFLINE)
  // 启动安装失败时仍可重试使用已下载的文件。
  const [installError, setInstallError] = useState('')

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
    (request: Promise<UpdateState>) => {
      request
        .then(accept)
        .catch(() => {})
    },
    [accept],
  )

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
        Bridge.installUpdate().catch((e: unknown) => setInstallError(e instanceof Error ? e.message : String(e)))
      },
      setAutoCheck: (enabled) => run(Bridge.setUpdateAutoCheck(enabled)),
    }),
    [run],
  )

  return { state, actions, installError }
}

export function formatMB(bytes: number): string {
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`
}
