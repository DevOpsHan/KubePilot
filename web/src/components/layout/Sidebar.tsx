import { useState } from 'react'
import { NavLink, useLocation } from 'react-router-dom'
import { LogOut, ChevronDown } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import {
  navGroups,
  navTranslationKeys,
  type CollapsibleNavKey,
  type NavItem,
  type NavGroup,
} from './nav'
import { useAuth } from '@/providers/AuthProvider'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'

const COLLAPSE_KEY = 'aiops-nav-collapsed'
const DEFAULT_COLLAPSED: Record<CollapsibleNavKey, boolean> = { ops: true }

function readCollapsedGroups(): Record<CollapsibleNavKey, boolean> {
  try {
    const stored = localStorage.getItem(COLLAPSE_KEY)
    if (!stored) return DEFAULT_COLLAPSED

    const parsed: unknown = JSON.parse(stored)
    if (typeof parsed === 'object' && parsed !== null && 'ops' in parsed
      && typeof parsed.ops === 'boolean') {
      return { ops: parsed.ops }
    }
  } catch {
    // 存储不可用或内容无效时使用默认折叠状态。
  }
  return DEFAULT_COLLAPSED
}

function CountBadge({ n }: { n: number }) {
  if (n <= 0) return null
  return (
    <Badge variant="secondary" className="h-5 min-w-5 justify-center rounded-full bg-primary px-1.5 text-[11px] text-primary-foreground">
      {n}
    </Badge>
  )
}

export function Sidebar({ alertCount, pendingCount }: { alertCount: number; pendingCount: number }) {
  const { user, can, logout } = useAuth()
  const { t } = useTranslation('common')
  const { pathname } = useLocation()

  const [collapsed, setCollapsed] = useState<Record<CollapsibleNavKey, boolean>>(readCollapsedGroups)

  function toggleGroup(key: CollapsibleNavKey) {
    setCollapsed((prev) => {
      const next = { ...prev, [key]: !prev[key] }
      try { localStorage.setItem(COLLAPSE_KEY, JSON.stringify(next)) } catch { /* ignore */ }
      return next
    })
  }

  const badgeOf = (item: NavItem) => (item.badge === 'alerts' ? alertCount : item.badge === 'pending' ? pendingCount : 0)
  const isActive = (to: string) => pathname === to || pathname.startsWith(to + '/')

  function renderItem(item: NavItem) {
    if (item.permission && !can(item.permission)) return null
    const Icon = item.icon
    return (
      <NavLink key={item.to} to={item.to} className={({ isActive: active }) => cn(
        'flex items-center gap-3 rounded-lg px-3 py-2 text-sm font-medium transition-colors',
        active
          ? 'bg-sidebar-accent text-sidebar-accent-foreground'
          : 'text-muted-foreground hover:bg-sidebar-accent/60 hover:text-sidebar-foreground',
      )}>
        <Icon className="size-[18px] shrink-0" />
        <span className="flex-1">{t(navTranslationKeys[item.key])}</span>
        <CountBadge n={badgeOf(item)} />
      </NavLink>
    )
  }

  function renderGroup(group: NavGroup) {
    const visible = group.items.filter((i) => !i.permission || can(i.permission))
    if (!visible.length) return null

    if (group.collapsible) {
      const isCollapsed = Boolean(collapsed[group.key])
      const groupBadge = visible.reduce((sum, i) => sum + badgeOf(i), 0)
      const hasActiveChild = visible.some((i) => isActive(i.to))
      const GroupIcon = group.icon
      const highlight = isCollapsed && hasActiveChild
      return (
        <div key={group.key} className="space-y-1">
          <button onClick={() => toggleGroup(group.key)}
            className={cn(
              'flex w-full items-center gap-3 rounded-lg px-3 py-2 text-sm font-medium transition-colors',
              highlight
                ? 'bg-sidebar-accent text-sidebar-accent-foreground'
                : 'text-muted-foreground hover:bg-sidebar-accent/60 hover:text-sidebar-foreground',
            )}>
            {GroupIcon && <GroupIcon className="size-[18px] shrink-0" />}
            <span className="flex-1 text-left">{t(navTranslationKeys[group.key])}</span>
            {isCollapsed && <CountBadge n={groupBadge} />}
            <ChevronDown className={cn('size-4 shrink-0 text-muted-foreground transition-transform duration-200', isCollapsed && '-rotate-90')} />
          </button>
          {/* grid-rows 0fr↔1fr:高度自适应的平滑折叠动画 */}
          <div className={cn('grid transition-[grid-template-rows] duration-200 ease-out', isCollapsed ? 'grid-rows-[0fr]' : 'grid-rows-[1fr]')}>
            <div className="overflow-hidden">
              <div className="ml-[21px] mt-1 space-y-1 border-l border-sidebar-border pl-2">
                {visible.map((i) => renderItem(i))}
              </div>
            </div>
          </div>
        </div>
      )
    }

    return <div key={group.key} className="space-y-1">{visible.map((i) => renderItem(i))}</div>
  }

  return (
    <aside className="flex h-screen w-64 shrink-0 flex-col border-r border-sidebar-border bg-sidebar">
      {/* 品牌 */}
      <div className="flex items-center gap-3 px-5 py-5">
        <img src="/favicon.svg" alt="" aria-hidden="true" className="size-10 shrink-0 object-contain" />
        <div className="min-w-0">
          <div className="truncate text-[15px] font-bold text-sidebar-foreground">KubePilot</div>
          <div className="truncate text-xs text-muted-foreground">{t('sidebar.tagline')}</div>
        </div>
      </div>

      {/* 导航 */}
      <nav className="flex-1 space-y-1 overflow-y-auto px-3 py-2">
        {navGroups.map(renderGroup)}
      </nav>

      {/* 底部:用户 + 退出(主题切换已移至顶栏) */}
      <div className="space-y-2 border-t border-sidebar-border p-3">
        {user && (
          <div className="rounded-lg bg-sidebar-accent/40 px-3 py-2">
            <div className="text-[11px] text-muted-foreground">{t('sidebar.currentUser')}</div>
            <div className="truncate text-sm font-semibold text-sidebar-foreground">{user.display_name || user.username}</div>
            <div className="truncate text-[11px] text-muted-foreground">{user.role}</div>
          </div>
        )}

        <button onClick={logout}
          className="flex w-full items-center gap-2 rounded-lg px-3 py-2 text-sm text-muted-foreground transition-colors hover:bg-destructive/10 hover:text-destructive">
          <LogOut className="size-4" /> {t('sidebar.logout')}
        </button>
      </div>
    </aside>
  )
}
