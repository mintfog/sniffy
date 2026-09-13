import { useEffect, useState } from 'react'
import { Bridge } from '@/lib/bridge'
import { APP_VERSION } from './links'

/** 读取后端应用版本；调用失败时保留浏览器演示版本。 */
export function useBackendVersion(): string {
  const [version, setVersion] = useState(APP_VERSION)
  useEffect(() => {
    let active = true
    Bridge.getVersion()
      .then((v) => {
        if (active && v) setVersion(v)
      })
      .catch(() => {})
    return () => {
      active = false
    }
  }, [])
  return version
}
