import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router-dom'
import { RotateCw, Plus } from 'lucide-react'
import {
  createCluster, deleteCluster, getSecurityStatus, syncAllClusters, syncCluster, updateCluster,
} from '@/lib/api'
import { normalizeLanguage } from '@/i18n'
import { localizeApiError, type CommonErrorKey } from '@/i18n/errors'
import { localizeStatus } from '@/i18n/status'
import { useClusters } from '@/providers/ClusterProvider'
import { useConfirm } from '@/providers/ConfirmProvider'
import { notify } from '@/lib/toast'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import {
  Sheet, SheetContent, SheetHeader, SheetTitle, SheetDescription, SheetFooter,
} from '@/components/ui/sheet'
import {
  Table, TableHeader, TableBody, TableRow, TableHead, TableCell,
} from '@/components/ui/table'

const MAX_CLUSTERS = 5

interface Form { name: string; slug: string; description: string; contextName: string; apiServerHost: string; kubeconfig: string }
type FormError =
  | { kind: 'limit' }
  | { kind: 'secret' }
  | { kind: 'nameAndKubeconfig' }
  | { kind: 'name' }
  | { kind: 'api'; error: unknown }

const emptyForm = (): Form => ({ name: '', slug: '', description: '', contextName: '', apiServerHost: '', kubeconfig: '' })
const HAN_SCRIPT = /\p{Script=Han}/u

// 编号按当前集群列表的顺序生成连续序号(CL-0001 起),删除后新增会接上,不暴露数据库自增 id
function clusterCode(seq: number) { return `CL-${String(seq).padStart(4, '0')}` }
function modeText(mode?: string) {
  if (mode === 'kubeconfig_data' || mode === 'kubeconfig_path') return 'kubeconfig'
  if (mode === 'in_cluster') return 'in-cluster'
  return mode || '-'
}
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

type PillTone = 'success' | 'error' | 'warning' | 'info' | 'muted'
function Pill({ tone, children }: { tone: PillTone; children: React.ReactNode }) {
  const map: Record<PillTone, string> = {
    success: 'bg-success-weak text-success-fg',
    error: 'bg-danger-weak text-danger-fg',
    warning: 'bg-warning-weak text-warning-fg',
    info: 'bg-info-weak text-info-fg',
    muted: 'bg-muted text-muted-foreground',
  }
  return <span className={cn('inline-block rounded-full px-2 py-0.5 text-[11px] font-medium', map[tone])}>{children}</span>
}

// 连不通或异常状态始终使用醒目的错误色，避免误导用户。
const clusterStatusTones: Record<string, PillTone> = {
  ready: 'success',
  syncing: 'info',
  degraded: 'warning',
  disconnected: 'error',
  error: 'error',
  imported: 'muted',
  unknown: 'muted',
}
function clusterStatusTone(status?: string): PillTone {
  return clusterStatusTones[String(status || '')] || 'muted'
}
function syncResultFailed(status?: string) {
  return status === 'disconnected' || status === 'degraded' || status === 'error'
}

