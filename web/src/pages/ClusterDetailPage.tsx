import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate, useParams } from 'react-router-dom'
import { ArrowLeft, RotateCw } from 'lucide-react'
import {
  getClusterSyncStatus, listClusterDeletedResources, listClusterResourceSnapshots,
  listClusterSyncResources, listClusterSyncTasks, syncCluster,
} from '@/lib/api'
import { normalizeLanguage } from '@/i18n'
import { localizeApiError, type CommonErrorKey } from '@/i18n/errors'
import { formatDateTime } from '@/i18n/format'
import { localizeStatus } from '@/i18n/status'
import { useClusters } from '@/providers/ClusterProvider'
import { notify } from '@/lib/toast'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { NodeList } from '@/components/NodeList'
import { ResourceBrowser } from '@/components/ResourceBrowser'
import { Select, SelectTrigger, SelectValue, SelectContent, SelectItem } from '@/components/ui/select'
import { Table, TableHeader, TableBody, TableRow, TableHead, TableCell } from '@/components/ui/table'
import { Tooltip, TooltipTrigger, TooltipContent } from '@/components/ui/tooltip'

function statusTone(s?: string): 'success' | 'warning' | 'error' | 'muted' {
  if (s === 'success') return 'success'; if (s === 'running') return 'warning'; if (s === 'failed') return 'error'; return 'muted'
}
function shortSummary(v?: string) { return !v ? '-' : v.length > 160 ? `${v.slice(0, 160)}...` : v }

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null
}

function errorText(value: unknown): string {
  if (typeof value === 'string') return value
  if (value instanceof Error) return value.message
  if (!isRecord(value)) return ''
  const response = isRecord(value.response) ? value.response : undefined
  const data = response && isRecord(response.data) ? response.data : undefined
  if (typeof data?.error === 'string') return data.error
  if (typeof data?.message === 'string') return data.message
  return typeof value.message === 'string' ? value.message : ''
}

function TonePill({ tone, children }: { tone: 'success' | 'warning' | 'error' | 'muted'; children: React.ReactNode }) {
  const map = { success: 'bg-success-weak text-success-fg', warning: 'bg-warning-weak text-warning-fg', error: 'bg-danger-weak text-danger-fg', muted: 'bg-muted text-muted-foreground' }
  return <span className={cn('inline-block rounded-full px-2 py-0.5 text-[11px] font-medium', map[tone])}>{children}</span>
}

export function ClusterDetailPage() {
  const { id } = useParams()
  return <ClusterDetailContent key={id} />
}

