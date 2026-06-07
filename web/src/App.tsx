import { Routes, Route } from 'react-router-dom'
import { NotFound } from '@/pages/NotFound'
import Workbench from '@/workbench/Workbench'

function App() {
  return (
    <Routes>
      {/* 默认进入工作台 */}
      <Route path="/" element={<Workbench />} />
      <Route path="*" element={<NotFound />} />
    </Routes>
  )
}

export default App
