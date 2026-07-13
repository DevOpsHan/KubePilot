import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { I18nextProvider } from 'react-i18next'
import { MemoryRouter, Route, Routes, useLocation, useNavigate } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { NodeList } from '@/components/NodeList'
import { TooltipProvider } from '@/components/ui/tooltip'
import { createAppI18n } from '@/i18n'
import {
  getClusterSyncStatus,
  getDashboardSummary,
  getModelStatus,
  getSecurityStatus,
  listAudits,
  listClusterDeletedResources,
  listClusterResourceSnapshots,
  listClusterSyncResources,
  listClusterSyncTasks,
  listClusters,
  listNodeMetrics,
  listNodes,
  syncCluster,
} from '@/lib/api'
import { useAuth } from '@/providers/AuthProvider'
import { useChatAssistant } from '@/hooks/useChatAssistant'
import { useClusters } from '@/providers/ClusterProvider'
import { useConfirm } from '@/providers/ConfirmProvider'
import { ChatPage } from './ChatPage'
import { ClusterDetailPage } from './ClusterDetailPage'
import { ClustersPage } from './ClustersPage'
import { DashboardPage } from './DashboardPage'

vi.mock('@/lib/api', () => ({
  createCluster: vi.fn(),
  deleteCluster: vi.fn(),
  getClusterSyncStatus: vi.fn(),
  getDashboardSummary: vi.fn(),
  getModelStatus: vi.fn(),
  getSecurityStatus: vi.fn(),
  listAudits: vi.fn(),
  listClusterDeletedResources: vi.fn(),
  listClusterResourceSnapshots: vi.fn(),
  listClusterSyncResources: vi.fn(),
  listClusterSyncTasks: vi.fn(),
  listClusters: vi.fn(),
  listNodeMetrics: vi.fn(),
  listNodes: vi.fn(),
  syncAllClusters: vi.fn(),
  syncCluster: vi.fn(),
  updateCluster: vi.fn(),
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

vi.mock('@/providers/AuthProvider', () => ({
  useAuth: vi.fn(),
}))

vi.mock('@/lib/toast', () => ({
  notify: {
    error: vi.fn(),
    success: vi.fn(),
  },
}))

const cluster = {
  id: 7,
  name: '生产-ACK',
  description: '核心业务',
  version: 'v1.34.3-aliyun.1',
  mode: 'kubeconfig_data',
  status: 'ready',
}

const chatSendMessage = vi.fn()

function networkError(): Error & { code: string; request: object } {
  return Object.assign(new Error('Network Error'), { code: 'ERR_NETWORK', request: {} })
}

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise
    reject = rejectPromise
  })
  return { promise, resolve, reject }
}