function ClusterDetailContent() {
  const { t, i18n } = useTranslation('clusters')
  const { t: commonT } = useTranslation('common')
  const language = normalizeLanguage(i18n.resolvedLanguage)
  const { id } = useParams()
  const navigate = useNavigate()
  const { clusters, loadClusters } = useClusters()
  const clusterId = id ? Number(id) : undefined
  const cluster = useMemo(() => clusters.find((c: any) => String(c.id) === String(id)), [clusters, id])
  // 与列表页一致:按 id 升序的连续显示序号
  const clusterSeq = useMemo(() => {
    const sorted = [...clusters].sort((a: any, b: any) => Number(a.id) - Number(b.id))
    const idx = sorted.findIndex((c: any) => String(c.id) === String(id))
    return idx >= 0 ? idx + 1 : Number(id) || 0
  }, [clusters, id])

  const [syncRows, setSyncRows] = useState<any[]>([])
  const [syncStatus, setSyncStatus] = useState<any>(null)
  const [snapshots, setSnapshots] = useState<any[]>([])
  const [tasks, setTasks] = useState<any[]>([])
  const [deleted, setDeleted] = useState<any[]>([])
  const [selectedResource, setSelectedResource] = useState('all')
  const [syncLoading, setSyncLoading] = useState(false)
  const [syncError, setSyncError] = useState<unknown>(null)
  const selRef = useRef(selectedResource)
  selRef.current = selectedResource
  const pollTimer = useRef<number | null>(null)
  const snapshotRequestSequence = useRef(0)
  const deletedRequestSequence = useRef(0)

  const resName = () => (selRef.current === 'all' ? undefined : selRef.current)

  const currentClusterSync = useMemo(() => (syncStatus?.clusters || []).find((it: any) => String(it.cluster_id) === String(id)) || null, [syncStatus, id])
  const dataStale = Boolean(currentClusterSync?.data_stale)
  const incrementalNotRun = currentClusterSync?.incremental_status === 'not_run'

  const readableSyncError = useCallback((error: unknown, fallbackKey: CommonErrorKey): string => {
    const text = errorText(error)
    if (text.includes('dial tcp') || text.includes('connectex')) {
      const host = text.match(/https:\/\/[^/"\s]+/)?.[0]
      return t('errors.apiServerUnreachable', { host: host ? `: ${host}` : '' })
    }
    const localized = localizeApiError(error, commonT, language, fallbackKey)
    return localized.length > 220 ? `${localized.slice(0, 220)}...` : localized
  }, [commonT, language, t])

  const resourceOptions = useMemo(() => {
    const seen = new Set<string>(); const options: { value: string; count?: number }[] = []
    for (const row of syncRows) { const n = row.resource_name; if (!n || seen.has(n)) continue; seen.add(n); options.push({ value: n, count: row.item_count }) }
    for (const row of snapshots) { const n = row.resource_name; if (!n || seen.has(n)) continue; seen.add(n); options.push({ value: n }) }
    return options.sort((a, b) => a.value.localeCompare(b.value))
  }, [snapshots, syncRows])

  const loadStatus = useCallback(async () => { try { setSyncStatus(await getClusterSyncStatus()) } catch (error) { setSyncError(error) } }, [])
  const loadRows = useCallback(async () => { if (!clusterId) return; try { setSyncRows((await listClusterSyncResources(clusterId)).resources || []) } catch (error) { setSyncError(error) } }, [clusterId])
  const loadSnapshots = useCallback(async () => {
    if (!clusterId) return
    const requestId = ++snapshotRequestSequence.current
    try {
      const result = await listClusterResourceSnapshots(clusterId, resName())
      if (requestId === snapshotRequestSequence.current) setSnapshots(result.snapshots || [])
    } catch (error) {
      if (requestId === snapshotRequestSequence.current) setSyncError(error)
    }
  }, [clusterId])
  const loadTasks = useCallback(async () => { if (!clusterId) return; try { setTasks((await listClusterSyncTasks(clusterId)).tasks || []) } catch (error) { setSyncError(error) } }, [clusterId])
  const loadDeleted = useCallback(async () => {
    if (!clusterId) return
    const requestId = ++deletedRequestSequence.current
    try {
      const result = await listClusterDeletedResources(clusterId, resName())
      if (requestId === deletedRequestSequence.current) setDeleted(result.resources || [])
    } catch (error) {
      if (requestId === deletedRequestSequence.current) setSyncError(error)
    }
  }, [clusterId])

  const loadAll = useCallback(async () => {
    setSyncError(null)
    await Promise.all([loadStatus(), loadRows(), loadSnapshots(), loadTasks(), loadDeleted(), loadClusters()])
  }, [loadStatus, loadRows, loadSnapshots, loadTasks, loadDeleted, loadClusters])

  useEffect(() => {
    loadAll()
    pollTimer.current = window.setInterval(loadAll, 5000)
    return () => {
      snapshotRequestSequence.current += 1
      deletedRequestSequence.current += 1
      if (pollTimer.current) window.clearInterval(pollTimer.current)
    }
  }, [loadAll])

  useEffect(() => { loadSnapshots(); loadDeleted() }, [selectedResource, loadSnapshots, loadDeleted])

  async function syncNow() {
    if (!clusterId || syncLoading) return
    setSyncLoading(true); setSyncError(null)
    try {
      const result = await syncCluster(clusterId)
      await loadAll()
      if (result?.status === 'ready') notify.success(t('feedback.syncSucceeded'))
      else notify.error(readableSyncError(result?.error, 'errors.actionFailed'))
    }
    catch (error) { setSyncError(error) }
    finally { setSyncLoading(false) }
  }

  function changeSelectedResource(value: string) {
    if (value === selRef.current) return
    selRef.current = value
    snapshotRequestSequence.current += 1
    deletedRequestSequence.current += 1
    setSelectedResource(value)
    setSnapshots([])
    setDeleted([])
    setSyncError(null)
  }

  const basicPanel = (
    <div className="grid grid-cols-2 gap-3 rounded-xl border border-border bg-card p-5 shadow-sm md:grid-cols-3">
      {([
        [t('detail.basic.id'), `CL-${String(clusterSeq).padStart(4, '0')}`],
        [t('detail.basic.name'), String(cluster?.name || '-')],
        [t('detail.basic.description'), String(cluster?.description || '-')],
        [t('detail.basic.version'), String(cluster?.version || '-')],
        [t('detail.basic.importMode'), 'kubeconfig'],
        [t('detail.basic.status'), localizeStatus(commonT, 'cluster', String(cluster?.status || 'unknown'))],
      ] as [string, string][]).map(([k, v]) => (
        <div key={k} className="rounded-lg bg-muted/50 px-4 py-3">
          <div className="text-xs text-muted-foreground">{k}</div>
          <div className="mt-1 font-semibold">{v}</div>
        </div>
      ))}
    </div>
  )

  const nodesPanel = <NodeList clusterId={clusterId} embedded />

  const syncPanel = (
    <div className="space-y-4">
      {syncError !== null && <div className="rounded-lg bg-warning-weak px-3 py-2 text-sm text-warning-fg">{t('errors.syncStatusLoadFailed', { error: readableSyncError(syncError, 'errors.loadFailed') })}</div>}

          {dataStale && (
            <div className="rounded-lg border border-destructive/30 bg-danger-weak px-4 py-3 text-sm text-danger-fg">
              <div className="flex flex-wrap items-center gap-2">
                <strong>{t('detail.stale.connectionFailed')}</strong>
                <TonePill tone="error">{t('detail.stale.syncFailed')}</TonePill>
                <TonePill tone="muted">{t('detail.stale.incrementalNotRun')}</TonePill>
              </div>
              <p className="mt-1.5">
                {t('detail.stale.description', { time: formatDateTime(currentClusterSync?.last_successful_at, language) })}
              </p>
              {currentClusterSync?.error && <p className="mt-1 text-xs opacity-90">{readableSyncError(currentClusterSync.error, 'errors.actionFailed')}</p>}
            </div>
          )}

          {/* 同步状态面板 */}
          <div className="rounded-xl border border-border bg-card p-4 shadow-sm">
            <div className="flex items-center gap-2.5">
              <span className="size-2.5 rounded-full" style={{ background: syncStatus?.running ? 'var(--warning)' : 'var(--success)' }} />
              <strong>{syncStatus?.running ? t('detail.syncSummary.running') : t('detail.syncSummary.idle')}</strong>
              <span className="text-sm text-muted-foreground">{t('detail.syncSummary.currentSource', { source: localizeStatus(commonT, 'source', syncStatus?.source) })}</span>
              <span className="ml-auto text-xs text-muted-foreground">{t('detail.syncSummary.latestTask')}</span>
              <TonePill tone={statusTone(currentClusterSync?.status)}>{localizeStatus(commonT, 'sync', currentClusterSync?.status || 'n/a')}</TonePill>
            </div>
            <div className="mt-2.5 flex flex-wrap gap-4 text-xs text-muted-foreground">
              <span>{t('detail.syncSummary.nextRun', { time: formatDateTime(syncStatus?.next_run_at, language) })}</span>
              <span>{t('detail.syncSummary.latestSource', { source: localizeStatus(commonT, 'source', currentClusterSync?.last_source) })}</span>
              <span>{t('detail.syncSummary.latestSync', { time: formatDateTime(currentClusterSync?.last_synced_at, language) })}</span>
              {incrementalNotRun && <span>{t('detail.syncSummary.currentIncremental')}</span>}
            </div>
          </div>

          {/* 同步资源表 */}
          <div className="overflow-hidden rounded-xl border border-border bg-card shadow-sm">
            <div className="overflow-x-auto">
              <Table className="min-w-[1400px]">
                <TableHeader>
                  <TableRow className="hover:bg-transparent">
                    {[
                      t('detail.syncTable.apiVersion'), t('detail.syncTable.kind'), t('detail.syncTable.resourceName'),
                      t('detail.syncTable.itemCount'), t('detail.syncTable.source'), t('detail.syncTable.changeCount'),
                      t('detail.syncTable.fullStarted'), t('detail.syncTable.fullFinished'),
                      dataStale ? t('detail.syncTable.previousFullStatus') : t('detail.syncTable.fullStatus'),
                      t('detail.syncTable.incrementalStarted'), t('detail.syncTable.incrementalFinished'), t('detail.syncTable.rounds'),
                      dataStale ? t('detail.syncTable.currentIncrementalStatus') : t('detail.syncTable.incrementalStatus'),
                      t('detail.syncTable.resourceVersion'),
                    ].map((header) => <TableHead key={header}>{header}</TableHead>)}
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {!syncRows.length && !syncLoading && <TableRow><TableCell colSpan={14} className="py-8 text-center text-sm text-muted-foreground">{t('detail.syncTable.empty')}</TableCell></TableRow>}
                  {syncRows.map((row) => {
                    const historicalSuccess = dataStale && row.full_status === 'success'
                    const currentIncrementalNotRun = dataStale && incrementalNotRun && row.incremental_status !== 'n/a'
                    return <TableRow key={row.resource_name}>
                      <TableCell className="text-xs">{row.api_version}</TableCell>
                      <TableCell className="text-xs">{row.kind}</TableCell>
                      <TableCell className="text-xs">{row.resource_name}</TableCell>
                      <TableCell>{row.item_count >= 0 ? row.item_count : '-'}</TableCell>
                      <TableCell className="text-xs">{localizeStatus(commonT, 'source', row.last_source)}</TableCell>
                      <TableCell>{row.last_change_count ?? 0}</TableCell>
                      <TableCell className="whitespace-nowrap text-xs text-muted-foreground">{formatDateTime(row.full_started_at, language)}</TableCell>
                      <TableCell className="whitespace-nowrap text-xs text-muted-foreground">{formatDateTime(row.full_finished_at, language)}</TableCell>
                      <TableCell><TonePill tone={historicalSuccess ? 'warning' : statusTone(row.full_status)}>{historicalSuccess ? t('detail.stale.historicalSuccess') : localizeStatus(commonT, 'sync', row.full_status || 'n/a')}</TonePill></TableCell>
                      <TableCell className="whitespace-nowrap text-xs text-muted-foreground">{formatDateTime(row.incremental_started_at, language)}</TableCell>
                      <TableCell className="whitespace-nowrap text-xs text-muted-foreground">{formatDateTime(row.incremental_finished_at, language)}</TableCell>
                      <TableCell>{row.incremental_rounds || '-'}</TableCell>
                      <TableCell><TonePill tone={currentIncrementalNotRun ? 'muted' : statusTone(row.incremental_status)}>{localizeStatus(commonT, 'sync', currentIncrementalNotRun ? 'not_run' : row.incremental_status || 'n/a')}</TonePill></TableCell>
                      <TableCell className="text-xs">{row.last_resource_version || '-'}</TableCell>
                    </TableRow>
                  })}
                </TableBody>
              </Table>
            </div>
          </div>

          {/* 资源缓存中心 */}
          <div className="rounded-xl border border-border bg-card shadow-sm">
            <div className="flex flex-wrap items-center justify-between gap-3 border-b border-border p-4">
              <div>
                <span className="inline-flex items-center gap-2">
                  <strong className="text-[15px] font-bold">{t('detail.cache.title')}</strong>
                  {dataStale && <TonePill tone="warning">{t('detail.stale.dataExpired')}</TonePill>}
                </span>
                <span className="ml-2 text-xs text-muted-foreground">{t('detail.cache.count', { count: snapshots.length })}</span>
              </div>
              <Select value={selectedResource} onValueChange={changeSelectedResource}>
                <SelectTrigger className="h-9 w-64"><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="all">{t('detail.cache.allTypes')}</SelectItem>
                  {resourceOptions.map((o) => <SelectItem key={o.value} value={o.value}>{o.value}{typeof o.count === 'number' && Number.isInteger(o.count) && o.count >= 0 ? ` · ${o.count}` : ''}</SelectItem>)}
                </SelectContent>
              </Select>
            </div>
            <div className="overflow-x-auto">
              <Table className="min-w-[1000px]">
                <TableHeader>
                  <TableRow className="hover:bg-transparent">
                    {[
                      t('detail.cache.resourceType'), t('detail.cache.namespace'), t('detail.cache.name'),
                      t('detail.cache.status'), t('detail.cache.ready'), t('detail.cache.node'),
                      t('detail.cache.resourceVersion'), t('detail.cache.lastSeen'), t('detail.cache.summary'),
                    ].map((header) => <TableHead key={header}>{header}</TableHead>)}
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {!snapshots.length && <TableRow><TableCell colSpan={9} className="py-8 text-center text-sm text-muted-foreground">{t('detail.cache.empty')}</TableCell></TableRow>}
                  {snapshots.map((item) => (
                    <TableRow key={`${item.resource_name}/${item.namespace}/${item.name}`}>
                      <TableCell className="text-xs">{item.resource_name || '-'}</TableCell>
                      <TableCell className="text-xs text-muted-foreground">{item.namespace || '-'}</TableCell>
                      <TableCell className="font-mono text-xs">{item.name}</TableCell>
                      <TableCell className="text-xs">{item.status || '-'}</TableCell>
                      <TableCell className="text-xs">{item.ready || '-'}</TableCell>
                      <TableCell className="font-mono text-xs">{item.node_name || '-'}</TableCell>
                      <TableCell className="text-xs">{item.resource_version || '-'}</TableCell>
                      <TableCell className="whitespace-nowrap text-xs text-muted-foreground">{formatDateTime(item.last_seen_at, language)}</TableCell>
                      <TableCell className="max-w-[240px] truncate font-mono text-xs text-muted-foreground">{shortSummary(item.summary)}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          </div>

          {/* 历史:同步任务 + 缓存消失 */}
          <div className="grid gap-4 lg:grid-cols-2">
            <div className="rounded-xl border border-border bg-card shadow-sm">
              <div className="border-b border-border p-4 text-[15px] font-bold">{t('detail.tasks.title')}</div>
              <Table>
                <TableHeader>
                  <TableRow className="hover:bg-transparent">{[
                    t('detail.tasks.source'), t('detail.tasks.status'), t('detail.tasks.resources'),
                    t('detail.tasks.changes'), t('detail.tasks.started'), t('detail.tasks.finished'),
                  ].map((header) => <TableHead key={header}>{header}</TableHead>)}</TableRow>
                </TableHeader>
                <TableBody>
                  {!tasks.length && <TableRow><TableCell colSpan={6} className="py-6 text-center text-sm text-muted-foreground">{t('detail.tasks.empty')}</TableCell></TableRow>}
                  {tasks.map((t) => (
                    <TableRow key={t.id}>
                      <TableCell className="text-xs">{localizeStatus(commonT, 'source', t.source)}</TableCell>
                      <TableCell><TonePill tone={statusTone(t.status)}>{localizeStatus(commonT, 'sync', t.status || 'n/a')}</TonePill></TableCell>
                      <TableCell>{t.resource_count ?? 0}</TableCell>
                      <TableCell>{t.changed_count ?? 0}</TableCell>
                      <TableCell className="whitespace-nowrap text-xs text-muted-foreground">{formatDateTime(t.started_at, language)}</TableCell>
                      <TableCell className="whitespace-nowrap text-xs text-muted-foreground">{formatDateTime(t.finished_at, language)}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>

            <div className="rounded-xl border border-border bg-card shadow-sm">
              <div className="flex items-center gap-2 border-b border-border p-4 text-[15px] font-bold">
                {t('detail.deleted.title')}
                <Tooltip>
                  <TooltipTrigger className="grid size-4 place-items-center rounded-full border border-border text-[10px] text-muted-foreground">?</TooltipTrigger>
                  <TooltipContent className="max-w-xs">{t('detail.deleted.help')}</TooltipContent>
                </Tooltip>
              </div>
              <div className="divide-y divide-border">
                {!deleted.length && <div className="py-6 text-center text-sm text-muted-foreground">{t('detail.deleted.empty')}</div>}
                {deleted.map((item) => (
                  <div key={`${item.resource_name}/${item.namespace}/${item.name}/${item.deleted_at}`} className="flex items-center justify-between gap-3 px-4 py-2.5 text-sm">
                    <div className="flex items-center gap-2">
                      <strong>{item.resource_name || '-'}</strong>
                      <span className="font-mono text-xs text-muted-foreground">{item.namespace || '-'}/{item.name}</span>
                    </div>
                    <span className="whitespace-nowrap text-xs text-muted-foreground">{formatDateTime(item.deleted_at, language)}</span>
                  </div>
                ))}
              </div>
            </div>
          </div>
    </div>
  )

  return (
    <div className="px-8 py-7">
      <div className="mb-4 flex items-center justify-between">
        <div>
          <Button variant="ghost" size="sm" className="mb-1 -ml-2" onClick={() => navigate('/clusters')}><ArrowLeft className="size-4" /> {t('detail.back')}</Button>
          <h2 className="text-xl font-bold tracking-tight">{t('detail.title')} · <span className="text-primary">{cluster?.name || '-'}</span></h2>
        </div>
        <Button variant="outline" size="sm" onClick={syncNow} disabled={syncLoading}><RotateCw className={cn('size-4', syncLoading && 'animate-spin')} /> {syncLoading ? t('page.syncing') : t('detail.syncRefresh')}</Button>
      </div>

      <ResourceBrowser
        clusterId={clusterId}
        basicPanel={basicPanel}
        nodesPanel={nodesPanel}
        syncPanel={syncPanel}
      />
    </div>
  )
}
