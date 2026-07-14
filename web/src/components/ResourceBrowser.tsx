import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Copy, RotateCw, ScrollText, TerminalSquare } from 'lucide-react'
import { getClusterResourceObject, listClusterResourceSnapshots } from '@/lib/api'
import { normalizeLanguage } from '@/i18n'
import { formatDateTime } from '@/i18n/format'
import { useAuth } from '@/providers/AuthProvider'
import { notify } from '@/lib/toast'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Table, TableHeader, TableBody, TableRow, TableHead, TableCell } from '@/components/ui/table'
import { Select, SelectTrigger, SelectValue, SelectContent, SelectItem } from '@/components/ui/select'
import { Sheet, SheetContent, SheetHeader, SheetTitle, SheetDescription } from '@/components/ui/sheet'
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui/tabs'
import { PodLogs } from '@/components/PodLogs'
import { PodTerminal } from '@/components/PodTerminal'

// 「主要信息」分组的特殊视图:不是资源快照,选中时渲染调用方传入的面板。
const PRIMARY_VIEW_KEYS = ['basic', 'nodes', 'sync'] as const
type PrimaryView = (typeof PRIMARY_VIEW_KEYS)[number]
function isPrimaryView(key: string): key is PrimaryView {
  return (PRIMARY_VIEW_KEYS as readonly string[]).includes(key)
}

// 「主要信息」分组内的集群级资源(仍走资源快照表)。
const PRIMARY_RESOURCES = ['namespaces', 'events', 'customresourcedefinitions']

// 资源分组配置:key 用于 i18n 分组名,resources 是后端复数资源名(与快照 resource_name 对齐)。
const RESOURCE_GROUPS: { key: string; resources: string[] }[] = [
  { key: 'workloads', resources: ['pods', 'deployments', 'statefulsets', 'daemonsets', 'jobs', 'cronjobs', 'horizontalpodautoscalers'] },
  { key: 'network', resources: ['services', 'ingresses'] },
  { key: 'config', resources: ['configmaps', 'secrets'] },
  { key: 'storage', resources: ['persistentvolumeclaims', 'persistentvolumes'] },
]

// 资源复数名 → Kubernetes Kind 显示名(K8s 通用术语,中英文一致)。
const RESOURCE_KIND: Record<string, string> = {
  pods: 'Pod', deployments: 'Deployment', statefulsets: 'StatefulSet', daemonsets: 'DaemonSet',
  jobs: 'Job', cronjobs: 'CronJob', horizontalpodautoscalers: 'HPA',
  services: 'Service', ingresses: 'Ingress',
  configmaps: 'ConfigMap', secrets: 'Secret',
  persistentvolumeclaims: 'PVC', persistentvolumes: 'PV',
  nodes: 'Node', namespaces: 'Namespace', events: 'Event', customresourcedefinitions: 'CRD',
}

// 集群级(无命名空间)资源:隐藏命名空间下拉与命名空间列。
const CLUSTER_SCOPED = new Set(['nodes', 'namespaces', 'persistentvolumes', 'customresourcedefinitions'])

interface Snapshot {
  api_version: string
  kind: string
  resource_name: string
  namespace: string
  name: string
  status: string
  ready: string
  node_name: string
  resource_version: string
  summary: string
  last_seen_at: string
}

const NS_ALL = '__all__'

function kindOf(resource: string): string {
  return RESOURCE_KIND[resource] || resource
}

// summary 是后端存的精简 JSON 字符串,尽量解析成键值对展示,失败则原样返回。
function parseSummary(summary: string): Record<string, unknown> | null {
  if (!summary) return null
  try {
    const parsed = JSON.parse(summary)
    return parsed && typeof parsed === 'object' ? (parsed as Record<string, unknown>) : null
  } catch {
    return null
  }
}

// 抽屉内部当前激活的分页:详情 / YAML / 日志 / 终端。
type DrawerTab = 'detail' | 'yaml' | 'logs' | 'terminal'

