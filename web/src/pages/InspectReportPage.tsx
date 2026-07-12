import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { RotateCw, Zap, CheckCircle2, Sparkles } from 'lucide-react'
import { runInspect, listReports, getReport, listClusters } from '@/lib/api'
import { normalizeLanguage } from '@/i18n'
import { localizeApiError, type CommonErrorKey } from '@/i18n/errors'
import { formatDateTime } from '@/i18n/format'
import { localizeStatus } from '@/i18n/status'
import { notify } from '@/lib/toast'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'

const sevBadge = (s: string) => (s === 'critical' ? 'bg-danger-weak text-danger-fg' : 'bg-warning-weak text-warning-fg')

interface ClusterOption {
  id: number
  name: string
}

interface ReportSummary {
  id: number
  cluster_id?: number
  started_at?: string
  created_at?: string
  anomaly_count: number
  node_count: number
  pod_count: number
}

interface ReportAnomaly {
  severity: string
  rule: string
  kind: string
  namespace?: string
  name: string
  message: string
  diagnosis?: string
  evidence?: string
}

interface ReportDetail extends ReportSummary {
  anomalies?: ReportAnomaly[]
}

type ReportErrorKey = 'reports.errors.loadList' | 'reports.errors.loadDetail' | 'reports.errors.run'

