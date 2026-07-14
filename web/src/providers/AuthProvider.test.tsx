import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { I18nextProvider, useTranslation } from 'react-i18next'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { LanguageSwitcher } from '@/components/LanguageSwitcher'
import { createAppI18n } from '@/i18n'
import { AuthProvider, useAuth, type SessionReason } from './AuthProvider'

const mocks = vi.hoisted(() => ({
  getCurrentUser: vi.fn(),
  logout: vi.fn(),
}))

vi.mock('@/lib/api', () => ({
  getCurrentUser: mocks.getCurrentUser,
  logout: mocks.logout,
}))

const user = {
  username: 'operator',
  display_name: 'Operator',
  role: 'global_admin',
}

function sessionKey(reason: SessionReason): 'session.idle' | 'session.unauthorized' {
  return reason === 'idle' ? 'session.idle' : 'session.unauthorized'
}

function AuthProbe() {
  const auth = useAuth()
  const { t } = useTranslation('auth')
  const notice = auth.loginNotice
  const noticeText = notice?.kind === 'session'
    ? t(sessionKey(notice.reason))
    : notice?.kind === 'passwordReset'
      ? t('login.passwordResetNotice')
    : notice?.kind === 'raw' ? notice.message : ''

  return (
    <div>
      <output data-testid="loading">{String(auth.loading)}</output>
      <output data-testid="authenticated">{String(auth.authenticated)}</output>
      <output data-testid="expired-reason">{auth.expiredReason ?? 'none'}</output>
      <output data-testid="expired-text">
        {auth.expiredReason ? t(sessionKey(auth.expiredReason)) : ''}
      </output>
      <output data-testid="login-notice">{noticeText}</output>
      <button type="button" onClick={auth.dismissExpired}>关闭过期提示</button>
      <button type="button" onClick={() => auth.setLoginNotice({ kind: 'raw', message: 'Raw administrator notice' })}>
        设置原始通知
      </button>
      <button type="button" onClick={() => auth.setLoginNotice({ kind: 'passwordReset' })}>
        设置密码重置通知
      </button>
      <button type="button" onClick={() => void auth.onLoggedIn(user)}>模拟登录成功</button>
      <button type="button" onClick={() => void auth.logout()}>退出登录</button>
    </div>
  )
}

async function renderAuthProvider() {
  const instance = await createAppI18n('zh')
  const view = render(
    <I18nextProvider i18n={instance}>
      <AuthProvider>
        <LanguageSwitcher />
        <AuthProbe />
      </AuthProvider>
    </I18nextProvider>,
  )

  await act(async () => {
    await Promise.resolve()
    await Promise.resolve()
  })

  return { ...view, i18n: instance }
}

