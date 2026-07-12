import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { I18nextProvider } from 'react-i18next'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useShell } from '@/components/layout/AppLayout'
import { createAppI18n } from '@/i18n'
import { formatDateTime } from '@/i18n/format'
import {
  apiFetch,
  getModelStatus,
  getReport,
  listAudits,
  listClusters,
  listReports,
  listUsers,
  runInspect,
} from '@/lib/api'
import { notify } from '@/lib/toast'
import { useChatAssistant } from '@/hooks/useChatAssistant'
import { useClusters } from '@/providers/ClusterProvider'
import { useConfirm } from '@/providers/ConfirmProvider'
import { ApprovalPage } from './ApprovalPage'
import { AuditPage } from './AuditPage'
import { ChatPage } from './ChatPage'
import { InspectReportPage } from './InspectReportPage'

vi.mock('@/lib/api', () => ({
  apiFetch: vi.fn(),
  getModelStatus: vi.fn(),
  getReport: vi.fn(),
  listAudits: vi.fn(),
  listClusters: vi.fn(),
  listReports: vi.fn(),
  listUsers: vi.fn(),
  runInspect: vi.fn(),
}))

vi.mock('@/hooks/useChatAssistant', () => ({
  useChatAssistant: vi.fn(),
}))

vi.mock('@/providers/ClusterProvider', () => ({
  useClusters: vi.fn(),
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
const reportTime = '2026-07-10T08:00:00Z'
const ticketTime = '2026-07-10T09:30:00Z'
const auditTime = '2026-07-10T10:45:00Z'
const confirmAction = vi.fn().mockResolvedValue(false)

const pendingTicket = {
  id: 'WO-1042',
  action: 'restart_pod',
  namespace: '业务空间',
  target: '支付服务-pod',
  cluster_id: cluster.id,
  cluster_name: cluster.name,
  risk: 'high',
  status: 'pending',
  approver: 'zhangsan',
  approver_display_name: '张三',
  reason: '业务原始原因：容器持续重启',
  created_at: ticketTime,
}

const executedTicket = {
  ...pendingTicket,
  id: 'WO-1041',
  target: '支付服务-pod-历史',
  reason: '历史业务原因：滚动重启',
  created_at: '2026-07-09T09:30:00Z',
  status: 'executed',
  verify_state: 'healthy',
  result: '原始执行输出：restart accepted',
  verify_detail: '原始复检详情：Pod Ready',
}

function okJson(body: object): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  })
}

function networkError(): Error & { code: string; request: object } {
  return Object.assign(new Error('Network Error'), { code: 'ERR_NETWORK', request: {} })
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
  vi.mocked(useShell).mockReturnValue({
    alertCount: 0,
    pendingCount: 0,
    refreshCounts: vi.fn(),
  })
  vi.mocked(useClusters).mockReturnValue({
    clusters: [cluster],
    selectedClusterId: String(cluster.id),
    currentClusterId: cluster.id,
    loadClusters: vi.fn().mockResolvedValue(undefined),
    selectCluster: vi.fn(),
  })
  vi.mocked(useChatAssistant).mockReturnValue({
    messages: [],
    sessionId: 'ops-session-42',
    sending: false,
    renderMd: vi.fn((text?: string) => text ?? ''),
    ensureSession: vi.fn(),
    loadHistory: vi.fn().mockResolvedValue(undefined),
    sendMessage: vi.fn().mockResolvedValue(undefined),
    clearChat: vi.fn().mockResolvedValue(undefined),
    scrollBottom: vi.fn(),
  })
  vi.mocked(getModelStatus).mockResolvedValue({ current: { ready: true } })
  vi.mocked(listClusters).mockResolvedValue({ clusters: [cluster] })
  vi.mocked(listUsers).mockResolvedValue({
    users: [{ username: 'zhangsan', display_name: '张三' }],
  })
  vi.mocked(listReports).mockResolvedValue({
    reports: [{
      id: 31,
      cluster_id: cluster.id,
      started_at: reportTime,
      anomaly_count: 1,
      node_count: 3,
      pod_count: 18,
    }],
  })
  vi.mocked(getReport).mockResolvedValue({
    id: 31,
    cluster_id: cluster.id,
    started_at: reportTime,
    anomaly_count: 1,
    node_count: 3,
    pod_count: 18,
    anomalies: [{
      severity: 'critical',
      rule: 'PodCrashLoop',
      kind: 'Pod',
      namespace: '业务空间',
      name: '支付服务-pod',
      message: '业务原始摘要：容器持续重启',
      diagnosis: '原始 AI 诊断：检查应用配置',
      evidence: '原始现场证据：BackOff',
    }],
  })
  vi.mocked(runInspect).mockResolvedValue({ status: 'success' })
  vi.mocked(listAudits).mockResolvedValue({
    audits: [{
      id: 91,
      cluster_id: cluster.id,
      cluster_name: cluster.name,
      operator: 'zhangsan',
      operator_display_name: '张三',
      action: 'restart_pod',
      target: '业务空间/支付服务-pod',
      result: 'failed',
      detail: '原始审计详情：admission denied',
      created_at: auditTime,
    }],
  })
  vi.mocked(apiFetch).mockImplementation(async () => okJson({
    tickets: [pendingTicket, executedTicket],
  }))
})

