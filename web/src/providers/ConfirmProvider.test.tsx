import { useState } from 'react'
import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { I18nextProvider } from 'react-i18next'
import { describe, expect, it } from 'vitest'
import { LanguageSwitcher } from '@/components/LanguageSwitcher'
import { createAppI18n } from '@/i18n'
import { ConfirmProvider, useConfirm } from './ConfirmProvider'

function ConfirmProbe() {
  const confirm = useConfirm()
  const [result, setResult] = useState('pending')

  async function openDefault() {
    setResult('pending')
    setResult(String(await confirm({ message: 'Keep this message unchanged' })))
  }

  async function openCustom() {
    setResult('pending')
    setResult(String(await confirm({
      title: 'Exact custom title',
      message: 'Exact custom message',
      confirmText: 'Proceed exactly',
      cancelText: 'Abort exactly',
    })))
  }

  return (
    <div>
      <LanguageSwitcher />
      <button type="button" onClick={() => void openDefault()}>打开默认确认框</button>
      <button type="button" onClick={() => void openCustom()}>打开自定义确认框</button>
      <output data-testid="confirm-result">{result}</output>
    </div>
  )
}

async function renderConfirmProvider() {
  const instance = await createAppI18n('zh')
  const user = userEvent.setup()

  render(
    <I18nextProvider i18n={instance}>
      <ConfirmProvider>
        <ConfirmProbe />
      </ConfirmProvider>
    </I18nextProvider>,
  )

  return { instance, user }
}

describe('ConfirmProvider', () => {
  it('默认确认框打开后切换语言会即时更新标题和按钮，并解析 true', async () => {
    const { instance, user } = await renderConfirmProvider()

    await user.click(screen.getByRole('button', { name: '打开默认确认框' }))
    expect(screen.getByRole('heading', { name: '确认操作' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '取消' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '确认' })).toBeInTheDocument()

    await act(() => instance.changeLanguage('en'))

    expect(await screen.findByRole('heading', { name: 'Confirm action' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Confirm' }))

    expect(await screen.findByTestId('confirm-result')).toHaveTextContent('true')
  })

  it('取消按钮解析 false', async () => {
    const { user } = await renderConfirmProvider()

    await user.click(screen.getByRole('button', { name: '打开默认确认框' }))
    await user.click(screen.getByRole('button', { name: '取消' }))

    expect(await screen.findByTestId('confirm-result')).toHaveTextContent('false')
  })

  it('openChange 关闭确认框时解析 false', async () => {
    const { user } = await renderConfirmProvider()

    await user.click(screen.getByRole('button', { name: '打开默认确认框' }))
    await user.click(screen.getByRole('button', { name: 'Close' }))

    await waitFor(() => expect(screen.getByTestId('confirm-result')).toHaveTextContent('false'))
  })

  it('自定义字符串在语言切换后保持原样', async () => {
    const { instance, user } = await renderConfirmProvider()

    await user.click(screen.getByRole('button', { name: '打开自定义确认框' }))
    await act(() => instance.changeLanguage('en'))

    expect(screen.getByRole('heading', { name: 'Exact custom title' })).toBeInTheDocument()
    expect(screen.getByText('Exact custom message')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Abort exactly' })).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Proceed exactly' }))
    expect(await screen.findByTestId('confirm-result')).toHaveTextContent('true')
  })
})
