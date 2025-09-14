import { useState, useEffect, useRef, useCallback } from 'react'
import { useQuery } from '@tanstack/react-query'

interface UseSmartRefreshOptions {
  queryKey: unknown[]
  queryFn: () => Promise<any>
  enabled?: boolean
  interval?: number
  maxRetries?: number
  staleTime?: number
  onDataChange?: (newData: any, oldData: any) => void
}

/**
 * 智能刷新Hook - 减少不必要的更新和渲染
 */
export function useSmartRefresh<T>({
  queryKey,
  queryFn,
  enabled = true,
  interval = 3000,
  maxRetries = 3,
  staleTime = 1000,
  onDataChange
}: UseSmartRefreshOptions) {
  const [lastDataHash, setLastDataHash] = useState<string>('')
  const [refreshCount, setRefreshCount] = useState(0)
  const [isUserActive, setIsUserActive] = useState(true)
  const errorCountRef = useRef(0)
  const retryTimeoutRef = useRef<NodeJS.Timeout>()

  // 监听用户活动状态
  useEffect(() => {
    const handleVisibilityChange = () => {
      setIsUserActive(!document.hidden)
    }

    const handleFocus = () => setIsUserActive(true)
    const handleBlur = () => setIsUserActive(false)

    document.addEventListener('visibilitychange', handleVisibilityChange)
    window.addEventListener('focus', handleFocus)
    window.addEventListener('blur', handleBlur)

    return () => {
      document.removeEventListener('visibilitychange', handleVisibilityChange)
      window.removeEventListener('focus', handleFocus)
      window.removeEventListener('blur', handleBlur)
    }
  }, [])

  // 简单的数据哈希函数
  const hashData = useCallback((data: any): string => {
    return JSON.stringify(data)
  }, [])

  // 智能刷新间隔计算
  const calculateRefreshInterval = useCallback(() => {
    if (!isUserActive) return interval * 2 // 用户不活跃时降低刷新频率
    if (errorCountRef.current > 0) return interval * (errorCountRef.current + 1) // 错误时增加间隔
    return interval
  }, [interval, isUserActive])

  const query = useQuery({
    queryKey,
    queryFn: async () => {
      try {
        const data = await queryFn()
        const newHash = hashData(data)
        
        // 检查数据是否真正发生了变化
        if (newHash !== lastDataHash) {
          const oldData = query.data
          setLastDataHash(newHash)
          setRefreshCount(prev => prev + 1)
          
          // 触发数据变化回调
          if (onDataChange && oldData) {
            onDataChange(data, oldData)
          }
        }
        
        // 重置错误计数
        errorCountRef.current = 0
        
        return data
      } catch (error) {
        errorCountRef.current++
        
        // 如果错误次数超过最大重试次数，暂停自动刷新
        if (errorCountRef.current >= maxRetries) {
          console.warn('达到最大重试次数，暂停自动刷新')
          return query.data // 返回之前的数据
        }
        
        throw error
      }
    },
    enabled,
    refetchInterval: enabled && isUserActive ? calculateRefreshInterval() : false,
    refetchIntervalInBackground: false,
    staleTime,
    retry: false, // 我们手动处理重试逻辑
    refetchOnWindowFocus: true,
    refetchOnReconnect: true
  })

  // 手动刷新函数
  const forceRefresh = useCallback(() => {
    errorCountRef.current = 0
    return query.refetch()
  }, [query])

  // 重置错误状态
  const resetErrors = useCallback(() => {
    errorCountRef.current = 0
  }, [])

  return {
    ...query,
    refreshCount,
    errorCount: errorCountRef.current,
    isUserActive,
    forceRefresh,
    resetErrors,
    hasDataChanged: refreshCount > 0
  }
}

/**
 * 批量更新Hook - 减少频繁的状态更新
 */
export function useBatchedUpdates<T>(
  initialValue: T,
  delay: number = 100
) {
  const [value, setValue] = useState<T>(initialValue)
  const [pendingValue, setPendingValue] = useState<T>(initialValue)
  const timeoutRef = useRef<NodeJS.Timeout>()

  const setBatchedValue = useCallback((newValue: T | ((prev: T) => T)) => {
    const resolvedValue = typeof newValue === 'function' 
      ? (newValue as (prev: T) => T)(pendingValue)
      : newValue

    setPendingValue(resolvedValue)

    if (timeoutRef.current) {
      clearTimeout(timeoutRef.current)
    }

    timeoutRef.current = setTimeout(() => {
      setValue(resolvedValue)
    }, delay)
  }, [pendingValue, delay])

  // 立即更新（跳过批处理）
  const setImmediateValue = useCallback((newValue: T | ((prev: T) => T)) => {
    const resolvedValue = typeof newValue === 'function' 
      ? (newValue as (prev: T) => T)(value)
      : newValue

    if (timeoutRef.current) {
      clearTimeout(timeoutRef.current)
    }

    setValue(resolvedValue)
    setPendingValue(resolvedValue)
  }, [value])

  useEffect(() => {
    return () => {
      if (timeoutRef.current) {
        clearTimeout(timeoutRef.current)
      }
    }
  }, [])

  return [value, setBatchedValue, setImmediateValue] as const
}
