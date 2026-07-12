import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { I18nextProvider } from 'react-i18next'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useShell } from '@/components/layout/AppLayout'
import { createAppI18n } from '@/i18n'
import { formatCalendarDate, formatDateTime, formatRelativeTime } from '@/i18n/format'
import { resources } from '@/i18n/resources'
import {
  acknowledgeAlert,
  createSilence,
  deleteSilence,
  diagnoseAlert,
  getAlertSummary,
  getAlertTrends,
  getNotificationSettings,
  listAlerts,
  listClusters,
  listSilences,
  remediateAlert,
  resolveAlert,
  saveNotificationSettings,
  testNotification,
} from '@/lib/api'
import { notify } from '@/lib/toast'
import { useAuth } from '@/providers/AuthProvider'
import { useConfirm } from '@/providers/ConfirmProvider'
import { AlertsPage } from './AlertsPage'

vi.mock('@/lib/api', () => ({
  acknowledgeAlert: vi.fn(),
  createSilence: vi.fn(),
  deleteSilence: vi.fn(),
  diagnoseAlert: vi.fn(),
  getAlertSummary: vi.fn(),
  getAlertTrends: vi.fn(),
  getNotificationSettings: vi.fn(),
  listAlerts: vi.fn(),
  listClusters: vi.fn(),
  listSilences: vi.fn(),
  remediateAlert: vi.fn(),
  resolveAlert: vi.fn(),
  saveNotificationSettings: vi.fn(),
  testNotification: vi.fn(),
}))

vi.mock('@/providers/AuthProvider', () => ({
  useAuth: vi.fn(),
}))

vi.mock('@/providers/ConfirmProvider', () => ({
  useConfirm: vi.fn(),
}))

vi.mock('@/components/layout/AppLayout', () => ({
  useShell: vi.fn(),
}))

vi.mock('@/lib/toast', () => ({
  notify: {
    error: vi.fn(),
    info: vi.fn(),
    success: vi.fn(),
  },
}))

const cluster = { id: 7, name: '生产-ACK' }
const firstSeenAt = '2026-07-10T08:00:00Z'
const lastSeenAt = '2026-07-10T09:30:00Z'
const silenceNow = new Date('2026-07-11T10:00:00Z')
const silenceExpiresAt = '2026-07-11T12:00:00Z'

const alertRecord = {
  id: 42,
  cluster_id: cluster.id,
  cluster_name: cluster.name,
  severity: 'critical',
  kind: 'Pod',
  namespace: '业务空间',
  name: '支付服务-pod',
  rule: 'PodCrashLoopBackOff',
  message: '原始告警详情：容器持续重启',
  status: 'active',
  count: 5,
  first_seen_at: firstSeenAt,
  last_seen_at: lastSeenAt,
  acknowledged_by: '',
  resolved_by: '',
  diagnosis: '原始 AI 诊断：检查应用配置',
  suggested_action: 'restart_pod',
  remediation_ticket_id: null,
  silenced: false,
}

const silenceRecord = {
  id: 81,
  cluster_id: cluster.id,
  cluster_name: cluster.name,
  rule: 'PodCrashLoopBackOff',
  namespace: '业务空间',
  name: '支付服务-pod',
  comment: '原始静默备注：计划维护',
  created_by: 'ops@example.com',
  expires_at: silenceExpiresAt,
}

const notificationSettings = {
  enabled: true,
  min_severity: 'warning',
  notify_resolved: true,
  dingtalk_webhook_configured: true,
  dingtalk_webhook_hint: '…ding1234',
  dingtalk_secret_configured: true,
  wecom_webhook_configured: true,
  wecom_webhook_hint: '…wecom5678',
  active_channels: ['dingtalk', 'wecom'],
  source: 'file',
}

const confirmAction = vi.fn().mockResolvedValue(false)
const refreshCounts = vi.fn()

function networkError(): Error & { code: string; request: object } {
  return Object.assign(new Error('Network Error'), { code: 'ERR_NETWORK', request: {} })
}

