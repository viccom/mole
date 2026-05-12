import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom'
import { AuthProvider, useAuth } from './hooks/useAuth'
import { TabBar } from './components/TabBar'
import { NodesPage } from './pages/NodesPage'
import { NodeDetailPage } from './pages/NodeDetailPage'
import { TunnelsPage } from './pages/TunnelsPage'
import { SettingsPage } from './pages/SettingsPage'
import { LoginPage } from './pages/LoginPage'
import { FeishuBindPage } from './pages/FeishuBindPage'

function ProtectedLayout() {
  const { user, loading } = useAuth()
  if (loading) return <div className="min-h-screen flex items-center justify-center text-gray-400">加载中...</div>
  if (!user) return <Navigate to="/" replace />
  return (
    <>
      <Routes>
        <Route path="/nodes" element={<NodesPage />} />
        <Route path="/nodes/:id" element={<NodeDetailPage />} />
        <Route path="/tunnels" element={<TunnelsPage />} />
        <Route path="/settings" element={<SettingsPage />} />
      </Routes>
      <TabBar />
    </>
  )
}

export default function App() {
  return (
    <BrowserRouter basename="/mobile">
      <AuthProvider>
        <Routes>
          <Route path="/" element={<LoginPage />} />
          <Route path="/feishu-bind" element={<FeishuBindPage />} />
          <Route path="/*" element={<ProtectedLayout />} />
        </Routes>
      </AuthProvider>
    </BrowserRouter>
  )
}
