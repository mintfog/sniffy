import { useState, useEffect, useRef } from 'react'

/**
 * 节流 Hook
 */
export function useThrottle<T>(value: T, limit: number): T {
  const [throttledValue, setThrottledValue] = useState<T>(value)
  const lastRun = useRef(Date.now())

  useEffect(() => {
    const handler = setTimeout(() => {
      if (Date.now() - lastRun.current >= limit) {
        setThrottledValue(value)
        lastRun.current = Date.now()
      }
    }, limit - (Date.now() - lastRun.current))

    return () => {
      clearTimeout(handler)
    }
  }, [value, limit])

  return throttledValue
}

/**
 * 节流回调 Hook
 */
export function useThrottledCallback<T extends (...args: any[]) => any>(
  callback: T,
  limit: number
): T {
  const inThrottle = useRef(false)

  const throttledCallback = ((...args: Parameters<T>) => {
    if (!inThrottle.current) {
      callback(...args)
      inThrottle.current = true
      setTimeout(() => {
        inThrottle.current = false
      }, limit)
    }
  }) as T

  return throttledCallback
}
