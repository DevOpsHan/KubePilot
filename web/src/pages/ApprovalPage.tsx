import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { RotateCw } from 'lucide-react'
import { apiFetch, listClusters, listUsers } from '@/lib/api'
import { normalizeLanguage } from '@/i18n'
import { localizeApiError } from '@/i18n/errors'
import { formatDateTime } from '@/i18n/format'
import { localizeStatus } from '@/i18n/status'
import { useConfirm } from '@/providers/ConfirmProvider'
import { useShell } from '@/components/layout/AppLayout'
import { notify } from '@/lib/toast'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import {
  Select, SelectTrigger, SelectValue, SelectContent, SelectItem,
} from '@/components/ui/select'

const ALL = '__all__'
type ApprovalActionKey =
  | 'approval.actions.restart_pod'
  | 'approval.actions.delete_pod'
  | 'approval.actions.scale_deployment'
  | 'approval.actions.restart_deployment'
type ApprovalRiskKey = 'approval.risks.high' | 'approval.risks.medium' | 'approval.risks.low'
type RefreshOptionKey =
  | 'approval.refreshOptions.disabled'
  | 'approval.refreshOptions.oneMinuteTwoSeconds'
  | 'approval.refreshOptions.threeMinutesTwoSeconds'
  | 'approval.refreshOptions.fiveMinutesThreeSeconds'
  | 'approval.refreshOptions.tenMinutesFiveSeconds'
  | 'approval.refreshOptions.tenMinutesTenSeconds'

const actionTranslationKeys: Readonly<Record<string, ApprovalActionKey>> = {
  restart_pod: 'approval.actions.restart_pod',
  delete_pod: 'approval.actions.delete_pod',
  scale_deployment: 'approval.actions.scale_deployment',
  restart_deployment: 'approval.actions.restart_deployment',
}
const riskTranslationKeys: Readonly<Record<string, ApprovalRiskKey>> = {
  high: 'approval.risks.high',
  medium: 'approval.risks.medium',
  low: 'approval.risks.low',
}

const refreshOptions: ReadonlyArray<{ key: RefreshOptionKey; interval: number; duration: number }> = [
  { key: 'approval.refreshOptions.disabled', interval: 0, duration: 0 },
  { key: 'approval.refreshOptions.oneMinuteTwoSeconds', interval: 2, duration: 60 },
  { key: 'approval.refreshOptions.threeMinutesTwoSeconds', interval: 2, duration: 180 },
  { key: 'approval.refreshOptions.fiveMinutesThreeSeconds', interval: 3, duration: 300 },
  { key: 'approval.refreshOptions.tenMinutesFiveSeconds', interval: 5, duration: 600 },
  { key: 'approval.refreshOptions.tenMinutesTenSeconds', interval: 10, duration: 600 },
]

const statusPill: Record<string, string> = {
  pending: 'bg-warning-weak text-warning-fg', approved: 'bg-primary/10 text-primary',
  executed: 'bg-success-weak text-success-fg', rejected: 'bg-danger-weak text-danger-fg', failed: 'bg-danger-weak text-danger-fg',
}
const riskPill: Record<string, string> = { high: 'bg-danger-weak text-danger-fg', medium: 'bg-warning-weak text-warning-fg', low: 'bg-success-weak text-success-fg' }
const verifyPill: Record<string, string> = { pending: 'bg-muted text-muted-foreground', healthy: 'bg-success-weak text-success-fg', failing: 'bg-danger-weak text-danger-fg', skipped: 'bg-muted text-muted-foreground' }

interface ApprovalTicket {
  id: string | number
  action: string
  namespace: string
  target: string
  cluster_id?: number
  cluster_name?: string
  risk: string
  status: string
  approver?: string
  approver_display_name?: string
  reason: string
  result?: string
  verify_state?: string
  verify_detail?: string
  created_at?: string
}

interface ClusterOption {
  id: number
  name: string
}

interface ApproverOption {
  value: string
  displayName?: string
  historical: boolean
}

type AutoRefreshState =
  | { kind: 'running'; seconds: number }
  | { kind: 'stopped' }
  | null

