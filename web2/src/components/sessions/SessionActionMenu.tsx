import React from 'react'
import { Link, Terminal, Download, RefreshCw, Share2, Trash2 } from 'lucide-react'
import { HttpSession, WebSocketSession } from '@/types'
import { useSessionActions } from '@/hooks/useSessionActions'

type UnifiedSession = (HttpSession & { sessionType: 'http' }) | (WebSocketSession & { sessionType: 'websocket' })

interface SessionActionMenuProps {
  session: UnifiedSession
  onClose: () => void
}

export function SessionActionMenu({ session, onClose }: SessionActionMenuProps) {
  const { handleSessionAction } = useSessionActions()

  const handleAction = async (action: string) => {
    await handleSessionAction(action, session)
    onClose()
  }

  return (
    <div className="absolute right-0 top-full mt-1 w-48 bg-white border border-gray-200 rounded-md shadow-lg z-50">
      <div className="py-1">
        <button
          className="w-full flex items-center px-4 py-2 text-sm text-gray-700 hover:bg-gray-100 transition-colors"
          onClick={() => handleAction('copy-url')}
        >
          <Link className="h-4 w-4 mr-3" />
          复制URL
        </button>
        
        {session.sessionType === 'http' && (
          <button
            className="w-full flex items-center px-4 py-2 text-sm text-gray-700 hover:bg-gray-100 transition-colors"
            onClick={() => handleAction('copy-curl')}
          >
            <Terminal className="h-4 w-4 mr-3" />
            复制为cURL
          </button>
        )}
        
        <button
          className="w-full flex items-center px-4 py-2 text-sm text-gray-700 hover:bg-gray-100 transition-colors"
          onClick={() => handleAction('export')}
        >
          <Download className="h-4 w-4 mr-3" />
          导出会话
        </button>
        
        {session.sessionType === 'http' && (
          <button
            className="w-full flex items-center px-4 py-2 text-sm text-gray-700 hover:bg-gray-100 transition-colors"
            onClick={() => handleAction('repeat')}
          >
            <RefreshCw className="h-4 w-4 mr-3" />
            重新发送
          </button>
        )}
        
        <button
          className="w-full flex items-center px-4 py-2 text-sm text-gray-700 hover:bg-gray-100 transition-colors"
          onClick={() => handleAction('share')}
        >
          <Share2 className="h-4 w-4 mr-3" />
          分享会话
        </button>
        
        <div className="border-t border-gray-100 my-1"></div>
        
        {session.sessionType === 'http' && (
          <button
            className="w-full flex items-center px-4 py-2 text-sm text-red-600 hover:bg-red-50 transition-colors"
            onClick={() => handleAction('delete')}
          >
            <Trash2 className="h-4 w-4 mr-3" />
            删除会话
          </button>
        )}
      </div>
    </div>
  )
}