interface ResourceBrowserProps {
  clusterId?: number
  // 「主要信息」三个特殊视图,由集群详情页承载数据后传入。
  basicPanel?: React.ReactNode
  nodesPanel?: React.ReactNode
  syncPanel?: React.ReactNode
}

export function ResourceBrowser({ clusterId, basicPanel, nodesPanel, syncPanel }: ResourceBrowserProps) {
  const { t, i18n } = useTranslation('clusters')
  const language = normalizeLanguage(i18n.resolvedLanguage)
  const { can } = useAuth()
  const canExec = can('clusters:exec')

  // 默认进入「基本信息」视图。
  const [selected, setSelected] = useState('basic')
  const [rows, setRows] = useState<Snapshot[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [nsFilter, setNsFilter] = useState(NS_ALL)
  const [nameFilter, setNameFilter] = useState('')
  const [detail, setDetail] = useState<Snapshot | null>(null)
  const [drawerTab, setDrawerTab] = useState<DrawerTab>('detail')
  const [yaml, setYaml] = useState('')
  const [yamlLoading, setYamlLoading] = useState(false)
  const [yamlError, setYamlError] = useState<string | null>(null)
  const listSeq = useRef(0)

  const primaryView = isPrimaryView(selected)
  const isPod = selected === 'pods'
  const clusterScoped = CLUSTER_SCOPED.has(selected)

  // manual=true 时给出成功反馈并保底最短转圈时间,避免缓存命中过快看不到刷新动作。
  const load = useCallback(async (manual = false) => {
    // 特殊视图不请求资源快照。
    if (!clusterId || isPrimaryView(selected)) return
    const seq = ++listSeq.current
    setLoading(true)
    setError(null)
    const minSpin = manual ? new Promise((resolve) => setTimeout(resolve, 400)) : Promise.resolve()
    try {
      const result = await listClusterResourceSnapshots(clusterId, selected)
      await minSpin
      if (seq === listSeq.current) {
        const snapshots = result.snapshots || []
        setRows(snapshots)
        if (manual) notify.success(t('resources.refreshed', { count: snapshots.length }))
      }
    } catch (e) {
      const err = e as { response?: { data?: { error?: string } }; message?: string }
      if (seq === listSeq.current) setError(err.response?.data?.error || err.message || t('resources.loadFailed'))
    } finally {
      if (seq === listSeq.current) setLoading(false)
    }
  }, [clusterId, selected, t])

  useEffect(() => { load() }, [load])
  // 切换资源类型时重置筛选,避免残留上一个类型的命名空间/名称条件。
  useEffect(() => { setNsFilter(NS_ALL); setNameFilter('') }, [selected])

  const namespaces = useMemo(() => {
    const set = new Set<string>()
    for (const row of rows) if (row.namespace) set.add(row.namespace)
    return Array.from(set).sort((a, b) => a.localeCompare(b))
  }, [rows])

  const filtered = useMemo(() => {
    const name = nameFilter.trim().toLowerCase()
    return rows.filter((row) =>
      (nsFilter === NS_ALL || row.namespace === nsFilter) &&
      (!name || row.name.toLowerCase().includes(name)))
  }, [rows, nsFilter, nameFilter])

  // 打开详情抽屉;进入抽屉即预取 YAML,确保切到 YAML 分页立即有内容。
  const openDetail = useCallback(async (row: Snapshot, tab: DrawerTab = 'detail') => {
    setDetail(row)
    setDrawerTab(tab)
    setYaml('')
    setYamlError(null)
    if (!clusterId) return
    setYamlLoading(true)
    try {
      const result = await getClusterResourceObject(clusterId, {
        apiVersion: row.api_version,
        resource: row.resource_name,
        namespace: row.namespace,
        name: row.name,
      })
      setYaml(result.yaml || '')
    } catch (e) {
      const err = e as { response?: { data?: { error?: string } }; message?: string }
      setYamlError(err.response?.data?.error || err.message || t('resources.yamlFailed'))
    } finally {
      setYamlLoading(false)
    }
  }, [clusterId, t])

  const copyYaml = useCallback(async () => {
    if (!yaml) return
    try {
      await navigator.clipboard.writeText(yaml)
      notify.success(t('resources.copied'))
    } catch {
      notify.error(t('resources.copyFailed'))
    }
  }, [yaml, t])

  const summaryEntries = useMemo(() => {
    if (!detail) return []
    const parsed = parseSummary(detail.summary)
    if (!parsed) return []
    return Object.entries(parsed)
  }, [detail])

  // 详情抽屉是否为 Pod(以对象自身的 resource_name 判断,而非当前左栏选中项)。
  const detailIsPod = detail?.resource_name === 'pods'
  // 列跨度:命名空间列可隐藏,Pod 多一列操作。
  const colSpan = 4 + (clusterScoped ? 0 : 1) + (isPod ? 1 : 0)

  // 左列导航项统一样式。
  const navButton = (key: string, label: string) => (
    <button
      key={key}
      type="button"
      onClick={() => setSelected(key)}
      className={cn(
        'w-full rounded-md px-2 py-1 text-left text-xs transition-colors',
        selected === key ? 'bg-primary/10 font-medium text-primary' : 'text-muted-foreground hover:bg-muted hover:text-foreground',
      )}
    >
      {label}
    </button>
  )

  return (
    <div className="flex min-h-[520px] gap-4">
      {/* 左侧分组导航:分组名大而粗(一级),项小(二级) */}
      <nav className="w-52 shrink-0 space-y-4 rounded-xl border border-border bg-card p-3 shadow-sm">
        {/* 主要信息:基本信息 / 节点状态 / 同步状态 + 集群级资源 */}
        <div>
          <div className="px-2 pb-1.5 text-sm font-bold text-foreground">{t('resources.groups.primary')}</div>
          <div className="space-y-0.5">
            {PRIMARY_VIEW_KEYS.map((view) => navButton(view, String(t(`resources.views.${view}` as Parameters<typeof t>[0]))))}
            {PRIMARY_RESOURCES.map((resource) => navButton(resource, kindOf(resource)))}
          </div>
        </div>

        {RESOURCE_GROUPS.map((group) => (
          <div key={group.key}>
            <div className="px-2 pb-1.5 text-sm font-bold text-foreground">
              {String(t(`resources.groups.${group.key}` as Parameters<typeof t>[0]))}
            </div>
            <div className="space-y-0.5">
              {group.resources.map((resource) => navButton(resource, kindOf(resource)))}
            </div>
          </div>
        ))}
      </nav>

      {/* 右侧内容:特殊视图渲染传入面板,资源视图渲染列表 */}
      {primaryView ? (
        <div className="min-w-0 flex-1">
          {selected === 'basic' && basicPanel}
          {selected === 'nodes' && nodesPanel}
          {selected === 'sync' && syncPanel}
        </div>
      ) : (
        <div className="min-w-0 flex-1 space-y-3">
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-sm font-semibold">{kindOf(selected)}</span>
            {!clusterScoped && (
              <Select value={nsFilter} onValueChange={setNsFilter}>
                <SelectTrigger className="h-9 w-52"><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value={NS_ALL}>{t('resources.allNamespaces')}</SelectItem>
                  {namespaces.map((ns) => <SelectItem key={ns} value={ns}>{ns}</SelectItem>)}
                </SelectContent>
              </Select>
            )}
            <Input
              value={nameFilter}
              onChange={(e) => setNameFilter(e.target.value)}
              placeholder={t('resources.filterName')}
              className="h-9 w-56"
            />
            <span className="text-xs text-muted-foreground">{t('resources.count', { count: filtered.length })}</span>
            <Button variant="outline" size="sm" className="ml-auto" onClick={() => load(true)} disabled={loading}>
              <RotateCw className={cn('size-4', loading && 'animate-spin')} /> {t('resources.refresh')}
            </Button>
          </div>

          {error && <div className="rounded-lg bg-danger-weak px-3 py-2 text-sm text-danger-fg">{error}</div>}

          <div className="overflow-hidden rounded-xl border border-border bg-card shadow-sm">
            <div className="overflow-x-auto">
              <Table className="min-w-[860px]">
                <TableHeader>
                  <TableRow className="hover:bg-transparent">
                    <TableHead>{t('resources.columns.name')}</TableHead>
                    {!clusterScoped && <TableHead>{t('resources.columns.namespace')}</TableHead>}
                    <TableHead>{t('resources.columns.status')}</TableHead>
                    <TableHead>{t('resources.columns.ready')}</TableHead>
                    <TableHead>{t('resources.columns.node')}</TableHead>
                    <TableHead>{t('resources.columns.lastSeen')}</TableHead>
                    {isPod && <TableHead>{t('resources.columns.actions')}</TableHead>}
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {!filtered.length && (
                    <TableRow>
                      <TableCell colSpan={colSpan} className="py-8 text-center text-sm text-muted-foreground">
                        {loading ? t('resources.loading') : t('resources.empty')}
                      </TableCell>
                    </TableRow>
                  )}
                  {filtered.map((row) => (
                    <TableRow
                      key={`${row.namespace}/${row.name}`}
                      className="cursor-pointer"
                      onClick={() => openDetail(row)}
                    >
                      <TableCell className="font-mono text-xs">{row.name}</TableCell>
                      {!clusterScoped && <TableCell className="text-xs text-muted-foreground">{row.namespace || '-'}</TableCell>}
                      <TableCell className="text-xs">{row.status || '-'}</TableCell>
                      <TableCell className="text-xs">{row.ready || '-'}</TableCell>
                      <TableCell className="font-mono text-xs text-muted-foreground">{row.node_name || '-'}</TableCell>
                      <TableCell className="whitespace-nowrap text-xs text-muted-foreground">{formatDateTime(row.last_seen_at, language)}</TableCell>
                      {isPod && (
                        <TableCell onClick={(e) => e.stopPropagation()}>
                          <div className="flex items-center gap-1.5">
                            <Button variant="ghost" size="sm" className="h-7 px-2 text-xs" onClick={() => openDetail(row, 'logs')}>
                              <ScrollText className="size-3.5" /> {t('resources.logsTab')}
                            </Button>
                            {canExec && (
                              <Button variant="ghost" size="sm" className="h-7 px-2 text-xs" onClick={() => openDetail(row, 'terminal')}>
                                <TerminalSquare className="size-3.5" /> {t('resources.terminalTab')}
                              </Button>
                            )}
                          </div>
                        </TableCell>
                      )}
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          </div>
        </div>
      )}

      {/* 抽屉:详情 / YAML / 日志 / 终端 */}
      <Sheet open={detail !== null} onOpenChange={(open) => { if (!open) setDetail(null) }}>
        <SheetContent side="right" className="w-full gap-0 sm:max-w-2xl">
          {detail && (
            <>
              <SheetHeader className="border-b border-border">
                <SheetTitle className="flex items-center gap-2 font-mono text-sm">
                  {detail.kind || kindOf(detail.resource_name)} · {detail.name}
                </SheetTitle>
                <SheetDescription>{detail.namespace ? `${detail.namespace}/${detail.name}` : detail.name}</SheetDescription>
              </SheetHeader>
              <div className="flex-1 overflow-hidden p-4">
                <Tabs value={drawerTab} onValueChange={(v) => setDrawerTab(v as DrawerTab)} className="flex h-full flex-col">
                  <TabsList>
                    <TabsTrigger value="detail">{t('resources.detail.tab')}</TabsTrigger>
                    <TabsTrigger value="yaml">{t('resources.yaml.tab')}</TabsTrigger>
                    {detailIsPod && <TabsTrigger value="logs">{t('resources.logsTab')}</TabsTrigger>}
                    {detailIsPod && canExec && <TabsTrigger value="terminal">{t('resources.terminalTab')}</TabsTrigger>}
                  </TabsList>

                  <TabsContent value="detail" className="mt-3 flex-1 overflow-y-auto">
                    <dl className="grid grid-cols-1 gap-2">
                      {([
                        [t('resources.detail.kind'), detail.kind || kindOf(detail.resource_name)],
                        [t('resources.detail.apiVersion'), detail.api_version],
                        [t('resources.detail.namespace'), detail.namespace || '-'],
                        [t('resources.detail.name'), detail.name],
                        [t('resources.detail.status'), detail.status || '-'],
                        [t('resources.detail.ready'), detail.ready || '-'],
                        [t('resources.detail.node'), detail.node_name || '-'],
                        [t('resources.detail.resourceVersion'), detail.resource_version || '-'],
                        [t('resources.detail.lastSeen'), formatDateTime(detail.last_seen_at, language)],
                      ] as [string, string][]).map(([k, v]) => (
                        <div key={k} className="flex gap-3 rounded-md bg-muted/50 px-3 py-2 text-xs">
                          <dt className="w-32 shrink-0 text-muted-foreground">{k}</dt>
                          <dd className="min-w-0 break-all font-mono">{v}</dd>
                        </div>
                      ))}
                    </dl>
                    {summaryEntries.length > 0 && (
                      <div className="mt-3">
                        <div className="mb-1 text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">{t('resources.detail.summary')}</div>
                        <dl className="grid grid-cols-1 gap-2">
                          {summaryEntries.map(([k, v]) => (
                            <div key={k} className="flex gap-3 rounded-md bg-muted/50 px-3 py-2 text-xs">
                              <dt className="w-32 shrink-0 text-muted-foreground">{k}</dt>
                              <dd className="min-w-0 break-all font-mono">{typeof v === 'object' ? JSON.stringify(v) : String(v)}</dd>
                            </div>
                          ))}
                        </dl>
                      </div>
                    )}
                  </TabsContent>

                  <TabsContent value="yaml" className="mt-3 flex-1 overflow-hidden">
                    <div className="flex h-full flex-col">
                      <div className="mb-2 flex items-center justify-end">
                        <Button variant="outline" size="sm" onClick={copyYaml} disabled={!yaml}>
                          <Copy className="size-3.5" /> {t('resources.yaml.copy')}
                        </Button>
                      </div>
                      {yamlLoading && <div className="text-sm text-muted-foreground">{t('resources.yaml.loading')}</div>}
                      {yamlError && <div className="rounded-lg bg-danger-weak px-3 py-2 text-sm text-danger-fg">{yamlError}</div>}
                      {!yamlLoading && !yamlError && (
                        <pre className="flex-1 overflow-auto rounded-lg bg-muted/50 p-3 font-mono text-xs leading-relaxed">{yaml || t('resources.yaml.empty')}</pre>
                      )}
                    </div>
                  </TabsContent>

                  {detailIsPod && clusterId && (
                    <TabsContent value="logs" className="mt-3 flex-1 overflow-hidden">
                      <PodLogs clusterId={clusterId} namespace={detail.namespace} pod={detail.name} />
                    </TabsContent>
                  )}

                  {detailIsPod && canExec && clusterId && (
                    <TabsContent value="terminal" className="mt-3 flex-1 overflow-hidden">
                      <PodTerminal clusterId={clusterId} namespace={detail.namespace} pod={detail.name} />
                    </TabsContent>
                  )}
                </Tabs>
              </div>
            </>
          )}
        </SheetContent>
      </Sheet>
    </div>
  )
}