async function withoutDetailPolling(run: () => Promise<void>) {
  const timer = {} as ReturnType<typeof window.setInterval>
  const setIntervalSpy = vi.spyOn(window, 'setInterval').mockImplementation(() => timer)
  try {
    await run()
  } finally {
    setIntervalSpy.mockRestore()
  }
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

function ChatPromptProbe() {
  const location = useLocation()
  const state = location.state as { quickMessage?: string } | null
  return <output data-testid="quick-message">{state?.quickMessage}</output>
}

function ClusterRouteProbe() {
  const navigate = useNavigate()
  return (
    <>
      <button type="button" onClick={() => navigate('/clusters/8')}>switch-cluster</button>
      <Routes><Route path="/clusters/:id" element={<ClusterDetailPage />} /></Routes>
    </>
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(useConfirm).mockReturnValue(vi.fn().mockResolvedValue(true))
  vi.mocked(useChatAssistant).mockReturnValue({
    messages: [],
    sessionId: 'test-session',
    sending: false,
    renderMd: vi.fn((text?: string) => text ?? ''),
    ensureSession: vi.fn(),
    loadHistory: vi.fn().mockResolvedValue(undefined),
    sendMessage: chatSendMessage.mockResolvedValue(undefined),
    clearChat: vi.fn().mockResolvedValue(undefined),
    scrollBottom: vi.fn(),
  })
  vi.mocked(useClusters).mockReturnValue({
    clusters: [cluster],
    selectedClusterId: String(cluster.id),
    currentClusterId: cluster.id,
    loadClusters: vi.fn().mockResolvedValue(undefined),
    selectCluster: vi.fn(),
  })
  vi.mocked(useAuth).mockReturnValue({
    user: { username: 'admin' },
    permissions: [],
    loading: false,
    authenticated: true,
    can: () => true,
    refresh: vi.fn(),
    onLoggedIn: vi.fn(),
    logout: vi.fn(),
    loginNotice: null,
    setLoginNotice: vi.fn(),
    expiredReason: null,
    dismissExpired: vi.fn(),
  } as unknown as ReturnType<typeof useAuth>)
  vi.mocked(getSecurityStatus).mockResolvedValue({ secret_key_ready: true })
  vi.mocked(getDashboardSummary).mockResolvedValue({
    cluster_count: 1,
    ready_clusters: 1,
    problem_clusters: 0,
    unknown_clusters: 0,
    pending_approvals: 0,
    active_alerts: 0,
    sync_running: false,
    sync_source: 'auto',
  })
  vi.mocked(listClusters).mockResolvedValue({ clusters: [cluster] })
  vi.mocked(listAudits).mockResolvedValue({ audits: [] })
  vi.mocked(getModelStatus).mockResolvedValue({ current: null })
  vi.mocked(listNodes).mockResolvedValue({
    nodes: [{ name: '生产节点-01', ready: true, version: 'v1.34.3' }],
  })
  vi.mocked(listNodeMetrics).mockResolvedValue({ metrics: [] })
  vi.mocked(getClusterSyncStatus).mockResolvedValue({ running: false, clusters: [] })
  vi.mocked(listClusterSyncResources).mockResolvedValue({ resources: [] })
  vi.mocked(listClusterResourceSnapshots).mockResolvedValue({ snapshots: [] })
  vi.mocked(listClusterSyncTasks).mockResolvedValue({ tasks: [] })
  vi.mocked(listClusterDeletedResources).mockResolvedValue({ resources: [] })
  vi.mocked(syncCluster).mockResolvedValue({ status: 'ready' })
})

describe('集群域国际化资源', () => {
  it('提供关键中英文文案、英文复数和双语快捷提示', async () => {
    const zh = await createAppI18n('zh')
    const en = await createAppI18n('en')

    expect(zh.t('dashboard:page.title')).toBe('运维总览')
    expect(en.t('dashboard:page.title')).toBe('Operations overview')
    expect(zh.t('clusters:page.connectedCount', { count: 1, max: 5 })).toBe('已接入 1/5 个集群')
    expect(en.t('clusters:page.connectedCount', { count: 1, max: 5 })).toBe('1/5 cluster connected')
    expect(en.t('clusters:page.connectedCount', { count: 2, max: 5 })).toBe('2/5 clusters connected')
    expect(zh.t('dashboard:health.clusterCount', { count: 1 })).toBe('1 个集群')
    expect(en.t('dashboard:health.clusterCount', { count: 1 })).toBe('1 Cluster')
    expect(en.t('dashboard:health.clusterCount', { count: 2 })).toBe('2 Clusters')
    expect(zh.t('operations:quickActions.overview.prompt')).toContain('所有已接入集群')
    expect(en.t('operations:quickActions.overview.prompt')).toContain('all connected clusters')
  })
})

describe('总览页面国际化', () => {
  it('切换语言时更新页面与状态，保留业务名称并发送当前语言快捷提示', async () => {
    const i18n = await createAppI18n('zh')
    const user = userEvent.setup()

    render(
      <I18nextProvider i18n={i18n}>
        <MemoryRouter initialEntries={['/']}>
          <Routes>
            <Route path="/" element={<DashboardPage />} />
            <Route path="/chat" element={<ChatPromptProbe />} />
          </Routes>
        </MemoryRouter>
      </I18nextProvider>,
    )

    expect(await screen.findByRole('heading', { name: '运维总览' })).toBeInTheDocument()
    expect(screen.getByText('生产-ACK')).toBeInTheDocument()
    expect(screen.getByText('就绪')).toBeInTheDocument()

    await act(() => i18n.changeLanguage('en'))

    expect(screen.getByRole('heading', { name: 'Operations overview' })).toBeInTheDocument()
    expect(screen.getByText('生产-ACK')).toBeInTheDocument()
    expect(screen.getByText('Ready')).toBeInTheDocument()
    expect(screen.getByText('1 Cluster')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: /Cluster overview/u }))
    expect(screen.getByTestId('quick-message')).toHaveTextContent(
      'Show me an overview of all connected clusters, including their node and Pod status.',
    )
  })

  it('未知中文审计结果在英文界面保持原值，不由页面猜测翻译', async () => {
    vi.mocked(listAudits).mockResolvedValue({
      audits: [{ id: 1, action: 'acknowledge_alert', operator: 'admin', result: '已确认', created_at: '2026-07-10T08:00:00Z' }],
    })
    const i18n = await createAppI18n('en')

    render(
      <I18nextProvider i18n={i18n}>
        <MemoryRouter><DashboardPage /></MemoryRouter>
      </I18nextProvider>,
    )

    expect(await screen.findByText('已确认')).toBeInTheDocument()
    expect(screen.queryByText('Acknowledged')).not.toBeInTheDocument()
  })

  it('刷新请求失败时显示本地化错误并保留最后一次成功数据', async () => {
    vi.mocked(getDashboardSummary).mockResolvedValueOnce({
      cluster_count: 1,
      ready_clusters: 1,
      problem_clusters: 0,
      unknown_clusters: 0,
      pending_approvals: 0,
      active_alerts: 0,
      sync_running: false,
    })
    vi.mocked(listClusters).mockResolvedValueOnce({ clusters: [cluster] })
    vi.mocked(getModelStatus).mockResolvedValueOnce({ current: { name: '模型-A', ready: true } })
    const i18n = await createAppI18n('en')
    const user = userEvent.setup()

    render(
      <I18nextProvider i18n={i18n}>
        <MemoryRouter><DashboardPage /></MemoryRouter>
      </I18nextProvider>,
    )

    expect(await screen.findByText('生产-ACK')).toBeInTheDocument()
    expect(await screen.findByText('模型-A')).toBeInTheDocument()
    vi.mocked(getDashboardSummary).mockRejectedValueOnce(networkError())
    vi.mocked(listClusters).mockRejectedValueOnce(networkError())
    vi.mocked(listAudits).mockRejectedValueOnce(networkError())
    vi.mocked(getModelStatus).mockRejectedValueOnce(networkError())

    await user.click(screen.getByRole('button', { name: 'Refresh' }))

    expect(await screen.findByText('Unable to reach the server')).toBeInTheDocument()
    expect(screen.getByText('生产-ACK')).toBeInTheDocument()
    expect(screen.getByText('模型-A')).toBeInTheDocument()
    expect(screen.queryByText('No model configured')).not.toBeInTheDocument()
  })

  it('模型首次加载失败时显示错误而不是未配置状态', async () => {
    vi.mocked(getModelStatus).mockRejectedValueOnce(networkError())
    const i18n = await createAppI18n('en')

    render(
      <I18nextProvider i18n={i18n}>
        <MemoryRouter><DashboardPage /></MemoryRouter>
      </I18nextProvider>,
    )

    expect(await screen.findByText('Unable to reach the server')).toBeInTheDocument()
    expect(screen.queryByText('No model configured')).not.toBeInTheDocument()
  })
})

