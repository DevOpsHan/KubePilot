import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { RotateCw } from 'lucide-react'
import { normalizeLanguage } from '@/i18n'
import { localizeApiError } from '@/i18n/errors'
import { listNodes, listNodeMetrics } from '@/lib/api'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import {
  Table, TableHeader, TableBody, TableRow, TableHead, TableCell,
} from '@/components/ui/table'

interface ScopedItems<T> {
  clusterId: number | undefined
  items: T[]
}

interface ScopedError {
  clusterId: number | undefined
  error: unknown
}

function formatBytes(bytes?: number) {
  if (!bytes) return '-'
  const gib = bytes / 1024 / 1024 / 1024
  return gib >= 1 ? `${gib.toFixed(1)} GiB` : `${Math.round(bytes / 1024 / 1024)} MiB`
}

// 节点列表:集群详情内嵌 + 独立页复用
export function NodeList({ clusterId, embedded = false }: { clusterId?: number; embedded?: boolean }) {
  const { t, i18n } = useTranslation('clusters')
  const { t: commonT } = useTranslation('common')
  const language = normalizeLanguage(i18n.resolvedLanguage)
  const [nodeState, setNodeState] = useState<ScopedItems<any>>({ clusterId, items: [] })
  const [metricState, setMetricState] = useState<ScopedItems<any>>({ clusterId, items: [] })
  const [loadingState, setLoadingState] = useState({ clusterId, loading: false })
  const [nodeErrorState, setNodeErrorState] = useState<ScopedError | null>(null)
  const [metricsErrorState, setMetricsErrorState] = useState<ScopedError | null>(null)
  const requestSequence = useRef(0)

  const nodes = nodeState.clusterId === clusterId ? nodeState.items : []
  const metrics = metricState.clusterId === clusterId ? metricState.items : []
  const loading = loadingState.clusterId === clusterId && loadingState.loading
  const nodeError = nodeErrorState && nodeErrorState.clusterId === clusterId ? nodeErrorState.error : null
  const metricsError = metricsErrorState && metricsErrorState.clusterId === clusterId ? metricsErrorState.error : null

  const load = useCallback(async () => {
    const requestId = ++requestSequence.current
    const requestClusterId = clusterId
    setLoadingState({ clusterId: requestClusterId, loading: true })
    setNodeErrorState(null)
    setMetricsErrorState(null)
    try {
      const nodeData = await listNodes(clusterId)
      if (requestId !== requestSequence.current) return
      setNodeState({ clusterId: requestClusterId, items: nodeData.nodes || [] })
      try {
        const metricData = await listNodeMetrics(clusterId)
        if (requestId !== requestSequence.current) return
        setMetricState({ clusterId: requestClusterId, items: metricData.metrics || [] })
      } catch (error) {
        if (requestId === requestSequence.current) {
          setMetricsErrorState({ clusterId: requestClusterId, error })
        }
      }
    } catch (error) {
      if (requestId === requestSequence.current) {
        setNodeErrorState({ clusterId: requestClusterId, error })
      }
    } finally {
      if (requestId === requestSequence.current) {
        setLoadingState({ clusterId: requestClusterId, loading: false })
      }
    }
  }, [clusterId])

  useEffect(() => {
    load()
    return () => { requestSequence.current += 1 }
  }, [load])

  const metricFor = (name: string) => metrics.find((m) => m.name === name)

  return (
    <div>
      {!embedded && (
        <div className="mb-4 flex items-end justify-between">
          <div>
            <h2 className="text-xl font-bold tracking-tight">{t('nodes.title')}</h2>
            <p className="mt-1 text-sm text-muted-foreground">{t('nodes.subtitle')}</p>
          </div>
          <Button variant="outline" size="sm" onClick={load} disabled={loading}><RotateCw className={cn('size-4', loading && 'animate-spin')} /> {commonT('actions.refresh')}</Button>
        </div>
      )}

      {nodeError !== null && <div className="mb-3 rounded-lg bg-danger-weak px-3 py-2 text-sm text-danger-fg">{localizeApiError(nodeError, commonT, language, 'errors.loadFailed')}</div>}
      {metricsError !== null && <div className="mb-3 rounded-lg bg-warning-weak px-3 py-2 text-sm text-warning-fg">{t('errors.metricsUnavailable', { error: localizeApiError(metricsError, commonT, language, 'errors.actionFailed') })}</div>}

      <div className="overflow-hidden rounded-xl border border-border bg-card shadow-sm">
        <Table>
          <TableHeader>
            <TableRow className="hover:bg-transparent">
              <TableHead>{t('nodes.node')}</TableHead><TableHead>{t('nodes.status')}</TableHead><TableHead>{t('nodes.version')}</TableHead><TableHead>{t('nodes.cpu')}</TableHead><TableHead>{t('nodes.memory')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {!nodes.length && !loading && nodeError === null && <TableRow><TableCell colSpan={5} className="py-10 text-center text-sm text-muted-foreground">{t('nodes.empty')}</TableCell></TableRow>}
            {nodes.map((n) => {
              const m = metricFor(n.name)
              return (
                <TableRow key={n.name}>
                  <TableCell className="font-mono text-xs font-semibold">{n.name}</TableCell>
                  <TableCell>
                    <span className={cn('inline-block rounded-full px-2 py-0.5 text-[11px] font-medium', n.ready ? 'bg-success-weak text-success-fg' : 'bg-danger-weak text-danger-fg')}>
                      {n.ready ? t('nodes.ready') : t('nodes.notReady')}
                    </span>
                  </TableCell>
                  <TableCell className="text-muted-foreground">{n.version || '-'}</TableCell>
                  <TableCell className="text-sm">
                    {m ? <>{m.cpu_usage_milli}m / {m.cpu_allocatable_milli}m <b className="text-primary">{m.cpu_usage_percent}%</b></> : '-'}
                  </TableCell>
                  <TableCell className="text-sm">
                    {m ? <>{formatBytes(m.memory_usage_bytes)} / {formatBytes(m.memory_allocatable_bytes)} <b className="text-primary">{m.memory_usage_percent}%</b></> : '-'}
                  </TableCell>
                </TableRow>
              )
            })}
          </TableBody>
        </Table>
      </div>
    </div>
  )
}