export function ClustersPage() {
  const { t, i18n } = useTranslation('clusters')
  const { t: commonT } = useTranslation('common')
  const language = normalizeLanguage(i18n.resolvedLanguage)
  const navigate = useNavigate()
  const confirm = useConfirm()
  const { clusters, selectedClusterId, loadClusters } = useClusters()
  const [filters, setFilters] = useState({ id: '', name: '' })
  const [security, setSecurity] = useState<any>(null)
  const [securityError, setSecurityError] = useState<unknown>(null)
  const [sheetOpen, setSheetOpen] = useState(false)
  const [editing, setEditing] = useState<any>(null)
  const [form, setForm] = useState<Form>(emptyForm())
  const [saving, setSaving] = useState(false)
  const [formError, setFormError] = useState<FormError | null>(null)
  const [syncing, setSyncing] = useState('')   // 'all' | id
  const [deleting, setDeleting] = useState('')

  useEffect(() => {
    getSecurityStatus()
      .then((value) => { setSecurity(value); setSecurityError(null) })
      .catch((error) => { setSecurity(null); setSecurityError(error) })
  }, [])

  const limitReached = clusters.length >= MAX_CLUSTERS
  const secretReady = security?.secret_key_ready === true
  const secretMessage = typeof security?.message === 'string' ? security.message : ''
  const securityErrorText = securityError !== null
    ? localizeApiError(securityError, commonT, language, 'errors.loadFailed')
    : ''
  const secretWarning = secretReady ? '' : (
    securityErrorText || (secretMessage && (language === 'zh' || !HAN_SCRIPT.test(secretMessage))
      ? secretMessage
      : t('security.secretMissing'))
  )

  function readableClusterError(error: unknown, fallbackKey: CommonErrorKey): string {
    const text = errorText(error)
    if (text.includes('dial tcp') || text.includes('connectex')) {
      const host = text.match(/https:\/\/[^/"\s]+/)?.[0]
      return t('errors.apiServerUnreachable', { host: host ? `: ${host}` : '' })
    }
    const localized = localizeApiError(error, commonT, language, fallbackKey)
    return localized.length > 220 ? `${localized.slice(0, 220)}...` : localized
  }

  const formErrorText = (() => {
    if (!formError) return ''
    if (formError.kind === 'limit') return t('validation.limitReached', { max: MAX_CLUSTERS })
    if (formError.kind === 'secret') return secretWarning
    if (formError.kind === 'nameAndKubeconfig') return t('validation.nameAndKubeconfigRequired')
    if (formError.kind === 'name') return t('validation.nameRequired')
    return readableClusterError(formError.error, 'errors.saveFailed')
  })()

  // 按 id 升序给每个集群分配连续显示序号(1..N),删除后自动重排、始终连续
  const seqOf = useMemo(() => {
    const sorted = [...clusters].sort((a: any, b: any) => Number(a.id) - Number(b.id))
    return new Map(sorted.map((c: any, i) => [String(c.id), i + 1]))
  }, [clusters])
  const codeOf = (c: any) => clusterCode(seqOf.get(String(c.id)) || Number(c.id))

  const filtered = useMemo(() => {
    const id = filters.id.trim().toLowerCase(), name = filters.name.trim().toLowerCase()
    return clusters.filter((c: any) => {
      const code = codeOf(c).toLowerCase(), cn = (c.name || '').toLowerCase()
      return (!id || code.includes(id)) && (!name || cn.includes(name))
    })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [clusters, filters, seqOf])

  function openCreate() { setEditing(null); setForm(emptyForm()); setFormError(null); setSheetOpen(true) }
  function openEdit(c: any) {
    setEditing(c); setFormError(null)
    setForm({ name: c.name || '', slug: c.slug || '', description: c.description || '', contextName: c.context_name || '', apiServerHost: c.api_server_host || '', kubeconfig: '' })
    setSheetOpen(true)
  }

  async function submit() {
    if (saving) return
    setFormError(null)
    const name = form.name.trim(), kubeconfig = form.kubeconfig.trim()
    if (!editing) {
      if (limitReached) { setFormError({ kind: 'limit' }); return }
      if (!secretReady) { setFormError({ kind: 'secret' }); return }
      if (!name || !kubeconfig) { setFormError({ kind: 'nameAndKubeconfig' }); return }
    } else {
      if (!name) { setFormError({ kind: 'name' }); return }
      if (kubeconfig && !secretReady) { setFormError({ kind: 'secret' }); return }
    }
    setSaving(true)
    try {
      const payload = { name, slug: form.slug.trim(), description: form.description.trim(), context_name: form.contextName.trim(), api_server_host: form.apiServerHost.trim(), kubeconfig }
      if (editing) await updateCluster(editing.id, payload)
      else await createCluster(payload)
      setSheetOpen(false)
      await loadClusters()
      notify.success(editing ? t('feedback.updated') : t('feedback.imported'))
    } catch (error) {
      setFormError({ kind: 'api', error })
    } finally { setSaving(false) }
  }

  async function syncAll() {
    if (syncing) return
    setSyncing('all')
    try {
      const result = await syncAllClusters()
      await loadClusters()
      const failed = (result?.results || []).filter((item: any) => syncResultFailed(item?.status))
      if (failed.length) notify.error(t('feedback.syncFailedCount', { count: failed.length }))
      else notify.success(t('feedback.allSynced'))
    }
    catch (error) { notify.error(readableClusterError(error, 'errors.actionFailed')) }
    finally { setSyncing('') }
  }
  async function syncOne(c: any) {
    if (syncing) return
    setSyncing(String(c.id))
    try {
      const result = await syncCluster(c.id)
      await loadClusters()
      if (syncResultFailed(result?.status)) notify.error(readableClusterError(result?.error, 'errors.actionFailed'))
      else notify.success(t('feedback.syncSucceeded'))
    }
    catch (error) { notify.error(readableClusterError(error, 'errors.actionFailed')) }
    finally { setSyncing('') }
  }
  async function remove(c: any) {
    const ok = await confirm({
      title: t('confirmDelete.title'),
      message: t('confirmDelete.message', { name: c.name }),
      tone: 'danger',
      confirmText: commonT('actions.delete'),
    })
    if (!ok) return
    setDeleting(String(c.id))
    try { await deleteCluster(c.id); await loadClusters(); notify.success(t('feedback.deleted')) }
    catch (error) { notify.error(readableClusterError(error, 'errors.deleteFailed')) }
    finally { setDeleting('') }
  }

  return (
    <div className="px-8 py-7">
      {/* 页头 */}
      <div className="mb-4 flex items-end justify-between">
        <div>
          <h2 className="text-xl font-bold tracking-tight">{t('page.title')}</h2>
          <p className="mt-1 text-sm text-muted-foreground">{t('page.summary', { count: clusters.length, max: MAX_CLUSTERS })}</p>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" onClick={syncAll} disabled={syncing === 'all' || !clusters.length}>
            <RotateCw className={cn('size-4', syncing === 'all' && 'animate-spin')} /> {syncing === 'all' ? t('page.syncing') : t('page.syncAll')}
          </Button>
          <Button size="sm" onClick={openCreate} disabled={limitReached}>
            <Plus className="size-4" /> {t('page.importCluster')}
          </Button>
        </div>
      </div>

      {/* 过滤条 */}
      <div className="mb-4 flex flex-wrap items-end gap-3 rounded-xl border border-border bg-card p-4 shadow-sm">
        <div className="space-y-1.5">
          <Label className="text-xs text-muted-foreground">{t('filters.id')}</Label>
          <Input value={filters.id} onChange={(e) => setFilters((f) => ({ ...f, id: e.target.value }))} placeholder={t('filters.idPlaceholder')} className="h-9 w-44" />
        </div>
        <div className="space-y-1.5">
          <Label className="text-xs text-muted-foreground">{t('filters.name')}</Label>
          <Input value={filters.name} onChange={(e) => setFilters((f) => ({ ...f, name: e.target.value }))} placeholder={t('filters.namePlaceholder')} className="h-9 w-56" />
        </div>
        <Button variant="ghost" size="sm" onClick={() => setFilters({ id: '', name: '' })}>{commonT('actions.reset')}</Button>
      </div>

      {limitReached && (
        <div className="mb-4 rounded-lg bg-warning-weak px-3 py-2 text-sm text-warning-fg">
          {t('page.limitReached', { max: MAX_CLUSTERS })}
        </div>
      )}

      {securityError !== null && (
        <div className="mb-4 rounded-lg bg-danger-weak px-3 py-2 text-sm text-danger-fg">{securityErrorText}</div>
      )}

      {/* 表格 */}
      <div className="overflow-hidden rounded-xl border border-border bg-card shadow-sm">
        <Table>
          <TableHeader>
            <TableRow className="hover:bg-transparent">
              <TableHead>{t('columns.id')}</TableHead>
              <TableHead>{t('columns.name')}</TableHead>
              <TableHead>{t('columns.description')}</TableHead>
              <TableHead>{t('columns.version')}</TableHead>
              <TableHead>{t('columns.importMode')}</TableHead>
              <TableHead>{t('columns.importStatus')}</TableHead>
              <TableHead>{t('columns.clusterStatus')}</TableHead>
              <TableHead className="text-right">{t('columns.actions')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {!filtered.length && (
              <TableRow><TableCell colSpan={8} className="py-10 text-center text-sm text-muted-foreground">{t('filters.empty')}</TableCell></TableRow>
            )}
            {filtered.map((c: any) => (
              <TableRow key={c.id} className={cn(String(c.id) === selectedClusterId && 'bg-primary/5')}>
                <TableCell className="font-mono text-xs">{codeOf(c)}</TableCell>
                <TableCell>
                  <button onClick={() => navigate(`/clusters/${c.id}`)} className="font-semibold text-primary hover:underline">{c.name}</button>
                </TableCell>
                <TableCell className="max-w-[180px] truncate text-muted-foreground">{c.description || '-'}</TableCell>
                <TableCell className="text-muted-foreground">{c.version || '-'}</TableCell>
                <TableCell className="text-muted-foreground">{modeText(c.mode)}</TableCell>
                <TableCell><Pill tone={c.last_error ? 'error' : 'success'}>{localizeStatus(commonT, 'import', c.last_error && c.status === 'error' ? 'error' : 'success')}</Pill></TableCell>
                <TableCell><Pill tone={clusterStatusTone(c.status)}>{localizeStatus(commonT, 'cluster', c.status || 'unknown')}</Pill></TableCell>
                <TableCell className="text-right">
                  <div className="flex justify-end gap-1">
                    <Button variant="ghost" size="sm" className="h-7 px-2 text-xs" onClick={() => openEdit(c)}>{commonT('actions.edit')}</Button>
                    <Button variant="ghost" size="sm" className="h-7 px-2 text-xs" onClick={() => navigate(`/clusters/${c.id}`)}>{t('actions.view')}</Button>
                    <Button variant="ghost" size="sm" className="h-7 px-2 text-xs" disabled={Boolean(syncing)} onClick={() => syncOne(c)}>
                      {syncing === String(c.id) ? t('actions.syncing') : t('actions.sync')}
                    </Button>
                    <Button variant="ghost" size="sm" className="h-7 px-2 text-xs text-destructive hover:text-destructive" disabled={deleting === String(c.id)} onClick={() => remove(c)}>
                      {deleting === String(c.id) ? t('actions.deleting') : commonT('actions.delete')}
                    </Button>
                  </div>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>

      {/* 导入/编辑抽屉 */}
      <Sheet open={sheetOpen} onOpenChange={setSheetOpen}>
        <SheetContent className="flex w-full flex-col gap-0 sm:max-w-xl">
          <SheetHeader>
            <SheetTitle>{editing ? t('form.editTitle') : t('form.createTitle')}</SheetTitle>
            <SheetDescription>{editing ? t('form.editDescription') : t('form.createDescription')}</SheetDescription>
          </SheetHeader>

          <div className="flex-1 space-y-4 overflow-y-auto px-4 py-2">
            {secretWarning && (!editing || form.kubeconfig.trim()) && (
              <div className="rounded-lg bg-warning-weak px-3 py-2 text-sm text-warning-fg">
                {securityError !== null ? secretWarning : t('security.warning', { message: secretWarning, action: t('security.restartRequired') })}
              </div>
            )}
            <div className="grid grid-cols-2 gap-3">
              <div className="space-y-1.5"><Label>{t('form.name')}</Label><Input value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} placeholder={t('form.namePlaceholder')} /></div>
              <div className="space-y-1.5"><Label>{t('form.slug')}</Label><Input value={form.slug} onChange={(e) => setForm({ ...form, slug: e.target.value })} placeholder={t('form.slugPlaceholder')} /></div>
              <div className="col-span-2 space-y-1.5"><Label>{t('form.description')}</Label><Input value={form.description} onChange={(e) => setForm({ ...form, description: e.target.value })} placeholder={t('form.descriptionPlaceholder')} /></div>
              <div className="space-y-1.5"><Label>{t('form.context')}</Label><Input value={form.contextName} onChange={(e) => setForm({ ...form, contextName: e.target.value })} placeholder={t('form.optional')} /></div>
              <div className="space-y-1.5"><Label>{t('form.apiServer')}</Label><Input value={form.apiServerHost} onChange={(e) => setForm({ ...form, apiServerHost: e.target.value })} placeholder={t('form.apiServerPlaceholder')} /></div>
              <div className="col-span-2 space-y-1.5">
                <Label>{t('form.kubeconfig')}</Label>
                <Textarea rows={9} value={form.kubeconfig} onChange={(e) => setForm({ ...form, kubeconfig: e.target.value })}
                  placeholder={editing ? t('form.kubeconfigEditPlaceholder') : t('form.kubeconfigCreatePlaceholder')} className="font-mono text-xs" />
              </div>
            </div>
            {formErrorText && <div className="rounded-lg bg-danger-weak px-3 py-2 text-sm text-danger-fg">{formErrorText}</div>}
          </div>

          <SheetFooter className="flex-row justify-end gap-2">
            <Button variant="outline" onClick={() => setSheetOpen(false)}>{commonT('actions.cancel')}</Button>
            <Button onClick={submit} disabled={saving || (!editing && (limitReached || !secretReady))}>
              {saving ? t('form.saving') : editing ? t('form.saveEdit') : t('form.saveCreate')}
            </Button>
          </SheetFooter>
        </SheetContent>
      </Sheet>
    </div>
  )
}
