import { useState } from 'react'
import { ChevronDown, ChevronRight, Copy } from 'lucide-react'
import clsx from 'clsx'

interface ExpandableCellProps {
  content: string
  maxLength?: number
  className?: string
  showCopy?: boolean
}

export function ExpandableCell({ 
  content, 
  maxLength = 50, 
  className = '', 
  showCopy = false 
}: ExpandableCellProps) {
  const [isExpanded, setIsExpanded] = useState(false)
  const [showTooltip, setShowTooltip] = useState(false)
  
  const shouldTruncate = content.length > maxLength
  const displayContent = shouldTruncate && !isExpanded 
    ? content.slice(0, maxLength) + '...' 
    : content

  const handleCopy = async (e: React.MouseEvent) => {
    e.stopPropagation()
    try {
      await navigator.clipboard.writeText(content)
      setShowTooltip(true)
      setTimeout(() => setShowTooltip(false), 2000)
    } catch (err) {
      console.error('复制失败:', err)
    }
  }

  if (!shouldTruncate) {
    return (
      <div className={clsx('relative group', className)}>
        <span className="text-sm text-gray-900">{content}</span>
        {showCopy && (
          <button
            onClick={handleCopy}
            className="ml-2 opacity-0 group-hover:opacity-100 transition-opacity p-1 text-gray-400 hover:text-gray-600"
            title="复制"
          >
            <Copy className="h-3 w-3" />
          </button>
        )}
        {showTooltip && (
          <div className="absolute z-10 px-2 py-1 text-xs text-white bg-gray-800 rounded shadow-lg -top-8 left-0 whitespace-nowrap">
            已复制
          </div>
        )}
      </div>
    )
  }

  return (
    <div className={clsx('relative group', className)}>
      <div className="flex items-start">
        <button
          onClick={() => setIsExpanded(!isExpanded)}
          className="flex items-start text-sm text-gray-900 hover:text-blue-600 transition-colors text-left w-full"
        >
          {isExpanded ? (
            <ChevronDown className="h-3 w-3 mr-1 flex-shrink-0 mt-0.5" />
          ) : (
            <ChevronRight className="h-3 w-3 mr-1 flex-shrink-0 mt-0.5" />
          )}
          <span className={clsx(
            'min-w-0',
            isExpanded ? 'whitespace-pre-wrap break-words' : 'truncate'
          )}>
            {displayContent}
          </span>
        </button>
        {showCopy && (
          <button
            onClick={handleCopy}
            className="ml-2 opacity-0 group-hover:opacity-100 transition-opacity p-1 text-gray-400 hover:text-gray-600 flex-shrink-0"
            title="复制"
          >
            <Copy className="h-3 w-3" />
          </button>
        )}
      </div>
      {showTooltip && (
        <div className="absolute z-10 px-2 py-1 text-xs text-white bg-gray-800 rounded shadow-lg -top-8 right-0 whitespace-nowrap">
          已复制
        </div>
      )}
    </div>
  )
}
