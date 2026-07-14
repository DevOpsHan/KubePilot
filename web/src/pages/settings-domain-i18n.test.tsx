import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { I18nextProvider } from 'react-i18next'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppI18n } from '@/i18n'
import { formatDateTime } from '@/i18n/format'
import {
  createCustomRule,
  createModel,
  createUser,
  deleteCustomRule,
  deleteModel,
  deleteUser,
  getInspectionRules,
  getModelStatus,
  getSecurityStatus,
  listClusters,
  listCustomRules,
  listModels,
  listUsers,
  resetInspectionRule,
  resetUserPassword,
  setDefaultModel,
  testCustomRule,
  testModel,
  updateClusterPrometheus,
  updateCustomRule,
  updateInspectionRule,
  updateModel,
  updateUser,
} from '@/lib/api'
import { notify } from '@/lib/toast'
import { useAuth } from '@/providers/AuthProvider'
import { useConfirm } from '@/providers/ConfirmProvider'
import { InspectionSettingsPage } from './InspectionSettingsPage'
import { ModelsPage } from './ModelsPage'
import { UsersPage } from './UsersPage'

vi.mock('@/lib/api', () => ({
  createCustomRule: vi.fn(),
  createModel: vi.fn(),
  createUser: vi.fn(),
  deleteCustomRule: vi.fn(),
  deleteModel: vi.fn(),
  deleteUser: vi.fn(),
  getInspectionRules: vi.fn(),
  getModelStatus: vi.fn(),
  getSecurityStatus: vi.fn(),
  listClusters: vi.fn(),
  listCustomRules: vi.fn(),
  listModels: vi.fn(),
  listUsers: vi.fn(),
  resetInspectionRule: vi.fn(),
  resetUserPassword: vi.fn(),
  setDefaultModel: vi.fn(),
  testCustomRule: vi.fn(),
  testModel: vi.fn(),
  updateClusterPrometheus: vi.fn(),
  updateCustomRule: vi.fn(),
  updateInspectionRule: vi.fn(),
  updateModel: vi.fn(),
  updateUser: vi.fn(),
}))

vi.mock('@/providers/AuthProvider', () => ({
  useAuth: vi.fn(),
}))

vi.mock('@/providers/ConfirmProvider', () => ({
  useConfirm: vi.fn(),
}))

vi.mock('@/lib/toast', () => ({
  notify: {
    error: vi.fn(),
    info: vi.fn(),
    success: vi.fn(),
  },
}))

const cluster = { id: 7, name: '生产-ACK' }
const model = {
  id: 12,
  source: 'database',
  read_only: false,
  name: '生产诊断模型',
  provider: 'openai-compatible',
  base_url: 'https://models.example/原始路径',
  model_name: 'ops-model-原始标识',
  max_completion_tokens: 4096,
  timeout_seconds: 120,
  temperature: 0.2,
  is_default: true,
  enabled: true,
  has_api_key: true,
}
const builtinRule = {
  name: 'PodHighRestarts',
  source: 'builtin',
  description: '原始内置规则说明',
  severity: 'critical',
  enabled: true,
  overridden: true,
  has_threshold: true,
  threshold: 5,
  threshold_unit: '次',
}
const prometheusRule = {
  name: 'NodeHighCPU',
  source: 'prometheus',
  description: '原始指标规则说明',
  severity: 'warning',
  enabled: true,
  overridden: false,
  has_threshold: true,
  threshold: 80,
  threshold_unit: '%',
}
const customRule = {
  id: 31,
  name: '磁盘原始规则名',
  promql: '100 - node_filesystem_avail_bytes 原始表达式',
  operator: '>',
  threshold: 90,
  severity: 'warning',
  object_label: 'instance',
  description: '原始自定义规则说明',
  enabled: true,
  inherited: false,
}
const userRecord = {
  id: 9,
  username: 'ops-zhangsan',
  display_name: '张三原始姓名',
  email: 'zhangsan@example.com',
  role: 'cluster_admin',
  status: 'active',
  expires_at: '2027-07-10T16:00:00Z',
  last_login_at: '2026-07-10T08:00:00Z',
  password_attempted_at: '2026-07-10T08:30:00Z',
  password_attempt_count: 2,
  created_at: '2026-01-02T03:04:05Z',
  updated_at: '2026-07-10T09:00:00Z',
  cluster_grants: [{ cluster_id: cluster.id }],
}

