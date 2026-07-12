import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { RotateCw, Plus } from 'lucide-react'
import {
  createUser, deleteUser, listClusters, listUsers, resetUserPassword, updateUser,
} from '@/lib/api'
import { useAuth } from '@/providers/AuthProvider'
import { useConfirm } from '@/providers/ConfirmProvider'
import { notify } from '@/lib/toast'
import { normalizeLanguage } from '@/i18n'
import { localizeApiError, type CommonErrorKey } from '@/i18n/errors'
import { formatDateTime } from '@/i18n/format'
import { localizeStatus } from '@/i18n/status'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Select, SelectTrigger, SelectValue, SelectContent, SelectItem,
} from '@/components/ui/select'
import {
  Sheet, SheetContent, SheetHeader, SheetTitle, SheetDescription, SheetFooter,
} from '@/components/ui/sheet'
import {
  Table, TableHeader, TableBody, TableRow, TableHead, TableCell,
} from '@/components/ui/table'

const expiryOptions = [
  { value: '3m', key: 'users.expiry.threeMonths' },
  { value: '6m', key: 'users.expiry.sixMonths' },
  { value: '1y', key: 'users.expiry.oneYear' },
  { value: '3y', key: 'users.expiry.threeYears' },
] as const

const roleNeedsClusters = (r: string) => r === 'cluster_admin' || r === 'inspector'
const numericIds = (vals: string[]) => vals.map(Number).filter((v) => v > 0)
const userClusterIds = (u: any): number[] => (u.cluster_grants || []).map((g: any) => Number(g.cluster_id)).filter((v: number) => v > 0)
const userCode = (u: any) => `USR-${String(u.id || '').padStart(4, '0')}`

function toDateInput(v?: string) {
  if (!v) return ''
  const d = new Date(v); if (Number.isNaN(d.getTime())) return ''
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
}
function isExpired(u: any) {
  if (!u.expires_at) return false
  const e = new Date(u.expires_at); return !Number.isNaN(e.getTime()) && e.getTime() < Date.now()
}
function statusTone(u: any): 'success' | 'warning' | 'muted' {
  if (isExpired(u)) return 'warning'
  return u.status === 'active' ? 'success' : 'muted'
}
function StatusPill({ u, text }: { u: any; text: string }) {
  const tone = statusTone(u)
  const map = { success: 'bg-success-weak text-success-fg', warning: 'bg-warning-weak text-warning-fg', muted: 'bg-muted text-muted-foreground' }
  return <span className={cn('inline-block rounded-full px-2 py-0.5 text-[11px] font-medium', map[tone])}>{text}</span>
}

function RequiredFieldLabel({ children }: { children: ReactNode }) {
  return (
    <Label>
      {children}
      <span aria-hidden="true" className="text-destructive" data-required-marker="true"> *</span>
    </Label>
  )
}

type UserValidationKey =
  | 'users.validation.username'
  | 'users.validation.clusters'
  | 'users.validation.editRequired'

interface UserFormError {
  error?: unknown
  fallback?: CommonErrorKey
  key?: UserValidationKey
}

