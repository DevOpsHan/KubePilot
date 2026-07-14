import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const apiMocks = vi.hoisted(() => ({
  fetchPodLogs: vi.fn(),
  listPodContainers: vi.fn(),
  openPodLogStream: vi.fn(),
}))
const translationMocks = vi.hoisted(() => ({ t: (key: string) => key }))

vi.mock('@/lib/api', () => apiMocks)
vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: translationMocks.t }),
}))

import { PodLogs } from './PodLogs'

interface TestSocket {
  binaryType: BinaryType
  onmessage: ((event: MessageEvent) => void) | null
  onerror: (() => void) | null
  onclose: (() => void) | null
  close: ReturnType<typeof vi.fn>
}

function createSocket(): TestSocket {
  return {
    binaryType: 'blob',
    onmessage: null,
    onerror: null,
    onclose: null,
    close: vi.fn(),
  }
}

function logContent(): string {
  return document.querySelector('pre')?.textContent || ''
}

describe('PodLogs', () => {
  afterEach(() => vi.unstubAllGlobals())

  beforeEach(() => {
    apiMocks.fetchPodLogs.mockReset().mockResolvedValue('旧日志\n')
    apiMocks.listPodContainers.mockReset().mockResolvedValue({ containers: ['app'] })
    apiMocks.openPodLogStream.mockReset()
    vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) => {
      callback(0)
      return 1
    })
  })

  it('开启跟随后以 WebSocket 首批快照替换旧的一次性日志', async () => {
    const socket = createSocket()
    apiMocks.openPodLogStream.mockReturnValue(socket as unknown as WebSocket)
    render(<PodLogs clusterId={1} namespace="default" pod="demo" />)

    await waitFor(() => expect(logContent()).toBe('旧日志\n'))
    fireEvent.click(screen.getByRole('switch'))
    await waitFor(() => expect(apiMocks.openPodLogStream).toHaveBeenCalledOnce())

    act(() => socket.onmessage?.(new MessageEvent('message', { data: '旧日志\n新日志\n' })))

    expect(logContent()).toBe('旧日志\n新日志\n')
  })

  it('流式解码跨 WebSocket 消息拆分的 UTF-8 字符', async () => {
    const socket = createSocket()
    apiMocks.openPodLogStream.mockReturnValue(socket as unknown as WebSocket)
    render(<PodLogs clusterId={1} namespace="default" pod="demo" />)

    await waitFor(() => expect(logContent()).toBe('旧日志\n'))
    fireEvent.click(screen.getByRole('switch'))
    await waitFor(() => expect(apiMocks.openPodLogStream).toHaveBeenCalledOnce())

    const bytes = new TextEncoder().encode('日志')
    act(() => {
      socket.onmessage?.(new MessageEvent('message', { data: bytes.slice(0, 2).buffer }))
      socket.onmessage?.(new MessageEvent('message', { data: bytes.slice(2).buffer }))
    })

    expect(logContent()).toBe('日志')
    expect(logContent()).not.toContain('\uFFFD')
  })

  it('持续日志最多保留一百万字符', async () => {
    const socket = createSocket()
    apiMocks.openPodLogStream.mockReturnValue(socket as unknown as WebSocket)
    render(<PodLogs clusterId={1} namespace="default" pod="demo" />)

    await waitFor(() => expect(logContent()).toBe('旧日志\n'))
    fireEvent.click(screen.getByRole('switch'))
    await waitFor(() => expect(apiMocks.openPodLogStream).toHaveBeenCalledOnce())

    act(() => socket.onmessage?.(new MessageEvent('message', { data: `开头${'x'.repeat(1_000_000)}结尾` })))

    expect(logContent()).toHaveLength(1_000_000)
    expect(logContent()).not.toContain('开头')
    expect(logContent().endsWith('结尾')).toBe(true)
  })

  it('日志流意外关闭后退出跟随状态', async () => {
    const socket = createSocket()
    apiMocks.openPodLogStream.mockReturnValue(socket as unknown as WebSocket)
    render(<PodLogs clusterId={1} namespace="default" pod="demo" />)

    await waitFor(() => expect(logContent()).toBe('旧日志\n'))
    fireEvent.click(screen.getByRole('switch'))
    await waitFor(() => expect(apiMocks.openPodLogStream).toHaveBeenCalledOnce())
    expect(screen.getByRole('switch')).toBeChecked()

    act(() => socket.onclose?.())

    await waitFor(() => expect(screen.getByRole('switch')).not.toBeChecked())
  })

  it('旧日志流延迟触发关闭回调时不影响新的跟随流', async () => {
    const first = createSocket()
    const second = createSocket()
    apiMocks.openPodLogStream
      .mockReturnValueOnce(first as unknown as WebSocket)
      .mockReturnValueOnce(second as unknown as WebSocket)
    render(<PodLogs clusterId={1} namespace="default" pod="demo" />)

    await waitFor(() => expect(logContent()).toBe('旧日志\n'))
    fireEvent.click(screen.getByRole('switch'))
    await waitFor(() => expect(apiMocks.openPodLogStream).toHaveBeenCalledTimes(1))
    const staleOnClose = first.onclose

    fireEvent.click(screen.getByRole('switch'))
    await waitFor(() => expect(screen.getByRole('switch')).not.toBeChecked())
    fireEvent.click(screen.getByRole('switch'))
    await waitFor(() => expect(apiMocks.openPodLogStream).toHaveBeenCalledTimes(2))

    act(() => staleOnClose?.())

    expect(screen.getByRole('switch')).toBeChecked()
    expect(second.close).not.toHaveBeenCalled()
  })
})
