import { lazy, Suspense } from 'react'
import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom'
import { Layout } from './components/Layout'
import { useAuth } from './hooks/useAuth'
import { Loading } from './components/Loading'

const LoginPage = lazy(() => import('./pages/LoginPage').then(module => ({ default: module.LoginPage })))
const FeishuBindPage = lazy(() => import('./pages/FeishuBindPage').then(module => ({ default: module.FeishuBindPage })))
const DashboardPage = lazy(() => import('./pages/DashboardPage').then(module => ({ default: module.DashboardPage })))
const NodesPage = lazy(() => import('./pages/NodesPage').then(module => ({ default: module.NodesPage })))
const TunnelsPage = lazy(() => import('./pages/TunnelsPage').then(module => ({ default: module.TunnelsPage })))
const UsersPage = lazy(() => import('./pages/UsersPage').then(module => ({ default: module.UsersPage })))
const AccessTokensPage = lazy(() => import('./pages/AccessTokensPage').then(module => ({ default: module.AccessTokensPage })))
const MQTTPage = lazy(() => import('./pages/MQTTPage').then(module => ({ default: module.MQTTPage })))
const SettingsPage = lazy(() => import('./pages/SettingsPage').then(module => ({ default: module.SettingsPage })))

function ProtectedRoute({ children }: { children: React.ReactNode }) {
  const { user, loading } = useAuth()
  if (loading) return <Loading fullScreen />
  if (!user) return <Navigate to="/login" replace />
  return <>{children}</>
}

function LazyPage({ children }: { children: React.ReactNode }) {
  return <Suspense fallback={<Loading />}>{children}</Suspense>
}

export default function App() {
  return (
    <BrowserRouter basename="/admin">
      <Routes>
        <Route path="/login" element={<LazyPage><LoginPage /></LazyPage>} />
        <Route path="/feishu-bind" element={<LazyPage><FeishuBindPage /></LazyPage>} />
        <Route element={<ProtectedRoute><Layout /></ProtectedRoute>}>
          <Route path="/dashboard" element={<LazyPage><DashboardPage /></LazyPage>} />
          <Route path="/nodes" element={<LazyPage><NodesPage /></LazyPage>} />
          <Route path="/tunnels" element={<LazyPage><TunnelsPage /></LazyPage>} />
          <Route path="/users" element={<LazyPage><UsersPage /></LazyPage>} />
          <Route path="/access-tokens" element={<LazyPage><AccessTokensPage /></LazyPage>} />
          <Route path="/mqtt" element={<LazyPage><MQTTPage /></LazyPage>} />
          <Route path="/settings" element={<LazyPage><SettingsPage /></LazyPage>} />
        </Route>
        <Route path="/" element={<Navigate to="/dashboard" replace />} />
        <Route path="*" element={<Navigate to="/dashboard" replace />} />
      </Routes>
    </BrowserRouter>
  )
}
