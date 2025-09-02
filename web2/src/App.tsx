import { Routes, Route } from 'react-router-dom'
import { Layout } from '@/components/layout/Layout'
import { Dashboard } from '@/pages/Dashboard'
import { Sessions } from '@/pages/Sessions'

import { Interceptors } from '@/pages/Interceptors'
import { WebSockets } from '@/pages/WebSockets'
import { Settings } from '@/pages/Settings'
import { NotFound } from '@/pages/NotFound'

function App() {
  return (
    <div className="min-h-screen bg-gray-50">
      <Routes>
        <Route path="/" element={<Layout />}>
          <Route index element={<Dashboard />} />
          <Route path="sessions" element={<Sessions />} />

          <Route path="interceptors" element={<Interceptors />} />
          <Route path="websockets" element={<WebSockets />} />
          <Route path="settings" element={<Settings />} />
          <Route path="*" element={<NotFound />} />
        </Route>
      </Routes>
    </div>
  )
}

export default App
