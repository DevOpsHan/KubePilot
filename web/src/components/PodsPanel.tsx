import { useCallback, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { RotateCw, ScrollText, TerminalSquare } from 'lucide-react'
import { listPods } from '@/lib/api'
import { useAuth } from '@/providers/AuthProvider'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Table, TableHeader, TableBody, TableRow, TableHead, TableCell } from '@/components/ui/table'
import { Sheet, SheetContent, SheetHeader, SheetTitle, SheetDescription } from '@/components/ui/sheet'
import { PodLogs } from '@/components/PodLogs'
import { PodTerminal } from '@/components/PodTerminal'

// Pod 精简信息,对齐后端 collector.PodInfo
interface PodInfo {
  namespace: string
  name: string
  phase: string
  ready: string
  restarts: number
  node: string
  reason?: string
}

// 抽屉承载的交互目标:查看日志 or 进入终端
type Drawer =
  | { mode: 'logs'; pod: PodInfo }
  | { mode: 'exec'; pod: PodInfo }
  | null

function phaseTone(phase: string, reason?: string): 'success' | 'warning' | 'error' | 'muted' {
  if (reason) return 'error'
  switch (phase) {
    case 'Running':
    case 'Succeeded':
      return 'success'
    case 'Pending':
      return 'warning'
    case 'Failed':
      return 'error'
    default:
      return 'muted'
  }
}

function StatusPill({ tone, children }: { tone: 'success' | 'warning' | 'error' | 'muted'; children: React.ReactNode }) {
  const map = {
    success: 'bg-success-weak text-success-fg',
    warning: 'bg-warning-weak text-warning-fg',
    error: 'bg-danger-weak text-danger-fg',
    muted: 'bg-muted text-muted-foreground',
  }
  return <span className={cn('inline-block rounded-full px-2 py-0.5 text-[11px] font-medium', map[tone])}>{children}</span>
}

export function PodsPanel({ clusterId }: { clusterId?: number }) {
  const { t } = useTranslation('clusters')
  const { can } = useAuth()
  const canExec = can('clusters:exec')

  const [pods, setPods] = useState<PodInfo[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [nsFilter, setNsFilter] = useState('')
  const [nameFilter, setNameFilter] = useState('')
  const [drawer, setDrawer] = useState<Drawer>(null)

  const load = useCallback(async () => {
    if (!clusterId) return
    setLoading(true)
    setError(null)
    try {
      const result = await listPods(clusterId)
      setPods(result.pods || [])
    } catch (e) {
      const err = e as { response?: { data?: { error?: string } }; message?: string }
      setError(err.response?.data?.error || err.message || t('pods.loadFailed'))
    } finally {
      setLoading(false)
    }
  }, [clusterId, t])

  useEffect(() => { load() }, [load])

  const filtered = useMemo(() => {
    const ns = nsFilter.trim().toLowerCase()
    const name = nameFilter.trim().toLowerCase()
    return pods.filter((p) =>
      (!ns || p.namespace.toLowerCase().includes(ns)) &&
      (!name || p.name.toLowerCase().includes(name)))
  }, [pods, nsFilter, nameFilter])

  return (
    <div className="space-y-4">
      {/* 工具条:命名空间 / 名称过滤 + 刷新 */}
      <div className="flex flex-wrap items-center gap-2">
        <Input
          value={nsFilter}
          onChange={(e) => setNsFilter(e.target.value)}
          placeholder={t('pods.filterNamespace')}
          className="h-9 w-48"
        />
        <Input
          value={nameFilter}
          onChange={(e) => setNameFilter(e.target.value)}
          placeholder={t('pods.filterName')}
          className="h-9 w-56"
        />
        <span className="text-xs text-muted-foreground">{t('pods.count', { count: filtered.length })}</span>
        <Button variant="outline" size="sm" className="ml-auto" onClick={load} disabled={loading}>
          <RotateCw className={cn('size-4', loading && 'animate-spin')} /> {t('pods.refresh')}
        </Button>
      </div>

      {error && <div className="rounded-lg bg-danger-weak px-3 py-2 text-sm text-danger-fg">{error}</div>}

      <div className="overflow-hidden rounded-xl border border-border bg-card shadow-sm">
        <div className="overflow-x-auto">
          <Table className="min-w-[900px]">
            <TableHeader>
              <TableRow className="hover:bg-transparent">
                {[
                  t('pods.columns.namespace'), t('pods.columns.name'), t('pods.columns.phase'),
                  t('pods.columns.ready'), t('pods.columns.restarts'), t('pods.columns.node'),
                  t('pods.columns.actions'),
                ].map((h) => <TableHead key={h}>{h}</TableHead>)}
              </TableRow>
            </TableHeader>
            <TableBody>
              {!filtered.length && (
                <TableRow>
                  <TableCell colSpan={7} className="py-8 text-center text-sm text-muted-foreground">
                    {loading ? t('pods.loading') : t('pods.empty')}
                  </TableCell>
                </TableRow>
              )}
              {filtered.map((pod) => (
                <TableRow key={`${pod.namespace}/${pod.name}`}>
                  <TableCell className="text-xs text-muted-foreground">{pod.namespace}</TableCell>
                  <TableCell className="font-mono text-xs">{pod.name}</TableCell>
                  <TableCell>
                    <StatusPill tone={phaseTone(pod.phase, pod.reason)}>{pod.reason || pod.phase || '-'}</StatusPill>
                  </TableCell>
                  <TableCell className="text-xs">{pod.ready || '-'}</TableCell>
                  <TableCell className="text-xs">{pod.restarts ?? 0}</TableCell>
                  <TableCell className="font-mono text-xs text-muted-foreground">{pod.node || '-'}</TableCell>
                  <TableCell>
                    <div className="flex items-center gap-1.5">
                      <Button variant="ghost" size="sm" className="h-7 px-2 text-xs" onClick={() => setDrawer({ mode: 'logs', pod })}>
                        <ScrollText className="size-3.5" /> {t('pods.viewLogs')}
                      </Button>
                      {canExec && (
                        <Button variant="ghost" size="sm" className="h-7 px-2 text-xs" onClick={() => setDrawer({ mode: 'exec', pod })}>
                          <TerminalSquare className="size-3.5" /> {t('pods.openTerminal')}
                        </Button>
                      )}
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      </div>

      {/* 抽屉:日志 / 终端 */}
      <Sheet open={drawer !== null} onOpenChange={(open) => { if (!open) setDrawer(null) }}>
        <SheetContent side="right" className="w-full gap-0 sm:max-w-3xl">
          {drawer && (
            <>
              <SheetHeader className="border-b border-border">
                <SheetTitle className="flex items-center gap-2 font-mono text-sm">
                  {drawer.mode === 'logs'
                    ? <ScrollText className="size-4" />
                    : <TerminalSquare className="size-4" />}
                  {drawer.pod.namespace}/{drawer.pod.name}
                </SheetTitle>
                <SheetDescription>
                  {drawer.mode === 'logs' ? t('pods.logsTitle') : t('pods.terminalTitle')}
                </SheetDescription>
              </SheetHeader>
              <div className="flex-1 overflow-hidden p-4">
                {clusterId && drawer.mode === 'logs' && (
                  <PodLogs clusterId={clusterId} namespace={drawer.pod.namespace} pod={drawer.pod.name} />
                )}
                {clusterId && drawer.mode === 'exec' && (
                  <PodTerminal clusterId={clusterId} namespace={drawer.pod.namespace} pod={drawer.pod.name} />
                )}
              </div>
            </>
          )}
        </SheetContent>
      </Sheet>
    </div>
  )
}
