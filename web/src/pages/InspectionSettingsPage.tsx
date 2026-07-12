import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { TFunction } from 'i18next'
import { RotateCw, Plus } from 'lucide-react'
import {
  getInspectionRules, updateInspectionRule, resetInspectionRule, updateClusterPrometheus,
  listClusters, listCustomRules, createCustomRule, updateCustomRule, deleteCustomRule, testCustomRule,
} from '@/lib/api'
import { useConfirm } from '@/providers/ConfirmProvider'
import { notify } from '@/lib/toast'
import { normalizeLanguage } from '@/i18n'
import { localizeApiError } from '@/i18n/errors'
import { localizeStatus } from '@/i18n/status'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { Select, SelectTrigger, SelectValue, SelectContent, SelectItem } from '@/components/ui/select'
import { Sheet, SheetContent, SheetHeader, SheetTitle, SheetDescription, SheetFooter } from '@/components/ui/sheet'

const operatorOptions = ['>', '>=', '<', '<=', '==']
const blankRuleForm = () => ({ name: '', promql: '', operator: '>', threshold: 0, severity: 'warning', object_label: '', description: '', enabled: true })
const sevBadge = (s: string) => (s === 'critical' ? 'bg-danger-weak text-danger-fg' : 'bg-warning-weak text-warning-fg')
const ruleDescriptionKeys = {
  PodCrashLoopBackOff: 'inspection.ruleDescriptions.PodCrashLoopBackOff',
  PodImagePullError: 'inspection.ruleDescriptions.PodImagePullError',
  PodOOMKilled: 'inspection.ruleDescriptions.PodOOMKilled',
  PodFailed: 'inspection.ruleDescriptions.PodFailed',
  PodPending: 'inspection.ruleDescriptions.PodPending',
  PodHighRestarts: 'inspection.ruleDescriptions.PodHighRestarts',
  PodNotReady: 'inspection.ruleDescriptions.PodNotReady',
  NodeNotReady: 'inspection.ruleDescriptions.NodeNotReady',
  NodeHighCPU: 'inspection.ruleDescriptions.NodeHighCPU',
  NodeHighMemory: 'inspection.ruleDescriptions.NodeHighMemory',
} as const

function ruleDescription(rule: any, translate: TFunction<'settings'>): string {
  const key = ruleDescriptionKeys[rule.name as keyof typeof ruleDescriptionKeys]
  return key ? translate(key) : rule.description || rule.name
}

function thresholdUnit(rule: any, translate: TFunction<'settings'>): string {
  return rule.name === 'PodHighRestarts'
    ? translate('inspection.units.restarts')
    : rule.threshold_unit || ''
}

function RuleRow({ children, unavailable }: { children: React.ReactNode; unavailable?: boolean }) {
  return <div className={cn('mb-2 flex flex-wrap items-center gap-4 rounded-xl border border-border bg-card px-[18px] py-3.5 shadow-sm', unavailable && 'opacity-65')}>{children}</div>
}