const confirmAction = vi.fn().mockResolvedValue(false)
const logoutAction = vi.fn().mockResolvedValue(undefined)
const setLoginNoticeAction = vi.fn()

function networkError(): Error & { code: string; request: object } {
  return Object.assign(new Error('Network Error'), { code: 'ERR_NETWORK', request: {} })
}

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((resolvePromise) => {
    resolve = resolvePromise
  })
  return { promise, resolve }
}

async function renderPage(page: React.ReactNode, language: 'zh' | 'en' = 'zh') {
  const i18n = await createAppI18n(language)
  render(
    <I18nextProvider i18n={i18n}>
      <MemoryRouter>{page}</MemoryRouter>
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
  logoutAction.mockResolvedValue(undefined)
  vi.mocked(useConfirm).mockReturnValue(confirmAction)
  vi.mocked(useAuth).mockReturnValue({
    user: { id: 1, username: 'admin', role: 'global_admin' },
    permissions: ['users:manage'],
    loading: false,
    loggingOut: false,
    authenticated: true,
    can: () => true,
    refresh: vi.fn().mockResolvedValue(undefined),
    onLoggedIn: vi.fn().mockResolvedValue(undefined),
    logout: logoutAction,
    loginNotice: null,
    setLoginNotice: setLoginNoticeAction,
    expiredReason: null,
    dismissExpired: vi.fn(),
  })
  vi.mocked(listClusters).mockResolvedValue({ clusters: [cluster] })
  vi.mocked(getModelStatus).mockResolvedValue({
    current: {
      ready: true,
      source: 'database',
      profile_id: model.id,
      name: model.name,
      provider: model.provider,
      base_url: model.base_url,
      model_name: model.model_name,
    },
  })
  vi.mocked(listModels).mockResolvedValue({ models: [model] })
  vi.mocked(getSecurityStatus).mockResolvedValue({ secret_key_ready: true })
  vi.mocked(createModel).mockResolvedValue({ model })
  vi.mocked(updateModel).mockResolvedValue({ model })
  vi.mocked(setDefaultModel).mockResolvedValue({ model })
  vi.mocked(testModel).mockResolvedValue({ ok: true, latency_ms: 42, message: '原始模型回复 OK' })
  vi.mocked(deleteModel).mockResolvedValue({ status: 'ok' })
  vi.mocked(getInspectionRules).mockResolvedValue({
    rules: [builtinRule, prometheusRule],
    prometheus: { configured: true, reachable: true, source: 'global' },
  })
  vi.mocked(listCustomRules).mockResolvedValue({ rules: [customRule] })
  vi.mocked(updateInspectionRule).mockResolvedValue({ rule: builtinRule })
  vi.mocked(resetInspectionRule).mockResolvedValue({ status: 'ok' })
  vi.mocked(updateClusterPrometheus).mockResolvedValue({ status: 'ok' })
  vi.mocked(createCustomRule).mockResolvedValue({ rule: customRule })
  vi.mocked(updateCustomRule).mockResolvedValue({ rule: customRule })
  vi.mocked(deleteCustomRule).mockResolvedValue({ status: 'ok' })
  vi.mocked(testCustomRule).mockResolvedValue({
    ok: true,
    sample_count: 2,
    matched: 1,
    preview: [{ name: '生产节点-01', value: 91.5, hit: true }],
  })
  vi.mocked(listUsers).mockResolvedValue({
    users: [userRecord],
    roles: ['global_admin', 'cluster_admin'],
  })
  vi.mocked(createUser).mockResolvedValue({ user: userRecord })
  vi.mocked(updateUser).mockResolvedValue({ user: userRecord })
  vi.mocked(resetUserPassword).mockResolvedValue({ temporary_password: 'k8s@ops123' })
  vi.mocked(deleteUser).mockResolvedValue({ status: 'ok' })
})

