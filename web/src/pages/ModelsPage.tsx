import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { RotateCw, Plus, CheckCircle2, XCircle } from 'lucide-react'
import {
  createModel, deleteModel, getModelStatus, getSecurityStatus, listModels,
  setDefaultModel, testModel, updateModel,
} from '@/lib/api'
import { useConfirm } from '@/providers/ConfirmProvider'
import { notify } from '@/lib/toast'
import { localizeApiError, type CommonErrorKey } from '@/i18n/errors'
import { normalizeLanguage } from '@/i18n'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Sheet, SheetContent, SheetHeader, SheetTitle, SheetDescription, SheetFooter,
} from '@/components/ui/sheet'

const MAX_MODELS = 10
interface Form {
  name: string; provider: string; baseURL: string; modelName: string; apiKey: string
  maxCompletionTokens: number; timeoutSeconds: number; temperature: number; enabled: boolean; isDefault: boolean
}
const emptyForm = (): Form => ({ name: '', provider: 'openai', baseURL: '', modelName: '', apiKey: '', maxCompletionTokens: 4096, timeoutSeconds: 120, temperature: 0, enabled: true, isDefault: true })

type ModelValidationKey =
  | 'models.validation.createRequired'
  | 'models.validation.editRequired'
  | 'models.secretFallback'

interface ModelApiError {
  error: unknown
  fallback: CommonErrorKey
}

