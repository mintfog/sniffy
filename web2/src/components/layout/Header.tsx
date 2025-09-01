import { Search, Play, Square, Download, Filter } from 'lucide-react'
import { useAppStore } from '@/store'
import clsx from 'clsx'

export function Header() {
  const { 
    isRecording, 
    searchTerm, 
    setRecording, 
    setSearchTerm, 
    setUIState,
    ui 
  } = useAppStore()

  const handleStartStop = () => {
    setRecording(!isRecording)
  }

  const handleSearch = (e: React.ChangeEvent<HTMLInputElement>) => {
    setSearchTerm(e.target.value)
  }

  const toggleFilterPanel = () => {
    setUIState({ filterPanelOpen: !ui.filterPanelOpen })
  }

  return (
    <header className="bg-white border-b border-gray-200 px-6 py-4">
      <div className="flex items-center justify-between">
        {/* 左侧：控制按钮 */}
        <div className="flex items-center space-x-4">
          {/* 录制控制 */}
          <button
            onClick={handleStartStop}
            className={clsx(
              'flex items-center px-4 py-2 rounded-md font-medium transition-colors',
              isRecording
                ? 'bg-red-600 hover:bg-red-700 text-white'
                : 'bg-green-600 hover:bg-green-700 text-white'
            )}
          >
            {isRecording ? (
              <>
                <Square className="h-4 w-4 mr-2" />
                停止录制
              </>
            ) : (
              <>
                <Play className="h-4 w-4 mr-2" />
                开始录制
              </>
            )}
          </button>

          {/* 导出按钮 */}
          <button className="flex items-center px-4 py-2 bg-gray-100 hover:bg-gray-200 text-gray-700 rounded-md font-medium transition-colors">
            <Download className="h-4 w-4 mr-2" />
            导出
          </button>

          {/* 过滤器按钮 */}
          <button
            onClick={toggleFilterPanel}
            className={clsx(
              'flex items-center px-4 py-2 rounded-md font-medium transition-colors',
              ui.filterPanelOpen
                ? 'bg-primary-100 text-primary-700'
                : 'bg-gray-100 hover:bg-gray-200 text-gray-700'
            )}
          >
            <Filter className="h-4 w-4 mr-2" />
            过滤器
          </button>
        </div>

        {/* 右侧：搜索框 */}
        <div className="flex items-center space-x-4">
          <div className="relative">
            <Search className="absolute left-3 top-1/2 transform -translate-y-1/2 h-4 w-4 text-gray-400" />
            <input
              type="text"
              placeholder="搜索请求..."
              value={searchTerm}
              onChange={handleSearch}
              className="pl-10 pr-4 py-2 w-80 border border-gray-300 rounded-md focus:ring-2 focus:ring-primary-500 focus:border-primary-500"
            />
          </div>
        </div>
      </div>
    </header>
  )
}
