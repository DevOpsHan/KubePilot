import { useState } from 'react'
import { Server, Zap, ShieldCheck } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { normalizeLanguage } from '@/i18n'
import { localizeApiError, type CommonErrorKey } from '@/i18n/errors'
import { login as loginApi, changePassword } from '@/lib/api'
import { useAuth, type SessionReason } from '@/providers/AuthProvider'
import { notify } from '@/lib/toast'
import { ThemeSwitcher } from '@/components/ThemeSwitcher'
import { LanguageSwitcher } from '@/components/LanguageSwitcher'
import { LoginBackground } from '@/components/LoginBackground'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

const features = [
  { icon: Server, titleKey: 'login.feat1Title', descKey: 'login.feat1Desc' },
  { icon: Zap, titleKey: 'login.feat2Title', descKey: 'login.feat2Desc' },
  { icon: ShieldCheck, titleKey: 'login.feat3Title', descKey: 'login.feat3Desc' },
] as const

type LocalErrorKey =
  | 'login.fillBoth'
  | 'login.signInFailed'
  | 'login.needBoth'
  | 'login.mismatch'
  | 'login.sameAsOld'
  | 'login.changeFailed'

type LoginError =
  | { kind: 'local'; key: LocalErrorKey }
  | { kind: 'api'; error: unknown; fallbackKey: CommonErrorKey }
  | null

function sessionReasonKey(reason: SessionReason): 'session.idle' | 'session.unauthorized' {
  return reason === 'idle' ? 'session.idle' : 'session.unauthorized'
}

// 按当前小时选择问候语:5-12 早上,12-18 下午,其余晚上。
function greetingKey(hour: number): 'greeting.morning' | 'greeting.afternoon' | 'greeting.evening' {
  if (hour >= 5 && hour < 12) return 'greeting.morning'
  if (hour >= 12 && hour < 18) return 'greeting.afternoon'
  return 'greeting.evening'
}

function apiError(error: unknown, fallbackKey: CommonErrorKey): LoginError {
  return { kind: 'api', error, fallbackKey }
}

