import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router-dom'
import { Bell, BellOff, RotateCw, Sparkles, Zap } from 'lucide-react'
import {
  acknowledgeAlert,
  createSilence,
  deleteSilence,
  diagnoseAlert,
  getAlertSummary,
  getAlertTrends,
  getNotificationSettings,
  listAlerts,
  listClusters,
  listSilences,
  remediateAlert,
  resolveAlert,
  saveNotificationSettings,
  testNotification,
} from '@/lib/api'
import { normalizeLanguage } from '@/i18n'
import { localizeApiError, type CommonErrorKey } from '@/i18n/errors'
import { formatCalendarDate, formatDateTime, formatRelativeTime } from '@/i18n/format'
import { localizeStatus } from '@/i18n/status'
import { useAuth } from '@/providers/AuthProvider'
import { useConfirm } from '@/providers/ConfirmProvider'
import { useShell } from '@/components/layout/AppLayout'
import { notify } from '@/lib/toast'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Sheet, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from '@/components/ui/sheet'

const ALL = '__all__'
const fixableRules = new Set(['PodCrashLoopBackOff', 'PodHighRestarts', 'PodNotReady', 'PodFailed'])
const suggestedActions = new Set(['restart_pod', 'restart_deployment', 'delete_pod'])
const defaultFilters: AlertFilters = { severity: '', status: 'active', clusterId: '' }
const emptySummary: AlertSummary = {
  active: 0,
  acknowledged: 0,
  resolved: 0,
  critical: 0,
  warning: 0,
  silenced: 0,
}
const durationOptions = [
  { value: 1, key: 'alerts.silences.durationOptions.oneHour' },
  { value: 8, key: 'alerts.silences.durationOptions.eightHours' },
  { value: 24, key: 'alerts.silences.durationOptions.oneDay' },
  { value: 72, key: 'alerts.silences.durationOptions.threeDays' },
  { value: 168, key: 'alerts.silences.durationOptions.sevenDays' },
] as const

type PillTone = 'error' | 'warning' | 'success' | 'muted'

interface AlertFilters {
  severity: string
  status: string
  clusterId: string
}

interface ClusterRecord {
  id: number
  name: string
}

interface AlertRecord {
  id: number
  cluster_id?: number
  cluster_name?: string
  severity: string
  kind: string
  namespace?: string
  name: string
  rule: string
  message: string
  status: string
  count: number
  first_seen_at?: string
  last_seen_at?: string
  acknowledged_by?: string
  resolved_by?: string
  diagnosis?: string
  suggested_action?: string
  remediation_ticket_id?: number | null
  silenced?: boolean
}

interface AlertSummary {
  active: number
  acknowledged: number
  resolved: number
  critical: number
  warning: number
  silenced: number
}

interface TrendBucket {
  date: string
  total: number
  critical: number
  warning: number
  resolved: number
}

interface TrendBar extends TrendBucket {
  axisLabel: string
  displayDate: string
  totalPct: number
  criticalShare: number
  warningShare: number
}

interface SilenceForm {
  cluster_id: number
  rule: string
  namespace: string
  name: string
  duration_hours: number
  comment: string
}

interface SilenceRecord {
  id: number
  cluster_id: number
  cluster_name?: string
  rule: string
  namespace: string
  name: string
  comment: string
  created_by?: string
  expires_at: string
}

interface NotificationSettings {
  enabled: boolean
  min_severity: string
  notify_resolved: boolean
  dingtalk_webhook_configured?: boolean
  dingtalk_webhook_hint?: string
  dingtalk_secret_configured?: boolean
  wecom_webhook_configured?: boolean
  wecom_webhook_hint?: string
  active_channels?: string[]
  source?: string
}

interface NotificationForm {
  enabled: boolean
  min_severity: string
  notify_resolved: boolean
  dingtalk_webhook: string
  dingtalk_secret: string
  wecom_webhook: string
  clear_dingtalk: boolean
  clear_dingtalk_secret: boolean
  clear_wecom: boolean
}

const tonePill: Record<PillTone, string> = {
  error: 'bg-danger-weak text-danger-fg',
  warning: 'bg-warning-weak text-warning-fg',
  success: 'bg-success-weak text-success-fg',
  muted: 'bg-muted text-muted-foreground',
}

function statusTone(status: string): PillTone {
  if (status === 'active') return 'error'
  if (status === 'acknowledged') return 'warning'
  if (status === 'resolved') return 'success'
  return 'muted'
}

function alertTarget(alert: AlertRecord): string {
  const objectName = alert.namespace ? `${alert.namespace}/${alert.name}` : alert.name
  return `${alert.rule} · ${objectName}`
}

function secretField(input: string, clearFlag: boolean): string | undefined {
  if (clearFlag) return ''
  const value = input.trim()
  return value || undefined
}

