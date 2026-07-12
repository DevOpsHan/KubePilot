import { createContext, useCallback, useContext, useEffect, useState } from 'react'
import { Outlet, useLocation } from 'react-router-dom'
import { Sidebar } from './Sidebar'
import { Topbar } from './Topbar'
import { CommandPalette } from './CommandPalette'
import { apiFetch } from '@/lib/api'
import { useClusters } from '@/providers/ClusterProvider'

interface ShellCtx {
  alertCount: number
  pendingCount: number
  refreshCounts: () => void
}
const Ctx = createContext<ShellCtx>({ alertCount: 0, pendingCount: 0, refreshCounts: () => {} })
export const useShell = () => useContext(Ctx)

export function AppLayout() {
  const [alertCount, setAlertCount] = useState(0)
  const [pendingCount, setPendingCount] = useState(0)
  const [cmdOpen, setCmdOpen] = useState(false)
  const { loadClusters } = useClusters()
  const location = useLocation()

  const refreshCounts = useCallback(async () => {
    try {
      const r = await apiFetch('/api/alerts/summary')
      setAlertCount((await r.json()).active || 0)
    } catch { setAlertCount(0) }
    try {
      const r = await apiFetch('/api/approvals?status=pending')
      setPendingCount((await r.json()).count || 0)
    } catch { setPendingCount(0) }
  }, [])

  useEffect(() => { loadClusters() }, [loadClusters])

  useEffect(() => {
    refreshCounts()
    const t = window.setInterval(refreshCounts, 30000)
    const onKey = (e: KeyboardEvent) => {
      const k = (e.key || '').toLowerCase()
      if ((e.metaKey || e.ctrlKey) && k === 'k') { e.preventDefault(); setCmdOpen((o) => !o) }
    }
    window.addEventListener('keydown', onKey)
    return () => { window.clearInterval(t); window.removeEventListener('keydown', onKey) }
  }, [refreshCounts])

  return (
    <Ctx.Provider value={{ alertCount, pendingCount, refreshCounts }}>
      <div className="flex h-screen overflow-hidden bg-background text-foreground">
        <Sidebar alertCount={alertCount} pendingCount={pendingCount} />
        <div className="flex min-w-0 flex-1 flex-col">
          <Topbar alertCount={alertCount} pendingCount={pendingCount} onSearch={() => setCmdOpen(true)} />
          {/* main 只做定高容器;滚动交给内层 wrapper,保证对话页 h-full 生效 */}
          <main className="min-h-0 flex-1 overflow-hidden">
            {/* 按一级路由 key 触发淡入,翻页更有质感;h-full 维持高度链 */}
            <div key={location.pathname.split('/')[1] || 'root'} className="h-full animate-in overflow-y-auto fade-in duration-300">
              <Outlet />
            </div>
          </main>
        </div>
      </div>
      <CommandPalette open={cmdOpen} onOpenChange={setCmdOpen} />
    </Ctx.Provider>
  )
}
