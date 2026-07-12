import { useCallback, useReducer, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { marked } from 'marked'
import { normalizeLanguage } from '@/i18n'
import { localizeApiError } from '@/i18n/errors'
import { apiFetch } from '@/lib/api'

marked.setOptions({ breaks: true, gfm: true })

const REVEAL_CHARS_PER_SECOND = 24
const REVEAL_CATCHUP_MS = 900
const AUTOSCROLL_BOTTOM_GAP = 180

export interface ChatStep { tool?: string; command?: string; args?: unknown; duration: number | null }
export interface ChatMessage {
  role: 'user' | 'assistant'
  content: string
  targetContent?: string
  steps: ChatStep[]
  thinking: boolean
  revealing?: boolean
  totalMs?: number
  localError?: {
    kind: 'requestFailed'
    error?: unknown
  }
}

interface Options {
  currentClusterId?: () => number | undefined
  getScrollEl?: () => HTMLElement | null
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null
}

function nonEmptyString(value: unknown): string | undefined {
  return typeof value === 'string' && value.trim() !== '' ? value : undefined
}

function payloadString(value: unknown): string | undefined {
  return typeof value === 'string' ? value : undefined
}

function historyMessage(value: unknown): ChatMessage | undefined {
  if (!isRecord(value)) return undefined
  const role = value.role
  const content = payloadString(value.content)
  if ((role !== 'user' && role !== 'assistant') || content === undefined) return undefined
  return { role, content, steps: [], thinking: false }
}

function eventNumber(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isFinite(value) ? value : undefined
}

interface ChatResponseError {
  response: {
    status: number
    statusText: string
    data: {
      error?: string
      message?: string
    }
  }
  message: string
}

async function readResponseError(response: Response): Promise<ChatResponseError> {
  let payload: unknown
  try { payload = await response.clone().json() } catch { payload = undefined }
  const data = isRecord(payload) ? {
    error: payloadString(payload.error),
    message: payloadString(payload.message),
  } : {}
  return {
    response: {
      status: response.status,
      statusText: response.statusText,
      data,
    },
    message: response.statusText,
  }
}

// 对话助手:SSE 流式 + requestAnimationFrame 逐字"打字机"揭示,1:1 复刻 Vue 版体验
export function useChatAssistant(options: Options = {}) {
  const { t, i18n } = useTranslation('operations')
  const { t: commonT } = useTranslation('common')
  const language = normalizeLanguage(i18n.resolvedLanguage)
  const messagesRef = useRef<ChatMessage[]>([])
  const [, force] = useReducer((x: number) => x + 1, 0)
  const sendingRef = useRef(false)
  const sessionRef = useRef('')
  const scrollRaf = useRef(0)
  const revealRaf = useRef(0)
  const revealMessage = useRef<ChatMessage | null>(null)
  const lastRevealAt = useRef(0)

  const clusterId = () => options.currentClusterId?.()

  const ensureSession = useCallback(() => {
    let id = localStorage.getItem('aiops_session')
    if (!id) { id = 'ops-' + Date.now().toString(36) + Math.random().toString(36).slice(2, 8); localStorage.setItem('aiops_session', id) }
    sessionRef.current = id
    force()
  }, [])

  const renderMd = useCallback((text?: string) => marked.parse(text || '') as string, [])

  const scrollBottom = useCallback((forceScroll = true) => {
    if (scrollRaf.current) return
    scrollRaf.current = window.requestAnimationFrame(() => {
      scrollRaf.current = 0
      const el = options.getScrollEl?.()
      if (!el) return
      const gap = el.scrollHeight - el.scrollTop - el.clientHeight
      if (forceScroll || gap < AUTOSCROLL_BOTTOM_GAP) el.scrollTop = el.scrollHeight
    })
  }, [options])

  function cancelReveal() {
    if (revealRaf.current) { window.cancelAnimationFrame(revealRaf.current); revealRaf.current = 0 }
    revealMessage.current = null
    lastRevealAt.current = 0
  }

  function scheduleReveal(aiMsg: ChatMessage) {
    if (!aiMsg) return
    if ((aiMsg.content || '') && !(aiMsg.targetContent || '').startsWith(aiMsg.content || '')) aiMsg.content = ''
    revealMessage.current = aiMsg
    aiMsg.revealing = true
    if (!lastRevealAt.current) lastRevealAt.current = performance.now()
    if (!revealRaf.current) revealRaf.current = window.requestAnimationFrame(revealTick)
  }

  function revealTick(now: number) {
    revealRaf.current = 0
    const aiMsg = revealMessage.current
    if (!aiMsg) { lastRevealAt.current = 0; return }
    const target = aiMsg.targetContent || '', current = aiMsg.content || ''
    const remaining = target.length - current.length
    if (remaining <= 0) { aiMsg.revealing = false; revealMessage.current = null; lastRevealAt.current = 0; force(); return }
    const elapsed = Math.max(0, now - lastRevealAt.current)
    const steadyMsPerChar = 1000 / REVEAL_CHARS_PER_SECOND
    const msPerChar = Math.min(steadyMsPerChar, REVEAL_CATCHUP_MS / remaining)
    if (elapsed < msPerChar) { revealRaf.current = window.requestAnimationFrame(revealTick); return }
    let charsToAdd = Math.floor(elapsed / msPerChar)
    if (charsToAdd < 1) charsToAdd = 1
    if (charsToAdd > remaining) charsToAdd = remaining
    lastRevealAt.current = now - (elapsed - charsToAdd * msPerChar)
    aiMsg.content = target.slice(0, current.length + charsToAdd)
    force()
    scrollBottom(false)
    revealRaf.current = window.requestAnimationFrame(revealTick)
  }

  const loadHistory = useCallback(async () => {
    try {
      const params = new URLSearchParams({ session_id: sessionRef.current })
      const id = clusterId()
      if (id) params.set('cluster_id', String(id))
      const r = await apiFetch('/api/chat/history?' + params.toString())
      const data: unknown = await r.json()
      const history = isRecord(data) && Array.isArray(data.messages) ? data.messages : []
      messagesRef.current = history.flatMap((message) => {
        const parsed = historyMessage(message)
        return parsed ? [parsed] : []
      })
      force()
    } catch { /* 忽略历史恢复失败 */ }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  function handleEvent(ev: Record<string, unknown>, aiMsg: ChatMessage) {
    const type = nonEmptyString(ev.type)
    if (type === 'token') {
      aiMsg.targetContent = (aiMsg.targetContent || aiMsg.content || '') + (payloadString(ev.content) ?? '')
      aiMsg.thinking = false
      scheduleReveal(aiMsg)
    } else if (type === 'step') {
      cancelReveal()
      aiMsg.content = ''; aiMsg.targetContent = ''; aiMsg.thinking = true
      aiMsg.steps.push({
        tool: nonEmptyString(ev.tool),
        command: nonEmptyString(ev.command),
        args: ev.args,
        duration: null,
      })
    } else if (type === 'step_done') {
      const duration = eventNumber(ev.duration_ms)
      for (let k = aiMsg.steps.length - 1; k >= 0; k--) {
        if (aiMsg.steps[k].duration === null) { aiMsg.steps[k].duration = duration ?? 0; break }
      }
    } else if (type === 'answer') {
      aiMsg.targetContent = payloadString(ev.content) ?? aiMsg.targetContent ?? aiMsg.content
      scheduleReveal(aiMsg)
      aiMsg.totalMs = eventNumber(ev.duration_ms); aiMsg.thinking = false
    } else if (type === 'error') {
      cancelReveal(); aiMsg.content = `⚠️ ${payloadString(ev.content) ?? ''}`; aiMsg.targetContent = aiMsg.content; aiMsg.thinking = false
    } else if (type === 'done') {
      aiMsg.thinking = false
    }
    force()
  }

  const sendMessage = useCallback(async (text?: string) => {
    const value = (text ?? '').trim()
    if (!value || sendingRef.current) return
    messagesRef.current.push({ role: 'user', content: value, steps: [], thinking: false })
    sendingRef.current = true
    messagesRef.current.push({ role: 'assistant', content: '', targetContent: '', steps: [], thinking: true, revealing: false })
    const aiMsg = messagesRef.current[messagesRef.current.length - 1]
    force(); scrollBottom()
    try {
      const resp = await apiFetch('/api/chat', {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ session_id: sessionRef.current, message: value, cluster_id: clusterId() }),
      })
      if (!resp.ok) {
        aiMsg.localError = { kind: 'requestFailed', error: await readResponseError(resp) }
        return
      }
      if (!resp.body) {
        aiMsg.localError = { kind: 'requestFailed' }
        return
      }
      const reader = resp.body.getReader()
      const decoder = new TextDecoder()
      let buffer = ''
      // eslint-disable-next-line no-constant-condition
      while (true) {
        const { done, value: chunk } = await reader.read()
        if (done) break
        buffer += decoder.decode(chunk, { stream: true })
        const parts = buffer.split('\n\n')
        buffer = parts.pop() || ''
        for (const part of parts) {
          const line = part.trim()
          if (!line.startsWith('data:')) continue
          let event: unknown
          try { event = JSON.parse(line.slice(5).trim()) } catch { continue }
          if (!isRecord(event)) continue
          handleEvent(event, aiMsg)
          scrollBottom()
        }
      }
    } catch (error) {
      cancelReveal(); aiMsg.localError = { kind: 'requestFailed', error }
    } finally {
      aiMsg.thinking = false; sendingRef.current = false; force(); scrollBottom()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [scrollBottom])

  const clearChat = useCallback(async () => {
    try {
      await apiFetch('/api/chat/clear', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ session_id: sessionRef.current, cluster_id: clusterId() }) })
    } catch { /* 忽略清理失败 */ }
    messagesRef.current = []
    cancelReveal()
    localStorage.removeItem('aiops_session')
    ensureSession()
    force()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ensureSession])

  const messages = messagesRef.current.map((message) => {
    if (!message.localError) return message
    const detail = message.localError.error === undefined
      ? t('chat.requestFailed')
      : localizeApiError(message.localError.error, commonT, language, 'errors.actionFailed')
    const content = t('chat.errorWithDetail', { message: detail })
    return { ...message, content, targetContent: content }
  })

  return {
    messages,
    sessionId: sessionRef.current,
    sending: sendingRef.current,
    renderMd, ensureSession, loadHistory, sendMessage, clearChat, scrollBottom,
  }
}