export function AlertsPage() {
  const { t, i18n } = useTranslation('operations')
  const { t: commonT } = useTranslation('common')
  const language = normalizeLanguage(i18n.resolvedLanguage)
  const localization = useRef({ operations: t, common: commonT, language })
  localization.current = { operations: t, common: commonT, language }
  const navigate = useNavigate()
  const { can } = useAuth()
  const confirm = useConfirm()
  const { refreshCounts } = useShell()
  const [alerts, setAlerts] = useState<AlertRecord[]>([])
  const [clusters, setClusters] = useState<ClusterRecord[]>([])
  const [summary, setSummary] = useState<AlertSummary>(emptySummary)
  const [loading, setLoading] = useState(false)
  const [filters, setFilters] = useState<AlertFilters>(defaultFilters)
  const [trends, setTrends] = useState<TrendBucket[]>([])
  const [hoverIndex, setHoverIndex] = useState(-1)
  const [busyId, setBusyId] = useState(0)
  const [loadError, setLoadError] = useState<unknown>(null)
  const [clusterError, setClusterError] = useState<unknown>(null)

  function actionLabel(action?: string): string {
    const translate = localization.current.operations
    switch (action) {
      case 'restart_pod': return translate('alerts.actions.restartPod')
      case 'delete_pod': return translate('alerts.actions.deletePod')
      case 'restart_deployment': return translate('alerts.actions.restartDeployment')
      case 'scale_deployment': return translate('alerts.actions.scaleDeployment')
      default: return action || translate('alerts.actions.fallbackRemediation')
    }
  }

  function channelLabel(channel: string): string {
    const translate = localization.current.operations
    switch (channel) {
      case 'dingtalk': return translate('alerts.notifications.channels.dingtalk')
      case 'wecom': return translate('alerts.notifications.channels.wecom')
      case 'email': return translate('alerts.notifications.channels.email')
      default: return channel
    }
  }

  function currentApiError(error: unknown, fallbackKey: CommonErrorKey): string {
    const current = localization.current
    return localizeApiError(error, current.common, current.language, fallbackKey)
  }

  const hasSuggested = (alert: AlertRecord) => Boolean(
    alert.suggested_action && suggestedActions.has(alert.suggested_action),
  )
  const isFixable = (alert: AlertRecord) => alert.status !== 'resolved'
    && (fixableRules.has(alert.rule) || hasSuggested(alert))

  const load = useCallback(async (nextFilters: AlertFilters) => {
    setLoading(true)
    setLoadError(null)
    try {
      const results = await Promise.allSettled([
        listAlerts({
          status: nextFilters.status,
          severity: nextFilters.severity,
          clusterId: nextFilters.clusterId,
        }),
        getAlertSummary(),
        getAlertTrends(7, nextFilters.clusterId),
      ])
      const [alertsResult, summaryResult, trendsResult] = results

      if (alertsResult.status === 'fulfilled') {
        const response: { alerts?: AlertRecord[] } = alertsResult.value
        setAlerts(response.alerts ?? [])
      }
      if (summaryResult.status === 'fulfilled') {
        const response: AlertSummary = summaryResult.value
        setSummary(response)
      }
      if (trendsResult.status === 'fulfilled') {
        const response: { buckets?: TrendBucket[] } = trendsResult.value
        setTrends(response.buckets ?? [])
      }

      const failed = results.find((result) => result.status === 'rejected')
      setLoadError(failed?.status === 'rejected' ? failed.reason : null)
    } finally {
      setLoading(false)
      void refreshCounts()
    }
  }, [refreshCounts])

  useEffect(() => {
    let active = true
    setClusterError(null)
    void listClusters()
      .then((response: { clusters?: ClusterRecord[] }) => {
        if (active) setClusters(response.clusters ?? [])
      })
      .catch((error: unknown) => {
        if (active) setClusterError(error)
      })
    void load(defaultFilters)
    return () => { active = false }
  }, [load])

  const trendBars = useMemo<TrendBar[]>(() => {
    const max = Math.max(1, ...trends.map((bucket) => bucket.total || 0))
    return trends.map((bucket) => {
      const total = bucket.total || 0
      return {
        ...bucket,
        total,
        critical: bucket.critical || 0,
        warning: bucket.warning || 0,
        resolved: bucket.resolved || 0,
        axisLabel: formatCalendarDate(bucket.date, language, 'short'),
        displayDate: formatCalendarDate(bucket.date, language, 'long'),
        totalPct: Math.round((total / max) * 100),
        criticalShare: total ? ((bucket.critical || 0) / total) * 100 : 0,
        warningShare: total ? ((bucket.warning || 0) / total) * 100 : 0,
      }
    })
  }, [language, trends])
  const hasTrendData = trendBars.some((bucket) => bucket.total > 0)
  const hoverInfo = useMemo(() => {
    if (hoverIndex >= 0 && trendBars[hoverIndex]) {
      const bucket = trendBars[hoverIndex]
      return t('alerts.trend.point', {
        date: bucket.displayDate,
        critical: bucket.critical,
        warning: bucket.warning,
        resolved: bucket.resolved,
      })
    }
    const newCount = trendBars.reduce((sum, bucket) => sum + bucket.total, 0)
    const resolvedCount = trendBars.reduce((sum, bucket) => sum + bucket.resolved, 0)
    return t('alerts.trend.total', { newCount, resolvedCount })
  }, [hoverIndex, t, trendBars])

  async function acknowledge(alert: AlertRecord) {
    const ok = await confirm({
      title: t('alerts.confirmations.acknowledgeTitle'),
      message: t('alerts.confirmations.acknowledgeMessage', { target: alertTarget(alert) }),
      confirmText: t('alerts.actions.acknowledge'),
    })
    if (!ok) return
    try {
      await acknowledgeAlert(alert.id, alert.cluster_id)
      notify.success(localization.current.operations('alerts.feedback.acknowledged'))
    } catch (error) {
      notify.error(currentApiError(error, 'errors.actionFailed'))
    }
    await load(filters)
  }

  async function resolve(alert: AlertRecord) {
    const ok = await confirm({
      title: t('alerts.confirmations.resolveTitle'),
      message: t('alerts.confirmations.resolveMessage', { target: alertTarget(alert) }),
      confirmText: t('alerts.actions.resolve'),
      tone: 'danger',
    })
    if (!ok) return
    try {
      await resolveAlert(alert.id, alert.cluster_id)
      notify.info(localization.current.operations('alerts.feedback.resolved'))
    } catch (error) {
      notify.error(currentApiError(error, 'errors.actionFailed'))
    }
    await load(filters)
  }

  async function diagnose(alert: AlertRecord) {
    setBusyId(alert.id)
    try {
      const response: { alert?: AlertRecord } = await diagnoseAlert(alert.id, alert.cluster_id)
      if (response.alert) {
        setAlerts((previous) => previous.map((item) => (
          item.id === alert.id ? response.alert ?? item : item
        )))
      }
      notify.success(localization.current.operations('alerts.feedback.diagnosisCompleted'))
    } catch (error) {
      notify.error(currentApiError(error, 'errors.actionFailed'))
    } finally {
      setBusyId(0)
    }
  }

  async function remediate(alert: AlertRecord) {
    const ok = await confirm({
      title: t('alerts.confirmations.remediationTitle'),
      message: t('alerts.confirmations.remediationMessage', { target: alertTarget(alert) }),
      confirmText: t('alerts.actions.createTicket'),
      tone: 'danger',
    })
    if (!ok) return
    setBusyId(alert.id)
    try {
      const response: {
        alert?: AlertRecord
        ticket?: { id?: number; action?: string; result?: string }
      } = await remediateAlert(alert.id, alert.cluster_id)
      if (response.alert) {
        setAlerts((previous) => previous.map((item) => (
          item.id === alert.id ? response.alert ?? item : item
        )))
      }
      notify.success(localization.current.operations('alerts.feedback.ticketCreated', {
        id: response.ticket?.id ?? '',
        action: actionLabel(response.ticket?.action),
      }))
      void refreshCounts()
    } catch (error) {
      notify.error(currentApiError(error, 'errors.createFailed'))
    } finally {
      setBusyId(0)
    }
  }

  // 告警静默
  const [silenceDrawer, setSilenceDrawer] = useState(false)
  const [silenceSaving, setSilenceSaving] = useState(false)
  const [silenceForm, setSilenceForm] = useState<SilenceForm>({
    cluster_id: 0,
    rule: '',
    namespace: '',
    name: '',
    duration_hours: 24,
    comment: '',
  })
  const [silenceListOpen, setSilenceListOpen] = useState(false)
  const [silences, setSilences] = useState<SilenceRecord[]>([])
  const [silenceLoading, setSilenceLoading] = useState(false)
  const [silenceError, setSilenceError] = useState<unknown>(null)

  function openSilence(alert: AlertRecord) {
    setSilenceForm({
      cluster_id: alert.cluster_id ?? 0,
      rule: alert.rule || '',
      namespace: alert.namespace || '',
      name: alert.name || '',
      duration_hours: 24,
      comment: '',
    })
    setSilenceDrawer(true)
  }

  function openBlankSilence() {
    setSilenceForm({
      cluster_id: clusters.length === 1 ? clusters[0].id : 0,
      rule: '',
      namespace: '',
      name: '',
      duration_hours: 24,
      comment: '',
    })
    setSilenceListOpen(false)
    setSilenceDrawer(true)
  }

  async function submitSilence() {
    if (!silenceForm.cluster_id) {
      notify.error(t('alerts.silences.validation.clusterRequired'))
      return
    }
    if (!silenceForm.rule && !silenceForm.namespace && !silenceForm.name) {
      notify.error(t('alerts.silences.validation.matcherRequired'))
      return
    }
    setSilenceSaving(true)
    try {
      await createSilence({
        ...silenceForm,
        rule: silenceForm.rule.trim(),
        namespace: silenceForm.namespace.trim(),
        name: silenceForm.name.trim(),
        comment: silenceForm.comment.trim(),
      })
      notify.success(localization.current.operations('alerts.feedback.silenceCreated'))
      setSilenceDrawer(false)
      await load(filters)
    } catch (error) {
      notify.error(currentApiError(error, 'errors.createFailed'))
    } finally {
      setSilenceSaving(false)
    }
  }

  const loadSilences = useCallback(async () => {
    setSilenceLoading(true)
    setSilenceError(null)
    try {
      const response: { silences?: SilenceRecord[] } = await listSilences()
      setSilences(response.silences ?? [])
    } catch (error) {
      setSilenceError(error)
    } finally {
      setSilenceLoading(false)
    }
  }, [])

  async function openSilenceList() {
    setSilenceListOpen(true)
    await loadSilences()
  }

  async function removeSilence(silence: SilenceRecord) {
    const ok = await confirm({
      title: t('alerts.confirmations.removeSilenceTitle'),
      message: t('alerts.confirmations.removeSilenceMessage'),
      confirmText: t('alerts.silences.remove'),
      tone: 'danger',
    })
    if (!ok) return
    try {
      await deleteSilence(silence.id)
      notify.info(localization.current.operations('alerts.feedback.silenceRemoved'))
      await Promise.all([loadSilences(), load(filters)])
    } catch (error) {
      notify.error(currentApiError(error, 'errors.deleteFailed'))
    }
  }

  function silenceScope(silence: SilenceRecord): string[] {
    const parts: string[] = []
    if (silence.rule) parts.push(t('alerts.silences.scope.rule', { value: silence.rule }))
    if (silence.namespace) {
      parts.push(t('alerts.silences.scope.namespace', { value: silence.namespace }))
    }
    if (silence.name) parts.push(t('alerts.silences.scope.object', { value: silence.name }))
    return parts
  }

  // 外部通知设置
  const [notifyDrawer, setNotifyDrawer] = useState(false)
  const [notifyLoading, setNotifyLoading] = useState(false)
  const [notifySaving, setNotifySaving] = useState(false)
  const [notifyTesting, setNotifyTesting] = useState(false)
  const [notifyStatus, setNotifyStatus] = useState<NotificationSettings | null>(null)
  const [notifyForm, setNotifyForm] = useState<NotificationForm>({
    enabled: false,
    min_severity: 'warning',
    notify_resolved: true,
    dingtalk_webhook: '',
    dingtalk_secret: '',
    wecom_webhook: '',
    clear_dingtalk: false,
    clear_dingtalk_secret: false,
    clear_wecom: false,
  })

  async function openNotifySettings() {
    setNotifyDrawer(true)
    setNotifyLoading(true)
    try {
      const settings: NotificationSettings = await getNotificationSettings()
      setNotifyStatus(settings)
      setNotifyForm({
        enabled: Boolean(settings.enabled),
        min_severity: settings.min_severity || 'warning',
        notify_resolved: Boolean(settings.notify_resolved),
        dingtalk_webhook: '',
        dingtalk_secret: '',
        wecom_webhook: '',
        clear_dingtalk: false,
        clear_dingtalk_secret: false,
        clear_wecom: false,
      })
    } catch (error) {
      notify.error(currentApiError(error, 'errors.loadFailed'))
      setNotifyDrawer(false)
    } finally {
      setNotifyLoading(false)
    }
  }

  async function submitNotifySettings() {
    setNotifySaving(true)
    try {
      const settings: NotificationSettings = await saveNotificationSettings({
        enabled: notifyForm.enabled,
        min_severity: notifyForm.min_severity,
        notify_resolved: notifyForm.notify_resolved,
        dingtalk_webhook: secretField(
          notifyForm.dingtalk_webhook,
          notifyForm.clear_dingtalk,
        ),
        dingtalk_secret: secretField(
          notifyForm.dingtalk_secret,
          notifyForm.clear_dingtalk_secret,
        ),
        wecom_webhook: secretField(notifyForm.wecom_webhook, notifyForm.clear_wecom),
      })
      setNotifyStatus(settings)
      setNotifyForm((previous) => ({
        ...previous,
        dingtalk_webhook: '',
        dingtalk_secret: '',
        wecom_webhook: '',
        clear_dingtalk: false,
        clear_dingtalk_secret: false,
        clear_wecom: false,
      }))
      notify.success(localization.current.operations('alerts.feedback.notificationSaved'))
    } catch (error) {
      notify.error(currentApiError(error, 'errors.saveFailed'))
    } finally {
      setNotifySaving(false)
    }
  }

  async function sendTest() {
    setNotifyTesting(true)
    try {
      const response: { ok?: boolean; channels?: Record<string, string> } = await testNotification()
      const detail = Object.entries(response.channels ?? {})
        .map(([channel, result]) => `${channelLabel(channel)}: ${result}`)
        .join(' · ')
      const translate = localization.current.operations
      if (response.ok) notify.success(translate('alerts.notifications.testSent', { detail }))
      else notify.error(translate('alerts.notifications.testPartial', { detail }))
    } catch (error) {
      notify.error(currentApiError(error, 'errors.actionFailed'))
    } finally {
      setNotifyTesting(false)
    }
  }

  const activeChannels = (notifyStatus?.active_channels ?? [])
    .map(channelLabel)
    .join(t('alerts.notifications.channelSeparator')) || t('alerts.notifications.none')

  return (
    <div className="px-8 py-7">
      <div className="mb-4 flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 className="text-xl font-bold tracking-tight">{t('alerts.title')}</h2>
          <p className="mt-1 text-sm text-muted-foreground">{t('alerts.subtitle')}</p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <span className="inline-flex items-center gap-1.5 rounded-full bg-danger-weak px-3 py-1 text-xs font-semibold text-danger-fg">
            <i className="size-2 rounded-full bg-danger" />
            {t('alerts.summary.metric', {
              label: localizeStatus(commonT, 'severity', 'critical'),
              count: summary.critical,
            })}
          </span>
          <span className="inline-flex items-center gap-1.5 rounded-full bg-warning-weak px-3 py-1 text-xs font-semibold text-warning-fg">
            <i className="size-2 rounded-full bg-warning" />
            {t('alerts.summary.metric', {
              label: localizeStatus(commonT, 'severity', 'warning'),
              count: summary.warning,
            })}
          </span>
          {summary.silenced > 0 && (
            <span className="inline-flex items-center gap-1.5 rounded-full bg-muted px-3 py-1 text-xs font-semibold text-muted-foreground">
              <i className="size-2 rounded-full bg-text-subtle" />
              {t('alerts.summary.silenced', { count: summary.silenced })}
            </span>
          )}
          <Button variant="outline" size="sm" onClick={openSilenceList}>
            <BellOff className="size-4" /> {t('alerts.actions.silenceManagement')}
          </Button>
          {can('notifications:manage') && (
            <Button variant="outline" size="sm" onClick={openNotifySettings}>
              <Bell className="size-4" /> {t('alerts.actions.notificationSettings')}
            </Button>
          )}
          <Button variant="outline" size="sm" onClick={() => load(filters)} disabled={loading}>
            <RotateCw className={cn('size-4', loading && 'animate-spin')} />
            {t('alerts.actions.refresh')}
          </Button>
        </div>
      </div>

      {loadError !== null && (
        <div className="mb-3 rounded-lg bg-danger-weak px-3 py-2 text-sm text-danger-fg">
          {localizeApiError(loadError, commonT, language, 'errors.loadFailed')}
        </div>
      )}
      {clusterError !== null && (
        <div className="mb-3 rounded-lg bg-danger-weak px-3 py-2 text-sm text-danger-fg">
          {localizeApiError(clusterError, commonT, language, 'errors.loadFailed')}
        </div>
      )}

      <div className="mb-4 rounded-xl border border-border bg-card p-[18px] shadow-sm">
        <div className="mb-3.5 flex flex-wrap items-center justify-between gap-3">
          <strong className="text-sm font-bold">{t('alerts.trend.title')}</strong>
          <div className="flex flex-wrap items-center gap-3.5 text-xs text-muted-foreground">
            <span className="inline-flex items-center gap-1.5">
              <i className="size-2.5 rounded-sm bg-danger" />
              {localizeStatus(commonT, 'severity', 'critical')}
            </span>
            <span className="inline-flex items-center gap-1.5">
              <i className="size-2.5 rounded-sm bg-warning" />
              {localizeStatus(commonT, 'severity', 'warning')}
            </span>
            <span className="font-semibold text-foreground">{hoverInfo}</span>
          </div>
        </div>
        {hasTrendData ? (
          <div className="flex h-[120px] items-end gap-1.5">
            {trendBars.map((bucket, index) => (
              <div
                key={bucket.date}
                className="flex h-full flex-1 flex-col items-center gap-1.5"
                onMouseEnter={() => setHoverIndex(index)}
                onMouseLeave={() => setHoverIndex(-1)}
              >
                <div className="flex min-h-0 w-full flex-1 flex-col items-center justify-end">
                  <div
                    className={cn(
                      'flex max-w-[32px] flex-col overflow-hidden rounded-t transition-all',
                      hoverIndex === index && 'brightness-110 ring-2 ring-primary/30',
                    )}
                    style={{ width: '60%', height: `${bucket.totalPct}%`, minHeight: 3 }}
                  >
                    <div className="w-full bg-warning" style={{ flexBasis: `${bucket.warningShare}%` }} />
                    <div className="w-full bg-danger" style={{ flexBasis: `${bucket.criticalShare}%` }} />
                  </div>
                </div>
                <time
                  className="font-mono text-[11px] text-text-subtle"
                  dateTime={bucket.date}
                  title={bucket.displayDate}
                >
                  {bucket.axisLabel}
                </time>
              </div>
            ))}
          </div>
        ) : (
          <div className="flex h-[120px] items-center justify-center text-sm text-muted-foreground">
            {t('alerts.trend.empty')}
          </div>
        )}
      </div>

      <div className="mb-4 grid grid-cols-1 gap-4 rounded-xl border border-border bg-card p-4 shadow-sm md:grid-cols-[repeat(3,minmax(150px,1fr))_auto] md:items-end">
        <div className="space-y-1.5">
          <Label className="text-xs text-muted-foreground">{t('alerts.filters.severity')}</Label>
          <Select
            value={filters.severity || ALL}
            onValueChange={(value) => setFilters((previous) => ({
              ...previous,
              severity: value === ALL ? '' : value,
            }))}
          >
            <SelectTrigger className="w-full"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value={ALL}>{t('alerts.filters.allSeverities')}</SelectItem>
              <SelectItem value="critical">{localizeStatus(commonT, 'severity', 'critical')}</SelectItem>
              <SelectItem value="warning">{localizeStatus(commonT, 'severity', 'warning')}</SelectItem>
            </SelectContent>
          </Select>
        </div>
        <div className="space-y-1.5">
          <Label className="text-xs text-muted-foreground">{t('alerts.filters.status')}</Label>
          <Select
            value={filters.status || ALL}
            onValueChange={(value) => setFilters((previous) => ({
              ...previous,
              status: value === ALL ? '' : value,
            }))}
          >
            <SelectTrigger className="w-full"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value={ALL}>{t('alerts.filters.allStatuses')}</SelectItem>
              <SelectItem value="active">{localizeStatus(commonT, 'alert', 'active')}</SelectItem>
              <SelectItem value="acknowledged">{localizeStatus(commonT, 'alert', 'acknowledged')}</SelectItem>
              <SelectItem value="resolved">{localizeStatus(commonT, 'alert', 'resolved')}</SelectItem>
            </SelectContent>
          </Select>
        </div>
        <div className="space-y-1.5">
          <Label className="text-xs text-muted-foreground">{t('alerts.filters.cluster')}</Label>
          <Select
            value={filters.clusterId || ALL}
            onValueChange={(value) => setFilters((previous) => ({
              ...previous,
              clusterId: value === ALL ? '' : value,
            }))}
          >
            <SelectTrigger className="w-full"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value={ALL}>{t('alerts.filters.allClusters')}</SelectItem>
              {clusters.map((cluster) => (
                <SelectItem key={cluster.id} value={String(cluster.id)}>{cluster.name}</SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
        <div className="flex gap-2">
          <Button onClick={() => load(filters)} disabled={loading}>{t('alerts.actions.query')}</Button>
          <Button
            variant="outline"
            onClick={() => {
              const resetFilters = { ...defaultFilters }
              setFilters(resetFilters)
              void load(resetFilters)
            }}
            disabled={loading}
          >
            {t('alerts.actions.reset')}
          </Button>
        </div>
      </div>

      {!alerts.length && !loading && (
        <div className="rounded-xl border border-dashed border-border py-14 text-center text-sm leading-8 text-muted-foreground">
          {t('alerts.empty.title')}<br />{t('alerts.empty.description')}
        </div>
      )}

      <div className="space-y-3">
        {alerts.map((alert) => (
          <div
            key={alert.id}
            className={cn(
              'rounded-xl border border-border bg-card p-4 shadow-sm border-l-[3px]',
              alert.severity === 'critical'
                ? 'border-l-danger'
                : alert.severity === 'warning' ? 'border-l-warning' : 'border-l-border-strong',
              alert.silenced && 'opacity-60',
            )}
          >
            <div className="flex flex-wrap items-center gap-3">
              <span className={cn(
                'rounded-full px-2.5 py-0.5 text-xs font-bold',
                alert.severity === 'critical'
                  ? 'bg-danger-weak text-danger-fg'
                  : 'bg-warning-weak text-warning-fg',
              )}>
                {localizeStatus(commonT, 'severity', alert.severity)}
              </span>
              <span className="text-sm font-semibold">
                {alert.kind} · {alert.namespace ? `${alert.namespace}/` : ''}{alert.name}
              </span>
              <span className="rounded-md border border-border bg-muted px-2 py-0.5 font-mono text-xs text-muted-foreground">
                {alert.rule}
              </span>
              {alert.silenced && (
                <span className="inline-flex items-center gap-1 rounded-full border border-border bg-muted px-2.5 py-0.5 text-xs font-medium text-muted-foreground">
                  <BellOff className="size-3" /> {t('alerts.item.silenced')}
                </span>
              )}
              <span className={cn(
                'ml-auto rounded-full px-2.5 py-0.5 text-xs font-medium',
                tonePill[statusTone(alert.status)],
              )}>
                {localizeStatus(commonT, 'alert', alert.status)}
              </span>
            </div>
            <div className="my-2 text-[13.5px] leading-relaxed">{alert.message}</div>
            <div className="flex flex-wrap gap-x-4 gap-y-2 text-xs text-muted-foreground">
              <span>{t('alerts.item.cluster', {
                name: alert.cluster_name
                  || (alert.cluster_id
                    ? t('alerts.item.clusterFallback', { id: alert.cluster_id })
                    : commonT('common.notAvailable')),
              })}</span>
              <span>{t('alerts.item.firstSeen', {
                time: formatDateTime(alert.first_seen_at, language),
              })}</span>
              <span>{t('alerts.item.lastSeen', {
                time: formatDateTime(alert.last_seen_at, language),
              })}</span>
              <span>{t('alerts.item.occurrences', { count: alert.count })}</span>
              {alert.acknowledged_by && (
                <span>{t('alerts.item.acknowledgedBy', { name: alert.acknowledged_by })}</span>
              )}
              {alert.resolved_by && (
                <span>{t('alerts.item.resolvedBy', { name: alert.resolved_by })}</span>
              )}
            </div>
            {alert.diagnosis && (
              <div className="mt-2.5 rounded-lg border border-primary/20 bg-primary/5 p-3">
                <div className="mb-1.5 flex items-center gap-1.5 text-xs font-bold text-primary">
                  <Sparkles className="size-3.5" /> {t('alerts.item.aiDiagnosis')}
                  {hasSuggested(alert) && (
                    <span className="ml-2 inline-flex items-center gap-1 rounded-full bg-warning-weak px-2 py-0.5 text-[11px] font-bold text-warning-fg">
                      <Zap className="size-3" />
                      {t('alerts.item.suggestedAction', {
                        action: actionLabel(alert.suggested_action),
                      })}
                    </span>
                  )}
                </div>
                <div className="whitespace-pre-wrap text-[13px] leading-relaxed">{alert.diagnosis}</div>
              </div>
            )}
            <div className="mt-3 flex flex-wrap items-center gap-2">
              <Button
                variant="outline"
                size="sm"
                className="border-primary/40 text-primary hover:bg-primary/5"
                disabled={busyId === alert.id}
                onClick={() => diagnose(alert)}
              >
                <Sparkles className="size-4" />
                {busyId === alert.id
                  ? t('alerts.actions.diagnosing')
                  : alert.diagnosis ? t('alerts.actions.diagnoseAgain') : t('alerts.actions.diagnose')}
              </Button>
              {isFixable(alert) && !alert.remediation_ticket_id && (
                <Button
                  size="sm"
                  className="bg-warning text-white hover:bg-warning/90"
                  disabled={busyId === alert.id}
                  onClick={() => remediate(alert)}
                >
                  {t('alerts.actions.createTicket')}
                </Button>
              )}
              {alert.remediation_ticket_id && (
                <Button variant="outline" size="sm" onClick={() => navigate('/approval')}>
                  {t('alerts.actions.ticketReview', { id: alert.remediation_ticket_id })}
                </Button>
              )}
              <span className="flex-1" />
              {can('alerts:manage') && alert.status !== 'resolved' && !alert.silenced && (
                <Button variant="ghost" size="sm" onClick={() => openSilence(alert)}>
                  <BellOff className="size-4" /> {t('alerts.actions.silence')}
                </Button>
              )}
              {alert.status === 'active' && (
                <Button
                  variant="outline"
                  size="sm"
                  className="border-primary/40 text-primary hover:bg-primary/5"
                  onClick={() => acknowledge(alert)}
                >
                  {t('alerts.actions.acknowledge')}
                </Button>
              )}
              {alert.status !== 'resolved' && (
                <Button
                  size="sm"
                  className="bg-success text-white hover:bg-success/90"
                  onClick={() => resolve(alert)}
                >
                  {t('alerts.actions.resolve')}
                </Button>
              )}
            </div>
          </div>
        ))}
      </div>

      <Sheet open={silenceDrawer} onOpenChange={setSilenceDrawer}>
        <SheetContent className="flex w-full flex-col gap-0 sm:max-w-md">
          <SheetHeader>
            <SheetTitle>{t('alerts.silences.createTitle')}</SheetTitle>
            <SheetDescription>{t('alerts.silences.createDescription')}</SheetDescription>
          </SheetHeader>
          <div className="flex-1 space-y-4 overflow-y-auto px-4 py-2">
            <div className="space-y-1.5">
              <Label htmlFor="silence-cluster">{t('alerts.silences.targetCluster')}</Label>
              <Select
                value={silenceForm.cluster_id ? String(silenceForm.cluster_id) : ''}
                onValueChange={(value) => setSilenceForm((previous) => ({
                  ...previous,
                  cluster_id: Number(value),
                }))}
              >
                <SelectTrigger id="silence-cluster" className="w-full">
                  <SelectValue placeholder={t('alerts.silences.selectCluster')} />
                </SelectTrigger>
                <SelectContent>
                  {clusters.map((cluster) => (
                    <SelectItem key={cluster.id} value={String(cluster.id)}>{cluster.name}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="silence-rule">{t('alerts.silences.rule')}</Label>
              <Input
                id="silence-rule"
                value={silenceForm.rule}
                onChange={(event) => setSilenceForm((previous) => ({
                  ...previous,
                  rule: event.target.value,
                }))}
                placeholder={t('alerts.silences.rulePlaceholder')}
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="silence-namespace">{t('alerts.silences.namespace')}</Label>
              <Input
                id="silence-namespace"
                value={silenceForm.namespace}
                onChange={(event) => setSilenceForm((previous) => ({
                  ...previous,
                  namespace: event.target.value,
                }))}
                placeholder={t('alerts.silences.namespacePlaceholder')}
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="silence-object">{t('alerts.silences.objectName')}</Label>
              <Input
                id="silence-object"
                value={silenceForm.name}
                onChange={(event) => setSilenceForm((previous) => ({
                  ...previous,
                  name: event.target.value,
                }))}
                placeholder={t('alerts.silences.objectPlaceholder')}
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="silence-duration">{t('alerts.silences.duration')}</Label>
              <Select
                value={String(silenceForm.duration_hours)}
                onValueChange={(value) => setSilenceForm((previous) => ({
                  ...previous,
                  duration_hours: Number(value),
                }))}
              >
                <SelectTrigger id="silence-duration" className="w-full"><SelectValue /></SelectTrigger>
                <SelectContent>
                  {durationOptions.map((duration) => (
                    <SelectItem key={duration.value} value={String(duration.value)}>
                      {t(duration.key)}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="silence-comment">{t('alerts.silences.comment')}</Label>
              <Input
                id="silence-comment"
                value={silenceForm.comment}
                onChange={(event) => setSilenceForm((previous) => ({
                  ...previous,
                  comment: event.target.value,
                }))}
                placeholder={t('alerts.silences.commentPlaceholder')}
              />
            </div>
          </div>
          <SheetFooter className="flex-row justify-end gap-2">
            <Button variant="outline" onClick={() => setSilenceDrawer(false)}>
              {t('alerts.silences.cancel')}
            </Button>
            <Button onClick={submitSilence} disabled={silenceSaving}>
              {silenceSaving ? t('alerts.silences.creating') : t('alerts.silences.create')}
            </Button>
          </SheetFooter>
        </SheetContent>
      </Sheet>

      <Sheet open={silenceListOpen} onOpenChange={setSilenceListOpen}>
        <SheetContent className="flex w-full flex-col gap-0 sm:max-w-md">
          <SheetHeader>
            <SheetTitle>{t('alerts.silences.managementTitle')}</SheetTitle>
            <SheetDescription>{t('alerts.silences.managementDescription')}</SheetDescription>
          </SheetHeader>
          <div className="flex-1 space-y-2 overflow-y-auto px-4 py-2">
            {silenceError !== null && (
              <div className="rounded-lg bg-danger-weak px-3 py-2 text-sm text-danger-fg">
                {localizeApiError(silenceError, commonT, language, 'errors.loadFailed')}
              </div>
            )}
            {silenceLoading && (
              <div className="py-6 text-center text-sm text-muted-foreground">
                {t('alerts.silences.loading')}
              </div>
            )}
            {!silenceLoading && !silences.length && silenceError === null && (
              <div className="py-6 text-center text-sm text-muted-foreground">
                {t('alerts.silences.empty')}
              </div>
            )}
            {silences.map((silence) => {
              const scope = silenceScope(silence)
              return (
                <div key={silence.id} className="flex items-start gap-3 border-b border-border py-3 last:border-none">
                  <div className="min-w-0 flex-1">
                    <div className="flex flex-wrap gap-x-2 break-all text-[13.5px] font-semibold">
                      {scope.length
                        ? scope.map((part) => <span key={part}>{part}</span>)
                        : commonT('common.notAvailable')}
                    </div>
                    <div className="mt-1 flex flex-wrap gap-x-3.5 gap-y-1 text-xs text-muted-foreground">
                      <span>{silence.cluster_name || t('alerts.silences.clusterFallback', {
                        id: silence.cluster_id,
                      })}</span>
                      {silence.created_by && <span>{silence.created_by}</span>}
                      <span title={formatDateTime(silence.expires_at, language)}>
                        {formatRelativeTime(silence.expires_at, language)}
                      </span>
                    </div>
                    {silence.comment && (
                      <div className="mt-1 text-xs text-text-subtle">{silence.comment}</div>
                    )}
                  </div>
                  {can('alerts:manage') && (
                    <Button
                      variant="ghost"
                      size="sm"
                      className="h-7 px-2 text-xs text-destructive hover:text-destructive"
                      onClick={() => removeSilence(silence)}
                    >
                      {t('alerts.silences.remove')}
                    </Button>
                  )}
                </div>
              )
            })}
          </div>
          <SheetFooter className="flex-row justify-end gap-2">
            <Button variant="outline" onClick={loadSilences} disabled={silenceLoading}>
              <RotateCw className={cn('size-4', silenceLoading && 'animate-spin')} />
              {t('alerts.silences.refresh')}
            </Button>
            {can('alerts:manage') && (
              <Button variant="outline" onClick={openBlankSilence}>
                <BellOff className="size-4" /> {t('alerts.silences.new')}
              </Button>
            )}
            <Button onClick={() => setSilenceListOpen(false)}>{t('alerts.silences.done')}</Button>
          </SheetFooter>
        </SheetContent>
      </Sheet>

      <Sheet open={notifyDrawer} onOpenChange={setNotifyDrawer}>
        <SheetContent className="flex w-full flex-col gap-0 sm:max-w-md">
          <SheetHeader>
            <SheetTitle>{t('alerts.notifications.title')}</SheetTitle>
            <SheetDescription>{t('alerts.notifications.description')}</SheetDescription>
          </SheetHeader>
          {notifyLoading ? (
            <div className="flex-1 py-8 text-center text-sm text-muted-foreground">
              {t('alerts.notifications.loading')}
            </div>
          ) : (
            <div className="flex-1 space-y-4 overflow-y-auto px-4 py-2">
              <div className="rounded-lg bg-muted/50 px-3 py-2 text-xs text-muted-foreground">
                <b className="text-foreground">
                  {t('alerts.notifications.currentChannels', { channels: activeChannels })}
                </b>
                {notifyStatus?.source && (
                  <span className="ml-1">
                    {t('alerts.notifications.fromSource', {
                      source: localizeStatus(commonT, 'source', notifyStatus.source),
                    })}
                  </span>
                )}
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="notification-enabled">{t('alerts.notifications.enabled')}</Label>
                <Select
                  value={String(notifyForm.enabled)}
                  onValueChange={(value) => setNotifyForm((previous) => ({
                    ...previous,
                    enabled: value === 'true',
                  }))}
                >
                  <SelectTrigger id="notification-enabled" className="w-full"><SelectValue /></SelectTrigger>
                  <SelectContent>
                    <SelectItem value="true">{t('alerts.notifications.enabledOption')}</SelectItem>
                    <SelectItem value="false">{t('alerts.notifications.disabledOption')}</SelectItem>
                  </SelectContent>
                </Select>
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="notification-severity">{t('alerts.notifications.minimumSeverity')}</Label>
                <Select
                  value={notifyForm.min_severity}
                  onValueChange={(value) => setNotifyForm((previous) => ({
                    ...previous,
                    min_severity: value,
                  }))}
                >
                  <SelectTrigger id="notification-severity" className="w-full"><SelectValue /></SelectTrigger>
                  <SelectContent>
                    <SelectItem value="warning">
                      {t('alerts.notifications.warningAndAbove', {
                        severity: localizeStatus(commonT, 'severity', 'warning'),
                      })}
                    </SelectItem>
                    <SelectItem value="critical">
                      {t('alerts.notifications.criticalOnly', {
                        severity: localizeStatus(commonT, 'severity', 'critical'),
                      })}
                    </SelectItem>
                  </SelectContent>
                </Select>
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="notification-resolved">
                  {t('alerts.notifications.resolvedNotifications')}
                </Label>
                <Select
                  value={String(notifyForm.notify_resolved)}
                  onValueChange={(value) => setNotifyForm((previous) => ({
                    ...previous,
                    notify_resolved: value === 'true',
                  }))}
                >
                  <SelectTrigger id="notification-resolved" className="w-full"><SelectValue /></SelectTrigger>
                  <SelectContent>
                    <SelectItem value="true">{t('alerts.notifications.sendResolved')}</SelectItem>
                    <SelectItem value="false">{t('alerts.notifications.skipResolved')}</SelectItem>
                  </SelectContent>
                </Select>
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="dingtalk-webhook">{t('alerts.notifications.dingtalkWebhook')}</Label>
                <Input
                  id="dingtalk-webhook"
                  value={notifyForm.dingtalk_webhook}
                  onChange={(event) => setNotifyForm((previous) => ({
                    ...previous,
                    dingtalk_webhook: event.target.value,
                  }))}
                  placeholder={notifyStatus?.dingtalk_webhook_configured
                    ? t('alerts.notifications.configuredHint', {
                      hint: notifyStatus.dingtalk_webhook_hint ?? '',
                    })
                    : t('alerts.notifications.dingtalkWebhookPlaceholder')}
                />
                {notifyStatus?.dingtalk_webhook_configured && (
                  <Label className="flex cursor-pointer items-center gap-2 text-xs text-muted-foreground">
                    <Checkbox
                      checked={notifyForm.clear_dingtalk}
                      onCheckedChange={(checked) => setNotifyForm((previous) => ({
                        ...previous,
                        clear_dingtalk: Boolean(checked),
                      }))}
                    />
                    {t('alerts.notifications.clearDingtalkWebhook')}
                  </Label>
                )}
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="dingtalk-secret">{t('alerts.notifications.dingtalkSecret')}</Label>
                <Input
                  id="dingtalk-secret"
                  value={notifyForm.dingtalk_secret}
                  onChange={(event) => setNotifyForm((previous) => ({
                    ...previous,
                    dingtalk_secret: event.target.value,
                  }))}
                  placeholder={notifyStatus?.dingtalk_secret_configured
                    ? t('alerts.notifications.configuredKeep')
                    : t('alerts.notifications.dingtalkSecretPlaceholder')}
                />
                {notifyStatus?.dingtalk_secret_configured && (
                  <Label className="flex cursor-pointer items-center gap-2 text-xs text-muted-foreground">
                    <Checkbox
                      checked={notifyForm.clear_dingtalk_secret}
                      onCheckedChange={(checked) => setNotifyForm((previous) => ({
                        ...previous,
                        clear_dingtalk_secret: Boolean(checked),
                      }))}
                    />
                    {t('alerts.notifications.clearDingtalkSecret')}
                  </Label>
                )}
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="wecom-webhook">{t('alerts.notifications.wecomWebhook')}</Label>
                <Input
                  id="wecom-webhook"
                  value={notifyForm.wecom_webhook}
                  onChange={(event) => setNotifyForm((previous) => ({
                    ...previous,
                    wecom_webhook: event.target.value,
                  }))}
                  placeholder={notifyStatus?.wecom_webhook_configured
                    ? t('alerts.notifications.configuredHint', {
                      hint: notifyStatus.wecom_webhook_hint ?? '',
                    })
                    : t('alerts.notifications.wecomWebhookPlaceholder')}
                />
                {notifyStatus?.wecom_webhook_configured && (
                  <Label className="flex cursor-pointer items-center gap-2 text-xs text-muted-foreground">
                    <Checkbox
                      checked={notifyForm.clear_wecom}
                      onCheckedChange={(checked) => setNotifyForm((previous) => ({
                        ...previous,
                        clear_wecom: Boolean(checked),
                      }))}
                    />
                    {t('alerts.notifications.clearWecomWebhook')}
                  </Label>
                )}
              </div>
            </div>
          )}
          <SheetFooter className="flex-row justify-end gap-2">
            <Button variant="outline" onClick={sendTest} disabled={notifyTesting || notifyLoading}>
              {notifyTesting ? t('alerts.notifications.sending') : t('alerts.notifications.sendTest')}
            </Button>
            <Button onClick={submitNotifySettings} disabled={notifySaving || notifyLoading}>
              {notifySaving ? t('alerts.notifications.saving') : t('alerts.notifications.save')}
            </Button>
          </SheetFooter>
        </SheetContent>
      </Sheet>
    </div>
  )
}
