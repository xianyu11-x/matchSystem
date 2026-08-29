import { useEffect } from 'react'
import { QueryClient, QueryClientProvider, useQueryClient } from '@tanstack/react-query'
import { BrowserRouter, NavLink, Route, Routes } from 'react-router-dom'
import { Dashboard } from './pages/Dashboard'
import { Rules } from './pages/Rules'
import { Tickets } from './pages/Tickets'
import { useHealth } from './lib/useHealth'
import { isDemoMode, subscribeEvents } from './lib/api'
import { queryKeys } from './lib/queries'
import './styles.css'

const queryClient = new QueryClient({
  defaultOptions: {
    queries: { staleTime: 5_000, retry: 1, refetchOnWindowFocus: false },
  },
})

function EventBridge() {
  const client = useQueryClient()
  useEffect(
    () =>
      subscribeEvents((event) => {
        if (event.type.includes('match'))
          void client.invalidateQueries({ queryKey: queryKeys.matches })
        if (event.type.includes('ticket')) void client.invalidateQueries({ queryKey: ['tickets'] })
        if (event.type.includes('topology') || event.type.includes('round'))
          void client.invalidateQueries({ queryKey: queryKeys.topology })
      }),
    [client],
  )
  return null
}

function AppShell() {
  const health = useHealth()
  return (
    <div className="app-shell">
      <aside className="app-sidebar">
        <div className="brand">
          <span className="brand-mark">M</span>
          <div>
            <strong>MatchScope</strong>
            <span>SIMULATOR CONSOLE</span>
          </div>
        </div>
        <nav className="main-nav" aria-label="主导航">
          <NavLink to="/" end className={({ isActive }) => (isActive ? 'active' : '')}>
            <span className="nav-icon">⌂</span>
            <span>运行总览</span>
            <small>01</small>
          </NavLink>
          <NavLink to="/tickets" className={({ isActive }) => (isActive ? 'active' : '')}>
            <span className="nav-icon">▤</span>
            <span>Tickets</span>
            <small>02</small>
          </NavLink>
          <NavLink to="/rules" className={({ isActive }) => (isActive ? 'active' : '')}>
            <span className="nav-icon">⌘</span>
            <span>Rules</span>
            <small>03</small>
          </NavLink>
        </nav>
        <div className="sidebar-footer">
          <div className="service-status">
            <span
              className={`service-dot ${health.isSuccess ? 'online' : health.isError ? 'offline' : 'checking'}`}
            />
            <div>
              <strong>
                {health.isSuccess
                  ? 'Simulator online'
                  : health.isError
                    ? 'Simulator offline'
                    : 'Connecting…'}
              </strong>
              <span>{isDemoMode ? 'DEMO DATA' : 'REST / SSE'}</span>
            </div>
          </div>
          <span className="version-label">API v1 · schema v3</span>
        </div>
      </aside>
      <main className="app-main">
        <div className="mobile-topbar">
          <div className="brand">
            <span className="brand-mark">M</span>
            <strong>MatchScope</strong>
          </div>
          <span className="version-label">API v1</span>
        </div>
        <Routes>
          <Route path="/" element={<Dashboard />} />
          <Route path="/tickets" element={<Tickets />} />
          <Route path="/rules" element={<Rules />} />
        </Routes>
      </main>
    </div>
  )
}

export function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <EventBridge />
        <AppShell />
      </BrowserRouter>
    </QueryClientProvider>
  )
}
