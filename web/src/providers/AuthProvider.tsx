import { createContext, useCallback, useContext, useEffect, useRef, useState, type ReactNode } from 'react'
import { getCurrentUser, logout as logoutApi } from '@/lib/api'

interface User {
  username: string
  display_name?: string
  role?: string
  [k: string]: unknown
}

export type SessionReason = 'idle' | 'unauthorized'

export type LoginNotice =
  | { kind: 'session'; reason: SessionReason }
  | { kind: 'passwordReset' }
  | { kind: 'raw'; message: string }
  | null

export type ManualLoginNotice =
  | { kind: 'passwordReset' }
  | { kind: 'raw'; message: string }

interface AuthCtx {
  user: User | null
  permissions: string[]
  loading: boolean
  authenticated: boolean
  can: (p: string) => boolean
  refresh: () => Promise<void>
  onLoggedIn: (user: User | null) => Promise<void>
  logout: () => Promise<void>
  loginNotice: LoginNotice
  setLoginNotice: (notice: ManualLoginNotice) => void
  expiredReason: SessionReason | null
  dismissExpired: () => void
}

const Ctx = createContext<AuthCtx | null>(null)
const IDLE_MS = 30 * 60 * 1000

function isSessionReason(value: unknown): value is SessionReason {
  return value === 'idle' || value === 'unauthorized'
}

function sessionReasonFromEvent(event: Event): SessionReason | undefined {
  if (!(event instanceof CustomEvent)) return undefined
  const detail: unknown = event.detail
  if (typeof detail !== 'object' || detail === null || !('reason' in detail)) return undefined
  return isSessionReason(detail.reason) ? detail.reason : undefined
}

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<User | null>(null)
  const [permissions, setPermissions] = useState<string[]>([])
  const [loading, setLoading] = useState(true)
  const [loginNotice, setLoginNoticeState] = useState<LoginNotice>(null)
  const [expiredReason, setExpiredReason] = useState<SessionReason | null>(null)
  const idleTimer = useRef<number | null>(null)
  const lastActivity = useRef(0)
  const authed = Boolean(user)

  const can = useCallback((p: string) => permissions.includes(p), [permissions])

  const refresh = useCallback(async () => {
    try {
      const data = await getCurrentUser()
      setUser(data.user || null)
      setPermissions(data.permissions || [])
    } catch {
      setUser(null)
      setPermissions([])
    }
  }, [])

  const clearIdle = useCallback(() => {
    if (idleTimer.current) { window.clearTimeout(idleTimer.current); idleTimer.current = null }
  }, [])

  const setLoginNotice = useCallback((notice: ManualLoginNotice) => {
    setLoginNoticeState(notice)
  }, [])

  const clearState = useCallback((notice: LoginNotice = null) => {
    setUser(null)
    setPermissions([])
    setExpiredReason(null)
    setLoginNoticeState(notice)
    clearIdle()
    if (window.location.hash !== '#/home') window.location.hash = '#/home'
  }, [clearIdle])

  const resetIdle = useCallback(() => {
    clearIdle()
    if (!authed || expiredReason) return
    idleTimer.current = window.setTimeout(() => {
      idleTimer.current = null
      setExpiredReason('idle')
    }, IDLE_MS)
  }, [authed, clearIdle, expiredReason])

  const onLoggedIn = useCallback(async (u: User | null) => {
    setExpiredReason(null)
    setLoginNoticeState(null)
    setUser(u)
    await refresh()
  }, [refresh])

  const logout = useCallback(async () => {
    try { await logoutApi() } catch { /* local logout */ }
    clearState()
  }, [clearState])

  const dismissExpired = useCallback(() => {
    const notice: LoginNotice = expiredReason
      ? { kind: 'session', reason: expiredReason }
      : null
    clearState(notice)
  }, [expiredReason, clearState])

  // 初始化当前用户。
  useEffect(() => {
    let active = true
    void refresh().finally(() => {
      if (active) setLoading(false)
    })
    return () => { active = false }
  }, [refresh])

  // 将 API 事件转换为与语言无关的会话原因。
  useEffect(() => {
    const onExpired = (e: Event) => {
      const reason = sessionReasonFromEvent(e)
      if (!authed || !reason) return
      clearIdle()
      setExpiredReason(reason)
    }
    window.addEventListener('aiops-auth-expired', onExpired)
    return () => window.removeEventListener('aiops-auth-expired', onExpired)
  }, [authed, clearIdle])

  // 用户活动仅重置空闲计时器，不改变会话语义状态。
  useEffect(() => {
    const onActivity = () => {
      const now = Date.now()
      if (now - lastActivity.current < 1000) return
      lastActivity.current = now
      resetIdle()
    }
    window.addEventListener('click', onActivity)
    window.addEventListener('keydown', onActivity)
    window.addEventListener('mousemove', onActivity)
    window.addEventListener('scroll', onActivity, true)
    return () => {
      window.removeEventListener('click', onActivity)
      window.removeEventListener('keydown', onActivity)
      window.removeEventListener('mousemove', onActivity)
      window.removeEventListener('scroll', onActivity, true)
    }
  }, [resetIdle])

  useEffect(() => {
    if (authed && !expiredReason) resetIdle()
    else clearIdle()
    return clearIdle
  }, [authed, clearIdle, expiredReason, resetIdle])

  return (
    <Ctx.Provider value={{
      user, permissions, loading, authenticated: authed, can, refresh, onLoggedIn, logout,
      loginNotice, setLoginNotice, expiredReason, dismissExpired,
    }}>
      {children}
    </Ctx.Provider>
  )
}

export function useAuth(): AuthCtx {
  const context = useContext(Ctx)
  if (!context) throw new Error('useAuth must be used within AuthProvider')
  return context
}