export function InspectionSettingsPage() {
  const { t: tSettings, i18n } = useTranslation('settings')
  const { t: tCommon } = useTranslation('common')
  const confirm = useConfirm()
  const [loading, setLoading] = useState(false)
  const [rules, setRules] = useState<any[]>([])
  const [prometheus, setPrometheus] = useState<any>({ configured: false, reachable: false, source: 'none' })
  const [busyRule, setBusyRule] = useState('')
  const [thresholdDrafts, setThresholdDrafts] = useState<Record<string, any>>({})
  const [clusters, setClusters] = useState<any[]>([])
  const [selectedLayer, setSelectedLayer] = useState(0)
  const [promUrlDraft, setPromUrlDraft] = useState('')
  const [promSaving, setPromSaving] = useState(false)
  const [customRules, setCustomRules] = useState<any[]>([])
  const language = normalizeLanguage(i18n.resolvedLanguage ?? i18n.language)
  const localization = useRef({ tSettings, tCommon, language })
  localization.current = { tSettings, tCommon, language }
  const severityText = (severity: string) => localizeStatus(tCommon, 'severity', severity)

  const isClusterLayer = selectedLayer !== 0
  const builtinRules = useMemo(() => rules.filter((r) => r.source === 'builtin'), [rules])
  const promRules = useMemo(() => rules.filter((r) => r.source === 'prometheus'), [rules])

  const promSourceText = (() => {
    const src = prometheus.source
    if (isClusterLayer) {
      if (src === 'cluster') return tSettings('inspection.prometheus.sources.cluster')
      if (src === 'global') return tSettings('inspection.prometheus.sources.global')
      return tSettings('inspection.prometheus.sources.none')
    }
    return src === 'none'
      ? tSettings('inspection.prometheus.sources.none')
      : tSettings('inspection.prometheus.sources.configured')
  })()
  const promPlaceholder = (() => {
    if (isClusterLayer) {
      return prometheus.source === 'cluster'
        ? tSettings('inspection.prometheus.placeholders.clusterConfigured')
        : tSettings('inspection.prometheus.placeholders.clusterFallback')
    }
    return prometheus.configured
      ? tSettings('inspection.prometheus.placeholders.globalConfigured')
      : tSettings('inspection.prometheus.placeholders.global')
  })()

  const loadCustom = useCallback(async (layer: number) => {
    try { setCustomRules((await listCustomRules(layer)).rules || []) } catch { setCustomRules([]) }
  }, [])

  const load = useCallback(async (layer: number) => {
    setLoading(true)
    try {
      const data = await getInspectionRules(layer)
      const rs = data.rules || []
      setRules(rs)
      setPrometheus(data.prometheus || { configured: false, reachable: false, source: 'none' })
      const drafts: Record<string, any> = {}
      for (const r of rs) if (r.has_threshold) drafts[r.name] = r.threshold
      setThresholdDrafts(drafts)
      await loadCustom(layer)
    } catch (error: unknown) {
      const current = localization.current
      notify.error(localizeApiError(error, current.tCommon, current.language, 'errors.loadFailed'))
    } finally { setLoading(false) }
  }, [loadCustom])

  useEffect(() => {
    listClusters().then((d) => setClusters(d.clusters || d || [])).catch(() => setClusters([]))
    load(0)
  }, [load])

  function switchLayer(layer: number) {
    if (selectedLayer === layer) return
    setSelectedLayer(layer); setPromUrlDraft(''); load(layer)
  }

  async function toggleRule(rule: any) {
    setBusyRule(rule.name)
    try {
      await updateInspectionRule(rule.name, { enabled: !rule.enabled }, selectedLayer)
      setRules((prev) => prev.map((r) => (r.name === rule.name ? { ...r, enabled: !r.enabled, overridden: true } : r)))
      const current = localization.current
      notify.success(current.tSettings('inspection.feedback.ruleToggled', {
        action: current.tSettings(!rule.enabled ? 'inspection.feedback.enabled' : 'inspection.feedback.disabled'),
        name: ruleDescription(rule, current.tSettings),
      }))
    } catch (error: unknown) {
      const current = localization.current
      notify.error(localizeApiError(error, current.tCommon, current.language, 'errors.saveFailed'))
    } finally { setBusyRule('') }
  }
  async function saveThreshold(rule: any) {
    const value = Number(thresholdDrafts[rule.name])
    if (!Number.isFinite(value) || value <= 0) { notify.error(tSettings('inspection.validation.positiveThreshold')); setThresholdDrafts((d) => ({ ...d, [rule.name]: rule.threshold })); return }
    if (value === rule.threshold) return
    setBusyRule(rule.name)
    try {
      await updateInspectionRule(rule.name, { threshold: value }, selectedLayer)
      setRules((prev) => prev.map((r) => (r.name === rule.name ? { ...r, threshold: value, overridden: true } : r)))
      notify.success(localization.current.tSettings('inspection.feedback.thresholdSaved', {
        name: ruleDescription(rule, localization.current.tSettings),
        value,
        unit: thresholdUnit(rule, localization.current.tSettings),
      }))
    } catch (error: unknown) {
      const current = localization.current
      notify.error(localizeApiError(error, current.tCommon, current.language, 'errors.saveFailed'))
      setThresholdDrafts((d) => ({ ...d, [rule.name]: rule.threshold }))
    } finally { setBusyRule('') }
  }
  async function resetRule(rule: any) {
    setBusyRule(rule.name)
    try {
      await resetInspectionRule(rule.name, selectedLayer)
      notify.success(localization.current.tSettings(
        isClusterLayer ? 'inspection.feedback.inherited' : 'inspection.feedback.defaulted',
        { name: ruleDescription(rule, localization.current.tSettings) },
      ))
      await load(selectedLayer)
    } catch (error: unknown) {
      const current = localization.current
      notify.error(localizeApiError(error, current.tCommon, current.language, 'errors.actionFailed'))
    } finally { setBusyRule('') }
  }
  async function savePromUrl() {
    setPromSaving(true)
    try {
      await updateClusterPrometheus(selectedLayer, promUrlDraft.trim())
      notify.success(localization.current.tSettings(
        promUrlDraft.trim()
          ? 'inspection.prometheus.configuredFeedback'
          : 'inspection.prometheus.clearedFeedback',
      ))
      setPromUrlDraft(''); await load(selectedLayer)
    } catch (error: unknown) {
      const current = localization.current
      notify.error(localizeApiError(error, current.tCommon, current.language, 'errors.saveFailed'))
    } finally { setPromSaving(false) }
  }

  // ---- 自定义规则抽屉 ----
  const [ruleDrawer, setRuleDrawer] = useState(false)
  const [ruleSaving, setRuleSaving] = useState(false)
  const [editingRuleId, setEditingRuleId] = useState(0)
  const [ruleForm, setRuleForm] = useState<any>(blankRuleForm())
  const [testResult, setTestResult] = useState<any>(null)
  const [testing, setTesting] = useState(false)

  function openCreateRule() { setEditingRuleId(0); setRuleForm(blankRuleForm()); setTestResult(null); setRuleDrawer(true) }
  function openEditRule(r: any) {
    setEditingRuleId(r.id)
    setRuleForm({ name: r.name, promql: r.promql, operator: r.operator, threshold: r.threshold, severity: r.severity, object_label: r.object_label || '', description: r.description || '', enabled: r.enabled })
    setTestResult(null); setRuleDrawer(true)
  }
  async function runTest() {
    if (!ruleForm.promql.trim()) { notify.error(tSettings('inspection.validation.promqlRequired')); return }
    setTesting(true); setTestResult(null)
    try {
      const result = await testCustomRule({ ...ruleForm }, selectedLayer)
      setTestResult(result.ok ? result : { ...result, apiError: result.error })
    }
    catch (error: unknown) { setTestResult({ ok: false, apiError: error }) } finally { setTesting(false) }
  }
  async function submitRule() {
    const f = ruleForm
    if (!f.name.trim() || !f.promql.trim()) { notify.error(tSettings('inspection.validation.ruleRequired')); return }
    setRuleSaving(true)
    try {
      if (editingRuleId) {
        await updateCustomRule(editingRuleId, { ...f })
        notify.success(localization.current.tSettings('inspection.feedback.customUpdated'))
      } else {
        await createCustomRule({ ...f }, selectedLayer)
        notify.success(localization.current.tSettings('inspection.feedback.customCreated'))
      }
      setRuleDrawer(false); await loadCustom(selectedLayer)
    } catch (error: unknown) {
      const current = localization.current
      notify.error(localizeApiError(error, current.tCommon, current.language, 'errors.saveFailed'))
    } finally { setRuleSaving(false) }
  }
  async function toggleCustomRule(r: any) {
    try {
      await updateCustomRule(r.id, { name: r.name, promql: r.promql, operator: r.operator, threshold: r.threshold, severity: r.severity, object_label: r.object_label, description: r.description, enabled: !r.enabled })
      setCustomRules((prev) => prev.map((x) => (x.id === r.id ? { ...x, enabled: !x.enabled } : x)))
      const current = localization.current
      notify.success(current.tSettings('inspection.feedback.customToggled', {
        action: current.tSettings(!r.enabled ? 'inspection.feedback.enabled' : 'inspection.feedback.disabled'),
        name: r.name,
      }))
    } catch (error: unknown) {
      const current = localization.current
      notify.error(localizeApiError(error, current.tCommon, current.language, 'errors.actionFailed'))
    }
  }
  async function removeCustomRule(r: any) {
    const ok = await confirm({
      title: tSettings('inspection.confirmDelete.title'),
      message: tSettings('inspection.confirmDelete.message', { name: r.name }),
      confirmText: tCommon('actions.delete'),
      tone: 'danger',
    })
    if (!ok) return
    try {
      await deleteCustomRule(r.id)
      notify.info(localization.current.tSettings('inspection.feedback.deleted'))
      await loadCustom(selectedLayer)
    } catch (error: unknown) {
      const current = localization.current
      notify.error(localizeApiError(error, current.tCommon, current.language, 'errors.deleteFailed'))
    }
  }

  return (
    <div className="px-8 py-7">
      <div className="mb-4 flex items-end justify-between">
        <div>
          <h2 className="text-xl font-bold tracking-tight">{tSettings('inspection.title')}</h2>
          <p className="mt-1 text-sm text-muted-foreground">{tSettings('inspection.subtitle')}</p>
        </div>
        <Button variant="outline" size="sm" onClick={() => load(selectedLayer)} disabled={loading}><RotateCw className={cn('size-4', loading && 'animate-spin')} /> {tCommon('actions.refresh')}</Button>
      </div>

      {/* 配置层选择器 */}
      <div className="mb-2.5 flex flex-wrap gap-2">
        <button onClick={() => switchLayer(0)} className={cn('rounded-full border px-4 py-1.5 text-[13px] transition-colors',
          selectedLayer === 0 ? 'border-primary bg-primary font-semibold text-white' : 'border-border bg-card text-muted-foreground hover:border-primary hover:text-foreground')}>{tSettings('inspection.globalDefault')}</button>
        {clusters.map((c) => (
          <button key={c.id} onClick={() => switchLayer(c.id)} className={cn('rounded-full border px-4 py-1.5 text-[13px] transition-colors',
            selectedLayer === c.id ? 'border-primary bg-primary font-semibold text-white' : 'border-border bg-card text-muted-foreground hover:border-primary hover:text-foreground')}>{c.name}</button>
        ))}
      </div>
      <p className="mb-5 text-[12.5px] text-muted-foreground">
        {isClusterLayer ? tSettings('inspection.clusterDescription') : tSettings('inspection.globalDescription')}
      </p>

      {/* Prometheus 接入 */}
      <div className="mb-5 rounded-xl border border-border bg-card p-[18px] shadow-sm">
        <div className="flex flex-wrap items-center gap-3">
          <strong className="text-[14.5px] font-bold">{tSettings('inspection.prometheus.title')}</strong>
          {!prometheus.configured ? <span className="rounded-full bg-muted px-2 py-0.5 text-[11px] font-medium text-muted-foreground">{tSettings('inspection.prometheus.unconfigured')}</span>
            : prometheus.reachable ? <span className="rounded-full bg-success-weak px-2 py-0.5 text-[11px] font-medium text-success-fg">{tSettings('inspection.prometheus.connected')}</span>
            : <span className="rounded-full bg-danger-weak px-2 py-0.5 text-[11px] font-medium text-danger-fg">{tSettings('inspection.prometheus.unreachable')}</span>}
          <span className="ml-auto text-xs text-text-subtle">{tSettings('inspection.prometheus.source', { source: promSourceText })}</span>
        </div>
        <div className="mt-3 flex gap-2">
          <Input value={promUrlDraft} onChange={(e) => setPromUrlDraft(e.target.value)} onKeyUp={(e) => e.key === 'Enter' && savePromUrl()} placeholder={promPlaceholder} disabled={promSaving} className="h-9 flex-1" />
          <Button size="sm" className="h-9" onClick={savePromUrl} disabled={promSaving}>{promSaving ? tSettings('inspection.prometheus.saving') : tCommon('actions.save')}</Button>
        </div>
        <p className="mt-2 text-[13px] leading-relaxed text-muted-foreground">
          {tSettings('inspection.prometheus.description')}{' '}
          {isClusterLayer
            ? tSettings('inspection.prometheus.clusterHint')
            : <>{tSettings('inspection.prometheus.globalHintBefore')}<code className="rounded bg-muted px-1.5 py-0.5 font-mono text-xs">prometheus.url</code>{tSettings('inspection.prometheus.globalHintAfter')}</>}
        </p>
      </div>

      {/* 内置状态规则 */}
      <RuleGroup title={tSettings('inspection.groups.builtin')}>
        {builtinRules.map((r) => (
          <RuleRow key={r.name}>
            <div className="min-w-0 flex-1">
              <div className="flex flex-wrap items-center gap-2.5 text-sm font-semibold">
                {ruleDescription(r, tSettings)}
                <span className={cn('rounded-full px-2 py-0.5 text-[11px] font-bold', sevBadge(r.severity))}>{severityText(r.severity)}</span>
                {r.overridden && <span className="rounded-full bg-primary/10 px-2 py-0.5 text-[11px] font-semibold text-primary">{isClusterLayer ? tSettings('inspection.overridden') : tSettings('inspection.changed')}</span>}
              </div>
              <div className="mt-0.5 font-mono text-xs text-text-subtle">{r.name}</div>
            </div>
            {r.overridden && <Button variant="outline" size="sm" className="h-8 text-xs" disabled={busyRule === r.name} onClick={() => resetRule(r)}>{isClusterLayer ? tSettings('inspection.resetInherited') : tSettings('inspection.resetDefault')}</Button>}
            {r.has_threshold && (
              <div className="flex items-center gap-1.5">
                <Input type="number" min={1} className="h-8 w-20" value={thresholdDrafts[r.name] ?? ''} disabled={busyRule === r.name}
                  onChange={(e) => setThresholdDrafts((d) => ({ ...d, [r.name]: e.target.value }))}
                  onKeyUp={(e) => e.key === 'Enter' && saveThreshold(r)} onBlur={() => saveThreshold(r)} />
                <span className="text-[12.5px] text-muted-foreground">{thresholdUnit(r, tSettings)}</span>
              </div>
            )}
            <Switch checked={r.enabled} disabled={busyRule === r.name} onCheckedChange={() => toggleRule(r)} />
          </RuleRow>
        ))}
      </RuleGroup>

      {/* Prometheus 指标规则 */}
      <RuleGroup title={tSettings('inspection.groups.prometheus')}>
        {promRules.map((r) => (
          <RuleRow key={r.name} unavailable={!prometheus.configured}>
            <div className="min-w-0 flex-1">
              <div className="flex flex-wrap items-center gap-2.5 text-sm font-semibold">
                {ruleDescription(r, tSettings)}
                <span className={cn('rounded-full px-2 py-0.5 text-[11px] font-bold', sevBadge(r.severity))}>{severityText(r.severity)}</span>
                {r.overridden && <span className="rounded-full bg-primary/10 px-2 py-0.5 text-[11px] font-semibold text-primary">{isClusterLayer ? tSettings('inspection.overridden') : tSettings('inspection.changed')}</span>}
              </div>
              <div className="mt-0.5 font-mono text-xs text-text-subtle">{r.name}</div>
            </div>
            {r.overridden && <Button variant="outline" size="sm" className="h-8 text-xs" disabled={busyRule === r.name} onClick={() => resetRule(r)}>{isClusterLayer ? tSettings('inspection.resetInherited') : tSettings('inspection.resetDefault')}</Button>}
            {r.has_threshold && (
              <div className="flex items-center gap-1.5">
                <Input type="number" min={1} max={100} className="h-8 w-20" value={thresholdDrafts[r.name] ?? ''} disabled={busyRule === r.name}
                  onChange={(e) => setThresholdDrafts((d) => ({ ...d, [r.name]: e.target.value }))}
                  onKeyUp={(e) => e.key === 'Enter' && saveThreshold(r)} onBlur={() => saveThreshold(r)} />
                <span className="text-[12.5px] text-muted-foreground">{thresholdUnit(r, tSettings)}</span>
              </div>
            )}
            <Switch checked={r.enabled} disabled={busyRule === r.name} onCheckedChange={() => toggleRule(r)} />
          </RuleRow>
        ))}
      </RuleGroup>

      {/* 自定义指标规则 */}
      <div className="mb-6">
        <div className="mb-1.5 flex items-center justify-between">
          <h3 className="text-sm font-bold text-muted-foreground">{tSettings('inspection.groups.custom')}</h3>
          <Button size="sm" disabled={!prometheus.configured} onClick={openCreateRule}><Plus className="size-4" /> {tSettings('inspection.custom.add')}</Button>
        </div>
        <p className="mb-3 text-[12.5px] text-muted-foreground">
          {!prometheus.configured ? tSettings('inspection.custom.requiresPrometheus') : tSettings('inspection.custom.description')}
        </p>
        {!customRules.length && <div className="rounded-xl border border-dashed border-border py-6 text-center text-sm text-muted-foreground">{tSettings('inspection.custom.empty')}</div>}
        {customRules.map((r) => (
          <RuleRow key={r.id} unavailable={!r.enabled}>
            <div className="min-w-0 flex-1">
              <div className="flex flex-wrap items-center gap-2.5 text-sm font-semibold">
                {r.name}
                <span className={cn('rounded-full px-2 py-0.5 text-[11px] font-bold', sevBadge(r.severity))}>{severityText(r.severity)}</span>
                {r.inherited && <span className="rounded-full bg-primary/10 px-2 py-0.5 text-[11px] font-semibold text-primary">{tSettings('inspection.custom.global')}</span>}
              </div>
              <div className="mt-0.5 truncate font-mono text-xs text-text-subtle">{r.promql} {r.operator} {r.threshold}</div>
            </div>
            {!r.inherited ? (
              <>
                <Button variant="outline" size="sm" className="h-8 text-xs" onClick={() => openEditRule(r)}>{tCommon('actions.edit')}</Button>
                <Button variant="outline" size="sm" className="h-8 text-xs hover:border-destructive hover:text-destructive" onClick={() => removeCustomRule(r)}>{tCommon('actions.delete')}</Button>
                <Switch checked={r.enabled} onCheckedChange={() => toggleCustomRule(r)} />
              </>
            ) : <span className="text-xs text-text-subtle">{tSettings('inspection.custom.managedGlobally')}</span>}
          </RuleRow>
        ))}
      </div>

      {/* 抽屉:新增/编辑自定义规则 */}
      <Sheet open={ruleDrawer} onOpenChange={setRuleDrawer}>
        <SheetContent className="flex w-full flex-col gap-0 sm:max-w-lg">
          <SheetHeader>
            <SheetTitle>{editingRuleId ? tSettings('inspection.custom.edit') : tSettings('inspection.custom.addTitle')}</SheetTitle>
            <SheetDescription>{tSettings('inspection.custom.drawerDescription')}</SheetDescription>
          </SheetHeader>
          <div className="flex-1 space-y-4 overflow-y-auto px-4 py-2">
            <div className="space-y-1.5"><Label>{tSettings('inspection.custom.fields.name')}</Label><Input value={ruleForm.name} onChange={(e) => setRuleForm({ ...ruleForm, name: e.target.value })} placeholder={tSettings('inspection.custom.placeholders.name')} /></div>
            <div className="space-y-1.5"><Label>{tSettings('inspection.custom.fields.promql')}</Label><Textarea rows={3} value={ruleForm.promql} onChange={(e) => setRuleForm({ ...ruleForm, promql: e.target.value })} placeholder={tSettings('inspection.custom.placeholders.promql')} className="font-mono text-xs" /></div>
            <div className="space-y-1.5"><Label>{tSettings('inspection.custom.fields.condition')}</Label>
              <div className="flex gap-2">
                <Select value={ruleForm.operator} onValueChange={(v) => setRuleForm({ ...ruleForm, operator: v })}>
                  <SelectTrigger className="w-24"><SelectValue /></SelectTrigger>
                  <SelectContent>{operatorOptions.map((op) => <SelectItem key={op} value={op}>{op}</SelectItem>)}</SelectContent>
                </Select>
                <Input type="number" step="any" className="flex-1" value={ruleForm.threshold} onChange={(e) => setRuleForm({ ...ruleForm, threshold: Number(e.target.value) })} placeholder={tSettings('inspection.custom.fields.threshold')} />
              </div>
            </div>
            <div className="grid grid-cols-2 gap-3">
              <div className="space-y-1.5"><Label>{tSettings('inspection.custom.fields.severity')}</Label>
                <Select value={ruleForm.severity} onValueChange={(v) => setRuleForm({ ...ruleForm, severity: v })}>
                  <SelectTrigger className="w-full"><SelectValue /></SelectTrigger>
                  <SelectContent><SelectItem value="warning">{severityText('warning')}</SelectItem><SelectItem value="critical">{severityText('critical')}</SelectItem></SelectContent>
                </Select>
              </div>
              <div className="space-y-1.5"><Label>{tSettings('inspection.custom.fields.enabled')}</Label>
                <Select value={String(ruleForm.enabled)} onValueChange={(v) => setRuleForm({ ...ruleForm, enabled: v === 'true' })}>
                  <SelectTrigger className="w-full"><SelectValue /></SelectTrigger>
                  <SelectContent><SelectItem value="true">{tSettings('inspection.custom.enabled')}</SelectItem><SelectItem value="false">{tSettings('inspection.custom.disabled')}</SelectItem></SelectContent>
                </Select>
              </div>
            </div>
            <div className="space-y-1.5"><Label>{tSettings('inspection.custom.fields.objectLabel')}</Label><Input value={ruleForm.object_label} onChange={(e) => setRuleForm({ ...ruleForm, object_label: e.target.value })} placeholder={tSettings('inspection.custom.placeholders.objectLabel')} /></div>
            <div className="space-y-1.5"><Label>{tSettings('inspection.custom.fields.description')}</Label><Input value={ruleForm.description} onChange={(e) => setRuleForm({ ...ruleForm, description: e.target.value })} placeholder={tSettings('inspection.custom.placeholders.description')} /></div>
            {testResult && (
              <div className={cn('rounded-lg px-3 py-2.5 text-[12.5px] leading-relaxed', testResult.ok ? 'bg-success-weak text-success-fg' : 'bg-danger-weak text-danger-fg')}>
                {!testResult.ok ? tSettings('inspection.custom.resultFailure', {
                  message: localizeApiError(testResult.apiError, tCommon, language, 'errors.actionFailed'),
                }) : (
                  <>
                    {tSettings('inspection.custom.resultSuccess', {
                      samples: testResult.sample_count,
                      matched: testResult.matched,
                    })}
                    {(testResult.preview || []).map((p: any, i: number) => (
                      <span key={i} className="mr-2.5 inline-block font-mono text-[11.5px]">{p.name}={Number(p.value).toPrecision(4)}{p.hit ? ' ✓' : ''}</span>
                    ))}
                  </>
                )}
              </div>
            )}
          </div>
          <SheetFooter className="flex-row justify-end gap-2">
            <Button variant="outline" onClick={runTest} disabled={testing}>{testing ? tSettings('inspection.custom.testing') : tSettings('inspection.custom.test')}</Button>
            <Button onClick={submitRule} disabled={ruleSaving}>{ruleSaving ? tSettings('inspection.prometheus.saving') : editingRuleId ? tCommon('actions.save') : tSettings('inspection.custom.creating')}</Button>
          </SheetFooter>
        </SheetContent>
      </Sheet>
    </div>
  )
}

function RuleGroup({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="mb-6">
      <h3 className="mb-2.5 text-sm font-bold text-muted-foreground">{title}</h3>
      {children}
    </div>
  )
}
