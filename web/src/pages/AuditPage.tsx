import { useCallback, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { RotateCw } from 'lucide-react'
import { listAudits, listClusters, listUsers } from '@/lib/api'
import { normalizeLanguage } from '@/i18n'
import { localizeApiError } from '@/i18n/errors'
import { formatDateTime } from '@/i18n/format'
import { localizeStatus, type StatusCategory } from '@/i18n/status'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import {
  Select, SelectTrigger, SelectValue, SelectContent, SelectItem,
} from '@/components/ui/select'
import {
  Table, TableHeader, TableBody, TableRow, TableHead, TableCell,
} from '@/components/ui/table'

const ALL = '__all__'

interface AuditRecord {
  id: string | number
  cluster_id?: number
  cluster_name?: string
  operator?: string
  operator_display_name?: string
  action?: string
  target?: string
  result?: string
  detail?: string
  created_at?: string
}

interface ClusterOption {
  id: number
  name: string
}

interface UserOption {
  username: string
  displayName?: string
}

interface OperatorOption {
  value: string
  displayName?: string
  historical: boolean
}

interface AuditFilters {
  clusterId: string
  operator: string
  action: string
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null
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

function parseAudit(value: Record<string, unknown>): AuditRecord | undefined {
  const id = value.id
  if (typeof id !== 'string' && typeof id !== 'number') return undefined
  return {
    id,
    cluster_id: optionalNumber(value.cluster_id),
    cluster_name: optionalString(value.cluster_name),
    operator: optionalString(value.operator),
    operator_display_name: optionalString(value.operator_display_name),
    action: optionalString(value.action),
    target: optionalString(value.target),
    result: optionalString(value.result),
    detail: optionalString(value.detail),
    created_at: optionalString(value.created_at),
  }
}

function parseCluster(value: Record<string, unknown>): ClusterOption | undefined {
  const id = optionalNumber(value.id)
  const name = optionalString(value.name)
  return id !== undefined && name ? { id, name } : undefined
}

function parseUser(value: Record<string, unknown>): UserOption | undefined {
  const username = optionalString(value.username)
  return username ? { username, displayName: optionalString(value.display_name) } : undefined
}

function resultTone(result?: string): 'success' | 'error' | 'muted' {
  const v = String(result || '').toLowerCase()
  if (!v) return 'muted'
  if (['failed', 'error', 'rejected'].includes(v)) return 'error'
  if (['success', 'approved', 'executed', 'acknowledged', 'resolved'].includes(v)) return 'success'
  return 'muted'
}

function resultStatusCategory(result?: string): StatusCategory {
  const value = String(result || '').toLowerCase()
  if (['approved', 'rejected', 'executed'].includes(value)) return 'approval'
  if (['active', 'acknowledged', 'resolved'].includes(value)) return 'alert'
  return 'execution'
}

export function AuditPage() {
  const { t, i18n } = useTranslation('operations')
  const { t: commonT } = useTranslation('common')
  const language = normalizeLanguage(i18n.resolvedLanguage)
  const [audits, setAudits] = useState<AuditRecord[]>([])
  const [allAudits, setAllAudits] = useState<AuditRecord[]>([])
  const [clusters, setClusters] = useState<ClusterOption[]>([])
  const [users, setUsers] = useState<UserOption[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const [lastRefresh, setLastRefresh] = useState<number | null>(null)
  const [filters, setFilters] = useState<AuditFilters>({ clusterId: '', operator: '', action: '' })

  const operatorOptions = useMemo(() => {
    const options = new Map<string, OperatorOption>()
    for (const user of users) {
      options.set(user.username, {
        value: user.username,
        displayName: user.displayName,
        historical: false,
      })
      if (user.displayName) {
        options.set(user.displayName, {
          value: user.displayName,
          displayName: user.displayName,
          historical: false,
        })
      }
    }
    for (const audit of allAudits) {
      const operator = audit.operator?.trim()
      if (operator && !options.has(operator)) {
        options.set(operator, {
          value: operator,
          displayName: audit.operator_display_name?.trim(),
          historical: true,
        })
      }
    }
    return Array.from(options.values())
  }, [users, allAudits])

  const actionOptions = useMemo(() => {
    const values = new Set<string>()
    allAudits.forEach((audit) => { if (audit.action) values.add(audit.action) })
    return Array.from(values).sort()
  }, [allAudits])

  const load = useCallback(async (activeFilters: AuditFilters = { clusterId: '', operator: '', action: '' }) => {
    setLoading(true); setError(null)
    try {
      const [clusterPayload, userPayload, auditPayload] = await Promise.all([
        listClusters(), listUsers(), listAudits(),
      ])
      setClusters(recordList(clusterPayload, 'clusters').flatMap((value) => {
        const parsed = parseCluster(value)
        return parsed ? [parsed] : []
      }))
      setUsers(recordList(userPayload, 'users').flatMap((value) => {
        const parsed = parseUser(value)
        return parsed ? [parsed] : []
      }))
      setAllAudits(recordList(auditPayload, 'audits').flatMap((value) => {
        const parsed = parseAudit(value)
        return parsed ? [parsed] : []
      }))
      const data = await listAudits(
        activeFilters.clusterId ? Number(activeFilters.clusterId) : undefined,
        { operator: activeFilters.operator, action: activeFilters.action },
      )
      setAudits(recordList(data, 'audits').flatMap((value) => {
        const parsed = parseAudit(value)
        return parsed ? [parsed] : []
      }))
      setLastRefresh(Date.now())
    } catch (loadError) {
      setError(loadError)
    } finally { setLoading(false) }
  }, [])

  useEffect(() => { load() }, [load])

  function reset() { const emptyFilters = { clusterId: '', operator: '', action: '' }; setFilters(emptyFilters); load(emptyFilters) }

  function operatorLabel(option: OperatorOption) {
    if (!option.displayName || option.displayName === option.value) return option.value
    return option.historical
      ? t('audit.historicalOption', { displayName: option.displayName, value: option.value })
      : t('audit.userOption', { displayName: option.displayName, value: option.value })
  }

  return (
    <div className="px-8 py-7">
      <div className="mb-5 flex items-end justify-between">
        <div>
          <h2 className="text-xl font-bold tracking-tight">{t('audit.title')}</h2>
          <p className="mt-1 text-sm text-muted-foreground">{t('audit.subtitle')}</p>
        </div>
        <div className="flex items-center gap-3 text-xs text-muted-foreground">
          {lastRefresh && <span>{t('audit.lastRefresh', { time: formatDateTime(lastRefresh, language) })}</span>}
          <Button variant="outline" size="sm" onClick={() => load(filters)} disabled={loading}><RotateCw className={cn('size-4', loading && 'animate-spin')} /> {t('audit.refresh')}</Button>
        </div>
      </div>

      {/* 过滤条 */}
      <div className="mb-4 grid grid-cols-1 gap-4 rounded-xl border border-border bg-card p-4 shadow-sm md:grid-cols-[repeat(3,minmax(180px,1fr))_auto] md:items-end">
        <div className="space-y-1.5">
          <Label className="text-xs text-muted-foreground">{t('audit.filters.cluster')}</Label>
          <Select value={filters.clusterId || ALL} onValueChange={(v) => setFilters((f) => ({ ...f, clusterId: v === ALL ? '' : v }))}>
            <SelectTrigger className="w-full"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value={ALL}>{t('audit.filters.allClusters')}</SelectItem>
              {clusters.map((cluster) => <SelectItem key={cluster.id} value={String(cluster.id)}>{cluster.name}</SelectItem>)}
            </SelectContent>
          </Select>
        </div>
        <div className="space-y-1.5">
          <Label className="text-xs text-muted-foreground">{t('audit.filters.operator')}</Label>
          <Select value={filters.operator || ALL} onValueChange={(v) => setFilters((f) => ({ ...f, operator: v === ALL ? '' : v }))}>
            <SelectTrigger className="w-full"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value={ALL}>{t('audit.filters.allOperators')}</SelectItem>
              {operatorOptions.map((option) => <SelectItem key={option.value} value={option.value}>{operatorLabel(option)}</SelectItem>)}
            </SelectContent>
          </Select>
        </div>
        <div className="space-y-1.5">
          <Label className="text-xs text-muted-foreground">{t('audit.filters.action')}</Label>
          <Select value={filters.action || ALL} onValueChange={(v) => setFilters((f) => ({ ...f, action: v === ALL ? '' : v }))}>
            <SelectTrigger className="w-full"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value={ALL}>{t('audit.filters.allActions')}</SelectItem>
              {actionOptions.map((action) => <SelectItem key={action} value={action}>{action}</SelectItem>)}
            </SelectContent>
          </Select>
        </div>
        <div className="flex gap-2">
          <Button onClick={() => load(filters)} disabled={loading}>{commonT('actions.query')}</Button>
          <Button variant="outline" onClick={reset} disabled={loading}>{commonT('actions.reset')}</Button>
        </div>
      </div>

      {error !== null && <div className="mb-4 rounded-lg bg-danger-weak px-3 py-2 text-sm text-danger-fg">
        {localizeApiError(error, commonT, language, 'errors.loadFailed')}
      </div>}

      <div className="overflow-hidden rounded-xl border border-border bg-card shadow-sm">
        <div className="overflow-x-auto">
          <Table className="min-w-[1100px]">
            <TableHeader>
              <TableRow className="hover:bg-transparent">
                <TableHead>{t('audit.columns.time')}</TableHead><TableHead>{t('audit.columns.cluster')}</TableHead><TableHead>{t('audit.columns.operator')}</TableHead>
                <TableHead>{t('audit.columns.action')}</TableHead><TableHead>{t('audit.columns.target')}</TableHead><TableHead>{t('audit.columns.result')}</TableHead><TableHead>{t('audit.columns.detail')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {!audits.length && <TableRow><TableCell colSpan={7} className="py-10 text-center text-sm text-muted-foreground">{t('audit.empty')}</TableCell></TableRow>}
              {audits.map((audit) => {
                const tone = resultTone(audit.result)
                return (
                  <TableRow key={audit.id}>
                    <TableCell className="whitespace-nowrap text-xs text-muted-foreground">{formatDateTime(audit.created_at, language)}</TableCell>
                    <TableCell>{audit.cluster_name || (audit.cluster_id ? t('audit.clusterFallback', { id: audit.cluster_id }) : '—')}</TableCell>
                    <TableCell>{audit.operator_display_name || audit.operator || '—'}</TableCell>
                    <TableCell className="font-mono text-xs">{audit.action || '—'}</TableCell>
                    <TableCell className="font-mono text-xs">{audit.target || '—'}</TableCell>
                    <TableCell>
                      <span className={cn('inline-block rounded-full px-2.5 py-0.5 text-xs font-semibold',
                        tone === 'success' ? 'bg-success-weak text-success-fg' : tone === 'error' ? 'bg-danger-weak text-danger-fg' : 'bg-muted text-muted-foreground')}>
                        {localizeStatus(commonT, resultStatusCategory(audit.result), audit.result)}
                      </span>
                    </TableCell>
                    <TableCell className="max-w-[320px] text-xs text-muted-foreground">{audit.detail || '—'}</TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        </div>
      </div>
    </div>
  )
}
