import React from 'react'
import ReactDOM from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { BrowserRouter } from 'react-router-dom'
import App from './App.tsx'
import { initializeMockData, startMockDataSimulation } from './store/mockStore'
import './index.css'

// 创建 React Query 客户端
const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: 3,
      staleTime: 5 * 60 * 1000, // 5 分钟
      refetchOnWindowFocus: false,
    },
  },
})

// 在开发模式下初始化模拟数据
if (import.meta.env.DEV || process.env.NODE_ENV === 'development') {
  // 延迟初始化以确保store已经设置完成
  setTimeout(() => {
    initializeMockData()
    startMockDataSimulation()
  }, 100)
}

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <BrowserRouter>
      <QueryClientProvider client={queryClient}>
        <App />
      </QueryClientProvider>
    </BrowserRouter>
  </React.StrictMode>
)
