import type { ReactNode } from 'react'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const apiMocks = vi.hoisted(() => ({
  getClusterResourceObject: vi.fn(),
  listClusterResourceSnapshots: vi.fn(),
}))
const translationMocks = vi.hoisted(() => ({ t: (key: string) => key }))

vi.mock('@/lib/api', () => apiMocks)
vi.mock('@/i18n', () => ({ normalizeLanguage: () => 'zh' }))
vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: translationMocks.t,
    i18n: { resolvedLanguage: 'zh' },
  }),
}))
vi.mock('@/providers/AuthProvider', () => ({
  useAuth: () => ({ can: () => false }),
}))
vi.mock('@/lib/toast', () => ({
  notify: { success: vi.fn(), error: vi.fn() },
}))
vi.mock('@/components/PodLogs', () => ({ PodLogs: () => null }))
vi.mock('@/components/PodTerminal', () => ({ PodTerminal: () => null }))
vi.mock('@/components/ui/sheet', () => ({
  Sheet: ({ open, children }: { open: boolean; children: ReactNode }) => open ? <div>{children}</div> : null,
  SheetContent: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  SheetHeader: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  SheetTitle: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  SheetDescription: ({ children }: { children: ReactNode }) => <div>{children}</div>,
}))
vi.mock('@/components/ui/tabs', () => ({
  Tabs: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  TabsList: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  TabsTrigger: ({ children }: { children: ReactNode }) => <button type="button">{children}</button>,
  TabsContent: ({ children }: { children: ReactNode }) => <div>{children}</div>,
}))

import { ResourceBrowser } from './ResourceBrowser'

interface Deferred<T> {
  promise: Promise<T>
  resolve: (value: T) => void
}

function deferred<T>(): Deferred<T> {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((accept) => { resolve = accept })
  return { promise, resolve }
}

function snapshot(name: string, resource = 'pods') {
  return {
    api_version: 'v1',
    kind: resource === 'pods' ? 'Pod' : 'Deployment',
    resource_name: resource,
    namespace: 'default',
    name,
    status: 'Running',
    ready: '1/1',
    node_name: 'node-1',
    resource_version: '1',
    summary: '{}',
    last_seen_at: '2026-01-01T00:00:00Z',
  }
}