function collectLeaves(value: unknown, path: string[] = []): Array<[string, string]> {
  if (typeof value === 'string') return [[path.join('.'), value]]
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    throw new TypeError(`告警资源叶子必须是字符串：${path.join('.')}`)
  }
  return Object.entries(value).flatMap(([key, child]) => collectLeaves(child, [...path, key]))
}

function interpolationParams(value: string): string[] {
  return [...value.matchAll(/\{\{\s*([^},\s]+)[^}]*\}\}/gu)]
    .map((match) => match[1])
    .sort()
}

async function renderAlerts(language: 'zh' | 'en' = 'zh') {
  const i18n = await createAppI18n(language)
  render(
    <I18nextProvider i18n={i18n}>
      <MemoryRouter initialEntries={['/alerts']}>
        <AlertsPage />
      </MemoryRouter>
    </I18nextProvider>,
  )
  return i18n
}

async function withSelectDomSupport(run: () => Promise<void>) {
  const descriptor = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'scrollIntoView')
  Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', {
    configurable: true,
    value: vi.fn(),
  })
  try {
    await run()
  } finally {
    if (descriptor) Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', descriptor)
    else delete (HTMLElement.prototype as Partial<HTMLElement>).scrollIntoView
  }
}

beforeEach(() => {
  vi.clearAllMocks()
  confirmAction.mockResolvedValue(false)
  vi.mocked(useConfirm).mockReturnValue(confirmAction)
  vi.mocked(useAuth).mockReturnValue({
    user: { username: 'admin' },
    permissions: ['alerts:manage', 'notifications:manage'],
    loading: false,
    authenticated: true,
    can: () => true,
    refresh: vi.fn().mockResolvedValue(undefined),
    onLoggedIn: vi.fn().mockResolvedValue(undefined),
    logout: vi.fn().mockResolvedValue(undefined),
    loginNotice: null,
    setLoginNotice: vi.fn(),
    expiredReason: null,
    dismissExpired: vi.fn(),
  })
  vi.mocked(useShell).mockReturnValue({ alertCount: 0, pendingCount: 0, refreshCounts })
  vi.mocked(listClusters).mockResolvedValue({ clusters: [cluster] })
  vi.mocked(listAlerts).mockResolvedValue({ alerts: [alertRecord] })
  vi.mocked(getAlertSummary).mockResolvedValue({
    active: 1,
    acknowledged: 0,
    resolved: 4,
    critical: 2,
    warning: 1,
    silenced: 1,
  })
  vi.mocked(getAlertTrends).mockResolvedValue({
    buckets: [{ date: '2026-07-10', total: 3, critical: 2, warning: 1, resolved: 1 }],
  })
  vi.mocked(acknowledgeAlert).mockResolvedValue({ alert: alertRecord })
  vi.mocked(resolveAlert).mockResolvedValue({ alert: alertRecord })
  vi.mocked(diagnoseAlert).mockResolvedValue({
    alert: { ...alertRecord, diagnosis: '原始 AI 新诊断：检查探针与配置' },
  })
  vi.mocked(remediateAlert).mockResolvedValue({
    alert: { ...alertRecord, remediation_ticket_id: 501 },
    ticket: { id: 501, action: 'restart_pod', result: '原始修复输出：accepted' },
  })
  vi.mocked(listSilences).mockResolvedValue({ silences: [silenceRecord] })
  vi.mocked(createSilence).mockResolvedValue({ silence: silenceRecord })
  vi.mocked(deleteSilence).mockResolvedValue({ message: 'removed' })
  vi.mocked(getNotificationSettings).mockResolvedValue(notificationSettings)
  vi.mocked(saveNotificationSettings).mockResolvedValue(notificationSettings)
  vi.mocked(testNotification).mockResolvedValue({
    ok: true,
    channels: { dingtalk: 'ok', wecom: 'ok' },
  })
})

