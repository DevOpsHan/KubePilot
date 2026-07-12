import { useCallback, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router-dom'
import {
  Boxes, CheckCircle2, ClipboardCheck, TriangleAlert, RotateCw, ArrowRight,
  type LucideIcon,
} from 'lucide-react'
import { getDashboardSummary, getModelStatus, listAudits, listClusters } from '@/lib/api'
import { normalizeLanguage } from '@/i18n'
import { localizeApiError } from '@/i18n/errors'
import { formatDateTime, formatRelativeTime } from '@/i18n/format'
import { localizeStatus } from '@/i18n/status'
import { quickActions } from '@/lib/chat-actions'
import { cn } from '@/lib/utils'
import { Skeleton } from '@/components/ui/skeleton'

type Tone = 'primary' | 'success' | 'warning' | 'danger'
const kpiIconWrap: Record<Tone, string> = {
  primary: 'bg-primary/10 text-primary',
  success: 'bg-success-weak text-success-fg',
  warning: 'bg-warning-weak text-warning-fg',
  danger: 'bg-danger-weak text-danger-fg',
}

function normalizeList<T = any>(data: any, key: string): T[] {
  if (!data) return []
  if (Array.isArray(data)) return data
  if (Array.isArray(data[key])) return data[key]
  return []
}

function statusTone(status?: string): 'success' | 'error' | 'warning' | 'muted' {
  const s = (status || '').toLowerCase()
  if (['ready', 'imported'].includes(s)) return 'success'
  if (['degraded', 'disconnected', 'error'].includes(s)) return 'error'
  if (s === 'syncing') return 'warning'
  return 'muted'
}
const pillClass: Record<string, string> = {
  success: 'bg-success-weak text-success-fg',
  error: 'bg-danger-weak text-danger-fg',
  warning: 'bg-warning-weak text-warning-fg',
  muted: 'bg-muted text-muted-foreground',
}
const dotColor: Record<string, string> = {
  success: 'var(--success)', error: 'var(--danger)', warning: 'var(--warning)', muted: 'var(--border-strong)',
}
type AuditStatusCategory = 'execution' | 'approval' | 'alert'

function auditStatusCategory(result?: string): AuditStatusCategory {
  const value = String(result || '').trim().toLowerCase()
  if (['active', 'acknowledged', 'resolved'].includes(value)) return 'alert'
  if (['approved', 'rejected', 'executed'].includes(value)) return 'approval'
  return 'execution'
}

function resultTone(result?: string): 'success' | 'error' | 'muted' {
  const value = String(result || '').trim().toLowerCase()
  if (['success', 'approved', 'executed', 'acknowledged', 'resolved'].includes(value)) return 'success'
  if (['failed', 'rejected'].includes(value)) return 'error'
  return 'muted'
}

function StatusPill({ tone, children }: { tone: string; children: React.ReactNode }) {
  return <span className={cn('rounded-full px-2 py-0.5 text-[11px] font-medium', pillClass[tone])}>{children}</span>
}

export function DashboardPage() {
  const { t, i18n } = useTranslation('dashboard')
  const { t: operationsT } = useTranslation('operations')
  const { t: commonT } = useTranslation('common')
  const language = normalizeLanguage(i18n.resolvedLanguage)
  const navigate = useNavigate()
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<unknown>(null)
  const [summary, setSummary] = useState<any>(null)
  const [clusters, setClusters] = useState<any[]>([])
  const [audits, setAudits] = useState<any[]>([])
  const [model, setModel] = useState<any>(null)
  const [modelState, setModelState] = useState<'loading' | 'loaded' | 'failed'>('loading')

  const loadAll = useCallback(async () => {
    setLoading(true); setError(null)
    const results = await Promise.allSettled([
      getDashboardSummary(),
      listClusters(),
      listAudits(),
      getModelStatus(),
    ])
    const [summaryResult, clustersResult, auditsResult, modelResult] = results
    if (summaryResult.status === 'fulfilled') setSummary(summaryResult.value)
    if (clustersResult.status === 'fulfilled') setClusters(normalizeList(clustersResult.value, 'clusters'))
    if (auditsResult.status === 'fulfilled') setAudits(normalizeList(auditsResult.value, 'audits').slice(0, 6))
    if (modelResult.status === 'fulfilled') {
      setModel(modelResult.value?.current || null)
      setModelState('loaded')
    } else {
      setModelState('failed')
    }
    const failed = results.find((result) => result.status === 'rejected')
    setError(failed?.status === 'rejected' ? failed.reason : null)
    setLoading(false)
  }, [])

  useEffect(() => { loadAll() }, [loadAll])

  function sendQuick(msg: string) { navigate('/chat', { state: { quickMessage: msg } }) }

  const showSkeleton = loading && !summary

  const kpis = useMemo(() => {
    const s = summary || {}
    return [
      { key: 'clusters', label: t('statistics.connectedClusters'), value: s.cluster_count ?? 0, icon: Boxes as LucideIcon, tone: 'primary' as Tone, to: '/clusters' },
      { key: 'ready', label: t('statistics.healthyClusters'), value: s.ready_clusters ?? 0, icon: CheckCircle2 as LucideIcon, tone: 'success' as Tone, to: '/clusters' },
      { key: 'pending', label: t('statistics.pendingApprovals'), value: s.pending_approvals ?? 0, icon: ClipboardCheck as LucideIcon, tone: 'warning' as Tone, to: '/approval' },
      { key: 'alerts', label: t('statistics.activeAlerts'), value: s.active_alerts ?? 0, icon: TriangleAlert as LucideIcon, tone: 'danger' as Tone, to: '/alerts' },
    ]
  }, [summary, t])

  const health = useMemo(() => {
    const s = summary || {}
    const ready = s.ready_clusters ?? 0, problem = s.problem_clusters ?? 0, unknown = s.unknown_clusters ?? 0
    return { ready, problem, unknown, total: ready + problem + unknown }
  }, [summary])

  const healthRing = useMemo(() => {
    const h = health
    if (!h.total) return 'conic-gradient(var(--border-strong) 0 360deg)'
    const readyDeg = (h.ready / h.total) * 360
    const problemDeg = (h.problem / h.total) * 360
    return `conic-gradient(var(--success) 0 ${readyDeg}deg, var(--danger) ${readyDeg}deg ${readyDeg + problemDeg}deg, var(--border-strong) ${readyDeg + problemDeg}deg 360deg)`
  }, [health])

  const card = 'rounded-xl border border-border bg-card p-[18px] shadow-sm'

  return (
    <div className="px-8 py-7">
      {/* 页头 */}
      <div className="mb-4 flex items-end justify-between">
        <div>
          <h2 className="text-xl font-bold tracking-tight">{t('page.title')}</h2>
          <p className="mt-1 text-sm text-muted-foreground">{t('page.subtitle')}</p>
        </div>
        <button onClick={loadAll} disabled={loading}
          className="flex items-center gap-2 rounded-lg border border-border bg-card px-3 py-2 text-sm font-medium transition-colors hover:border-primary hover:text-primary disabled:opacity-60">
          <RotateCw className={cn('size-4', loading && 'animate-spin')} /> {loading ? t('page.refreshing') : t('page.refresh')}
        </button>
      </div>

      {error !== null && <div className="mb-4 rounded-lg bg-danger-weak px-3 py-2 text-sm text-danger-fg">{localizeApiError(error, commonT, language, 'errors.loadFailed')}</div>}

      {showSkeleton ? (
        <>
          <div className="mb-4 grid grid-cols-2 gap-3.5 lg:grid-cols-4">
            {[0, 1, 2, 3].map((n) => (
              <div key={n} className={cn(card, 'flex items-center gap-3.5')}>
                <Skeleton className="size-[46px] rounded-xl" />
                <div className="flex-1 space-y-2"><Skeleton className="h-5 w-1/2" /><Skeleton className="h-3 w-3/4" /></div>
              </div>
            ))}
          </div>
          <div className="grid gap-4 lg:grid-cols-[1.3fr_1fr]">
            <div className="space-y-4"><div className={card}><Skeleton className="h-52 w-full" /></div><div className={card}><Skeleton className="h-24 w-full" /></div></div>
            <div className="space-y-4"><div className={card}><Skeleton className="h-24 w-full" /></div><div className={card}><Skeleton className="h-24 w-full" /></div></div>
          </div>
        </>
      ) : (
        <>
          {/* KPI */}
          <div className="mb-4 grid grid-cols-2 gap-3.5 lg:grid-cols-4">
            {kpis.map((k, i) => {
              const Icon = k.icon
              return (
                <button key={k.key} onClick={() => navigate(k.to)} style={{ animationDelay: `${i * 60}ms` }}
                  className={cn(card, 'group flex animate-in items-center gap-3.5 fade-in slide-in-from-bottom-2 text-left duration-500',
                    'transition-all hover:-translate-y-0.5 hover:border-border-strong hover:shadow-md')}>
                  <div className={cn('grid size-[46px] shrink-0 place-items-center rounded-xl', kpiIconWrap[k.tone])}>
                    <Icon className="size-5" />
                  </div>
                  <div className="min-w-0 flex-1">
                    <div className="text-[28px] font-extrabold leading-none tracking-tight">{k.value}</div>
                    <div className="mt-0.5 text-[13px] text-muted-foreground">{k.label}</div>
                  </div>
                  <ArrowRight className="size-4 -translate-x-1 text-muted-foreground opacity-0 transition-all group-hover:translate-x-0 group-hover:opacity-100" />
                </button>
              )
            })}
          </div>

          <div className="grid gap-4 lg:grid-cols-[1.3fr_1fr]">
            {/* 左列 */}
            <div className="flex min-w-0 flex-col gap-4">
              {/* 集群健康 */}
              <div className={card}>
                <div className="mb-3.5 flex items-center justify-between">
                  <strong className="text-[15px] font-bold">{t('health.title')}</strong>
                  <button onClick={() => navigate('/clusters')} className="text-sm text-primary hover:underline">{t('health.viewAll')}</button>
                </div>
                <div className="flex items-center gap-6">
                  <div className="grid size-[118px] shrink-0 place-items-center rounded-full" style={{ background: healthRing }}>
                    <div className="grid size-[82px] place-items-center rounded-full bg-card">
                      <b className="text-base font-extrabold">{t('health.clusterCount', { count: health.total })}</b>
                    </div>
                  </div>
                  <div className="flex flex-col gap-2 text-[13px] text-muted-foreground">
                    <div className="flex items-center gap-2"><span className="size-2.5 rounded-full" style={{ background: 'var(--success)' }} />{t('health.healthy')} <b className="text-foreground">{health.ready}</b></div>
                    <div className="flex items-center gap-2"><span className="size-2.5 rounded-full" style={{ background: 'var(--danger)' }} />{t('health.problem')} <b className="text-foreground">{health.problem}</b></div>
                    <div className="flex items-center gap-2"><span className="size-2.5 rounded-full" style={{ background: 'var(--border-strong)' }} />{t('health.unknown')} <b className="text-foreground">{health.unknown}</b></div>
                  </div>
                </div>
                <div className="mt-4 border-t border-border pt-1.5">
                  {clusters.slice(0, 5).map((c) => {
                    const tone = statusTone(c.status)
                    return (
                      <div key={c.id} onClick={() => navigate('/clusters')}
                        className="flex cursor-pointer items-center gap-2.5 rounded-md px-1 py-2 text-[13px] transition-colors hover:bg-muted">
                        <span className="size-2 rounded-full" style={{ background: dotColor[tone] }} />
                        <span className="flex-1 truncate font-semibold">{c.name}</span>
                        <StatusPill tone={tone}>{localizeStatus(commonT, 'cluster', c.status || 'unknown')}</StatusPill>
                      </div>
                    )
                  })}
                  {!clusters.length && <div className="py-4 text-center text-[13px] text-muted-foreground">{t('health.empty')}</div>}
                </div>
              </div>

              {/* 最近操作 */}
              <div className={card}>
                <div className="mb-3.5 flex items-center justify-between">
                  <strong className="text-[15px] font-bold">{t('audit.title')}</strong>
                  <button onClick={() => navigate('/audit')} className="text-sm text-primary hover:underline">{t('audit.openCenter')}</button>
                </div>
                <div>
                  {audits.map((a) => {
                    const category = auditStatusCategory(a.result)
                    return (
                      <div key={a.id} className="flex items-center gap-2.5 border-b border-border py-2 text-[13px] last:border-none">
                        <span className="font-mono text-xs font-semibold">{a.action}</span>
                        <span className="text-muted-foreground">{a.operator}</span>
                        <StatusPill tone={resultTone(a.result)}>{localizeStatus(commonT, category, a.result)}</StatusPill>
                        <span className="ml-auto whitespace-nowrap text-xs text-muted-foreground" title={formatDateTime(a.created_at, language)}>{formatRelativeTime(a.created_at, language)}</span>
                      </div>
                    )
                  })}
                  {!audits.length && <div className="py-4 text-center text-[13px] text-muted-foreground">{t('audit.empty')}</div>}
                </div>
              </div>
            </div>

            {/* 右列 */}
            <div className="flex min-w-0 flex-col gap-4">
              {/* 同步状态 */}
              <div className={card}>
                <div className="mb-3.5"><strong className="text-[15px] font-bold">{t('sync.title')}</strong></div>
                <div className="mb-3.5 flex items-center gap-2.5 text-sm">
                  <span className="size-2 rounded-full" style={{ background: summary?.sync_running ? 'var(--warning)' : 'var(--success)' }} />
                  <span>{summary?.sync_running ? t('sync.running') : t('sync.idle')}</span>
                  {summary?.sync_source && <StatusPill tone="muted">{localizeStatus(commonT, 'source', summary.sync_source)}</StatusPill>}
                </div>
                <div className="grid grid-cols-2 gap-3">
                  <div className="rounded-md bg-muted px-3 py-2.5"><span className="mb-1 block text-xs text-muted-foreground">{t('sync.lastFinished')}</span><b className="text-[13px]" title={formatDateTime(summary?.sync_finished_at, language)}>{formatRelativeTime(summary?.sync_finished_at, language)}</b></div>
                  <div className="rounded-md bg-muted px-3 py-2.5"><span className="mb-1 block text-xs text-muted-foreground">{t('sync.nextScheduled')}</span><b className="text-[13px]" title={formatDateTime(summary?.next_sync_at, language)}>{formatRelativeTime(summary?.next_sync_at, language)}</b></div>
                </div>
                {summary?.last_sync_error && <div className="mt-3 rounded-md bg-danger-weak px-3 py-2 text-xs text-danger-fg">{localizeApiError(summary.last_sync_error, commonT, language, 'errors.actionFailed')}</div>}
              </div>

              {/* 快捷操作 */}
              <div className={card}>
                <div className="mb-3.5"><strong className="text-[15px] font-bold">{t('quickActions.title')}</strong></div>
                <div className="flex flex-col gap-2">
                  {quickActions.map((q) => {
                    const Icon = q.icon
                    const label = operationsT(`quickActions.${q.key}.label`)
                    const prompt = operationsT(`quickActions.${q.key}.prompt`)
                    return (
                      <button key={q.key} onClick={() => sendQuick(prompt)}
                        className="group flex items-center gap-3 rounded-md border border-border bg-card px-3.5 py-3 text-left transition-colors hover:border-primary hover:bg-primary/5">
                        <span className="text-primary"><Icon className="size-[18px]" /></span>
                        <span className="flex-1 text-[13.5px] font-semibold">{label}</span>
                        <ArrowRight className="size-[15px] text-muted-foreground group-hover:text-primary" />
                      </button>
                    )
                  })}
                </div>
              </div>

              {/* AI 模型 */}
              <div className={card}>
                <div className="mb-3.5 flex items-center justify-between">
                  <strong className="text-[15px] font-bold">{t('model.title')}</strong>
                  <button onClick={() => navigate('/models')} className="text-sm text-primary hover:underline">{t('model.manage')}</button>
                </div>
                {model ? (
                  <div className="flex items-center gap-3">
                    <span className="size-2 rounded-full" style={{ background: 'var(--success)' }} />
                    <div>
                      <div className="font-semibold">{model.model || model.model_name || model.name || t('model.configured')}</div>
                      <div className="text-xs capitalize text-muted-foreground">{model.provider || ''}</div>
                    </div>
                  </div>
                ) : modelState === 'loaded' ? (
                  <div className="py-4 text-center text-[13px] text-muted-foreground">{t('model.notConfigured')}</div>
                ) : modelState === 'failed' ? (
                  <div className="py-4 text-center text-[13px] text-muted-foreground">{t('model.unavailable')}</div>
                ) : null}
              </div>
            </div>
          </div>
        </>
      )}
    </div>
  )
}
