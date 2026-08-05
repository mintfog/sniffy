import { useEffect, useState } from 'react'
import { Events, Window } from '@wailsio/runtime'

export function useWindowMaximised() {
  const [maximised, setMaximised] = useState(false)

  useEffect(() => {
    let alive = true
    let revision = 0
    const update = (value: boolean) => {
      revision += 1
      if (alive) setMaximised(value)
    }
    const offs = [
      Events.On(Events.Types.Windows.WindowMaximise, () => update(true)),
      Events.On(Events.Types.Windows.WindowRestore, () => update(false)),
      Events.On(Events.Types.Windows.WindowUnMaximise, () => update(false)),
    ]

    // 初始查询可能与原生窗口事件并发；事件发生后丢弃旧查询结果，避免状态倒退。
    const initialRevision = revision
    void Window.IsMaximised()
      .then(value => {
        if (alive && revision === initialRevision) setMaximised(value)
      })
      .catch(() => {})

    return () => {
      alive = false
      offs.forEach(off => off())
    }
  }, [])

  return maximised
}