describe('ResourceBrowser', () => {
  beforeEach(() => {
    apiMocks.listClusterResourceSnapshots.mockReset().mockResolvedValue({ snapshots: [] })
    apiMocks.getClusterResourceObject.mockReset().mockResolvedValue({ yaml: '' })
  })

  it('切换资源类型时立即清空上一类资源行', async () => {
    apiMocks.listClusterResourceSnapshots.mockResolvedValueOnce({ snapshots: [snapshot('pod-a')] })
    const deployments = deferred<{ snapshots: ReturnType<typeof snapshot>[] }>()
    apiMocks.listClusterResourceSnapshots.mockImplementationOnce(() => deployments.promise)
    render(<ResourceBrowser clusterId={1} basicPanel={<div>basic</div>} />)

    fireEvent.click(screen.getByRole('button', { name: 'Pod' }))
    await screen.findByText('pod-a')

    fireEvent.click(screen.getByRole('button', { name: 'Deployment' }))

    expect(screen.queryByText('pod-a')).not.toBeInTheDocument()
    await act(async () => deployments.resolve({ snapshots: [] }))
  })

  it('首屏只拉取二百条，点击加载更多后才请求下一页', async () => {
    const firstPage = Array.from({ length: 200 }, (_, index) => snapshot(`pod-${index + 1}`))
    apiMocks.listClusterResourceSnapshots
      .mockResolvedValueOnce({ generation: 'gen-1', snapshots: firstPage })
      .mockResolvedValueOnce({ generation: 'gen-1', snapshots: [snapshot('pod-201')] })
    render(<ResourceBrowser clusterId={1} basicPanel={<div>basic</div>} />)

    fireEvent.click(screen.getByRole('button', { name: 'Pod' }))

    await screen.findByText('pod-200')
    expect(apiMocks.listClusterResourceSnapshots).toHaveBeenCalledTimes(1)
    expect(apiMocks.listClusterResourceSnapshots).toHaveBeenNthCalledWith(
      1,
      1,
      'pods',
      { offset: 0, limit: 200 },
    )

    fireEvent.click(screen.getByRole('button', { name: 'resources.loadMore' }))

    await screen.findByText('pod-201')
    expect(apiMocks.listClusterResourceSnapshots).toHaveBeenNthCalledWith(
      2,
      1,
      'pods',
      { offset: 200, limit: 200, generation: 'gen-1' },
    )
  })

  it('加载更多发现快照代次变化时清空旧行并自动重载第一页', async () => {
    const firstPage = Array.from({ length: 200 }, (_, index) => snapshot(`pod-${index + 1}`))
    apiMocks.listClusterResourceSnapshots
      .mockResolvedValueOnce({ generation: 'gen-1', snapshots: firstPage })
      .mockRejectedValueOnce({
        response: {
          status: 409,
          data: { code: 'snapshot_generation_changed', error: '资源快照已更新' },
        },
      })
      .mockResolvedValueOnce({ generation: 'gen-2', snapshots: [snapshot('pod-new')] })
    render(<ResourceBrowser clusterId={1} basicPanel={<div>basic</div>} />)

    fireEvent.click(screen.getByRole('button', { name: 'Pod' }))
    await screen.findByText('pod-200')
    fireEvent.click(screen.getByRole('button', { name: 'resources.loadMore' }))

    await screen.findByText('pod-new')
    expect(screen.queryByText('pod-1')).not.toBeInTheDocument()
    expect(apiMocks.listClusterResourceSnapshots).toHaveBeenNthCalledWith(
      3,
      1,
      'pods',
      { offset: 0, limit: 200 },
    )
  })

  it('切换资源后不会继续处理旧资源的分页请求', async () => {
    const pods = deferred<{ snapshots: ReturnType<typeof snapshot>[] }>()
    const firstPage = Array.from({ length: 200 }, (_, index) => snapshot(`pod-${index + 1}`))
    apiMocks.listClusterResourceSnapshots
      .mockImplementationOnce(() => pods.promise)
      .mockResolvedValueOnce({ snapshots: [] })
    render(<ResourceBrowser clusterId={1} basicPanel={<div>basic</div>} />)

    fireEvent.click(screen.getByRole('button', { name: 'Pod' }))
    await waitFor(() => expect(apiMocks.listClusterResourceSnapshots).toHaveBeenCalledTimes(1))
    fireEvent.click(screen.getByRole('button', { name: 'Deployment' }))
    await waitFor(() => expect(apiMocks.listClusterResourceSnapshots).toHaveBeenCalledTimes(2))

    await act(async () => pods.resolve({ snapshots: firstPage }))

    expect(apiMocks.listClusterResourceSnapshots).toHaveBeenCalledTimes(2)
    expect(screen.queryByText('pod-1')).not.toBeInTheDocument()
  })

  it('组件卸载后不会继续拉取旧请求的下一页', async () => {
    const pods = deferred<{ snapshots: ReturnType<typeof snapshot>[] }>()
    const firstPage = Array.from({ length: 200 }, (_, index) => snapshot(`pod-${index + 1}`))
    apiMocks.listClusterResourceSnapshots.mockImplementationOnce(() => pods.promise)
    const { unmount } = render(<ResourceBrowser clusterId={1} basicPanel={<div>basic</div>} />)

    fireEvent.click(screen.getByRole('button', { name: 'Pod' }))
    await waitFor(() => expect(apiMocks.listClusterResourceSnapshots).toHaveBeenCalledTimes(1))
    unmount()
    await act(async () => pods.resolve({ snapshots: firstPage }))

    expect(apiMocks.listClusterResourceSnapshots).toHaveBeenCalledTimes(1)
  })

  it('较早返回的 YAML 请求不会覆盖当前对象', async () => {
    apiMocks.listClusterResourceSnapshots.mockResolvedValueOnce({
      snapshots: [snapshot('pod-a'), snapshot('pod-b')],
    })
    const firstYaml = deferred<{ yaml: string }>()
    apiMocks.getClusterResourceObject
      .mockImplementationOnce(() => firstYaml.promise)
      .mockResolvedValueOnce({ yaml: 'yaml-b' })
    render(<ResourceBrowser clusterId={1} basicPanel={<div>basic</div>} />)

    fireEvent.click(screen.getByRole('button', { name: 'Pod' }))
    await screen.findByText('pod-a')
    fireEvent.click(screen.getByText('pod-a'))
    fireEvent.click(screen.getByText('pod-b'))
    await screen.findByText('yaml-b')

    await act(async () => firstYaml.resolve({ yaml: 'yaml-a' }))

    expect(screen.getByText('yaml-b')).toBeInTheDocument()
    expect(screen.queryByText('yaml-a')).not.toBeInTheDocument()
  })
})