export function UsersPage() {
  const { t: tSettings, i18n } = useTranslation('settings')
  const { t: tCommon } = useTranslation('common')
  const { user: currentUser, logout, setLoginNotice } = useAuth()
  const confirm = useConfirm()
  const [users, setUsers] = useState<any[]>([])
  const [roles, setRoles] = useState<string[]>([])
  const [clusters, setClusters] = useState<any[]>([])
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)
  const [creating, setCreating] = useState(false)
  const [editing, setEditing] = useState<any>(null)
  const [formError, setFormError] = useState<UserFormError | null>(null)

  const [form, setForm] = useState({ username: '', displayName: '', email: '', role: 'inspector', expiresIn: '3m', clusterIds: [] as string[] })
  const [editForm, setEditForm] = useState({ displayName: '', email: '', expiresAt: '', clusterIds: [] as string[] })

  const language = normalizeLanguage(i18n.resolvedLanguage ?? i18n.language)
  const localization = useRef({ tSettings, tCommon, language })
  localization.current = { tSettings, tCommon, language }
  const roleOptions = roles.length ? roles : ['global_admin', 'cluster_admin', 'inspector']
  const formNeedsClusters = roleNeedsClusters(form.role)
  const editingNeedsClusters = editing && roleNeedsClusters(editing.role)
  const roleText = (role: string) => localizeStatus(tCommon, 'role', role)
  const statusText = (target: any) => isExpired(target)
    ? tSettings('users.expired')
    : localizeStatus(tCommon, 'userStatus', target.status)
  const formErrorMessage = formError?.key
    ? tSettings(formError.key)
    : formError?.error && formError.fallback
      ? localizeApiError(formError.error, tCommon, language, formError.fallback)
      : ''

  const loadUsers = useCallback(async () => {
    setLoading(true)
    try { const d = await listUsers(); setUsers(d.users || []); setRoles(d.roles || []) }
    catch (error: unknown) {
      setUsers([])
      const current = localization.current
      notify.error(localizeApiError(error, current.tCommon, current.language, 'errors.loadFailed'))
    }
    finally { setLoading(false) }
  }, [])

  useEffect(() => {
    listClusters().then((d) => setClusters(d.clusters || [])).catch(() => setClusters([]))
    loadUsers()
  }, [loadUsers])

  function openCreate() {
    setFormError(null)
    setForm({ username: '', displayName: '', email: '', role: 'inspector', expiresIn: '3m', clusterIds: [] })
    setCreating(true)
  }
  function toggleFormCluster(id: string) {
    setForm((f) => ({ ...f, clusterIds: f.clusterIds.includes(id) ? f.clusterIds.filter((x) => x !== id) : [...f.clusterIds, id] }))
  }
  function toggleEditCluster(id: string) {
    setEditForm((f) => ({ ...f, clusterIds: f.clusterIds.includes(id) ? f.clusterIds.filter((x) => x !== id) : [...f.clusterIds, id] }))
  }

  async function submitCreate() {
    if (saving) return
    setFormError(null)
    if (!form.username.trim()) { setFormError({ key: 'users.validation.username' }); return }
    if (formNeedsClusters && !numericIds(form.clusterIds).length) { setFormError({ key: 'users.validation.clusters' }); return }
    setSaving(true)
    try {
      await createUser({
        username: form.username.trim(), display_name: form.displayName.trim(), email: form.email.trim(),
        role: form.role, expires_in: form.expiresIn, cluster_ids: formNeedsClusters ? numericIds(form.clusterIds) : [],
      })
      setCreating(false); await loadUsers(); notify.success(localization.current.tSettings('users.feedback.created'))
    } catch (error: unknown) { setFormError({ error, fallback: 'errors.createFailed' }) }
    finally { setSaving(false) }
  }

  function startEdit(u: any) {
    setFormError(null)
    setEditing(u)
    setEditForm({ displayName: u.display_name || u.username, email: u.email || '', expiresAt: toDateInput(u.expires_at), clusterIds: userClusterIds(u).map(String) })
  }

  async function submitEdit() {
    if (!editing || saving) return
    setFormError(null)
    if (!editForm.displayName.trim() || !editForm.expiresAt) { setFormError({ key: 'users.validation.editRequired' }); return }
    if (editingNeedsClusters && !numericIds(editForm.clusterIds).length) { setFormError({ key: 'users.validation.clusters' }); return }
    setSaving(true)
    try {
      await updateUser(editing.id, {
        display_name: editForm.displayName.trim(), email: editForm.email.trim(), expires_at: editForm.expiresAt,
        cluster_ids: editingNeedsClusters ? numericIds(editForm.clusterIds) : [],
      })
      setEditing(null); await loadUsers(); notify.success(localization.current.tSettings('users.feedback.updated'))
    } catch (error: unknown) { setFormError({ error, fallback: 'errors.updateFailed' }) }
    finally { setSaving(false) }
  }

  async function toggleStatus(u: any) {
    try { await updateUser(u.id, { status: u.status === 'active' ? 'disabled' : 'active' }); await loadUsers() }
    catch (error: unknown) {
      const current = localization.current
      notify.error(localizeApiError(error, current.tCommon, current.language, 'errors.updateFailed'))
    }
  }
  async function resetPassword(u: any) {
    const ok = await confirm({
      title: tSettings('users.confirmations.resetTitle'),
      message: tSettings('users.confirmations.resetMessage', { username: u.username }),
      confirmText: tSettings('users.confirmations.resetAction'),
    })
    if (!ok) return
    try {
      await resetUserPassword(u.id)
      if (String(currentUser?.id || '') === String(u.id)) {
        await logout(); setLoginNotice({ kind: 'passwordReset' }); return
      }
      notify.success(localization.current.tSettings('users.feedback.reset')); await loadUsers()
    } catch (error: unknown) {
      const current = localization.current
      notify.error(localizeApiError(error, current.tCommon, current.language, 'errors.actionFailed'))
    }
  }
  async function remove(u: any) {
    const ok = await confirm({
      title: tSettings('users.confirmations.deleteTitle'),
      message: tSettings('users.confirmations.deleteMessage', { username: u.username }),
      tone: 'danger',
      confirmText: tCommon('actions.delete'),
    })
    if (!ok) return
    try {
      await deleteUser(u.id)
      if (editing?.id === u.id) setEditing(null)
      notify.success(localization.current.tSettings('users.feedback.deleted')); await loadUsers()
    } catch (error: unknown) {
      const current = localization.current
      notify.error(localizeApiError(error, current.tCommon, current.language, 'errors.deleteFailed'))
    }
  }

  function grantClusterText(u: any) {
    if (u.role === 'global_admin') return tSettings('users.allClusters')
    const ids = userClusterIds(u)
    if (!ids.length) return '-'
    const separator = language === 'zh' ? '、' : ', '
    return ids.map((id) => clusters.find((c) => Number(c.id) === id)?.name || tSettings('users.clusterFallback', { id })).join(separator)
  }

  return (
    <div className="px-8 py-7">
      <div className="mb-4 flex items-end justify-between">
        <div>
          <h2 className="text-xl font-bold tracking-tight">{tSettings('users.title')}</h2>
          <p className="mt-1 text-sm text-muted-foreground">{tSettings('users.subtitle')}</p>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" onClick={loadUsers} disabled={loading}><RotateCw className={cn('size-4', loading && 'animate-spin')} /> {tCommon('actions.refresh')}</Button>
          <Button size="sm" onClick={openCreate}><Plus className="size-4" /> {tSettings('users.add')}</Button>
        </div>
      </div>

      <div className="overflow-hidden rounded-xl border border-border bg-card shadow-sm">
        <div className="overflow-x-auto">
          <Table className="min-w-[1500px]">
            <TableHeader>
              <TableRow className="hover:bg-transparent">
                {[
                  tSettings('users.columns.code'), tSettings('users.columns.username'),
                  tSettings('users.columns.name'), tSettings('users.columns.email'),
                  tSettings('users.columns.role'), tSettings('users.columns.clusters'),
                  tSettings('users.columns.status'), tSettings('users.columns.expiresAt'),
                  tSettings('users.columns.lastLogin'), tSettings('users.columns.passwordAttemptedAt'),
                  tSettings('users.columns.passwordAttempts'), tSettings('users.columns.createdAt'),
                ].map((heading) => <TableHead key={heading}>{heading}</TableHead>)}
                <TableHead className="text-right">{tSettings('users.columns.actions')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {!users.length && !loading && <TableRow><TableCell colSpan={13} className="py-10 text-center text-sm text-muted-foreground">{tSettings('users.empty')}</TableCell></TableRow>}
              {users.map((u) => (
                <TableRow key={u.id}>
                  <TableCell className="font-mono text-xs">{userCode(u)}</TableCell>
                  <TableCell className="font-mono text-xs">{u.username}</TableCell>
                  <TableCell>{u.display_name || '-'}</TableCell>
                  <TableCell className="text-muted-foreground">{u.email || '-'}</TableCell>
                  <TableCell>{roleText(u.role)}</TableCell>
                  <TableCell className="max-w-[160px] truncate text-muted-foreground">{grantClusterText(u)}</TableCell>
                  <TableCell><StatusPill u={u} text={statusText(u)} /></TableCell>
                  <TableCell className="whitespace-nowrap text-xs text-muted-foreground">{formatDateTime(u.expires_at, language)}</TableCell>
                  <TableCell className="whitespace-nowrap text-xs text-muted-foreground">{formatDateTime(u.last_login_at, language)}</TableCell>
                  <TableCell className="whitespace-nowrap text-xs text-muted-foreground">{formatDateTime(u.password_attempted_at, language)}</TableCell>
                  <TableCell className="text-center">{u.password_attempt_count || 0}</TableCell>
                  <TableCell className="whitespace-nowrap text-xs text-muted-foreground">{formatDateTime(u.created_at, language)}</TableCell>
                  <TableCell className="text-right">
                    <div className="flex justify-end gap-1">
                      <Button variant="ghost" size="sm" className="h-7 px-2 text-xs" onClick={() => startEdit(u)}>{tSettings('users.edit')}</Button>
                      <Button variant="ghost" size="sm" className="h-7 px-2 text-xs" onClick={() => toggleStatus(u)}>{u.status === 'active' ? tSettings('users.disable') : tSettings('users.enable')}</Button>
                      <Button variant="ghost" size="sm" className="h-7 px-2 text-xs" onClick={() => resetPassword(u)}>{tSettings('users.resetPassword')}</Button>
                      <Button variant="ghost" size="sm" className="h-7 px-2 text-xs text-destructive hover:text-destructive" onClick={() => remove(u)}>{tSettings('users.delete')}</Button>
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      </div>

      {/* 新增用户抽屉 */}
      <Sheet open={creating} onOpenChange={setCreating}>
        <SheetContent className="flex w-full flex-col gap-0 sm:max-w-md">
          <SheetHeader><SheetTitle>{tSettings('users.createDrawer.title')}</SheetTitle><SheetDescription>{tSettings('users.createDrawer.description')}</SheetDescription></SheetHeader>
          <div className="flex-1 space-y-4 overflow-y-auto px-4 py-2">
            <div className="space-y-1.5"><RequiredFieldLabel>{tSettings('users.fields.username')}</RequiredFieldLabel><Input required value={form.username} onChange={(e) => setForm({ ...form, username: e.target.value })} placeholder={tSettings('users.placeholders.username')} /></div>
            <div className="space-y-1.5"><Label>{tSettings('users.fields.name')}</Label><Input value={form.displayName} onChange={(e) => setForm({ ...form, displayName: e.target.value })} placeholder={tSettings('users.placeholders.name')} /></div>
            <div className="space-y-1.5"><Label>{tSettings('users.fields.email')}</Label><Input type="email" value={form.email} onChange={(e) => setForm({ ...form, email: e.target.value })} placeholder={tSettings('users.placeholders.email')} /></div>
            <div className="grid grid-cols-2 gap-3">
              <div className="space-y-1.5"><Label>{tSettings('users.fields.role')}</Label>
                <Select value={form.role} onValueChange={(v) => setForm({ ...form, role: v, clusterIds: roleNeedsClusters(v) ? form.clusterIds : [] })}>
                  <SelectTrigger className="w-full"><SelectValue /></SelectTrigger>
                  <SelectContent>{roleOptions.map((r) => <SelectItem key={r} value={r}>{roleText(r)}</SelectItem>)}</SelectContent>
                </Select>
              </div>
              <div className="space-y-1.5"><Label>{tSettings('users.fields.expiry')}</Label>
                <Select value={form.expiresIn} onValueChange={(v) => setForm({ ...form, expiresIn: v })}>
                  <SelectTrigger className="w-full"><SelectValue /></SelectTrigger>
                  <SelectContent>{expiryOptions.map((option) => <SelectItem key={option.value} value={option.value}>{tSettings(option.key)}</SelectItem>)}</SelectContent>
                </Select>
              </div>
            </div>
            {formNeedsClusters && (
              <div className="space-y-2">
                <RequiredFieldLabel>{tSettings('users.fields.clusters')}</RequiredFieldLabel>
                <div role="group" aria-label={tSettings('users.fields.clusters')} aria-required="true" className="grid grid-cols-2 gap-2 rounded-lg border border-border p-3">
                  {clusters.map((c) => (
                    <label key={c.id} className="flex cursor-pointer items-center gap-2 text-sm">
                      <Checkbox checked={form.clusterIds.includes(String(c.id))} onCheckedChange={() => toggleFormCluster(String(c.id))} />
                      <span className="truncate">{c.name}</span>
                    </label>
                  ))}
                  {!clusters.length && <span className="col-span-2 text-xs text-muted-foreground">{tCommon('common.empty')}</span>}
                </div>
              </div>
            )}
            {formErrorMessage && <div className="rounded-lg bg-danger-weak px-3 py-2 text-sm text-danger-fg">{formErrorMessage}</div>}
          </div>
          <SheetFooter className="flex-row justify-end gap-2">
            <Button variant="outline" onClick={() => setCreating(false)}>{tCommon('actions.cancel')}</Button>
            <Button onClick={submitCreate} disabled={saving}>{saving ? tSettings('users.saving') : tSettings('users.createDrawer.create')}</Button>
          </SheetFooter>
        </SheetContent>
      </Sheet>

      {/* 编辑用户抽屉 */}
      <Sheet open={Boolean(editing)} onOpenChange={(o) => { if (!o) setEditing(null) }}>
        <SheetContent className="flex w-full flex-col gap-0 sm:max-w-md">
          <SheetHeader><SheetTitle>{tSettings('users.editDrawer.title')}</SheetTitle><SheetDescription>{editing && `${userCode(editing)} · ${editing.username} · ${roleText(editing.role)}`}</SheetDescription></SheetHeader>
          {editing && (
            <div className="flex-1 space-y-4 overflow-y-auto px-4 py-2">
              <div className="space-y-1.5"><RequiredFieldLabel>{tSettings('users.fields.name')}</RequiredFieldLabel><Input required value={editForm.displayName} onChange={(e) => setEditForm({ ...editForm, displayName: e.target.value })} /></div>
              <div className="space-y-1.5"><Label>{tSettings('users.fields.email')}</Label><Input type="email" value={editForm.email} onChange={(e) => setEditForm({ ...editForm, email: e.target.value })} placeholder={tSettings('users.placeholders.editEmail')} /></div>
              <div className="space-y-1.5"><RequiredFieldLabel>{tSettings('users.fields.expiresAt')}</RequiredFieldLabel><Input required type="date" value={editForm.expiresAt} onChange={(e) => setEditForm({ ...editForm, expiresAt: e.target.value })} /></div>
              {editingNeedsClusters && (
                <div className="space-y-2">
                  <RequiredFieldLabel>{tSettings('users.fields.clusters')}</RequiredFieldLabel>
                  <div role="group" aria-label={tSettings('users.fields.clusters')} aria-required="true" className="grid grid-cols-2 gap-2 rounded-lg border border-border p-3">
                    {clusters.map((c) => (
                      <label key={c.id} className="flex cursor-pointer items-center gap-2 text-sm">
                        <Checkbox checked={editForm.clusterIds.includes(String(c.id))} onCheckedChange={() => toggleEditCluster(String(c.id))} />
                        <span className="truncate">{c.name}</span>
                      </label>
                    ))}
                  </div>
                </div>
              )}
              <div className="space-y-2 rounded-lg bg-muted/50 p-3 text-xs text-muted-foreground">
                <div className="flex justify-between"><span>{tSettings('users.fields.status')}</span><StatusPill u={editing} text={statusText(editing)} /></div>
                <div className="flex justify-between"><span>{tSettings('users.fields.lastLogin')}</span><b className="text-foreground">{formatDateTime(editing.last_login_at, language)}</b></div>
                <div className="flex justify-between"><span>{tSettings('users.fields.passwordAttemptedAt')}</span><b className="text-foreground">{formatDateTime(editing.password_attempted_at, language)}</b></div>
                <div className="flex justify-between"><span>{tSettings('users.fields.passwordAttempts')}</span><b className="text-foreground">{editing.password_attempt_count || 0}</b></div>
                <div className="flex justify-between"><span>{tSettings('users.fields.createdAt')}</span><b className="text-foreground">{formatDateTime(editing.created_at, language)}</b></div>
                <div className="flex justify-between"><span>{tSettings('users.fields.updatedAt')}</span><b className="text-foreground">{formatDateTime(editing.updated_at, language)}</b></div>
              </div>
              {formErrorMessage && <div className="rounded-lg bg-danger-weak px-3 py-2 text-sm text-danger-fg">{formErrorMessage}</div>}
            </div>
          )}
          <SheetFooter className="flex-row justify-end gap-2">
            <Button variant="outline" onClick={() => setEditing(null)}>{tCommon('actions.cancel')}</Button>
            <Button onClick={submitEdit} disabled={saving}>{saving ? tSettings('users.saving') : tCommon('actions.confirm')}</Button>
          </SheetFooter>
        </SheetContent>
      </Sheet>
    </div>
  )
}
