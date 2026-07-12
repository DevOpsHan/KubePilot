import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { I18nextProvider } from 'react-i18next'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppI18n } from '@/i18n'
import { AuthProvider, useAuth } from '@/providers/AuthProvider'
import { Topbar } from './Topbar'

const apiMocks = vi.hoisted(() => ({
  getCurrentUser: vi.fn(),
  logout: vi.fn(),
}))

vi.mock('@/lib/api', () => ({
  getCurrentUser: apiMocks.getCurrentUser,
  logout: apiMocks.logout,
}))

vi.mock('@/components/ThemeSwitcher', () => ({
  ThemeSwitcher: () => null,
}))

vi.mock('@/components/LanguageSwitcher', () => ({
  LanguageSwitcher: () => null,
}))

function RefreshIdentityButton() {
  const { refresh } = useAuth()
  return <button type="button" onClick={() => void refresh()}>刷新身份</button>
}

async function renderAuthenticatedTopbar() {
  const i18n = await createAppI18n('en')
  return render(
    <I18nextProvider i18n={i18n}>
      <MemoryRouter initialEntries={['/dashboard']}>
        <AuthProvider>
          <Topbar alertCount={0} pendingCount={0} onSearch={vi.fn()} />
          <RefreshIdentityButton />
        </AuthProvider>
      </MemoryRouter>
    </I18nextProvider>,
  )
}

describe('Topbar 与认证上下文集成', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    apiMocks.getCurrentUser.mockResolvedValue({
      user: { username: 'admin', display_name: '系统管理员', role: 'global_admin' },
      permissions: [],
    })
    apiMocks.logout.mockResolvedValue({})
  })

  it('认证上下文刷新用户后同步更新顶栏身份', async () => {
    const user = userEvent.setup()
    await renderAuthenticatedTopbar()

    expect(await screen.findByText('admin · Global admin')).toBeInTheDocument()

    apiMocks.getCurrentUser.mockResolvedValueOnce({
      user: { username: 'ops', display_name: '集群运维员', role: 'cluster_admin' },
      permissions: [],
    })
    await user.click(screen.getByRole('button', { name: '刷新身份' }))

    expect(await screen.findByText('ops · Cluster admin')).toBeInTheDocument()
    expect(screen.queryByText('admin · Global admin')).not.toBeInTheDocument()
  })
})
