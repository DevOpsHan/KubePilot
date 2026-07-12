import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { I18nextProvider } from 'react-i18next'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppI18n } from '@/i18n'
import { LoginPage } from './LoginPage'

const mocks = vi.hoisted(() => ({
  changePassword: vi.fn(),
  login: vi.fn(),
  onLoggedIn: vi.fn(),
  loginNotice: {
    current: null as
      | { kind: 'session'; reason: 'idle' | 'unauthorized' }
      | { kind: 'passwordReset' }
      | { kind: 'raw'; message: string }
      | null,
  },
}))

vi.mock('@/lib/api', () => ({
  changePassword: mocks.changePassword,
  login: mocks.login,
}))

vi.mock('@/providers/AuthProvider', () => ({
  useAuth: () => ({
    loginNotice: mocks.loginNotice.current,
    onLoggedIn: mocks.onLoggedIn,
  }),
}))

vi.mock('@/components/ThemeSwitcher', () => ({
  ThemeSwitcher: () => null,
}))

vi.mock('@/components/LoginBackground', () => ({
  LoginBackground: () => null,
}))

async function renderLoginPage(initialLanguage: 'zh' | 'en' = 'zh') {
  const instance = await createAppI18n(initialLanguage)
  const user = userEvent.setup()

  render(
    <I18nextProvider i18n={instance}>
      <LoginPage />
    </I18nextProvider>,
  )

  return { user }
}