describe('模型设置国际化', () => {
  it('切换语言时重译系统状态并保留模型原始值和编辑表单', async () => {
    const user = userEvent.setup()
    const i18n = await renderPage(<ModelsPage />)

    expect(await screen.findByRole('heading', { name: '智能助手 · 模型管理' })).toBeInTheDocument()
    expect(screen.getAllByText(model.name).length).toBeGreaterThan(0)
    expect(screen.getByText(model.base_url)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '编辑' }))
    const dialog = await screen.findByRole('dialog')
    const nameInput = within(dialog).getByDisplayValue(model.name)
    const apiKeyInput = within(dialog).getByPlaceholderText(/保留原已保存密钥/u)
    await user.clear(nameInput)
    await user.type(nameInput, '模型原始草稿')
    await user.type(apiKeyInput, 'sk-原始草稿')

    await act(() => i18n.changeLanguage('en'))

    expect(screen.getByText('AI assistant · Model management')).toBeInTheDocument()
    expect(screen.getByText(/Current active model:/u)).toHaveTextContent(model.model_name)
    expect(screen.getByText(model.base_url)).toBeInTheDocument()
    expect(within(dialog).getByRole('heading', { name: 'Edit model configuration' })).toBeInTheDocument()
    expect(nameInput).toHaveValue('模型原始草稿')
    expect(apiKeyInput).toHaveValue('sk-原始草稿')
    expect(within(dialog).getByRole('button', { name: 'Save changes' })).toBeInTheDocument()
  })

  it('异步连接测试按完成时语言显示结果并本地化网络错误', async () => {
    const pending = deferred<{ ok: boolean; latency_ms: number; message: string }>()
    vi.mocked(testModel).mockReturnValueOnce(pending.promise)
    const user = userEvent.setup()
    const i18n = await renderPage(<ModelsPage />)
    await screen.findByRole('heading', { name: '智能助手 · 模型管理' })

    await user.click(await screen.findByRole('button', { name: '测试连接' }))
    await act(() => i18n.changeLanguage('en'))
    await act(async () => pending.resolve({ ok: true, latency_ms: 42, message: '原始模型回复 OK' }))

    expect(await screen.findByText('Connection successful')).toBeInTheDocument()
    expect(screen.getByText(/原始模型回复 OK/u)).toBeInTheDocument()

    vi.mocked(testModel).mockRejectedValueOnce(networkError())
    await user.click(screen.getByRole('button', { name: 'Test connection' }))
    expect(await screen.findByText(/Unable to reach the server/u)).toBeInTheDocument()
  })
})