describe('集群和节点页面国际化', () => {
  it('集群列表切换语言后更新状态但不翻译业务字段', async () => {
    const i18n = await createAppI18n('zh')

    render(
      <I18nextProvider i18n={i18n}>
        <MemoryRouter>
          <ClustersPage />
        </MemoryRouter>
      </I18nextProvider>,
    )

    expect(await screen.findByRole('heading', { name: 'Kubernetes 集群' })).toBeInTheDocument()
    expect(screen.getByText('生产-ACK')).toBeInTheDocument()
    expect(screen.getByText('就绪')).toBeInTheDocument()

    await act(() => i18n.changeLanguage('en'))

    expect(screen.getByRole('heading', { name: 'Kubernetes clusters' })).toBeInTheDocument()
    expect(screen.getByText('生产-ACK')).toBeInTheDocument()
    expect(screen.getByText('v1.34.3-aliyun.1')).toBeInTheDocument()
    expect(screen.getByText('Ready')).toBeInTheDocument()
    expect(screen.getByText(/1\/5 cluster connected/u)).toBeInTheDocument()
  })

  it('节点列表切换语言后更新界面且保留节点名和版本', async () => {
    const i18n = await createAppI18n('zh')

    render(
      <I18nextProvider i18n={i18n}>
        <NodeList clusterId={cluster.id} />
      </I18nextProvider>,
    )

    expect(await screen.findByRole('heading', { name: '节点状态' })).toBeInTheDocument()
    expect(await screen.findByText('生产节点-01')).toBeInTheDocument()

    await act(() => i18n.changeLanguage('en'))

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'Node status' })).toBeInTheDocument()
    })
    expect(screen.getByText('生产节点-01')).toBeInTheDocument()
    expect(screen.getByText('v1.34.3')).toBeInTheDocument()
  })

  it('节点刷新失败时显示本地化错误并保留已有节点', async () => {
    const i18n = await createAppI18n('en')
    const user = userEvent.setup()

    render(
      <I18nextProvider i18n={i18n}>
        <NodeList clusterId={cluster.id} />
      </I18nextProvider>,
    )

    expect(await screen.findByText('生产节点-01')).toBeInTheDocument()
    vi.mocked(listNodes).mockRejectedValueOnce(networkError())
    await user.click(screen.getByRole('button', { name: 'Refresh' }))

    expect(await screen.findByText('Unable to reach the server')).toBeInTheDocument()
    expect(screen.getByText('生产节点-01')).toBeInTheDocument()
    expect(screen.queryByText('No node data is available for this cluster')).not.toBeInTheDocument()
  })

  it('切换集群后不显示上一集群的节点或指标', async () => {
    vi.mocked(listNodes).mockReset()
      .mockResolvedValueOnce({ nodes: [{ name: 'shared-node', ready: true, version: 'v1.33.0' }] })
      .mockRejectedValueOnce(networkError())
      .mockResolvedValueOnce({ nodes: [{ name: 'shared-node', ready: true, version: 'v1.34.0' }] })
    vi.mocked(listNodeMetrics).mockReset()
      .mockResolvedValueOnce({
        metrics: [{
          name: 'shared-node',
          cpu_usage_milli: 110,
          cpu_allocatable_milli: 1000,
          cpu_usage_percent: 11,
          memory_usage_bytes: 1024,
          memory_allocatable_bytes: 2048,
          memory_usage_percent: 50,
        }],
      })
      .mockRejectedValueOnce(networkError())
    const i18n = await createAppI18n('en')
    const view = render(
      <I18nextProvider i18n={i18n}>
        <NodeList clusterId={7} />
      </I18nextProvider>,
    )

    expect(await screen.findByText('v1.33.0')).toBeInTheDocument()
    expect(screen.getByText('11%')).toBeInTheDocument()

    view.rerender(
      <I18nextProvider i18n={i18n}>
        <NodeList clusterId={8} />
      </I18nextProvider>,
    )

    expect(await screen.findByText('Unable to reach the server')).toBeInTheDocument()
    expect(screen.queryByText('v1.33.0')).not.toBeInTheDocument()
    expect(screen.queryByText('11%')).not.toBeInTheDocument()

    view.rerender(
      <I18nextProvider i18n={i18n}>
        <NodeList clusterId={9} />
      </I18nextProvider>,
    )

    expect(await screen.findByText('v1.34.0')).toBeInTheDocument()
    expect(await screen.findByText(/metrics-server metrics are temporarily unavailable/u)).toBeInTheDocument()
    expect(screen.queryByText('11%')).not.toBeInTheDocument()
  })

  it('忽略集群切换后才返回的过期节点响应', async () => {
    const oldClusterNodes = deferred<{ nodes: Array<{ name: string; ready: boolean; version: string }> }>()
    vi.mocked(listNodes).mockReset()
      .mockReturnValueOnce(oldClusterNodes.promise)
      .mockResolvedValueOnce({ nodes: [{ name: 'cluster-b-node', ready: true, version: 'v1.34.0' }] })
    vi.mocked(listNodeMetrics).mockReset().mockResolvedValue({ metrics: [] })
    const i18n = await createAppI18n('en')
    const view = render(
      <I18nextProvider i18n={i18n}>
        <NodeList clusterId={7} />
      </I18nextProvider>,
    )

    view.rerender(
      <I18nextProvider i18n={i18n}>
        <NodeList clusterId={8} />
      </I18nextProvider>,
    )
    expect(await screen.findByText('cluster-b-node')).toBeInTheDocument()

    await act(async () => {
      oldClusterNodes.resolve({ nodes: [{ name: 'late-cluster-a-node', ready: true, version: 'v1.33.0' }] })
      await oldClusterNodes.promise
    })

    expect(screen.getByText('cluster-b-node')).toBeInTheDocument()
    expect(screen.queryByText('late-cluster-a-node')).not.toBeInTheDocument()
  })

  it('安全状态请求失败时按未就绪处理并显示本地化错误', async () => {
    vi.mocked(useClusters).mockReturnValue({
      clusters: [],
      selectedClusterId: '',
      currentClusterId: undefined,
      loadClusters: vi.fn().mockResolvedValue(undefined),
      selectCluster: vi.fn(),
    })
    vi.mocked(getSecurityStatus).mockRejectedValueOnce(networkError())
    const i18n = await createAppI18n('en')
    const user = userEvent.setup()

    render(
      <I18nextProvider i18n={i18n}>
        <MemoryRouter><ClustersPage /></MemoryRouter>
      </I18nextProvider>,
    )

    expect(await screen.findByText('Unable to reach the server')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Import cluster' }))
    expect(screen.getByRole('button', { name: 'Import' })).toBeDisabled()
  })
})

