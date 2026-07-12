import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { I18nextProvider } from 'react-i18next'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppI18n, type SupportedLanguage } from '@/i18n'
import { useAuth } from '@/providers/AuthProvider'
import { Sidebar } from './Sidebar'
import { Topbar } from './Topbar'

vi.mock('@/providers/AuthProvider', () => ({
  useAuth: vi.fn(),
}))

vi.mock('@/components/ThemeSwitcher', () => ({
  ThemeSwitcher: () => null,
}))

vi.mock('@/components/LanguageSwitcher', () => ({
  LanguageSwitcher: () => null,
}))

const logoutAction = vi.fn().mockResolvedValue(undefined)

function setAuthenticatedUser(user: ReturnType<typeof useAuth>['user']) {
  vi.mocked(useAuth).mockReturnValue({
    user,
    permissions: [],
    loading: false,
    authenticated: Boolean(user),
    can: () => true,
    refresh: vi.fn().mockResolvedValue(undefined),
    onLoggedIn: vi.fn().mockResolvedValue(undefined),
    logout: logoutAction,
    loginNotice: null,
    setLoginNotice: vi.fn(),
    expiredReason: null,
    dismissExpired: vi.fn(),
  })
}

async function renderTopbar(language: SupportedLanguage) {
  const i18n = await createAppI18n(language)
  const createContent = () => (
    <I18nextProvider i18n={i18n}>
      <MemoryRouter initialEntries={['/dashboard']}>
        <Topbar alertCount={0} pendingCount={0} onSearch={vi.fn()} />
      </MemoryRouter>
    </I18nextProvider>
  )
  const view = render(createContent())
  return { ...view, rerenderTopbar: () => view.rerender(createContent()) }
}

async function renderSidebar(language: SupportedLanguage) {
  const i18n = await createAppI18n(language)
  return render(
    <I18nextProvider i18n={i18n}>
      <MemoryRouter initialEntries={['/dashboard']}>
        <Sidebar alertCount={0} pendingCount={0} />
      </MemoryRouter>
    </I18nextProvider>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  localStorage.clear()
  logoutAction.mockResolvedValue(undefined)
  setAuthenticatedUser({
    username: 'admin',
    display_name: '系统管理员',
    role: 'global_admin',
  })
})

describe('布局登录身份展示', () => {
  it('英文顶栏使用用户名和本地化角色，不显示中文用户资料', async () => {
    await renderTopbar('en')

    expect(screen.getByText('admin · Global admin')).toBeInTheDocument()
    expect(screen.queryByText('系统管理员')).not.toBeInTheDocument()
  })

  it('认证用户变化后同步更新顶栏账号和权限', async () => {
    const view = await renderTopbar('en')

    setAuthenticatedUser({
      username: 'ops',
      display_name: '集群运维员',
      role: 'cluster_admin',
    })
    view.rerenderTopbar()

    expect(screen.queryByText('admin · Global admin')).not.toBeInTheDocument()
    expect(screen.getByText('ops · Cluster admin')).toBeInTheDocument()
  })

  it('中文顶栏使用用户名和中文角色', async () => {
    await renderTopbar('zh')

    expect(screen.getByText('admin · 全局管理员')).toBeInTheDocument()
  })

  it('侧栏仅保留退出登录命令', async () => {
    const user = userEvent.setup()
    await renderSidebar('en')

    expect(screen.queryByText('Current user')).not.toBeInTheDocument()
    expect(screen.queryByText('系统管理员')).not.toBeInTheDocument()
    expect(screen.queryByText('global_admin')).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Log out' }))
    expect(logoutAction).toHaveBeenCalledTimes(1)
  })
})