describe('巡检设置国际化', () => {
  it('切换语言时重译三层来源、严重级别和操作并保留规则原始值', async () => {
    const i18n = await renderPage(<InspectionSettingsPage />)

    expect(await screen.findByRole('heading', { name: '巡检设置' })).toBeInTheDocument()
    expect(await screen.findByText(customRule.promql, { exact: false })).toBeInTheDocument()
    expect(screen.getByText('Pod 重启次数超过阈值')).toBeInTheDocument()
    expect(screen.getAllByText('严重').length).toBeGreaterThan(0)
    expect(screen.getByText(/来源.*已配置/u)).toBeInTheDocument()

    await act(() => i18n.changeLanguage('en'))

    expect(screen.getByRole('heading', { name: 'Inspection settings' })).toBeInTheDocument()
    expect(screen.getByText('Source: Configured')).toBeInTheDocument()
    expect(screen.getAllByText('Critical').length).toBeGreaterThan(0)
    expect(screen.getByText('Pod restart count exceeds the threshold')).toBeInTheDocument()
    expect(screen.getByText(
      'The 5-minute average node CPU usage exceeds the threshold (requires Prometheus)',
    )).toBeInTheDocument()
    expect(screen.getByText('restarts')).toBeInTheDocument()
    expect(screen.queryByText('次')).not.toBeInTheDocument()
    expect(screen.getByText(customRule.promql, { exact: false })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Add rule' })).toBeInTheDocument()
  })

  it('切换语言不重置 Prometheus 和 PromQL 草稿，并本地化测试结果', async () => {
    const user = userEvent.setup()
    const i18n = await renderPage(<InspectionSettingsPage />)
    await screen.findByRole('heading', { name: '巡检设置' })

    const prometheusInput = await screen.findByPlaceholderText(/已配置全局地址/u)
    await user.type(prometheusInput, 'https://prom.example/原始路径')
    await user.click(screen.getByRole('button', { name: '新增规则' }))
    const dialog = await screen.findByRole('dialog')
    const nameInput = within(dialog).getByPlaceholderText(/磁盘使用率过高/u)
    const promqlInput = within(dialog).getByPlaceholderText(
      /node_filesystem_avail_bytes/u,
    )
    await user.type(nameInput, '原始规则草稿')
    await user.type(promqlInput, 'up == 1 # 原始作业')

    await act(() => i18n.changeLanguage('en'))

    expect(prometheusInput).toHaveValue('https://prom.example/原始路径')
    expect(nameInput).toHaveValue('原始规则草稿')
    expect(promqlInput).toHaveValue('up == 1 # 原始作业')
    expect(within(dialog).getByRole('heading', { name: 'Add custom rule' })).toBeInTheDocument()
    await user.click(within(dialog).getByRole('button', { name: 'Test' }))
    expect(await within(dialog).findByText(/Found 2 samples; 1 match/u)).toBeInTheDocument()
    expect(within(dialog).getByText(/生产节点-01/u)).toBeInTheDocument()

    vi.mocked(testCustomRule).mockResolvedValueOnce({
      ok: false,
      error: 'context deadline exceeded',
    })
    await user.click(within(dialog).getByRole('button', { name: 'Test' }))
    expect(await within(dialog).findByText(
      'Test failed: context deadline exceeded',
    )).toBeInTheDocument()
  })
})

describe('用户管理国际化', () => {
  function expectRedRequiredMarker(container: HTMLElement, labelText: string) {
    const label = Array.from(container.querySelectorAll('label'))
      .find((candidate) => candidate.textContent?.includes(labelText))
    expect(label, `未找到字段标签：${labelText}`).toBeDefined()
    const marker = label?.querySelector('[data-required-marker="true"]')
    expect(marker).toHaveTextContent('*')
    expect(marker).toHaveClass('text-destructive')
  }

  it('切换语言时重译角色、状态和日期并保留用户与集群原始值', async () => {
    const i18n = await renderPage(<UsersPage />)

    expect(await screen.findByRole('heading', { name: '用户管理' })).toBeInTheDocument()
    expect(screen.getByText(userRecord.display_name)).toBeInTheDocument()
    expect(screen.getByText(cluster.name)).toBeInTheDocument()
    expect(screen.getByText('集群管理员')).toBeInTheDocument()
    expect(screen.getByText(formatDateTime(userRecord.created_at, 'zh'))).toBeInTheDocument()

    await act(() => i18n.changeLanguage('en'))

    expect(screen.getByRole('heading', { name: 'User management' })).toBeInTheDocument()
    expect(screen.getByText(userRecord.display_name)).toBeInTheDocument()
    expect(screen.getByText(cluster.name)).toBeInTheDocument()
    expect(screen.getByText('Cluster admin')).toBeInTheDocument()
    expect(screen.getByText(formatDateTime(userRecord.created_at, 'en'))).toBeInTheDocument()
    expect(screen.getByText('Active')).toBeInTheDocument()
  })

  it('切换语言不重置新增用户表单', async () => {
    await withSelectDomSupport(async () => {
      const user = userEvent.setup()
      const i18n = await renderPage(<UsersPage />)
      await screen.findByRole('heading', { name: '用户管理' })
      await user.click(screen.getByRole('button', { name: '新增用户' }))
      const dialog = await screen.findByRole('dialog')
      const usernameInput = within(dialog).getByPlaceholderText(/ops-user/u)
      const displayNameInput = within(dialog).getByPlaceholderText(/运维同学/u)
      await user.type(usernameInput, 'ops-原始用户')
      await user.type(displayNameInput, '李四原始姓名')

      await act(() => i18n.changeLanguage('en'))

      expect(usernameInput).toHaveValue('ops-原始用户')
      expect(displayNameInput).toHaveValue('李四原始姓名')
      expect(within(dialog).getByRole('heading', { name: 'Add user' })).toBeInTheDocument()
      expect(within(dialog).getByRole('button', { name: 'Create user' })).toBeInTheDocument()
    })
  })

  it('新增和编辑用户抽屉使用红色星号标记必填项', async () => {
    const user = userEvent.setup()
    await renderPage(<UsersPage />)
    await screen.findByRole('heading', { name: '用户管理' })

    await user.click(screen.getByRole('button', { name: '新增用户' }))
    const createDialog = await screen.findByRole('dialog')
    expectRedRequiredMarker(createDialog, '用户名')
    expectRedRequiredMarker(createDialog, '授权集群')

    await user.click(within(createDialog).getByRole('button', { name: '取消' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())

    await user.click(screen.getByRole('button', { name: '编辑' }))
    const editDialog = await screen.findByRole('dialog')
    expectRedRequiredMarker(editDialog, '姓名')
    expectRedRequiredMarker(editDialog, '失效日期')
    expectRedRequiredMarker(editDialog, '授权集群')
  })

  it('英文确认框包含原始用户名，并精确本地化管理员保护错误', async () => {
    confirmAction.mockResolvedValue(true)
    vi.mocked(deleteUser).mockRejectedValueOnce({
      response: { status: 400, data: { error: '不能删除最后一个全局管理员' } },
    })
    const user = userEvent.setup()
    await renderPage(<UsersPage />, 'en')
    await screen.findByRole('heading', { name: 'User management' })

    await user.click(await screen.findByRole('button', { name: 'Reset password' }))
    expect(confirmAction).toHaveBeenLastCalledWith({
      title: 'Reset password',
      message: 'Reset the password for ops-zhangsan to the default password k8s@ops123?',
      confirmText: 'Reset',
    })
    await waitFor(() => expect(notify.success).toHaveBeenCalledWith(
      'Password reset to k8s@ops123',
    ))

    await user.click(screen.getByRole('button', { name: 'Delete' }))
    expect(confirmAction).toHaveBeenLastCalledWith({
      title: 'Delete user',
      message: 'Delete user ops-zhangsan? Data is retained for 7 days and then permanently deleted.',
      confirmText: 'Delete',
      tone: 'danger',
    })
    await waitFor(() => expect(notify.error).toHaveBeenCalledWith(
      'The last global administrator cannot be deleted',
    ))
  })

  it('当前管理员重置自身密码后保存语义通知而不是已翻译字符串', async () => {
    confirmAction.mockResolvedValue(true)
    vi.mocked(useAuth).mockReturnValue({
      user: { id: userRecord.id, username: userRecord.username, role: 'cluster_admin' },
      permissions: ['users:manage'],
      loading: false,
      loggingOut: false,
      authenticated: true,
      can: () => true,
      refresh: vi.fn().mockResolvedValue(undefined),
      onLoggedIn: vi.fn().mockResolvedValue(undefined),
      logout: logoutAction,
      loginNotice: null,
      setLoginNotice: setLoginNoticeAction,
      expiredReason: null,
      dismissExpired: vi.fn(),
    })
    const user = userEvent.setup()
    await renderPage(<UsersPage />, 'en')
    await screen.findByRole('heading', { name: 'User management' })

    await user.click(screen.getByRole('button', { name: 'Reset password' }))

    await waitFor(() => expect(logoutAction).toHaveBeenCalled())
    expect(setLoginNoticeAction).toHaveBeenCalledWith({ kind: 'passwordReset' })
    expect(setLoginNoticeAction).not.toHaveBeenCalledWith(
      'Your administrator password was reset to k8s@ops123. Sign in again and change it.',
    )
  })
})