describe('对话页面国际化', () => {
  it('切换语言时重译所有系统文案并保留会话编号', async () => {
    const i18n = await createAppI18n('zh')

    render(
      <I18nextProvider i18n={i18n}>
        <MemoryRouter initialEntries={['/chat']}><ChatPage /></MemoryRouter>
      </I18nextProvider>,
    )

    expect(await screen.findByText('你好，我是小智')).toBeInTheDocument()
    expect(screen.getByText('会话编号：ops-session-42')).toBeInTheDocument()
    expect(screen.getByPlaceholderText('问问小智集群的情况，按回车发送…')).toBeInTheDocument()

    await act(() => i18n.changeLanguage('en'))

    expect(screen.getByText("Hi, I'm KubePilot AI")).toBeInTheDocument()
    expect(screen.getByText('Session ID: ops-session-42')).toBeInTheDocument()
    expect(screen.getByPlaceholderText('Ask about your clusters and press Enter to send…')).toBeInTheDocument()
    expect(screen.queryByText('你好，我是小智')).not.toBeInTheDocument()
  })

  it('翻译工具执行与耗时但保留 AI 内容和命令原文', async () => {
    vi.mocked(useChatAssistant).mockReturnValue({
      messages: [{
        role: 'assistant',
        content: '原始 AI 结论：支付服务需要扩容',
        steps: [{ command: 'kubectl get pods -n 业务空间', duration: null }],
        thinking: false,
        totalMs: 1250,
      }],
      sessionId: 'ops-session-raw',
      sending: false,
      renderMd: vi.fn((text?: string) => text ?? ''),
      ensureSession: vi.fn(),
      loadHistory: vi.fn().mockResolvedValue(undefined),
      sendMessage: vi.fn().mockResolvedValue(undefined),
      clearChat: vi.fn().mockResolvedValue(undefined),
      scrollBottom: vi.fn(),
    })
    const i18n = await createAppI18n('en')

    render(
      <I18nextProvider i18n={i18n}>
        <MemoryRouter initialEntries={['/chat']}><ChatPage /></MemoryRouter>
      </I18nextProvider>,
    )

    expect(await screen.findByText('Running…')).toBeInTheDocument()
    expect(screen.getByText('Total time: 1.3 s')).toBeInTheDocument()
    expect(screen.getByText('kubectl get pods -n 业务空间')).toBeInTheDocument()
    expect(screen.getByText('原始 AI 结论：支付服务需要扩容')).toBeInTheDocument()
  })
})