describe('AuthProvider 会话语义状态', () => {
  beforeEach(() => {
    mocks.getCurrentUser.mockReset()
    mocks.getCurrentUser.mockResolvedValue({ user, permissions: ['clusters:view'] })
    mocks.logout.mockReset()
    mocks.logout.mockResolvedValue({})
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('登录后空闲三十分钟保存 idle 原因而不是已翻译文本', async () => {
    vi.useFakeTimers()
    const { i18n } = await renderAuthProvider()

    expect(screen.getByTestId('authenticated')).toHaveTextContent('true')
    act(() => vi.advanceTimersByTime(30 * 60 * 1000 - 1000))

    expect(screen.getByTestId('authenticated')).toHaveTextContent('true')
    expect(screen.getByTestId('expired-reason')).toHaveTextContent('none')

    act(() => vi.advanceTimersByTime(1000))

    expect(screen.getByTestId('expired-reason')).toHaveTextContent('idle')
    expect(screen.getByTestId('expired-text'))
      .toHaveTextContent('登录已超过 30 分钟无操作，请重新登录。')

    await act(async () => {
      await i18n.changeLanguage('en')
    })
    expect(screen.getByTestId('expired-text'))
      .toHaveTextContent('You were signed out after 30 minutes of inactivity. Please sign in again.')
  })

  it('401 事件保存 unauthorized，关闭后语义通知随语言切换更新', async () => {
    const userActions = userEvent.setup()
    await renderAuthProvider()

    act(() => {
      window.dispatchEvent(new CustomEvent('aiops-auth-expired', {
        detail: { reason: 'unauthorized' },
      }))
    })

    expect(screen.getByTestId('expired-reason')).toHaveTextContent('unauthorized')
    expect(screen.getByTestId('expired-text')).toHaveTextContent('会话已过期，请重新登录。')

    await userActions.click(screen.getByRole('button', { name: 'English' }))
    expect(screen.getByTestId('expired-text'))
      .toHaveTextContent('Your session has expired. Please sign in again.')

    await userActions.click(screen.getByRole('button', { name: 'Chinese' }))
    expect(screen.getByTestId('expired-text')).toHaveTextContent('会话已过期，请重新登录。')

    await userActions.click(screen.getByRole('button', { name: '关闭过期提示' }))

    expect(screen.getByTestId('authenticated')).toHaveTextContent('false')
    expect(screen.getByTestId('expired-reason')).toHaveTextContent('none')
    expect(screen.getByTestId('login-notice')).toHaveTextContent('会话已过期，请重新登录。')

    await userActions.click(screen.getByRole('button', { name: 'English' }))

    expect(screen.getByTestId('login-notice'))
      .toHaveTextContent('Your session has expired. Please sign in again.')
  })

  it('setLoginNotice 继续保存用户管理传入的原始通知', async () => {
    const userActions = userEvent.setup()
    await renderAuthProvider()

    await userActions.click(screen.getByRole('button', { name: '设置原始通知' }))
    expect(screen.getByTestId('login-notice')).toHaveTextContent('Raw administrator notice')

    await userActions.click(screen.getByRole('button', { name: 'English' }))
    expect(screen.getByTestId('login-notice')).toHaveTextContent('Raw administrator notice')
  })

  it('密码重置通知保存语义并随语言切换重新渲染', async () => {
    const userActions = userEvent.setup()
    await renderAuthProvider()

    await userActions.click(screen.getByRole('button', { name: '设置密码重置通知' }))
    expect(screen.getByTestId('login-notice'))
      .toHaveTextContent('当前管理员密码已重置为 k8s@ops123，请重新登录并修改密码')

    await userActions.click(screen.getByRole('button', { name: 'English' }))
    expect(screen.getByTestId('login-notice'))
      .toHaveTextContent('Your administrator password was reset to k8s@ops123. Sign in again and change it.')
  })

  it('登录成功和退出登录清理过期原因与通知', async () => {
    const userActions = userEvent.setup()
    await renderAuthProvider()

    act(() => {
      window.dispatchEvent(new CustomEvent('aiops-auth-expired', {
        detail: { reason: 'unauthorized' },
      }))
    })
    await userActions.click(screen.getByRole('button', { name: '关闭过期提示' }))
    expect(screen.getByTestId('login-notice')).not.toBeEmptyDOMElement()

    await userActions.click(screen.getByRole('button', { name: '模拟登录成功' }))
    expect(screen.getByTestId('authenticated')).toHaveTextContent('true')
    expect(screen.getByTestId('expired-reason')).toHaveTextContent('none')
    expect(screen.getByTestId('login-notice')).toBeEmptyDOMElement()

    await userActions.click(screen.getByRole('button', { name: '设置原始通知' }))
    await userActions.click(screen.getByRole('button', { name: '退出登录' }))
    // 退出带加载态与短暂过渡,等状态清理完成后再断言
    await waitFor(() => expect(screen.getByTestId('authenticated')).toHaveTextContent('false'))
    expect(screen.getByTestId('expired-reason')).toHaveTextContent('none')
    expect(screen.getByTestId('login-notice')).toBeEmptyDOMElement()
  })

  it('卸载时清理计时器和所有全局事件监听', async () => {
    vi.useFakeTimers()
    const addEventListener = vi.spyOn(window, 'addEventListener')
    const removeEventListener = vi.spyOn(window, 'removeEventListener')
    const view = await renderAuthProvider()
    const trackedEvents = ['aiops-auth-expired', 'click', 'keydown', 'mousemove', 'scroll']

    expect(vi.getTimerCount()).toBeGreaterThan(0)
    view.unmount()

    expect(vi.getTimerCount()).toBe(0)
    for (const eventName of trackedEvents) {
      expect(addEventListener.mock.calls.some(([name]) => name === eventName)).toBe(true)
      expect(removeEventListener.mock.calls.some(([name]) => name === eventName)).toBe(true)
    }
  })
})