export function ModelsPage() {
  const { t: tSettings, i18n } = useTranslation('settings')
  const { t: tCommon } = useTranslation('common')
  const confirm = useConfirm()
  const [models, setModels] = useState<any[]>([])
  const [current, setCurrent] = useState<any>(null)
  const [security, setSecurity] = useState<any>(null)
  const [loading, setLoading] = useState(true)
  const [errorKey, setErrorKey] = useState<ModelValidationKey | ''>('')
  const [apiError, setApiError] = useState<ModelApiError | null>(null)
  const [testing, setTesting] = useState<Record<string, boolean>>({})
  const [testResults, setTestResults] = useState<Record<string, any>>({})
  const [sheetOpen, setSheetOpen] = useState(false)
  const [saving, setSaving] = useState(false)
  const [editingId, setEditingId] = useState(0) // 0=新增,>0=编辑该模型
  const [form, setForm] = useState<Form>(emptyForm())
  const language = normalizeLanguage(i18n.resolvedLanguage ?? i18n.language)
  const localization = useRef({ tSettings, tCommon, language })
  localization.current = { tSettings, tCommon, language }

  const canCreate = models.length < MAX_MODELS
  const secretReady = security?.secret_key_ready !== false
  const secretWarning = secretReady
    ? ''
    : language === 'zh' && security?.message
      ? security.message
      : tSettings('models.secretFallback')
  const errorMessage = errorKey
    ? tSettings(errorKey)
    : apiError
      ? localizeApiError(apiError.error, tCommon, language, apiError.fallback)
      : ''

  const displayIds = useMemo(() => {
    const sorted = [...models].sort((a, b) => Number(a.id) - Number(b.id))
    return new Map(sorted.map((m, i) => [Number(m.id), i + 1]))
  }, [models])
  const currentDisplayId = current?.profile_id ? displayIds.get(Number(current.profile_id)) || '' : ''

  const load = useCallback(async () => {
    setLoading(true); setErrorKey(''); setApiError(null)
    try {
      const [status, result, sec] = await Promise.all([getModelStatus(), listModels(), getSecurityStatus()])
      setCurrent(status.current || null)
      setModels(result.models || [])
      setSecurity(sec || null)
    } catch (error: unknown) {
      setApiError({ error, fallback: 'errors.loadFailed' })
    } finally { setLoading(false) }
  }, [])
  useEffect(() => { load() }, [load])

  function openCreate() {
    if (!canCreate) { notify.error(tSettings('models.validation.limit', { count: MAX_MODELS })); return }
    setEditingId(0); setForm(emptyForm()); setErrorKey(''); setApiError(null); setSheetOpen(true)
  }
  function openEdit(m: any) {
    setEditingId(m.id); setErrorKey(''); setApiError(null)
    setForm({
      name: m.name || '', provider: m.provider || 'openai', baseURL: m.base_url || '', modelName: m.model_name || '',
      apiKey: '', // 留空表示不修改已保存的密钥
      maxCompletionTokens: m.max_completion_tokens || 4096, timeoutSeconds: m.timeout_seconds || 120,
      temperature: m.temperature || 0, enabled: m.enabled, isDefault: m.is_default,
    })
    setSheetOpen(true)
  }

  async function submit() {
    if (saving) return
    setErrorKey(''); setApiError(null)
    const name = form.name.trim(), baseURL = form.baseURL.trim(), modelName = form.modelName.trim(), apiKey = form.apiKey.trim()
    if (!secretReady) { setErrorKey('models.secretFallback'); return }
    // 编辑时 API key 可留空(保留原密钥);新增时必填
    if (!name || !baseURL || !modelName || (!editingId && !apiKey)) {
      setErrorKey(editingId ? 'models.validation.editRequired' : 'models.validation.createRequired')
      return
    }
    setSaving(true)
    try {
      const payload: Record<string, unknown> = {
        name, provider: form.provider.trim() || 'openai', base_url: baseURL, model_name: modelName,
        max_completion_tokens: Number(form.maxCompletionTokens) || 4096, timeout_seconds: Number(form.timeoutSeconds) || 120,
        temperature: Number(form.temperature) || 0, enabled: form.enabled, is_default: form.isDefault,
      }
      if (apiKey) payload.api_key = apiKey // 仅在填写了新密钥时更新
      if (editingId) {
        await updateModel(editingId, payload)
        notify.success(localization.current.tSettings('models.feedback.updated'))
      } else {
        await createModel({ ...payload, api_key: apiKey })
        notify.success(localization.current.tSettings('models.feedback.saved'))
      }
      setSheetOpen(false); await load()
    } catch (error: unknown) { setApiError({ error, fallback: 'errors.saveFailed' }) }
    finally { setSaving(false) }
  }

  async function activate(m: any) {
    if (m.is_default || !m.enabled) return
    try {
      await setDefaultModel(m.id); await load()
      notify.success(localization.current.tSettings('models.feedback.defaultSet', { name: m.name }))
    } catch (error: unknown) {
      const current = localization.current
      notify.error(localizeApiError(error, current.tCommon, current.language, 'errors.actionFailed'))
    }
  }
  async function toggle(m: any) {
    try { await updateModel(m.id, { enabled: !m.enabled }); await load() }
    catch (error: unknown) {
      const current = localization.current
      notify.error(localizeApiError(error, current.tCommon, current.language, 'errors.updateFailed'))
    }
  }
  async function check(m: any) {
    setTesting((t) => ({ ...t, [m.id]: true })); setTestResults((r) => ({ ...r, [m.id]: null }))
    try {
      const result = await testModel(m.id)
      setTestResults((r) => ({ ...r, [m.id]: { ok: true, message: result.message, latency: result.latency_ms } }))
    } catch (error: unknown) {
      const response = typeof error === 'object' && error !== null && 'response' in error
        ? error.response
        : undefined
      const data = typeof response === 'object' && response !== null && 'data' in response
        ? response.data
        : undefined
      const latency = typeof data === 'object' && data !== null && 'latency_ms' in data
        ? data.latency_ms
        : undefined
      setTestResults((r) => ({ ...r, [m.id]: { ok: false, error, latency } }))
    } finally { setTesting((t) => ({ ...t, [m.id]: false })) }
  }
  async function remove(m: any) {
    const ok = await confirm({
      title: tSettings('models.confirmDelete.title'),
      message: tSettings('models.confirmDelete.message', { name: m.name }),
      tone: 'danger',
      confirmText: tCommon('actions.delete'),
    })
    if (!ok) return
    try {
      await deleteModel(m.id); await load()
      notify.success(localization.current.tSettings('models.feedback.deleted'))
    } catch (error: unknown) {
      const current = localization.current
      notify.error(localizeApiError(error, current.tCommon, current.language, 'errors.deleteFailed'))
    }
  }

  const chip = 'rounded-md bg-muted px-2 py-1 text-[11px] text-muted-foreground'

  return (
    <div className="px-8 py-7">
      <div className="mb-4 flex items-end justify-between">
        <div>
          <h2 className="text-xl font-bold tracking-tight">{tSettings('models.title')}</h2>
          <p className="mt-1 text-sm text-muted-foreground">{tSettings('models.subtitle', { count: MAX_MODELS })}</p>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" onClick={load} disabled={loading}><RotateCw className={cn('size-4', loading && 'animate-spin')} /> {tCommon('actions.refresh')}</Button>
          <Button size="sm" onClick={openCreate} disabled={!canCreate}><Plus className="size-4" /> {tSettings('models.add')}</Button>
        </div>
      </div>

      {errorMessage && <div className="mb-4 rounded-lg bg-danger-weak px-3 py-2 text-sm text-danger-fg">{errorMessage}</div>}
      {secretWarning && <div className="mb-4 rounded-lg bg-warning-weak px-3 py-2 text-sm text-warning-fg">{tSettings('models.secretAction', { message: secretWarning })}</div>}

      {/* 当前生效模型 */}
      <div className="mb-5 flex items-center gap-3 rounded-xl border border-border bg-card p-4 shadow-sm">
        <span className="size-2.5 shrink-0 rounded-full" style={{ background: current?.ready ? 'var(--success)' : 'var(--danger)' }} />
        <div>
          <div className="font-semibold">{tSettings('models.current', { name: current?.model_name || tSettings('models.notEnabled') })}</div>
          <div className="text-xs text-muted-foreground">
            {tSettings('models.source', {
              source: current?.source === 'database'
                ? tSettings('models.sources.database')
                : current?.source === 'runtime'
                  ? tSettings('models.sources.runtime')
                  : tSettings('models.sources.none'),
            })}
            {current?.provider && <> · {current.provider}</>}
            {currentDisplayId && <> · {tSettings('models.configId', { id: currentDisplayId })}</>}
          </div>
          {current?.last_error && <div className="mt-1 text-xs text-danger-fg">{localizeApiError(current.last_error, tCommon, language, 'errors.actionFailed')}</div>}
        </div>
      </div>

      {/* 模型卡片网格 */}
      {loading ? (
        <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
          {[0, 1, 2].map((n) => <div key={n} className="rounded-xl border border-border bg-card p-5 shadow-sm"><Skeleton className="h-32 w-full" /></div>)}
        </div>
      ) : (
        <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
          {models.map((m) => {
            const tr = testResults[m.id]
            return (
              <div key={m.id} className={cn('flex flex-col gap-3 rounded-xl border bg-card p-5 shadow-sm transition-colors',
                m.is_default ? 'border-primary ring-1 ring-primary/30' : 'border-border', !m.enabled && 'opacity-60')}>
                <div className="flex items-start justify-between">
                  <div>
                    <div className="font-bold">{m.name}</div>
                    <div className="text-xs text-muted-foreground">{tSettings('models.configId', { id: displayIds.get(Number(m.id)) || '-' })} · {m.provider} · {m.model_name}</div>
                  </div>
                  {m.is_default && <span className="rounded-full bg-primary px-2 py-0.5 text-[11px] font-medium text-primary-foreground">{tSettings('models.default')}</span>}
                </div>
                <div className="truncate rounded-md bg-muted px-2.5 py-1.5 font-mono text-xs text-muted-foreground">{m.base_url}</div>
                <div className="flex flex-wrap gap-1.5">
                  <span className={chip}>{m.max_completion_tokens || 4096} tokens</span>
                  <span className={chip}>{m.timeout_seconds || 120}s</span>
                  <span className={chip}>{tSettings('models.temperature', { value: m.temperature || 0 })}</span>
                  <span className={chip}>{m.has_api_key ? tSettings('models.keySaved') : tSettings('models.keyMissing')}</span>
                </div>
                <div className="flex flex-wrap gap-1">
                  <Button variant="ghost" size="sm" className="h-7 px-2 text-xs" onClick={() => openEdit(m)}>{tSettings('models.edit')}</Button>
                  <Button variant="ghost" size="sm" className="h-7 px-2 text-xs" disabled={m.is_default || !m.enabled} onClick={() => activate(m)}>{tSettings('models.setDefault')}</Button>
                  <Button variant="ghost" size="sm" className="h-7 px-2 text-xs" disabled={testing[m.id]} onClick={() => check(m)}>{testing[m.id] ? tSettings('models.testing') : tSettings('models.test')}</Button>
                  <Button variant="ghost" size="sm" className="h-7 px-2 text-xs" onClick={() => toggle(m)}>{m.enabled ? tSettings('models.disable') : tSettings('models.enable')}</Button>
                  <Button variant="ghost" size="sm" className="h-7 px-2 text-xs text-destructive hover:text-destructive" onClick={() => remove(m)}>{tCommon('actions.delete')}</Button>
                </div>
                {tr && (
                  <div className={cn('flex items-center gap-1.5 rounded-md px-2.5 py-1.5 text-xs', tr.ok ? 'bg-success-weak text-success-fg' : 'bg-danger-weak text-danger-fg')}>
                    {tr.ok ? <CheckCircle2 className="size-3.5" /> : <XCircle className="size-3.5" />}
                    {tr.ok ? tSettings('models.connectionSuccess') : tSettings('models.connectionFailed')}
                    {tr.latency !== undefined && <span>· {tr.latency}ms</span>}
                    <span className="truncate">· {tr.ok
                      ? tr.message || tSettings('models.connectionFallback')
                      : localizeApiError(tr.error, tCommon, language, 'errors.actionFailed')}
                    </span>
                  </div>
                )}
              </div>
            )
          })}
          {!models.length && <div className="col-span-full rounded-xl border border-dashed border-border py-12 text-center text-sm text-muted-foreground">{tSettings('models.empty')}</div>}
        </div>
      )}

      {/* 新增/编辑模型抽屉 */}
      <Sheet open={sheetOpen} onOpenChange={setSheetOpen}>
        <SheetContent className="flex w-full flex-col gap-0 sm:max-w-lg">
          <SheetHeader>
            <SheetTitle>{editingId ? tSettings('models.drawer.edit') : tSettings('models.drawer.add')}</SheetTitle>
            <SheetDescription>{editingId ? tSettings('models.drawer.editDescription') : tSettings('models.drawer.addDescription')}</SheetDescription>
          </SheetHeader>
          <div className="flex-1 space-y-4 overflow-y-auto px-4 py-2">
            <div className="grid grid-cols-2 gap-3">
              <div className="col-span-2 space-y-1.5"><Label>{tSettings('models.fields.name')}</Label><Input value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} placeholder={tSettings('models.placeholders.name')} /></div>
              <div className="space-y-1.5"><Label>{tSettings('models.fields.provider')}</Label><Input value={form.provider} onChange={(e) => setForm({ ...form, provider: e.target.value })} placeholder={tSettings('models.placeholders.provider')} /></div>
              <div className="space-y-1.5"><Label>{tSettings('models.fields.modelName')}</Label><Input value={form.modelName} onChange={(e) => setForm({ ...form, modelName: e.target.value })} placeholder={tSettings('models.placeholders.modelName')} /></div>
              <div className="col-span-2 space-y-1.5"><Label>{tSettings('models.fields.baseUrl')}</Label><Input value={form.baseURL} onChange={(e) => setForm({ ...form, baseURL: e.target.value })} placeholder={tSettings('models.placeholders.baseUrl')} /></div>
              <div className="col-span-2 space-y-1.5"><Label>{tSettings('models.fields.apiKey')}</Label><Input type="password" value={form.apiKey} onChange={(e) => setForm({ ...form, apiKey: e.target.value })} placeholder={editingId ? tSettings('models.placeholders.apiKeyEdit') : tSettings('models.placeholders.apiKeyNew')} /></div>
              <div className="space-y-1.5"><Label>{tSettings('models.fields.maxTokens')}</Label><Input type="number" min={1} value={form.maxCompletionTokens} onChange={(e) => setForm({ ...form, maxCompletionTokens: Number(e.target.value) })} /></div>
              <div className="space-y-1.5"><Label>{tSettings('models.fields.timeout')}</Label><Input type="number" min={1} value={form.timeoutSeconds} onChange={(e) => setForm({ ...form, timeoutSeconds: Number(e.target.value) })} /></div>
              <div className="space-y-1.5"><Label>{tSettings('models.fields.temperature')}</Label><Input type="number" min={0} max={2} step={0.1} value={form.temperature} onChange={(e) => setForm({ ...form, temperature: Number(e.target.value) })} /></div>
            </div>
            <div className="flex items-center justify-between rounded-lg border border-border px-3 py-2.5">
              <Label className="cursor-pointer">{tSettings('models.fields.enabled')}</Label>
              <Switch checked={form.enabled} onCheckedChange={(v) => setForm({ ...form, enabled: v })} />
            </div>
            <div className="flex items-center justify-between rounded-lg border border-border px-3 py-2.5">
              <Label className="cursor-pointer">{tSettings('models.fields.default')}</Label>
              <Switch checked={form.isDefault} onCheckedChange={(v) => setForm({ ...form, isDefault: v })} />
            </div>
            {errorMessage && <div className="rounded-lg bg-danger-weak px-3 py-2 text-sm text-danger-fg">{errorMessage}</div>}
          </div>
          <SheetFooter className="flex-row justify-end gap-2">
            <Button variant="outline" onClick={() => setSheetOpen(false)}>{tCommon('actions.cancel')}</Button>
            <Button onClick={submit} disabled={saving || !secretReady}>{saving ? tSettings('models.drawer.saving') : editingId ? tSettings('models.drawer.saveEdit') : tSettings('models.drawer.saveNew')}</Button>
          </SheetFooter>
        </SheetContent>
      </Sheet>
    </div>
  )
}
