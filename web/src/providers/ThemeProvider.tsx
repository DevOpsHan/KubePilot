import { createContext, useContext, useEffect, useState, type ReactNode } from 'react'

type Preference = 'light' | 'dark' | 'auto'
type Mode = 'light' | 'dark'

interface ThemeCtx {
  preference: Preference
  mode: Mode
  setPreference: (p: Preference) => void
  toggle: () => void
}

const Ctx = createContext<ThemeCtx>(null as unknown as ThemeCtx)
const STORAGE_KEY = 'aiops-cloud-theme-mode'

function systemMode(): Mode {
  if (typeof window === 'undefined' || !window.matchMedia) return 'light'
  return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
}
function resolveMode(p: Preference): Mode {
  return p === 'auto' ? systemMode() : p
}
function loadPreference(): Preference {
  try {
    const s = localStorage.getItem(STORAGE_KEY)
    if (s === 'light' || s === 'dark' || s === 'auto') return s
  } catch { /* ignore */ }
  return 'light'
}

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [preference, setPref] = useState<Preference>(loadPreference)
  const [mode, setMode] = useState<Mode>(() => resolveMode(loadPreference()))

  function apply(p: Preference) {
    const m = resolveMode(p)
    setPref(p)
    setMode(m)
    try { localStorage.setItem(STORAGE_KEY, p) } catch { /* ignore */ }
    const root = document.documentElement
    root.classList.toggle('dark', m === 'dark')
    root.dataset.aiopsTheme = m
    root.dataset.aiopsThemePreference = p
  }

  useEffect(() => { apply(preference) /* eslint-disable-next-line */ }, [])

  useEffect(() => {
    if (!window.matchMedia) return
    const mq = window.matchMedia('(prefers-color-scheme: dark)')
    const listener = () => { if (preference === 'auto') apply('auto') }
    mq.addEventListener('change', listener)
    return () => mq.removeEventListener('change', listener)
  }, [preference])

  return (
    <Ctx.Provider value={{ preference, mode, setPreference: apply, toggle: () => apply(mode === 'dark' ? 'light' : 'dark') }}>
      {children}
    </Ctx.Provider>
  )
}

export const useTheme = () => useContext(Ctx)