describe('巡检报告国际化', () => {
  it('重译级别和时间并保持 Kubernetes 与报告业务内容原文', async () => {
    const i18n = await createAppI18n('zh')

    render(
      <I18nextProvider i18n={i18n}>
        <InspectReportPage />
      </I18nextProvider>,
    )

    expect(await screen.findByText('巡检报告')).toBeInTheDocument()
    expect(await screen.findByText('严重')).toBeInTheDocument()
    expect(screen.getAllByText(formatDateTime(reportTime, 'zh')).length).toBeGreaterThan(0)
    expect(screen.getByText('Pod/业务空间/支付服务-pod')).toBeInTheDocument()
    expect(screen.getByText('业务原始摘要：容器持续重启')).toBeInTheDocument()
    expect(screen.getByText('原始 AI 诊断：检查应用配置')).toBeInTheDocument()
    expect(screen.getByText('原始现场证据：BackOff')).toBeInTheDocument()

    await act(() => i18n.changeLanguage('en'))

    expect(screen.getByText('Inspection reports')).toBeInTheDocument()
    expect(screen.getByText('Critical')).toBeInTheDocument()
    expect(screen.getAllByText(formatDateTime(reportTime, 'en')).length).toBeGreaterThan(0)
    expect(screen.getByText('Pod/业务空间/支付服务-pod')).toBeInTheDocument()
    expect(screen.getByText('业务原始摘要：容器持续重启')).toBeInTheDocument()
    expect(screen.getByText('原始 AI 诊断：检查应用配置')).toBeInTheDocument()
    expect(screen.getByText('原始现场证据：BackOff')).toBeInTheDocument()
  })

  it('按触发时的语言显示巡检 API 错误', async () => {
    const i18n = await createAppI18n('en')
    const user = userEvent.setup()
    render(
      <I18nextProvider i18n={i18n}>
        <InspectReportPage />
      </I18nextProvider>,
    )
    await screen.findByText('Inspection reports')
    await waitFor(() => expect(listReports).toHaveBeenCalled())
    vi.mocked(runInspect).mockRejectedValueOnce(networkError())

    await user.click(screen.getByRole('button', { name: 'Run inspection now' }))

    await waitFor(() => expect(notify.error).toHaveBeenCalledWith('Unable to reach the server'))
  })
})

describe('审批页面国际化', () => {
  it('重译动态状态和时间，保留筛选值及工单业务字段', async () => {
    await withSelectDomSupport(async () => {
      const i18n = await createAppI18n('zh')
      const user = userEvent.setup()
      render(
        <I18nextProvider i18n={i18n}>
          <ApprovalPage />
        </I18nextProvider>,
      )

      expect(await screen.findByText('审批中心')).toBeInTheDocument()
      expect(await screen.findByText('待审批')).toBeInTheDocument()
      expect(screen.getByText('已执行')).toBeInTheDocument()
      expect(screen.getByText(/复检：健康/u)).toBeInTheDocument()
      expect(screen.getByText(`日期：${formatDateTime(ticketTime, 'zh')}`)).toBeInTheDocument()
      expect(screen.getByText('业务空间/支付服务-pod')).toBeInTheDocument()
      expect(screen.getByText(/业务原始原因：容器持续重启/u)).toBeInTheDocument()
      expect(screen.getByText(/原始执行输出：restart accepted/u)).toBeInTheDocument()

      const statusSelect = screen.getAllByRole('combobox')[3]
      statusSelect.focus()
      await user.keyboard('{Enter}{ArrowDown}{Enter}')
      expect(statusSelect).toHaveTextContent('待审批')

      await act(() => i18n.changeLanguage('en'))

      expect(screen.getByText('Approval center')).toBeInTheDocument()
      expect(screen.getAllByText('Pending').length).toBeGreaterThanOrEqual(2)
      expect(screen.getByText('Executed')).toBeInTheDocument()
      expect(screen.getByText(/Verification: Healthy/u)).toBeInTheDocument()
      expect(screen.getByText(`Date: ${formatDateTime(ticketTime, 'en')}`)).toBeInTheDocument()
      expect(screen.getAllByRole('combobox')[3]).toHaveTextContent('Pending')
      expect(screen.getByText('业务空间/支付服务-pod')).toBeInTheDocument()
      expect(screen.getByText(/业务原始原因：容器持续重启/u)).toBeInTheDocument()
      expect(screen.getByText(/原始执行输出：restart accepted/u)).toBeInTheDocument()
    })
  })

  it('使用当前语言生成包含完整工单 ID 的批准确认句', async () => {
    const i18n = await createAppI18n('zh')
    const user = userEvent.setup()
    render(
      <I18nextProvider i18n={i18n}>
        <ApprovalPage />
      </I18nextProvider>,
    )
    await screen.findByText('#WO-1042')
    await act(() => i18n.changeLanguage('en'))

    await user.click(screen.getByRole('button', { name: 'Approve and execute' }))

    expect(confirmAction).toHaveBeenCalledWith({
      title: 'Approve and execute',
      message: 'Approve ticket #WO-1042 and execute it now? This operation will modify the cluster.',
      confirmText: 'Approve and execute',
      tone: 'danger',
    })
  })

  it('自动刷新计时器运行时切换语言不会重置选择和语义状态', async () => {
    await withSelectDomSupport(async () => {
      const i18n = await createAppI18n('zh')
      const user = userEvent.setup()
      render(
        <I18nextProvider i18n={i18n}>
          <ApprovalPage />
        </I18nextProvider>,
      )
      await screen.findByText('#WO-1042')

      const intervalSpy = vi.spyOn(window, 'setInterval')
      const timeoutSpy = vi.spyOn(window, 'setTimeout')

      try {
        const refreshSelect = screen.getAllByRole('combobox')[0]
        refreshSelect.focus()
        await user.keyboard('{Enter}{ArrowDown}{Enter}')

        expect(screen.getByText('自动刷新中（每 2 秒）')).toBeInTheDocument()
        expect(refreshSelect).toHaveTextContent('1 分钟内，每 2 秒刷新')
        const appIntervalIndices = intervalSpy.mock.calls.flatMap(([, timeout], index) => (
          timeout === 2_000 ? [index] : []
        ))
        expect(appIntervalIndices).toHaveLength(1)
        expect(timeoutSpy.mock.calls.some(([, timeout]) => timeout === 60_000)).toBe(true)
        const intervalHandle = intervalSpy.mock.results[appIntervalIndices[0]]?.value
        const stopTimerIndex = timeoutSpy.mock.calls.findIndex(([, timeout]) => timeout === 60_000)
        const stopHandle = timeoutSpy.mock.results[stopTimerIndex]?.value
        if (intervalHandle !== undefined) window.clearInterval(intervalHandle)
        if (stopHandle !== undefined) window.clearTimeout(stopHandle)

        await act(() => i18n.changeLanguage('en'))

        expect(refreshSelect).toHaveTextContent('Every 2 seconds for 1 minute')
        expect(screen.getByText('Auto-refreshing every 2 seconds')).toBeInTheDocument()
        expect(intervalSpy.mock.calls.filter(([, timeout]) => timeout === 2_000)).toHaveLength(1)
        expect(timeoutSpy.mock.calls.filter(([, timeout]) => timeout === 60_000)).toHaveLength(1)
      } finally {
        timeoutSpy.mockRestore()
        intervalSpy.mockRestore()
      }
    })
  })
})

