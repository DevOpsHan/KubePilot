import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

interface AxiosLikeFailure {
  response?: {
    status?: number
    data?: { error?: string }
  }
  config?: { url?: string }
}

type RejectedInterceptor = (error: AxiosLikeFailure) => Promise<never>

const axiosMock = vi.hoisted(() => {
  let rejectResponse: RejectedInterceptor = (error) => Promise.reject(error)

  const fail = (url: string) => rejectResponse({
    response: { status: 401, data: { error: '请先登录' } },
    config: { url },
  })

  const http = {
    interceptors: {
      response: {
        use: vi.fn((
          _onFulfilled: (response: object) => object,
          onRejected: RejectedInterceptor,
        ) => {
          rejectResponse = onRejected
        }),
      },
    },
    get: vi.fn((url: string) => fail(url)),
    post: vi.fn((url: string) => fail(url)),
    put: vi.fn((url: string) => fail(url)),
    delete: vi.fn((url: string) => fail(url)),
  }

  return { http, fail }
})

vi.mock('axios', () => ({
  default: {
    create: () => axiosMock.http,
  },
}))

import {
  apiFetch,
  changePassword,
  getCurrentUser,
  listClusterResourceSnapshots,
  login,
} from './api'

function captureAuthExpired() {
  const details: unknown[] = []
  const listener = (event: Event) => {
    if (event instanceof CustomEvent) details.push(event.detail)
  }
  window.addEventListener('aiops-auth-expired', listener)

  return {
    details,
    cleanup: () => window.removeEventListener('aiops-auth-expired', listener),
  }
}

describe('鉴权过期语义事件', () => {
  beforeEach(() => {
    axiosMock.http.get.mockClear()
    axiosMock.http.post.mockClear()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('Axios 非登录请求 401 只发送 unauthorized 原因', async () => {
    const events = captureAuthExpired()

    try {
      await expect(getCurrentUser()).rejects.toMatchObject({ response: { status: 401 } })
      expect(events.details).toEqual([{ reason: 'unauthorized' }])
    } finally {
      events.cleanup()
    }
  })

  it('Axios 登录和改密请求 401 不发送过期事件', async () => {
    const events = captureAuthExpired()

    try {
      await expect(login('operator', 'wrong')).rejects.toMatchObject({ response: { status: 401 } })
      await expect(changePassword('operator', 'wrong', 'new-password'))
        .rejects.toMatchObject({ response: { status: 401 } })
      expect(events.details).toEqual([])
    } finally {
      events.cleanup()
    }
  })

  it('Axios 仅豁免准确的登录和改密端点', async () => {
    const events = captureAuthExpired()

    try {
      await expect(axiosMock.fail('/auth/login-history')).rejects
        .toMatchObject({ response: { status: 401 } })
      await expect(axiosMock.fail('/clusters?redirect=/auth/login')).rejects
        .toMatchObject({ response: { status: 401 } })
      expect(events.details).toEqual([
        { reason: 'unauthorized' },
        { reason: 'unauthorized' },
      ])
    } finally {
      events.cleanup()
    }
  })

  it('fetch 非登录请求 401 只发送 unauthorized 原因', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(
      JSON.stringify({ error: '请先登录' }),
      { status: 401, headers: { 'Content-Type': 'application/json' } },
    ))
    vi.stubGlobal('fetch', fetchMock)
    const events = captureAuthExpired()

    try {
      await apiFetch('/api/clusters')
      expect(events.details).toEqual([{ reason: 'unauthorized' }])
    } finally {
      events.cleanup()
    }
  })

  it('fetch 登录和改密请求 401 不发送过期事件', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 401 }))
    vi.stubGlobal('fetch', fetchMock)
    const events = captureAuthExpired()

    try {
      await apiFetch('/api/auth/login')
      await apiFetch(new Request('http://localhost/api/auth/change-password'))
      expect(events.details).toEqual([])
    } finally {
      events.cleanup()
    }
  })

  it('fetch 仅按 pathname 豁免准确的认证端点', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 401 }))
    vi.stubGlobal('fetch', fetchMock)
    const events = captureAuthExpired()

    try {
      await apiFetch('/api/auth/login?redirect=/clusters')
      await apiFetch('/api/auth/login-history')
      await apiFetch('/api/clusters?redirect=/auth/login')
      expect(events.details).toEqual([
        { reason: 'unauthorized' },
        { reason: 'unauthorized' },
      ])
    } finally {
      events.cleanup()
    }
  })
})

describe('资源快照分页参数', () => {
  it('传递资源类型、偏移量、每页数量和快照代次', async () => {
    axiosMock.http.get.mockResolvedValueOnce({ data: { snapshots: [] } } as never)

    await listClusterResourceSnapshots(7, 'pods', { offset: 200, limit: 200, generation: 'gen-1' })

    expect(axiosMock.http.get).toHaveBeenLastCalledWith(
      '/clusters/7/resource-snapshots',
      { params: { resource: 'pods', offset: 200, limit: 200, generation: 'gen-1' } },
    )
  })
})
