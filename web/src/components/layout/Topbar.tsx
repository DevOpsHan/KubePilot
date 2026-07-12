import { useLocation } from 'react-router-dom'
import { Search } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { titleTranslationKeys, viewTitleKeys } from './nav'
import { useAuth } from '@/providers/AuthProvider'
import { ThemeSwitcher } from '@/components/ThemeSwitcher'
import { LanguageSwitcher } from '@/components/LanguageSwitcher'
import { localizeStatus } from '@/i18n/status'

export function Topbar({ alertCount, pendingCount, onSearch }: { alertCount: number; pendingCount: number; onSearch: () => void }) {
  const { user } = useAuth()
  const { t } = useTranslation('common')
  const { pathname } = useLocation()
  const base = '/' + (pathname.split('/')[1] || 'dashboard')
  const titleKey = viewTitleKeys[base] || 'dashboard'

  return (
    <header className="flex h-[60px] shrink-0 items-center justify-between border-b border-border bg-card/60 px-6 backdrop-blur">
      <nav className="flex items-center gap-2 text-sm">
        <span className="font-semibold text-muted-foreground">KubePilot</span>
        <span className="text-border">/</span>
        <strong className="font-semibold">{t(titleTranslationKeys[titleKey])}</strong>
      </nav>
      <div className="flex items-center gap-3 text-xs text-muted-foreground">
        <button onClick={onSearch}
          className="flex items-center gap-2 rounded-lg border border-border bg-background px-3 py-1.5 transition-colors hover:border-primary hover:text-foreground">
          <Search className="size-3.5" /> {t('topbar.search')}
          <kbd className="rounded bg-muted px-1.5 py-0.5 font-mono text-[10px]">⌘K</kbd>
        </button>
        <span className="hidden sm:inline">{t('topbar.alerts')} {alertCount}</span>
        <span className="hidden sm:inline">{t('topbar.pending')} {pendingCount}</span>
        <ThemeSwitcher />
        <LanguageSwitcher />
        {user && (
          <span className="font-medium text-foreground">
            {user.username} · {localizeStatus(t, 'role', user.role)}
          </span>
        )}
      </div>
    </header>
  )
}