describe('集群详情同步状态', () => {
  it('双语标记断连历史数据并保持 Kubernetes 原始字段', async () => {
    await withoutDetailPolling(async () => {
    vi.mocked(useClusters).mockReturnValue({
      clusters: [{ ...cluster, status: 'disconnected' }],
      selectedClusterId: String(cluster.id),
      currentClusterId: cluster.id,
      loadClusters: vi.fn().mockResolvedValue(undefined),
      selectCluster: vi.fn(),
    })
    vi.mocked(getClusterSyncStatus).mockResolvedValue({
      running: false,
      source: 'auto',
      clusters: [{
        cluster_id: cluster.id,
        status: 'failed',
        data_stale: true,
        incremental_status: 'not_run',
        last_source: 'auto',
        last_successful_at: '2026-07-10T08:00:00Z',
        last_synced_at: '2026-07-10T08:00:00Z',
        error: 'dial tcp 10.0.0.1:6443: connectex',
      }],
    })
    vi.mocked(listClusterSyncResources).mockResolvedValue({
      resources: [{
        api_version: 'v1',
        kind: 'Pod',
        resource_name: 'pods',
        item_count: 1,
        last_source: 'auto',
        last_change_count: 0,
        full_status: 'success',
        incremental_status: 'success',
        incremental_rounds: 1,
        last_resource_version: 'rv-100',
      }],
    })
    vi.mocked(listClusterResourceSnapshots).mockResolvedValue({
      snapshots: [{
        resource_name: 'pods',
        namespace: '业务空间',
        name: '支付服务-pod',
        status: 'Running',
        ready: '1/1',
        node_name: 'node-a',
        resource_version: 'rv-100',
        last_seen_at: '2026-07-10T08:00:00Z',
        summary: '原始摘要：CPU 正常',
      }],
    })
    vi.mocked(listClusterSyncTasks).mockResolvedValue({ tasks: [] })
    vi.mocked(listClusterDeletedResources).mockResolvedValue({ resources: [] })
    const i18n = await createAppI18n('zh')
    const user = userEvent.setup()

    render(
      <I18nextProvider i18n={i18n}>
        <TooltipProvider>
          <MemoryRouter initialEntries={[`/clusters/${cluster.id}`]}>
            <Routes><Route path="/clusters/:id" element={<ClusterDetailPage />} /></Routes>
          </MemoryRouter>
        </TooltipProvider>
      </I18nextProvider>,
    )

    await user.click(await screen.findByRole('button', { name: '同步状态' }))
    expect(await screen.findByText('当前集群连接失败')).toBeInTheDocument()
    expect(screen.getAllByText('同步失败').length).toBeGreaterThan(0)
    expect(screen.getAllByText(/未执行/u).length).toBeGreaterThan(0)
    expect(screen.getByText('上次成功')).toBeInTheDocument()
    expect(screen.getByText('数据已过期')).toBeInTheDocument()
    expect(screen.getAllByText('pods')).toHaveLength(2)
    expect(screen.getByText('业务空间')).toBeInTheDocument()
    expect(screen.getByText('支付服务-pod')).toBeInTheDocument()
    expect(screen.getByText('原始摘要：CPU 正常')).toBeInTheDocument()

    await act(() => i18n.changeLanguage('en'))

    expect(screen.getByText('The cluster is currently disconnected')).toBeInTheDocument()
    expect(screen.getAllByText('Synchronization failed').length).toBeGreaterThan(0)
    expect(screen.getAllByText(/Not run|not run/u).length).toBeGreaterThan(0)
    expect(screen.getByText('Last success')).toBeInTheDocument()
    expect(screen.getByText('Data is stale')).toBeInTheDocument()
    expect(screen.getAllByText('pods')).toHaveLength(2)
    expect(screen.getByText('业务空间')).toBeInTheDocument()
    expect(screen.getByText('支付服务-pod')).toBeInTheDocument()
    expect(screen.getByText('原始摘要：CPU 正常')).toBeInTheDocument()

    const snapshotCallsBeforeRefresh = vi.mocked(listClusterResourceSnapshots).mock.calls.length
    vi.mocked(listClusterResourceSnapshots).mockRejectedValueOnce(networkError())
    await user.click(screen.getByRole('button', { name: 'Sync / Refresh' }))

    expect(await screen.findByText(/Unable to reach the server/u)).toBeInTheDocument()
    expect(listClusterResourceSnapshots).toHaveBeenCalledTimes(snapshotCallsBeforeRefresh + 1)
    expect(screen.getByText('支付服务-pod')).toBeInTheDocument()
    expect(screen.getByText('原始摘要：CPU 正常')).toBeInTheDocument()
    })
  })

  it('切换路由集群时立即隔离上一集群的详情状态', async () => {
    await withoutDetailPolling(async () => {
      const nextClusterSnapshots = deferred<{ snapshots: Array<{ resource_name: string; name: string }> }>()
      vi.mocked(useClusters).mockReturnValue({
        clusters: [cluster, { ...cluster, id: 8, name: '测试-ACK' }],
        selectedClusterId: String(cluster.id),
        currentClusterId: cluster.id,
        loadClusters: vi.fn().mockResolvedValue(undefined),
        selectCluster: vi.fn(),
      })
      vi.mocked(listClusterResourceSnapshots).mockImplementation((clusterId) => {
        if (clusterId === cluster.id) {
          return Promise.resolve({
            snapshots: [{
              resource_name: 'pods',
              namespace: 'namespace-a',
              name: 'cluster-a-object',
              summary: 'cluster-a-summary',
            }],
          })
        }
        return nextClusterSnapshots.promise
      })
      const i18n = await createAppI18n('en')
      const user = userEvent.setup()

      render(
        <I18nextProvider i18n={i18n}>
          <TooltipProvider>
            <MemoryRouter initialEntries={[`/clusters/${cluster.id}`]}>
              <ClusterRouteProbe />
            </MemoryRouter>
          </TooltipProvider>
        </I18nextProvider>,
      )

      await user.click(await screen.findByRole('button', { name: 'Synchronization' }))
      expect(await screen.findByText('cluster-a-object')).toBeInTheDocument()

      await user.click(screen.getByRole('button', { name: 'switch-cluster' }))
      await waitFor(() => {
        expect(listClusterResourceSnapshots).toHaveBeenCalledWith(8, undefined)
      })
      expect(screen.queryByText('cluster-a-object')).not.toBeInTheDocument()
      expect(screen.queryByText('cluster-a-summary')).not.toBeInTheDocument()
    })
  })

  it('切换资源筛选后请求失败不会显示上一筛选的快照和删除记录', async () => {
    await withoutDetailPolling(async () => {
      await withSelectDomSupport(async () => {
      vi.mocked(listClusterSyncResources).mockResolvedValue({
        resources: [{
          api_version: 'v1',
          kind: 'Pod',
          resource_name: 'pods',
          item_count: 1,
          full_status: 'success',
          incremental_status: 'success',
        }],
      })
      vi.mocked(listClusterResourceSnapshots).mockImplementation((_clusterId, resourceName) => {
        if (resourceName === 'pods') return Promise.reject(networkError())
        return Promise.resolve({
          snapshots: [{
            resource_name: 'services',
            namespace: 'namespace-all',
            name: 'all-filter-object',
            summary: 'all-filter-summary',
          }],
        })
      })
      vi.mocked(listClusterDeletedResources).mockImplementation((_clusterId, resourceName) => {
        if (resourceName === 'pods') return Promise.reject(networkError())
        return Promise.resolve({
          resources: [{
            resource_name: 'services',
            namespace: 'namespace-all',
            name: 'all-filter-deleted-object',
            deleted_at: '2026-07-10T08:00:00Z',
          }],
        })
      })
      const i18n = await createAppI18n('en')
      const user = userEvent.setup()

      render(
        <I18nextProvider i18n={i18n}>
          <TooltipProvider>
            <MemoryRouter initialEntries={[`/clusters/${cluster.id}`]}>
              <Routes><Route path="/clusters/:id" element={<ClusterDetailPage />} /></Routes>
            </MemoryRouter>
          </TooltipProvider>
        </I18nextProvider>,
      )

      await user.click(await screen.findByRole('button', { name: 'Synchronization' }))
      expect(await screen.findByText('all-filter-object')).toBeInTheDocument()
      expect(screen.getByText(/all-filter-deleted-object/u)).toBeInTheDocument()

      screen.getByRole('combobox').focus()
      await user.keyboard('{Enter}{ArrowDown}{Enter}')
      await waitFor(() => {
        expect(listClusterResourceSnapshots).toHaveBeenCalledWith(cluster.id, 'pods')
      })

      expect(await screen.findByText(/Unable to reach the server/u)).toBeInTheDocument()
      expect(screen.queryByText('all-filter-object')).not.toBeInTheDocument()
      expect(screen.queryByText('all-filter-summary')).not.toBeInTheDocument()
      expect(screen.queryByText(/all-filter-deleted-object/u)).not.toBeInTheDocument()
      })
    })
  })
})

describe('对话快捷操作', () => {
  it('点击快捷动作时发送当前语言的 prompt', async () => {
    vi.mocked(getModelStatus).mockResolvedValueOnce({ current: { ready: true } })
    const i18n = await createAppI18n('zh')
    const user = userEvent.setup()

    render(
      <I18nextProvider i18n={i18n}>
        <MemoryRouter initialEntries={['/chat']}><ChatPage /></MemoryRouter>
      </I18nextProvider>,
    )

    expect(await screen.findByRole('button', { name: '集群概览' })).toBeInTheDocument()
    await act(() => i18n.changeLanguage('en'))
    await user.click(screen.getByRole('button', { name: 'Cluster overview' }))

    expect(chatSendMessage).toHaveBeenCalledWith(
      'Show me an overview of all connected clusters, including their node and Pod status.',
    )
  })
})