class ResponseError extends Error {
  response: { status: number; data: unknown }

  constructor(response: Response, data: unknown) {
    super(response.statusText)
    this.response = { status: response.status, data }
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null
}

function stringValue(value: unknown): string {
  return typeof value === 'string' ? value : ''
}

function optionalString(value: unknown): string | undefined {
  return typeof value === 'string' && value.trim() !== '' ? value : undefined
}

function optionalNumber(value: unknown): number | undefined {
  if (typeof value === 'number' && Number.isFinite(value)) return value
  if (typeof value === 'string' && value.trim() !== '' && Number.isFinite(Number(value))) return Number(value)
  return undefined
}

function recordList(value: unknown, key: string): Record<string, unknown>[] {
  const list = isRecord(value) && Array.isArray(value[key]) ? value[key] : []
  return list.filter(isRecord)
}

function parseCluster(value: Record<string, unknown>): ClusterOption | undefined {
  const id = optionalNumber(value.id)
  const name = optionalString(value.name)
  return id !== undefined && name ? { id, name } : undefined
}

function parseTicket(value: Record<string, unknown>): ApprovalTicket | undefined {
  const id = value.id
  if (typeof id !== 'string' && typeof id !== 'number') return undefined
  return {
    id,
    action: stringValue(value.action),
    namespace: stringValue(value.namespace),
    target: stringValue(value.target),
    cluster_id: optionalNumber(value.cluster_id),
    cluster_name: optionalString(value.cluster_name),
    risk: stringValue(value.risk),
    status: stringValue(value.status),
    approver: optionalString(value.approver),
    approver_display_name: optionalString(value.approver_display_name),
    reason: stringValue(value.reason),
    result: optionalString(value.result),
    verify_state: optionalString(value.verify_state),
    verify_detail: optionalString(value.verify_detail),
    created_at: optionalString(value.created_at),
  }
}

async function responseData(response: Response): Promise<unknown> {
  let data: unknown
  try { data = await response.json() } catch { data = undefined }
  if (!response.ok) throw new ResponseError(response, data)
  return data
}

export function ApprovalPage() {
  const { t, i18n } = useTranslation('operations')
  const { t: commonT } = useTranslation('common')
  const language = normalizeLanguage(i18n.resolvedLanguage)
  const confirm = useConfirm()
  const { refreshCounts } = useShell()
  const [tickets, setTickets] = useState<ApprovalTicket[]>([])
  const [clusterOptions, setClusterOptions] = useState<ClusterOption[]>([])
  const [approverOptions, setApproverOptions] = useState<ApproverOption[]>([])
  const [filters, setFilters] = useState({ clusterId: '', approver: '', status: '' })
  const [loading, setLoading] = useState(false)
  const [loadError, setLoadError] = useState<unknown>(null)
  const [lastRefresh, setLastRefresh] = useState<number | null>(null)
  const [selIdx, setSelIdx] = useState(0)
  const [autoRefreshState, setAutoRefreshState] = useState<AutoRefreshState>(null)
  const filtersRef = useRef(filters)
  filtersRef.current = filters
  const refreshTimer = useRef<number | null>(null)
  const stopTimer = useRef<number | null>(null)

  function approvalQuery(activeFilters = filtersRef.current) {
    const p = new URLSearchParams()
    if (activeFilters.clusterId) p.set('cluster_id', activeFilters.clusterId)
    if (activeFilters.approver) p.set('approver', activeFilters.approver)
    if (activeFilters.status) p.set('status', activeFilters.status)
    const q = p.toString()
    return q ? '?' + q : ''
  }

  const load = useCallback(async (activeFilters = filtersRef.current) => {
    setLoading(true)
    try {
      const [clusterPayload, userPayload, allResponse] = await Promise.all([
        listClusters(), listUsers(), apiFetch('/api/approvals'),
      ])
      const clusters = recordList(clusterPayload, 'clusters').flatMap((value) => {
        const parsed = parseCluster(value)
        return parsed ? [parsed] : []
      })
      setClusterOptions(clusters)
      const allPayload = await responseData(allResponse)
      const allTickets = recordList(allPayload, 'tickets').flatMap((value) => {
        const parsed = parseTicket(value)
        return parsed ? [parsed] : []
      })

      // 审批人下拉:用户 + 历史工单审批人
      const options = new Map<string, ApproverOption>()
      for (const user of recordList(userPayload, 'users')) {
        const username = optionalString(user.username)
        if (username) {
          options.set(username, {
            value: username,
            displayName: optionalString(user.display_name),
            historical: false,
          })
        }
      }
      for (const ticket of allTickets) {
        const approver = ticket.approver?.trim()
        if (approver && !options.has(approver)) {
          options.set(approver, {
            value: approver,
            displayName: ticket.approver_display_name?.trim(),
            historical: true,
          })
        }
      }
      setApproverOptions(Array.from(options.values()))

      const response = await apiFetch('/api/approvals' + approvalQuery(activeFilters))
      const payload = await responseData(response)
      setTickets(recordList(payload, 'tickets').flatMap((value) => {
        const parsed = parseTicket(value)
        return parsed ? [parsed] : []
      }))
      setLastRefresh(Date.now())
      setLoadError(null)
    } catch (error) { setLoadError(error) }
    finally { setLoading(false) }
  }, [])

  useEffect(() => { load(); return () => clearTimers() }, [load])

  function clearTimers() {
    if (refreshTimer.current) { window.clearInterval(refreshTimer.current); refreshTimer.current = null }
    if (stopTimer.current) { window.clearTimeout(stopTimer.current); stopTimer.current = null }
  }
  function applyRefresh(idx: number) {
    setSelIdx(idx); clearTimers()
    const opt = refreshOptions[idx] ?? refreshOptions[0]
    if (opt.interval > 0) {
      load()
      refreshTimer.current = window.setInterval(load, opt.interval * 1000)
      setAutoRefreshState({ kind: 'running', seconds: opt.interval })
      stopTimer.current = window.setTimeout(() => {
        clearTimers(); setSelIdx(0); setAutoRefreshState({ kind: 'stopped' })
      }, opt.duration * 1000)
    } else setAutoRefreshState(null)
  }

  function ticketClusterQuery(ticket: ApprovalTicket) {
    return ticket.cluster_id
      ? '?' + new URLSearchParams({ cluster_id: String(ticket.cluster_id) }).toString()
      : ''
  }

  function actionLabel(action: string) {
    const key = actionTranslationKeys[action]
    return key ? t(key) : action
  }

  function riskLabel(risk: string) {
    const key = riskTranslationKeys[risk]
    return key ? t(key) : risk
  }

  function approverLabel(option: ApproverOption) {
    if (!option.displayName || option.displayName === option.value) return option.value
    return option.historical
      ? t('approval.historicalOption', { displayName: option.displayName, value: option.value })
      : t('approval.userOption', { displayName: option.displayName, value: option.value })
  }

  async function approve(ticket: ApprovalTicket) {
    const ok = await confirm({
      title: t('approval.approve'),
      message: t('approval.confirmApprove', { id: ticket.id }),
      confirmText: t('approval.approve'),
      tone: 'danger',
    })
    if (!ok) return
    try {
      const response = await apiFetch(`/api/approvals/${ticket.id}/approve${ticketClusterQuery(ticket)}`, { method: 'POST' })
      const payload = await responseData(response)
      const serverError = isRecord(payload) ? optionalString(payload.error) : undefined
      if (serverError) {
        notify.error(t('approval.executionFailed', {
          message: localizeApiError(serverError, commonT, language, 'errors.actionFailed'),
        }))
      } else notify.success(t('approval.approved'))
    } catch (error) { notify.error(localizeApiError(error, commonT, language, 'errors.actionFailed')) }
    await load(); refreshCounts()
  }
  async function reject(ticket: ApprovalTicket) {
    const ok = await confirm({
      title: t('approval.confirmRejectTitle'),
      message: t('approval.confirmReject', { id: ticket.id }),
      confirmText: t('approval.reject'),
      tone: 'danger',
    })
    if (!ok) return
    try {
      const response = await apiFetch(`/api/approvals/${ticket.id}/reject${ticketClusterQuery(ticket)}`, { method: 'POST' })
      await responseData(response)
      notify.info(t('approval.rejected'))
    } catch (error) { notify.error(localizeApiError(error, commonT, language, 'errors.actionFailed')) }
    await load(); refreshCounts()
  }

  const showVerify = (ticket: ApprovalTicket) => ticket.status === 'executed' && Boolean(ticket.verify_state)
  const autoTip = autoRefreshState?.kind === 'running'
    ? t('approval.autoRefreshRunning', { seconds: autoRefreshState.seconds })
    : autoRefreshState?.kind === 'stopped' ? t('approval.autoRefreshStopped') : ''

  return (
    <div className="px-8 py-7">
      <div className="mb-5 flex flex-wrap items-center justify-between gap-3">
        <div>
          <h2 className="text-xl font-bold tracking-tight">{t('approval.title')}</h2>
          <p className="mt-1 text-sm text-muted-foreground">{t('approval.subtitle')}</p>
        </div>
        <div className="flex items-center gap-2 text-xs">
          {lastRefresh && <span className="text-muted-foreground">{t('approval.lastRefresh', { time: formatDateTime(lastRefresh, language) })}</span>}
          {autoTip && <span className="text-success-fg">{autoTip}</span>}
          <Select value={String(selIdx)} onValueChange={(v) => applyRefresh(Number(v))}>
            <SelectTrigger className="h-9 w-[210px]"><SelectValue /></SelectTrigger>
            <SelectContent>{refreshOptions.map((option, index) => (
              <SelectItem key={option.key} value={String(index)}>{t(option.key)}</SelectItem>
            ))}</SelectContent>
          </Select>
          <Button variant="outline" size="icon" className="size-9" disabled={loading} onClick={() => load()} aria-label={commonT('actions.refresh')}>
            <RotateCw className={cn('size-4', loading && 'animate-spin')} />
          </Button>
        </div>
      </div>

      {loadError !== null && (
        <div className="mb-4 rounded-lg bg-danger-weak px-3 py-2 text-sm text-danger-fg">
          {localizeApiError(loadError, commonT, language, 'errors.loadFailed')}
        </div>
      )}

      {/* 过滤条 */}
      <div className="mb-4 grid grid-cols-1 gap-4 rounded-xl border border-border bg-card p-4 shadow-sm md:grid-cols-[repeat(3,minmax(160px,1fr))_auto] md:items-end">
        <div className="space-y-1.5">
          <Label className="text-xs text-muted-foreground">{t('approval.filters.cluster')}</Label>
          <Select value={filters.clusterId || ALL} onValueChange={(v) => setFilters((f) => ({ ...f, clusterId: v === ALL ? '' : v }))}>
            <SelectTrigger className="w-full"><SelectValue /></SelectTrigger>
            <SelectContent><SelectItem value={ALL}>{t('approval.filters.allClusters')}</SelectItem>{clusterOptions.map((cluster) => <SelectItem key={cluster.id} value={String(cluster.id)}>{cluster.name}</SelectItem>)}</SelectContent>
          </Select>
        </div>
        <div className="space-y-1.5">
          <Label className="text-xs text-muted-foreground">{t('approval.filters.approver')}</Label>
          <Select value={filters.approver || ALL} onValueChange={(v) => setFilters((f) => ({ ...f, approver: v === ALL ? '' : v }))}>
            <SelectTrigger className="w-full"><SelectValue /></SelectTrigger>
            <SelectContent><SelectItem value={ALL}>{t('approval.filters.allApprovers')}</SelectItem>{approverOptions.map((option) => <SelectItem key={option.value} value={option.value}>{approverLabel(option)}</SelectItem>)}</SelectContent>
          </Select>
        </div>
        <div className="space-y-1.5">
          <Label className="text-xs text-muted-foreground">{t('approval.filters.status')}</Label>
          <Select value={filters.status || ALL} onValueChange={(v) => setFilters((f) => ({ ...f, status: v === ALL ? '' : v }))}>
            <SelectTrigger className="w-full"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value={ALL}>{t('approval.filters.allStatuses')}</SelectItem>
              {['pending', 'approved', 'executed', 'rejected', 'failed'].map((status) => (
                <SelectItem key={status} value={status}>{localizeStatus(commonT, 'approval', status)}</SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
        <div className="flex gap-2">
          <Button onClick={() => load()} disabled={loading}>{commonT('actions.query')}</Button>
          <Button variant="outline" onClick={() => {
            const emptyFilters = { clusterId: '', approver: '', status: '' }
            setFilters(emptyFilters); filtersRef.current = emptyFilters; load(emptyFilters)
          }} disabled={loading}>{commonT('actions.reset')}</Button>
        </div>
      </div>

      {!tickets.length && (
        <div className="rounded-xl border border-dashed border-border py-14 text-center text-sm leading-8 text-muted-foreground">
          {t('approval.empty')}<br />{t('approval.emptyDescription')}
        </div>
      )}

      <div className="space-y-3.5">
        {tickets.map((ticket) => (
          <div key={ticket.id} className="rounded-xl border border-border bg-card p-5 shadow-sm">
            <div className="flex flex-wrap items-center gap-3">
              <span className="font-mono text-sm text-muted-foreground">#{ticket.id}</span>
              <span className="text-[15px] font-bold">{actionLabel(ticket.action)}</span>
              <span className="rounded-md border border-border bg-muted px-2 py-0.5 font-mono text-sm text-primary">{ticket.namespace ? `${ticket.namespace}/` : ''}{ticket.target}</span>
              <span className="rounded-md border border-border bg-muted px-2 py-0.5 text-xs text-muted-foreground">{t('approval.clusterLabel', {
                name: ticket.cluster_name || (ticket.cluster_id ? t('approval.clusterFallback', { id: ticket.cluster_id }) : '—'),
              })}</span>
              <span className={cn('rounded-full px-2.5 py-0.5 text-xs font-medium', riskPill[ticket.risk] || 'bg-warning-weak text-warning-fg')}>
                {t('approval.riskLabel', { risk: riskLabel(ticket.risk) })}
              </span>
              <span className={cn('ml-auto rounded-full px-2.5 py-0.5 text-xs font-medium', statusPill[ticket.status] || 'bg-muted text-muted-foreground')}>
                {localizeStatus(commonT, 'approval', ticket.status)}
              </span>
              {showVerify(ticket) && <span className={cn('rounded-full px-2.5 py-0.5 text-xs font-medium', verifyPill[ticket.verify_state || ''] || 'bg-muted text-muted-foreground')}>
                {t('approval.verificationLabel', { status: localizeStatus(commonT, 'verification', ticket.verify_state) })}
              </span>}
            </div>
            <div className="mt-2.5 flex flex-wrap gap-3.5 text-xs text-muted-foreground">
              <span>{t('approval.dateLabel', { date: formatDateTime(ticket.created_at, language) })}</span>
              <span>{t('approval.approverLabel', { name: ticket.approver_display_name || ticket.approver || '—' })}</span>
            </div>
            <div className="mt-2.5 text-sm leading-relaxed">{t('approval.reasonLabel', { reason: ticket.reason })}</div>
            {ticket.result && <div className="mt-1 text-[13px] text-success-fg">{t('approval.resultLabel', { result: ticket.result })}</div>}
            {showVerify(ticket) && ticket.verify_detail && (
              <div className={cn('mt-1 text-[13px]', ticket.verify_state === 'healthy' ? 'text-success-fg' : ticket.verify_state === 'failing' ? 'text-danger-fg' : 'text-muted-foreground')}>
                {t('approval.verificationDetail', { detail: ticket.verify_detail })}
              </div>
            )}
            {ticket.status === 'pending' && (
              <div className="mt-3 flex gap-2.5">
                <Button size="sm" className="bg-success text-white hover:bg-success/90" onClick={() => approve(ticket)}>{t('approval.approve')}</Button>
                <Button size="sm" variant="outline" className="border-destructive/40 text-destructive hover:bg-danger-weak" onClick={() => reject(ticket)}>{t('approval.reject')}</Button>
              </div>
            )}
          </div>
        ))}
      </div>
    </div>
  )
}