describe('告警中心国际化', () => {
  it('中英文告警资源键和插值参数严格对称', () => {
    const zhLeaves = new Map(collectLeaves(resources.zh.operations.alerts))
    const enLeaves = new Map(collectLeaves(resources.en.operations.alerts))

    expect([...enLeaves.keys()].sort()).toEqual([...zhLeaves.keys()].sort())
    for (const [key, zhValue] of zhLeaves) {
      expect(interpolationParams(enLeaves.get(key) ?? '')).toEqual(interpolationParams(zhValue))
    }
  })

  it('切换语言时重译统计、筛选、趋势、状态与时间，并保留筛选和业务原文', async () => {
    await withSelectDomSupport(async () => {
      const user = userEvent.setup()
      const i18n = await renderAlerts('zh')

      expect(await screen.findByRole('heading', { name: '告警中心' })).toBeInTheDocument()
      expect(await screen.findByText('原始告警详情：容器持续重启')).toBeInTheDocument()
      expect(screen.getByText('严重 2')).toBeInTheDocument()
      expect(screen.getByText('警告 1')).toBeInTheDocument()
      expect(screen.getByText('静默 1')).toBeInTheDocument()
      expect(screen.getByText('告警趋势 · 近 7 天')).toBeInTheDocument()
      expect(screen.getByText('合计新增 3 · 恢复 1')).toBeInTheDocument()
      const trendDate = screen.getByText(formatCalendarDate('2026-07-10', 'zh', 'short'))
      expect(trendDate).toHaveAttribute(
        'title',
        formatCalendarDate('2026-07-10', 'zh', 'long'),
      )
      await user.hover(trendDate)
      expect(screen.getByText(
        `${formatCalendarDate('2026-07-10', 'zh', 'long')}：严重 2 · 警告 1 · 恢复 1`,
      )).toBeInTheDocument()
      await user.unhover(trendDate)
      expect(screen.getByText('原始 AI 诊断：检查应用配置')).toBeInTheDocument()
      expect(screen.getByText(`首次：${formatDateTime(firstSeenAt, 'zh')}`)).toBeInTheDocument()
      expect(screen.getAllByText('活跃').length).toBeGreaterThanOrEqual(2)

      const severityFilter = screen.getAllByRole('combobox')[0]
      severityFilter.focus()
      await user.keyboard('{Enter}{ArrowDown}{Enter}')
      expect(severityFilter).toHaveTextContent('严重')
      const requestCount = vi.mocked(listAlerts).mock.calls.length

      await act(() => i18n.changeLanguage('en'))

      expect(screen.getByRole('heading', { name: 'Alert center' })).toBeInTheDocument()
      expect(screen.getByText('Critical 2')).toBeInTheDocument()
      expect(screen.getByText('Warning 1')).toBeInTheDocument()
      expect(screen.getByText('Silenced 1')).toBeInTheDocument()
      expect(screen.getByText('Alert trend · Last 7 days')).toBeInTheDocument()
      expect(screen.getByText('3 new · 1 resolved')).toBeInTheDocument()
      const englishTrendDate = screen.getByText(
        formatCalendarDate('2026-07-10', 'en', 'short'),
      )
      expect(englishTrendDate).toHaveAttribute(
        'title',
        formatCalendarDate('2026-07-10', 'en', 'long'),
      )
      await user.hover(englishTrendDate)
      expect(screen.getByText(
        `${formatCalendarDate('2026-07-10', 'en', 'long')}: 2 critical · 1 warning · 1 resolved`,
      )).toBeInTheDocument()
      await user.unhover(englishTrendDate)
      expect(severityFilter).toHaveTextContent('Critical')
      expect(screen.getAllByText('Active').length).toBeGreaterThanOrEqual(2)
      expect(screen.getByText(`First seen: ${formatDateTime(firstSeenAt, 'en')}`)).toBeInTheDocument()
      expect(screen.getByText('原始告警详情：容器持续重启')).toBeInTheDocument()
      expect(screen.getByText('原始 AI 诊断：检查应用配置')).toBeInTheDocument()
      expect(listAlerts).toHaveBeenCalledTimes(requestCount)
    })
  })

  it('使用触发时语言生成确认句和 Toast，并通过公共规则本地化 API 错误', async () => {
    confirmAction.mockResolvedValue(true)
    vi.mocked(resolveAlert).mockRejectedValueOnce(networkError())
    const user = userEvent.setup()
    await renderAlerts('en')
    await screen.findByRole('heading', { name: 'Alert center' })

    await user.click(await screen.findByRole('button', { name: 'Acknowledge' }))
    expect(confirmAction).toHaveBeenCalledWith({
      title: 'Acknowledge alert',
      message: 'Acknowledge alert "PodCrashLoopBackOff · 业务空间/支付服务-pod"? It will remain under observation and resolve automatically when the issue disappears.',
      confirmText: 'Acknowledge',
    })
    await waitFor(() => expect(notify.success).toHaveBeenCalledWith('Alert acknowledged'))

    await user.click(screen.getByRole('button', { name: 'Resolve' }))
    expect(confirmAction).toHaveBeenLastCalledWith({
      title: 'Resolve alert',
      message: 'Manually resolve alert "PodCrashLoopBackOff · 业务空间/支付服务-pod"?',
      confirmText: 'Resolve',
      tone: 'danger',
    })
    await waitFor(() => expect(notify.error).toHaveBeenCalledWith('Unable to reach the server'))
  })

  it('本地化 AI 诊断和修复工单操作，同时保留诊断与修复结果原文', async () => {
    confirmAction.mockResolvedValue(true)
    const user = userEvent.setup()
    await renderAlerts('en')
    await screen.findByRole('heading', { name: 'Alert center' })

    await user.click(await screen.findByRole('button', { name: 'Run diagnosis again' }))
    expect(await screen.findByText('原始 AI 新诊断：检查探针与配置')).toBeInTheDocument()
    expect(notify.success).toHaveBeenCalledWith('AI diagnosis completed')

    await user.click(screen.getByRole('button', { name: 'Create remediation ticket' }))
    expect(confirmAction).toHaveBeenLastCalledWith({
      title: 'Create remediation ticket',
      message: 'Create a remediation approval ticket for "PodCrashLoopBackOff · 业务空间/支付服务-pod"? The remediation runs only after approval.',
      confirmText: 'Create remediation ticket',
      tone: 'danger',
    })
    await waitFor(() => expect(notify.success).toHaveBeenCalledWith(
      'Created remediation ticket #501 (Restart Pod). Open the approval center to review it.',
    ))
    expect(screen.getByRole('button', { name: 'Ticket #501 · Review' })).toBeInTheDocument()
  })

  it('异步操作完成时使用当时的语言发送 Toast', async () => {
    let finishDiagnosis: ((value: { alert: typeof alertRecord }) => void) | undefined
    vi.mocked(diagnoseAlert).mockReturnValueOnce(new Promise((resolve) => {
      finishDiagnosis = resolve
    }))
    const user = userEvent.setup()
    const i18n = await renderAlerts('zh')
    await screen.findByRole('heading', { name: '告警中心' })

    await user.click(await screen.findByRole('button', { name: '重新诊断' }))
    await waitFor(() => expect(diagnoseAlert).toHaveBeenCalledWith(alertRecord.id, cluster.id))
    await act(() => i18n.changeLanguage('en'))
    await act(async () => {
      finishDiagnosis?.({
        alert: { ...alertRecord, diagnosis: '原始异步诊断：完成' },
      })
    })

    expect(notify.success).toHaveBeenCalledWith('AI diagnosis completed')
    expect(screen.getByText('原始异步诊断：完成')).toBeInTheDocument()
  })

  it('切换语言不重置静默表单，并本地化创建、列表与解除流程', async () => {
    vi.useFakeTimers({ toFake: ['Date'] })
    vi.setSystemTime(silenceNow)
    try {
      await withSelectDomSupport(async () => {
        confirmAction.mockResolvedValue(true)
        const user = userEvent.setup()
        const i18n = await renderAlerts('zh')
        await screen.findByRole('heading', { name: '告警中心' })
        await screen.findByText('原始告警详情：容器持续重启')

        await user.click(screen.getByRole('button', { name: '静默管理' }))
        const listDialog = await screen.findByRole('dialog')
        expect(within(listDialog).getByText('规则：PodCrashLoopBackOff')).toBeInTheDocument()
        expect(within(listDialog).getByText('命名空间：业务空间')).toBeInTheDocument()
        expect(within(listDialog).getByText('对象：支付服务-pod')).toBeInTheDocument()
        expect(within(listDialog).getByText('原始静默备注：计划维护')).toBeInTheDocument()
        const chineseRelativeTime = within(listDialog).getByText(
          formatRelativeTime(silenceExpiresAt, 'zh', silenceNow.getTime()),
        )
        expect(chineseRelativeTime).toHaveAttribute(
          'title',
          formatDateTime(silenceExpiresAt, 'zh'),
        )

        await act(() => i18n.changeLanguage('en'))
        expect(within(listDialog).getByText('Silence management')).toBeInTheDocument()
        expect(within(listDialog).getByText('Rule: PodCrashLoopBackOff')).toBeInTheDocument()
        const englishRelativeTime = within(listDialog).getByText(
          formatRelativeTime(silenceExpiresAt, 'en', silenceNow.getTime()),
        )
        expect(englishRelativeTime).toHaveAttribute(
          'title',
          formatDateTime(silenceExpiresAt, 'en'),
        )

        await user.click(within(listDialog).getByRole('button', { name: 'New silence' }))
        const createDialog = await screen.findByRole('dialog')
        const textboxes = within(createDialog).getAllByRole('textbox')
        await user.type(textboxes[0], 'Rule-原始值')
        await user.type(textboxes[1], 'Namespace-原始值')
        await user.type(textboxes[2], 'Object-原始值')
        await user.type(textboxes[3], 'Comment-原始值')

        await act(() => i18n.changeLanguage('zh'))
        expect(textboxes[0]).toHaveValue('Rule-原始值')
        expect(textboxes[1]).toHaveValue('Namespace-原始值')
        expect(textboxes[2]).toHaveValue('Object-原始值')
        expect(textboxes[3]).toHaveValue('Comment-原始值')
        await act(() => i18n.changeLanguage('en'))

        await user.click(within(createDialog).getByRole('button', { name: 'Create silence' }))
        await waitFor(() => expect(createSilence).toHaveBeenCalledWith({
          cluster_id: cluster.id,
          rule: 'Rule-原始值',
          namespace: 'Namespace-原始值',
          name: 'Object-原始值',
          duration_hours: 24,
          comment: 'Comment-原始值',
        }))
        expect(notify.success).toHaveBeenCalledWith(
          'Silence created. Matching alerts will no longer send notifications.',
        )

        await user.click(screen.getByRole('button', { name: 'Silence management' }))
        const reopenedList = await screen.findByRole('dialog')
        await user.click(within(reopenedList).getByRole('button', { name: 'Remove' }))
        expect(confirmAction).toHaveBeenLastCalledWith({
          title: 'Remove silence',
          message: 'Remove this silence? Matching alerts will resume external notifications.',
          confirmText: 'Remove',
          tone: 'danger',
        })
        await waitFor(() => expect(deleteSilence).toHaveBeenCalledWith(silenceRecord.id))
        expect(notify.info).toHaveBeenCalledWith('Silence removed')
      })
    } finally {
      vi.useRealTimers()
    }
  })

  it('切换语言不重置通知密钥，并本地化渠道、保存与测试发送结果', async () => {
    await withSelectDomSupport(async () => {
      const user = userEvent.setup()
      const i18n = await renderAlerts('zh')
      await screen.findByRole('heading', { name: '告警中心' })
      await screen.findByText('原始告警详情：容器持续重启')

      await user.click(screen.getByRole('button', { name: '通知设置' }))
      const dialog = await screen.findByRole('dialog')
      expect(await within(dialog).findByText(/当前生效渠道：/u)).toHaveTextContent('钉钉、企业微信')
      expect(within(dialog).getByText('来自文件')).toBeInTheDocument()
      const textboxes = within(dialog).getAllByRole('textbox')
      await user.type(textboxes[0], 'https://hooks.example/钉钉原始地址')
      await user.type(textboxes[1], 'SEC-原始密钥')
      await user.type(textboxes[2], 'https://hooks.example/企微原始地址')

      const [enabledSelect, severitySelect, resolvedSelect] = within(dialog).getAllByRole('combobox')
      for (const select of [enabledSelect, severitySelect, resolvedSelect]) {
        select.focus()
        await user.keyboard('{Enter}{ArrowDown}{Enter}')
      }

      const clearDingtalk = within(dialog).getByRole('checkbox', {
        name: '清除已配置的钉钉 Webhook',
      })
      const clearDingtalkSecret = within(dialog).getByRole('checkbox', {
        name: '清除加签密钥',
      })
      const clearWecom = within(dialog).getByRole('checkbox', {
        name: '清除已配置的企业微信 Webhook',
      })
      await user.click(clearDingtalk)
      await user.click(clearDingtalkSecret)
      await user.click(clearWecom)

      await act(() => i18n.changeLanguage('en'))
      expect(within(dialog).getByText(/Current channels:/u)).toHaveTextContent('DingTalk, WeCom')
      expect(within(dialog).getByText('from File')).toBeInTheDocument()
      expect(textboxes[0]).toHaveValue('https://hooks.example/钉钉原始地址')
      expect(textboxes[1]).toHaveValue('SEC-原始密钥')
      expect(textboxes[2]).toHaveValue('https://hooks.example/企微原始地址')
      expect(enabledSelect).toHaveTextContent('Disabled')
      expect(severitySelect).toHaveTextContent('Critical only')
      expect(resolvedSelect).toHaveTextContent('Do not send')
      expect(clearDingtalk).toBeChecked()
      expect(clearDingtalkSecret).toBeChecked()
      expect(clearWecom).toBeChecked()

      await user.click(within(dialog).getByRole('button', { name: 'Send test' }))
      await waitFor(() => expect(notify.success).toHaveBeenCalledWith(
        'Test notification sent (DingTalk: ok · WeCom: ok)',
      ))

      await user.click(within(dialog).getByRole('button', { name: 'Save and apply' }))
      await waitFor(() => expect(saveNotificationSettings).toHaveBeenCalledWith({
        enabled: false,
        min_severity: 'critical',
        notify_resolved: false,
        dingtalk_webhook: '',
        dingtalk_secret: '',
        wecom_webhook: '',
      }))
      expect(notify.success).toHaveBeenCalledWith('Notification settings saved and applied')
      expect(enabledSelect).toHaveTextContent('Disabled')
      expect(severitySelect).toHaveTextContent('Critical only')
      expect(resolvedSelect).toHaveTextContent('Do not send')

      vi.mocked(testNotification).mockRejectedValueOnce(networkError())
      await user.click(within(dialog).getByRole('button', { name: 'Send test' }))
      await waitFor(() => expect(notify.error).toHaveBeenCalledWith('Unable to reach the server'))
    })
  })

  it('保留部分成功数据，并在语言切换时重新本地化页面请求错误', async () => {
    vi.mocked(listAlerts).mockRejectedValueOnce(networkError())
    const i18n = await renderAlerts('zh')

    expect(await screen.findByText('无法连接服务器')).toBeInTheDocument()
    expect(screen.getByText('严重 2')).toBeInTheDocument()
    expect(screen.getByText('合计新增 3 · 恢复 1')).toBeInTheDocument()

    await act(() => i18n.changeLanguage('en'))
    expect(screen.getByText('Unable to reach the server')).toBeInTheDocument()
    expect(screen.getByText('Critical 2')).toBeInTheDocument()
    expect(screen.getByText('3 new · 1 resolved')).toBeInTheDocument()
  })
})
