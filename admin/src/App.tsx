import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom'
import { Layout } from './components/Layout'
import { LoginPage } from './pages/LoginPage'
import { DashboardPage } from './pages/DashboardPage'
import { NodesPage } from './pages/NodesPage'
import { TunnelsPage } from './pages/TunnelsPage'
import { UsersPage } from './pages/UsersPage'
import { AccessTokensPage } from './pages/AccessTokensPage'
import { MQTTPage } from './pages/MQTTPage'
import { SettingsPage } from './pages/SettingsPage'
import { useAuth } from './hooks/useAuth'
import { Loading } from './components/Loading'

function ProtectedRoute({ children }: { children: React.ReactNode }) {
  const { user, loading } = useAuth()
  if (loading) return <Loading fullScreen />
  if (!user) return <Navigate to="/login" replace />
  return <>{children}</>
}

export default function App() {
  return (
    <BrowserRouter basename="/admin">
      <Routes>
        <Route path="/login" element={<LoginPage />} />
        <Route element={<ProtectedRoute><Layout /></ProtectedRoute>}>
          <Route path="/dashboard" element={<DashboardPage />} />
          <Route path="/nodes" element={<NodesPage />} />
          <Route path="/tunnels" element={<TunnelsPage />} />
          <Route path="/users" element={<UsersPage />} />
          <Route path="/access-tokens" element={<AccessTokensPage />} />
          <Route path="/mqtt" element={<MQTTPage />} />
          <Route path="/settings" element={<SettingsPage />} />
        </Route>
        <Route path="/" element={<Navigate to="/dashboard" replace />} />
        <Route path="*" element={<Navigate to="/dashboard" replace />} />
      </Routes>
    </BrowserRouter>
  )
}
