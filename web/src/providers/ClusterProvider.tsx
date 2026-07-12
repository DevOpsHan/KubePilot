import { createContext, useCallback, useContext, useState, type ReactNode } from 'react'
import { listClusters } from '@/lib/api'

export interface Cluster { id: number; name: string; status?: string; [k: string]: unknown }

interface ClusterCtx {
  clusters: Cluster[]
  selectedClusterId: string
  currentClusterId: number | undefined
  loadClusters: () => Promise<void>
  selectCluster: (id: number | string) => void
}

const Ctx = createContext<ClusterCtx>(null as unknown as ClusterCtx)
const STORAGE_KEY = 'aiops-selected-cluster'

export function ClusterProvider({ children }: { children: ReactNode }) {
  const [clusters, setClusters] = useState<Cluster[]>([])
  const [selectedClusterId, setSelected] = useState<string>(() => {
    try { return localStorage.getItem(STORAGE_KEY) || '' } catch { return '' }
  })

  const loadClusters = useCallback(async () => {
    try {
      const data = await listClusters()
      const list: Cluster[] = data.clusters || data || []
      setClusters(list)
      setSelected((cur) => {
        if (cur && list.some((c) => String(c.id) === cur)) return cur
        return list.length ? String(list[0].id) : ''
      })
    } catch {
      setClusters([])
    }
  }, [])

  const selectCluster = useCallback((id: number | string) => {
    const v = String(id)
    setSelected(v)
    try { localStorage.setItem(STORAGE_KEY, v) } catch { /* ignore */ }
  }, [])

  const currentClusterId = selectedClusterId ? Number(selectedClusterId) : undefined

  return (
    <Ctx.Provider value={{ clusters, selectedClusterId, currentClusterId, loadClusters, selectCluster }}>
      {children}
    </Ctx.Provider>
  )
}

export const useClusters = () => useContext(Ctx)