export function InspectReportPage() {
  const { t, i18n } = useTranslation('operations')
  const { t: commonT } = useTranslation('common')
  const language = normalizeLanguage(i18n.resolvedLanguage)
  const localizationRef = useRef({ t, commonT, language })
  localizationRef.current = { t, commonT, language }
  const [clusters, setClusters] = useState<ClusterOption[]>([])
  const [selectedCluster, setSelectedCluster] = useState(0)
  const selectedClusterRef = useRef(0)
  const [reports, setReports] = useState<ReportSummary[]>([])
  const [loading, setLoading] = useState(false)
  const [running, setRunning] = useState(false)
  const [detail, setDetail] = useState<ReportDetail | null>(null)
  const [detailLoading, setDetailLoading] = useState(false)
  const [activeId, setActiveId] = useState(0)
  const activeIdRef = useRef(0)

  const clusterName = (id: number) => clusters.find((cluster) => cluster.id === id)?.name
    || t('reports.clusterFallback', { id })

  const reportError = useCallback((error: unknown, fallbackKey: CommonErrorKey, operationKey: ReportErrorKey) => {
    const current = localizationRef.current
    const localized = localizeApiError(error, current.commonT, current.language, fallbackKey)
    return localized === current.commonT(fallbackKey) ? current.t(operationKey) : localized
  }, [])

  const openReport = useCallback(async (record: ReportSummary) => {
    setActiveId(record.id); activeIdRef.current = record.id; setDetailLoading(true)
    try { setDetail(await getReport(record.id, record.cluster_id || 0)) }
    catch (error) { notify.error(reportError(error, 'errors.loadFailed', 'reports.errors.loadDetail')); setDetail(null) }
    finally { setDetailLoading(false) }
  }, [reportError])

  const loadReports = useCallback(async (cluster = selectedClusterRef.current) => {
    setLoading(true)
    try {
      const data = await listReports(cluster)
      const list: ReportSummary[] = data.reports || []
      setReports(list)
      if (list.length && !list.some((report) => report.id === activeIdRef.current)) await openReport(list[0])
      else if (!list.length) { setDetail(null); setActiveId(0); activeIdRef.current = 0 }
    } catch (error) { notify.error(reportError(error, 'errors.loadFailed', 'reports.errors.loadList')) }
    finally { setLoading(false) }
  }, [openReport, reportError])

  useEffect(() => {
    listClusters()
      .then((data) => setClusters(data.clusters || data || []))
      .catch((error) => { setClusters([]); notify.error(reportError(error, 'errors.loadFailed', 'reports.errors.loadList')) })
    loadReports(0)
  }, [loadReports, reportError])

  function switchCluster(id: number) {
    if (selectedCluster === id) return
    setSelectedCluster(id); selectedClusterRef.current = id
    setActiveId(0); activeIdRef.current = 0; loadReports(id)
  }
  async function triggerInspect() {
    setRunning(true)
    try {
      await runInspect(selectedCluster)
      notify.success(t('reports.completed'))
      setActiveId(0); activeIdRef.current = 0
      await loadReports(selectedClusterRef.current)
    } catch (error) { notify.error(reportError(error, 'errors.actionFailed', 'reports.errors.run')) }
    finally { setRunning(false) }
  }

  const anomalies = detail?.anomalies || []

  return (
    <div className="px-8 py-7">
      <div className="mb-4 flex items-end justify-between">
        <div>
          <h2 className="text-xl font-bold tracking-tight">{t('reports.title')}</h2>
          <p className="mt-1 text-sm text-muted-foreground">{t('reports.subtitle')}</p>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" onClick={() => loadReports()} disabled={loading || running}>
            <RotateCw className={cn('size-4', loading && 'animate-spin')} /> {t('reports.refresh')}
          </Button>
          <Button size="sm" onClick={triggerInspect} disabled={running}>
            <Zap className="size-4" /> {running ? t('reports.running') : t('reports.runNow')}
          </Button>
        </div>
      </div>

      {/* 集群筛选 */}
      <div className="mb-5 flex flex-wrap gap-2">
        <button onClick={() => switchCluster(0)} className={cn('rounded-full border px-4 py-1.5 text-[13px] transition-colors',
          selectedCluster === 0 ? 'border-primary bg-primary font-semibold text-white' : 'border-border bg-card text-muted-foreground hover:border-primary hover:text-foreground')}>{t('reports.allClusters')}</button>
        {clusters.map((c) => (
          <button key={c.id} onClick={() => switchCluster(c.id)} className={cn('rounded-full border px-4 py-1.5 text-[13px] transition-colors',
            selectedCluster === c.id ? 'border-primary bg-primary font-semibold text-white' : 'border-border bg-card text-muted-foreground hover:border-primary hover:text-foreground')}>{c.name}</button>
        ))}
      </div>

      <div className="grid items-start gap-5 lg:grid-cols-[300px_1fr]">
        {/* 报告列表 */}
        <div className="flex flex-col gap-2">
          {!reports.length && !loading && <div className="rounded-xl border border-dashed border-border py-10 text-center text-sm text-muted-foreground">{t('reports.empty')}</div>}
          {reports.map((r) => (
            <button key={r.id} onClick={() => openReport(r)}
              className={cn('rounded-xl border bg-card px-3.5 py-3 text-left transition-all', activeId === r.id ? 'border-primary ring-2 ring-primary/20' : 'border-border hover:border-primary')}>
              <div className="flex items-center justify-between gap-2">
                <span className="text-[12.5px] font-semibold">{formatDateTime(r.started_at || r.created_at, language)}</span>
                <span className={cn('whitespace-nowrap text-xs font-bold', r.anomaly_count === 0 ? 'text-success-fg' : 'text-danger-fg')}>
                  {r.anomaly_count === 0 ? t('reports.noAnomalies') : t('reports.anomalyCount', { count: r.anomaly_count })}
                </span>
              </div>
              <div className="mt-1.5 flex gap-3 text-[11.5px] text-text-subtle">
                {r.cluster_id ? <span>{clusterName(r.cluster_id)}</span> : null}
                <span>{t('reports.nodeCount', { count: r.node_count })}</span>
                <span>{t('reports.podCount', { count: r.pod_count })}</span>
              </div>
            </button>
          ))}
        </div>

        {/* 报告详情 */}
        <div className="min-h-[200px] rounded-xl border border-border bg-card p-5 shadow-sm">
          {detailLoading ? <div className="py-10 text-center text-sm text-muted-foreground">{t('reports.loading')}</div>
            : !detail ? <div className="py-10 text-center text-sm text-muted-foreground">{t('reports.selectReport')}</div>
            : (
              <>
                <div className="mb-4 flex flex-wrap items-center justify-between gap-2 border-b border-border pb-3.5">
                  <div>
                    <strong className="text-[15px]">{formatDateTime(detail.started_at, language)}</strong>
                    {detail.cluster_id ? <span className="ml-2.5 text-[13px] text-muted-foreground">{clusterName(detail.cluster_id)}</span> : null}
                  </div>
                  <div className="text-[12.5px] text-muted-foreground">{t('reports.summary', {
                    nodes: detail.node_count,
                    pods: detail.pod_count,
                    anomalies: detail.anomaly_count,
                  })}</div>
                </div>

                {!anomalies.length && (
                  <div className="flex items-center gap-2 rounded-xl bg-success-weak px-4 py-3.5 text-[13.5px] font-semibold text-success-fg">
                    <CheckCircle2 className="size-5" /> {t('reports.healthy')}
                  </div>
                )}

                {anomalies.map((anomaly: ReportAnomaly, index: number) => (
                  <div key={index} className="mb-3 rounded-xl border border-border p-4">
                    <div className="flex flex-wrap items-center gap-2.5">
                      <span className={cn('rounded-full px-2 py-0.5 text-[11px] font-bold', sevBadge(anomaly.severity))}>
                        {localizeStatus(commonT, 'severity', anomaly.severity)}
                      </span>
                      <span className="font-mono text-xs text-muted-foreground">{anomaly.rule}</span>
                      <span className="text-[13px] font-semibold">{anomaly.kind}/{anomaly.namespace ? `${anomaly.namespace}/` : ''}{anomaly.name}</span>
                    </div>
                    <div className="mt-2 text-[13px] leading-relaxed">{anomaly.message}</div>
                    {anomaly.diagnosis && (
                      <div className="mt-3 rounded-lg bg-muted/60 p-3">
                        <div className="mb-1.5 flex items-center gap-1.5 text-xs font-bold text-primary"><Sparkles className="size-3.5" /> {t('reports.aiDiagnosis')}</div>
                        <pre className="whitespace-pre-wrap break-words font-mono text-xs leading-relaxed">{anomaly.diagnosis}</pre>
                      </div>
                    )}
                    {anomaly.evidence && (
                      <details className="mt-2.5">
                        <summary className="cursor-pointer select-none text-[12.5px] text-muted-foreground">{t('reports.evidence')}</summary>
                        <pre className="mt-2 max-h-80 overflow-auto rounded-lg bg-muted/60 p-3 font-mono text-xs leading-relaxed">{anomaly.evidence}</pre>
                      </details>
                    )}
                  </div>
                ))}
              </>
            )}
        </div>
      </div>
    </div>
  )
}
