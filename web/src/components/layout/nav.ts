import {
  LayoutDashboard, MessageSquare, Boxes, Bot, Activity,
  TriangleAlert, ClipboardCheck, FileText, Users, Zap, ShieldCheck,
  type LucideIcon,
} from 'lucide-react'

export type NavKey =
  | 'dashboard'
  | 'chat'
  | 'clusters'
  | 'models'
  | 'inspectReport'
  | 'alerts'
  | 'approval'
  | 'audit'
  | 'inspectionSettings'
  | 'users'
  | 'ops'

export type TitleKey =
  | 'dashboard'
  | 'chat'
  | 'clusters'
  | 'clusterDetail'
  | 'models'
  | 'alerts'
  | 'inspectReport'
  | 'approval'
  | 'audit'
  | 'users'
  | 'inspectionSettings'

export type NavItemKey = Exclude<NavKey, 'ops'>
export type CollapsibleNavKey = 'ops'
type StaticNavGroupKey = 'main' | 'system'

type NavTranslationMap = { readonly [Key in NavKey]: `nav.${Key}` }
type TitleTranslationMap = { readonly [Key in TitleKey]: `title.${Key}` }

export const navTranslationKeys = {
  dashboard: 'nav.dashboard',
  chat: 'nav.chat',
  clusters: 'nav.clusters',
  models: 'nav.models',
  inspectReport: 'nav.inspectReport',
  alerts: 'nav.alerts',
  approval: 'nav.approval',
  audit: 'nav.audit',
  inspectionSettings: 'nav.inspectionSettings',
  users: 'nav.users',
  ops: 'nav.ops',
} as const satisfies NavTranslationMap

export const titleTranslationKeys = {
  dashboard: 'title.dashboard',
  chat: 'title.chat',
  clusters: 'title.clusters',
  clusterDetail: 'title.clusterDetail',
  models: 'title.models',
  alerts: 'title.alerts',
  inspectReport: 'title.inspectReport',
  approval: 'title.approval',
  audit: 'title.audit',
  users: 'title.users',
  inspectionSettings: 'title.inspectionSettings',
} as const satisfies TitleTranslationMap

// 导航项以稳定 key 标识,标签在渲染处经 i18n t('nav.<key>') 解析
export interface NavItem {
  to: string
  key: NavItemKey
  icon: LucideIcon
  permission?: string       // 需要的权限(无则所有登录用户可见)
  badge?: 'alerts' | 'pending'
}

interface NavGroupBase {
  icon?: LucideIcon         // 可折叠分组的行图标
  items: NavItem[]
}

interface StaticNavGroup extends NavGroupBase {
  key: StaticNavGroupKey
  collapsible?: false
}

interface CollapsibleNavGroup extends NavGroupBase {
  key: CollapsibleNavKey
  icon: LucideIcon
  collapsible: true         // 标题取共享的 navTranslationKeys
}

export type NavGroup = StaticNavGroup | CollapsibleNavGroup

// 侧边栏分组导航:核心入口平铺,运维治理类收进可折叠的「运维中心」
export const navGroups: NavGroup[] = [
  {
    key: 'main',
    items: [
      { to: '/dashboard', key: 'dashboard', icon: LayoutDashboard },
      { to: '/chat', key: 'chat', icon: MessageSquare },
      { to: '/clusters', key: 'clusters', icon: Boxes },
      { to: '/models', key: 'models', icon: Bot },
    ],
  },
  {
    key: 'ops',
    icon: ShieldCheck,
    collapsible: true,
    items: [
      { to: '/inspect-report', key: 'inspectReport', icon: Activity, permission: 'inspect:run' },
      { to: '/alerts', key: 'alerts', icon: TriangleAlert, permission: 'alerts:view', badge: 'alerts' },
      { to: '/approval', key: 'approval', icon: ClipboardCheck, badge: 'pending' },
      { to: '/audit', key: 'audit', icon: FileText, permission: 'approvals:view' },
      { to: '/inspection-settings', key: 'inspectionSettings', icon: Zap, permission: 'inspection:manage' },
    ],
  },
  {
    key: 'system',
    items: [
      { to: '/users', key: 'users', icon: Users, permission: 'users:manage' },
    ],
  },
]

// 扁平清单:命令面板(⌘K)按此遍历全部可跳转项
export const navItems: NavItem[] = navGroups.flatMap((g) => g.items)

// 路径(一级)→ 顶栏面包屑标题的 i18n key(title.*)
export const viewTitleKeys: Record<string, TitleKey> = {
  '/dashboard': 'dashboard',
  '/chat': 'chat',
  '/clusters': 'clusters',
  '/cluster-detail': 'clusterDetail',
  '/models': 'models',
  '/alerts': 'alerts',
  '/inspect-report': 'inspectReport',
  '/approval': 'approval',
  '/audit': 'audit',
  '/users': 'users',
  '/inspection-settings': 'inspectionSettings',
}