describe('审计页面国际化', () => {
  it('重译状态与时间并保持操作者、对象和详情原文', async () => {
    const i18n = await createAppI18n('zh')
    render(
      <I18nextProvider i18n={i18n}>
        <AuditPage />
      </I18nextProvider>,
    )

    expect(await screen.findByText('审计中心')).toBeInTheDocument()
    expect(await screen.findByText('失败')).toBeInTheDocument()
    expect(screen.getByText(formatDateTime(auditTime, 'zh'))).toBeInTheDocument()
    expect(screen.getByText('张三')).toBeInTheDocument()
    expect(screen.getByText('业务空间/支付服务-pod')).toBeInTheDocument()
    expect(screen.getByText('原始审计详情：admission denied')).toBeInTheDocument()

    await act(() => i18n.changeLanguage('en'))

    expect(screen.getByText('Audit center')).toBeInTheDocument()
    expect(screen.getByText('Failed')).toBeInTheDocument()
    expect(screen.getByText(formatDateTime(auditTime, 'en'))).toBeInTheDocument()
    expect(screen.getByText('张三')).toBeInTheDocument()
    expect(screen.getByText('业务空间/支付服务-pod')).toBeInTheDocument()
    expect(screen.getByText('原始审计详情：admission denied')).toBeInTheDocument()
  })

  it('保存错误语义并在语言切换后重新本地化', async () => {
    vi.mocked(listAudits).mockRejectedValue(networkError())
    const i18n = await createAppI18n('zh')
    render(
      <I18nextProvider i18n={i18n}>
        <AuditPage />
      </I18nextProvider>,
    )

    expect(await screen.findByText('无法连接服务器')).toBeInTheDocument()
    await act(() => i18n.changeLanguage('en'))
    expect(screen.getByText('Unable to reach the server')).toBeInTheDocument()
  })
})