export function LoginPage() {
  const { onLoggedIn, loginNotice } = useAuth()
  const { t } = useTranslation('auth')
  const { t: tCommon, i18n } = useTranslation('common')
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState<LoginError>(null)
  const [busy, setBusy] = useState(false)
  // 首次登录改密
  const [mustChange, setMustChange] = useState(false)
  const [newPassword, setNewPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')

  // 登录成功后弹出按时段问候。用户名回退到输入框内容。
  function greet(user: { username?: string; display_name?: string } | null | undefined) {
    const name = user?.display_name || user?.username || username.trim()
    if (!name) return
    notify.success(t('greeting.welcome', { greeting: t(greetingKey(new Date().getHours())), name }))
  }

  async function submitLogin(e: React.FormEvent) {
    e.preventDefault()
    setError(null)
    if (!username.trim() || !password.trim()) {
      setError({ kind: 'local', key: 'login.fillBoth' })
      return
    }
    setBusy(true)
    try {
      const data = await loginApi(username.trim(), password)
      if (data.password_change_required) { setMustChange(true); return }
      greet(data.user)
      await onLoggedIn(data.user)
    } catch (err: unknown) {
      setError(apiError(err, 'errors.signInFailed'))
    } finally { setBusy(false) }
  }

  async function submitChange(e: React.FormEvent) {
    e.preventDefault()
    setError(null)
    if (!newPassword.trim() || !confirmPassword.trim()) {
      setError({ kind: 'local', key: 'login.needBoth' })
      return
    }
    if (newPassword !== confirmPassword) {
      setError({ kind: 'local', key: 'login.mismatch' })
      return
    }
    if (newPassword === password) {
      setError({ kind: 'local', key: 'login.sameAsOld' })
      return
    }
    setBusy(true)
    try {
      await changePassword(username.trim(), password, newPassword)
      const data = await loginApi(username.trim(), newPassword)
      greet(data.user)
      await onLoggedIn(data.user)
    } catch (err: unknown) {
      setError(apiError(err, 'errors.changePasswordFailed'))
    } finally { setBusy(false) }
  }

  const language = normalizeLanguage(i18n.resolvedLanguage ?? i18n.language)
  const errorMessage = error?.kind === 'local'
    ? t(error.key)
    : error?.kind === 'api'
      ? localizeApiError(error.error, tCommon, language, error.fallbackKey)
      : undefined
  const loginNoticeMessage = loginNotice?.kind === 'session'
    ? t(sessionReasonKey(loginNotice.reason))
    : loginNotice?.kind === 'passwordReset'
      ? t('login.passwordResetNotice')
    : loginNotice?.kind === 'raw' ? loginNotice.message : undefined

  return (
    <div className="relative min-h-screen overflow-hidden bg-background">
      <LoginBackground />

      {/* 右上角:主题 + 语言切换 */}
      <div className="absolute right-5 top-5 z-20 flex items-center gap-2">
        <ThemeSwitcher />
        <LanguageSwitcher />
      </div>

      <div className="relative z-10 mx-auto flex min-h-screen max-w-6xl items-center justify-center gap-10 px-6 lg:justify-between lg:px-12">
        {/* 左:品牌插画区(叠在动态背景之上) */}
        <div className="hidden max-w-lg flex-1 lg:block">
          <div className="mb-4 inline-flex items-center gap-2 rounded-full bg-primary/10 px-3 py-1 text-xs font-medium text-primary">
            <span className="size-2 rounded-full bg-primary" /> {t('login.badge')}
          </div>
          <h1 className="text-4xl font-extrabold leading-tight tracking-tight text-foreground">
            {t('login.headline1')}<br />{t('login.headline2')}
          </h1>
          <p className="mt-3 text-sm text-muted-foreground">{t('login.subtitle')}</p>
          <div className="mt-8 space-y-3">
            {features.map((f) => {
              const Icon = f.icon
              return (
                <div key={f.titleKey} className="flex items-center gap-3 rounded-xl border border-border bg-card/60 px-4 py-3 backdrop-blur">
                  <div className="grid size-9 place-items-center rounded-lg text-white" style={{ background: 'var(--gradient-brand)' }}>
                    <Icon className="size-4" />
                  </div>
                  <div>
                    <div className="text-sm font-semibold">{t(f.titleKey)}</div>
                    <div className="text-xs text-muted-foreground">{t(f.descKey)}</div>
                  </div>
                </div>
              )
            })}
          </div>
        </div>

        {/* 右:登录卡 */}
        <div className="w-full max-w-md shrink-0">
          <div className="rounded-2xl border border-border bg-card/95 p-8 shadow-[0_18px_50px_rgba(15,23,42,0.14)] backdrop-blur">
            <div className="mb-7 flex items-center gap-3">
              <img src="/favicon.svg" alt="" aria-hidden="true" className="size-12 shrink-0 object-contain" />
              <div>
                <div className="text-2xl font-bold">KubePilot</div>
                <div className="text-xs text-muted-foreground">{t('login.tagline')}</div>
              </div>
            </div>

            {loginNoticeMessage && (
              <div className="mb-4 rounded-lg bg-warning-weak px-3 py-2 text-sm text-warning-fg">
                {loginNoticeMessage}
              </div>
            )}

            {!mustChange ? (
              <form onSubmit={submitLogin} className="space-y-4">
                <div className="space-y-1.5">
                  <Label>{t('login.username')}</Label>
                  <Input value={username} onChange={(e) => setUsername(e.target.value)} autoFocus placeholder={t('login.usernamePlaceholder')} />
                </div>
                <div className="space-y-1.5">
                  <Label>{t('login.password')}</Label>
                  <Input type="password" value={password} onChange={(e) => setPassword(e.target.value)} placeholder={t('login.passwordPlaceholder')} />
                </div>
                {errorMessage && <div className="rounded-lg bg-destructive/10 px-3 py-2 text-sm text-destructive">{errorMessage}</div>}
                <Button type="submit" className="h-11 w-full" disabled={busy}>{busy ? t('login.signingIn') : t('login.signIn')}</Button>
              </form>
            ) : (
              <form onSubmit={submitChange} className="space-y-4">
                <p className="text-sm text-muted-foreground">{t('login.changeHint')}</p>
                <div className="space-y-1.5">
                  <Label>{t('login.newPassword')}</Label>
                  <Input type="password" value={newPassword} onChange={(e) => setNewPassword(e.target.value)} autoFocus placeholder={t('login.newPasswordPlaceholder')} />
                </div>
                <div className="space-y-1.5">
                  <Label>{t('login.confirmPassword')}</Label>
                  <Input type="password" value={confirmPassword} onChange={(e) => setConfirmPassword(e.target.value)} placeholder={t('login.passwordPlaceholder')} />
                </div>
                {errorMessage && <div className="rounded-lg bg-destructive/10 px-3 py-2 text-sm text-destructive">{errorMessage}</div>}
                <Button type="submit" className="h-11 w-full" disabled={busy}>{busy ? t('login.changing') : t('login.changeSubmit')}</Button>
              </form>
            )}
          </div>
        </div>
      </div>
    </div>
  )
}
