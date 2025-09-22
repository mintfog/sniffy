import React from 'react'
import { Monitor, Chrome, Code, Terminal, Database, Zap, Activity, Globe, Settings } from 'lucide-react'
import clsx from 'clsx'

interface ProcessIconProps {
  iconData?: string
  iconType?: string
  processName?: string
  iconCategory?: string
  hasIcon?: boolean
  size?: 'sm' | 'md' | 'lg'
  className?: string
}

export function ProcessIcon({ 
  iconData, 
  iconType, 
  processName, 
  iconCategory, 
  hasIcon = false,
  size = 'md',
  className 
}: ProcessIconProps) {
  const sizeClasses = {
    sm: 'h-4 w-4',
    md: 'h-5 w-5', 
    lg: 'h-6 w-6'
  }

  // 如果有Base64图标数据，优先使用
  if (hasIcon && iconData) {
    const imageSrc = `data:image/${iconType || 'png'};base64,${iconData}`
    
    return (
      <img
        src={imageSrc}
        alt={processName || 'Process'}
        className={clsx(sizeClasses[size], 'flex-shrink-0 rounded', className)}
        onError={(e) => {
          // 如果图标加载失败，显示fallback图标
          const target = e.target as HTMLImageElement
          target.style.display = 'none'
          const fallback = target.nextElementSibling as HTMLElement
          if (fallback) {
            fallback.style.display = 'block'
          }
        }}
      />
    )
  }

  // Fallback: 使用预设图标
  const FallbackIcon = getFallbackIcon(processName, iconCategory)
  const iconColor = getFallbackIconColor(processName, iconCategory)

  return (
    <div className={clsx('flex items-center justify-center', className)}>
      <FallbackIcon className={clsx(sizeClasses[size], iconColor)} />
    </div>
  )
}

// 根据进程名称和类别获取fallback图标
function getFallbackIcon(processName?: string, iconCategory?: string) {
  const name = processName?.toLowerCase() || ''
  
  // 根据类别选择图标
  switch (iconCategory) {
    case 'browser':
      if (name.includes('chrome')) return Chrome
      return Globe
    case 'development':
      return Code
    case 'terminal':
      return Terminal
    case 'database':
      return Database
    case 'api-tools':
      return Zap
    case 'networking':
      return Activity
    case 'system':
      return Settings
    default:
      // 根据进程名称猜测图标
      if (name.includes('chrome') || name.includes('firefox') || name.includes('edge')) {
        return Globe
      }
      if (name.includes('code') || name.includes('idea') || name.includes('studio')) {
        return Code
      }
      if (name.includes('cmd') || name.includes('powershell') || name.includes('bash')) {
        return Terminal
      }
      if (name.includes('postman') || name.includes('curl') || name.includes('httpie')) {
        return Zap
      }
      if (name.includes('mysql') || name.includes('postgres') || name.includes('mongodb')) {
        return Database
      }
      
      return Monitor
  }
}

// 根据进程名称和类别获取fallback图标颜色
function getFallbackIconColor(processName?: string, iconCategory?: string) {
  const name = processName?.toLowerCase() || ''
  
  // 根据类别选择颜色
  switch (iconCategory) {
    case 'browser':
      return 'text-blue-500'
    case 'development':
      return 'text-green-600'
    case 'terminal':
      return 'text-gray-700'
    case 'database':
      return 'text-purple-600'
    case 'api-tools':
      return 'text-orange-500'
    case 'networking':
      return 'text-cyan-600'
    case 'system':
      return 'text-gray-500'
    default:
      // 根据进程名称选择颜色
      if (name.includes('chrome')) {
        return 'text-blue-500'
      }
      if (name.includes('firefox')) {
        return 'text-orange-500'
      }
      if (name.includes('edge')) {
        return 'text-blue-600'
      }
      if (name.includes('code')) {
        return 'text-blue-600'
      }
      if (name.includes('postman')) {
        return 'text-orange-500'
      }
      if (name.includes('node')) {
        return 'text-green-500'
      }
      
      // 根据名称哈希选择颜色
      let hash = 0
      for (let i = 0; i < name.length; i++) {
        hash += name.charCodeAt(i)
      }
      
      const colors = [
        'text-blue-500',
        'text-green-500', 
        'text-red-500',
        'text-orange-500',
        'text-purple-500',
        'text-yellow-500',
        'text-cyan-500',
        'text-pink-500'
      ]
      
      return colors[hash % colors.length]
  }
}

// 进程图标容器组件，带有悬浮提示
export function ProcessIconWithTooltip({ 
  iconData, 
  iconType, 
  processName, 
  iconCategory, 
  hasIcon,
  size = 'md',
  processId,
  processPath,
  className 
}: ProcessIconProps & { processId?: number; processPath?: string }) {
  const tooltipContent = [
    processName && `进程: ${processName}`,
    processId && `PID: ${processId}`,
    processPath && `路径: ${processPath}`,
    iconCategory && `类别: ${iconCategory}`
  ].filter(Boolean).join('\n')

  return (
    <div 
      className={clsx('relative group', className)}
      title={tooltipContent}
    >
      <ProcessIcon 
        iconData={iconData}
        iconType={iconType}
        processName={processName}
        iconCategory={iconCategory}
        hasIcon={hasIcon}
        size={size}
      />
      
      {/* 悬浮提示 */}
      {tooltipContent && (
        <div className="absolute bottom-full left-1/2 transform -translate-x-1/2 mb-2 px-2 py-1 bg-gray-900 text-white text-xs rounded shadow-lg opacity-0 group-hover:opacity-100 transition-opacity duration-200 pointer-events-none z-10 whitespace-pre-line">
          {tooltipContent}
        </div>
      )}
    </div>
  )
}