describe('登录页本地校验国际化', () => {
  beforeEach(() => {
    mocks.changePassword.mockReset()
    mocks.login.mockReset()
    mocks.onLoggedIn.mockReset()
    mocks.loginNotice.current = null
  })

  it('切换语言后重新翻译登录校验错误并保留输入值', async () => {
    const { user } = await renderLoginPage()
    const usernameInput = screen.getByPlaceholderText('请输入用户名')

    await user.type(usernameInput, 'operator')
    await user.click(screen.getByRole('button', { name: '登录' }))

    expect(screen.getByText('请输入用户名和密码')).toBeInTheDocument()
    expect(mocks.login).not.toHaveBeenCalled()

    await user.click(screen.getByRole('button', { name: 'English' }))

    expect(await screen.findByText('Please enter username and password')).toBeInTheDocument()
    expect(screen.queryByText('请输入用户名和密码')).not.toBeInTheDocument()
    expect(usernameInput).toHaveValue('operator')
    expect(screen.getByRole('button', { name: 'Sign in' })).toBeInTheDocument()
  })

  it('切换语言后重新翻译改密校验错误并保留改密模式与输入值', async () => {
    mocks.login.mockResolvedValueOnce({
      password_change_required: true,
      user: null,
    })
    const { user } = await renderLoginPage()

    await user.type(screen.getByPlaceholderText('请输入用户名'), 'operator')
    await user.type(screen.getByPlaceholderText('请输入密码'), 'old-password')
    await user.click(screen.getByRole('button', { name: '登录' }))

    expect(await screen.findByText('首次登录需修改初始密码。')).toBeInTheDocument()
    const newPasswordInput = screen.getByPlaceholderText('至少 6 位')
    const confirmPasswordInput = screen.getByPlaceholderText('请输入密码')
    await user.type(newPasswordInput, 'new-password')
    await user.type(confirmPasswordInput, 'different-password')
    await user.click(screen.getByRole('button', { name: '修改并登录' }))

    expect(screen.getByText('两次输入的新密码不一致')).toBeInTheDocument()
    expect(mocks.changePassword).not.toHaveBeenCalled()

    await user.click(screen.getByRole('button', { name: 'English' }))

    expect(await screen.findByText('The two passwords do not match')).toBeInTheDocument()
    expect(screen.queryByText('两次输入的新密码不一致')).not.toBeInTheDocument()
    expect(newPasswordInput).toHaveValue('new-password')
    expect(confirmPasswordInput).toHaveValue('different-password')
    expect(screen.getByRole('button', { name: 'Change & sign in' })).toBeInTheDocument()
  })

  it('API 没有原始消息时保留可重新翻译的语义 fallback', async () => {
    mocks.login.mockRejectedValueOnce({})
    const { user } = await renderLoginPage()

    await user.type(screen.getByPlaceholderText('请输入用户名'), 'operator')
    await user.type(screen.getByPlaceholderText('请输入密码'), 'password')
    await user.click(screen.getByRole('button', { name: '登录' }))

    expect(await screen.findByText('登录失败')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'English' }))

    expect(await screen.findByText('Sign-in failed')).toBeInTheDocument()
    expect(screen.queryByText('登录失败')).not.toBeInTheDocument()
  })

  it('响应错误为空时继续保留 Error.message 原文', async () => {
    mocks.login.mockRejectedValueOnce({
      message: 'ECONNREFUSED',
      response: { data: { error: '' } },
    })
    const { user } = await renderLoginPage()

    await user.type(screen.getByPlaceholderText('请输入用户名'), 'operator')
    await user.type(screen.getByPlaceholderText('请输入密码'), 'password')
    await user.click(screen.getByRole('button', { name: '登录' }))

    expect(await screen.findByText('ECONNREFUSED')).toBeInTheDocument()
  })

  it('语义会话通知会随语言切换重新渲染', async () => {
    mocks.loginNotice.current = { kind: 'session', reason: 'idle' }
    const { user } = await renderLoginPage()

    expect(screen.getByText('登录已超过 30 分钟无操作，请重新登录。')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'English' }))

    expect(await screen.findByText(
      'You were signed out after 30 minutes of inactivity. Please sign in again.',
    )).toBeInTheDocument()
    expect(screen.queryByText('登录已超过 30 分钟无操作，请重新登录。'))
      .not.toBeInTheDocument()
  })

  it('密码重置通知会随语言切换重新渲染', async () => {
    mocks.loginNotice.current = { kind: 'passwordReset' }
    const { user } = await renderLoginPage()

    expect(screen.getByText(
      '当前管理员密码已重置为 k8s@ops123，请重新登录并修改密码',
    )).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'English' }))

    expect(await screen.findByText(
      'Your administrator password was reset to k8s@ops123. Sign in again and change it.',
    )).toBeInTheDocument()
    expect(screen.queryByText(
      '当前管理员密码已重置为 k8s@ops123，请重新登录并修改密码',
    )).not.toBeInTheDocument()
  })

  it('用户管理传入的原始登录通知保持原样', async () => {
    mocks.loginNotice.current = { kind: 'raw', message: 'Raw administrator notice' }
    const { user } = await renderLoginPage()

    expect(screen.getByText('Raw administrator notice')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'English' }))
    expect(screen.getByText('Raw administrator notice')).toBeInTheDocument()
  })

  it('已知中文 API 错误在英文环境映射为具体消息', async () => {
    mocks.login.mockRejectedValueOnce({
      response: { status: 401, data: { error: '用户名或密码不正确' } },
    })
    const { user } = await renderLoginPage('en')

    await user.type(screen.getByPlaceholderText('Enter username'), 'operator')
    await user.type(screen.getByPlaceholderText('Enter password'), 'wrong-password')
    await user.click(screen.getByRole('button', { name: 'Sign in' }))

    expect(await screen.findByText('Incorrect username or password')).toBeInTheDocument()
    expect(screen.queryByText('用户名或密码不正确')).not.toBeInTheDocument()
  })

  it('未知中文 API 错误在英文环境使用场景 fallback', async () => {
    mocks.login.mockRejectedValueOnce({
      response: { status: 500, data: { error: '数据库连接池暂时不可用' } },
    })
    const { user } = await renderLoginPage('en')

    await user.type(screen.getByPlaceholderText('Enter username'), 'operator')
    await user.type(screen.getByPlaceholderText('Enter password'), 'password')
    await user.click(screen.getByRole('button', { name: 'Sign in' }))

    expect(await screen.findByText('Sign-in failed')).toBeInTheDocument()
    expect(screen.queryByText('数据库连接池暂时不可用')).not.toBeInTheDocument()
  })
})
