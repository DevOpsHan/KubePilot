import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useLocation, useNavigate } from 'react-router-dom'
import { Sparkles, User as UserIcon, Trash2, Send } from 'lucide-react'
import { getModelStatus } from '@/lib/api'
import { normalizeLanguage } from '@/i18n'
import { localizeApiError } from '@/i18n/errors'
import { useClusters } from '@/providers/ClusterProvider'
import { useChatAssistant } from '@/hooks/useChatAssistant'
import { quickActions } from '@/lib/chat-actions'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'

export function ChatPage() {
  const { t: operationsT, i18n } = useTranslation('operations')
  const { t: commonT } = useTranslation('common')
  const language = normalizeLanguage(i18n.resolvedLanguage)
  const navigate = useNavigate()
  const location = useLocation()
  const { currentClusterId } = useClusters()
  const clusterRef = useRef<number | undefined>(currentClusterId)
  clusterRef.current = currentClusterId
  const bodyRef = useRef<HTMLDivElement>(null)

  const chat = useChatAssistant({
    currentClusterId: () => clusterRef.current,
    getScrollEl: () => bodyRef.current,
  })
  const { messages, sessionId, sending, renderMd, ensureSession, loadHistory, sendMessage, clearChat } = chat

  const [input, setInput] = useState('')
  const [modelReady, setModelReady] = useState(false)
  const [modelError, setModelError] = useState<unknown>(null)
  const pendingRef = useRef<string | null>(null)

  // 初始化会话 + 拉历史 + 模型状态
  useEffect(() => {
    ensureSession()
    getModelStatus()
      .then((model) => { setModelReady(Boolean(model?.current?.ready)); setModelError(null) })
      .catch((error) => { setModelReady(false); setModelError(error) })
    // 会话建立后再拉历史
    const t = setTimeout(() => loadHistory(), 0)
    return () => clearTimeout(t)
  }, [ensureSession, loadHistory])

  // 来自总览「快捷操作」的自动发送
  useEffect(() => {
    const state: unknown = location.state
    const msg = typeof state === 'object' && state !== null && 'quickMessage' in state
      && typeof state.quickMessage === 'string' ? state.quickMessage : undefined
    if (msg) { pendingRef.current = msg; navigate(location.pathname, { replace: true, state: null }) }
  }, [location, navigate])

  // 模型就绪且有待发消息 → 自动发送
  useEffect(() => {
    if (modelReady && pendingRef.current) { const m = pendingRef.current; pendingRef.current = null; sendMessage(m) }
  }, [modelReady, sendMessage])

  function submit() {
    if (!modelReady) { navigate('/models'); return }
    const text = input.trim()
    if (!text) return
    setInput('')
    sendMessage(text)
  }
  function runQuick(msg: string) {
    if (!modelReady) { navigate('/models'); return }
    sendMessage(msg)
  }

  return (
    <div className="flex h-full flex-col">
      {/* 消息区 */}
      <div ref={bodyRef} className="min-h-0 flex-1 overflow-y-auto px-6 py-6">
        <div className="mx-auto max-w-3xl">
          <div className="mb-4 flex items-center gap-2 text-xs text-muted-foreground">
            <strong className="rounded bg-muted px-2 py-0.5 font-mono">
              {operationsT('chat.sessionId', { id: sessionId || 'default' })}
            </strong>
            {messages.length > 0 && (
              <Button variant="ghost" size="sm" className="ml-auto h-7 px-2 text-xs" onClick={clearChat}>
                <Trash2 className="size-3.5" /> {operationsT('chat.clearSession')}
              </Button>
            )}
          </div>

          {!messages.length ? (
            <div className="flex flex-col items-center py-12 text-center">
              <div className="mb-4 grid size-16 place-items-center rounded-2xl text-white shadow-[0_10px_30px_rgba(79,70,229,0.28)]" style={{ background: 'var(--gradient-brand)' }}>
                <Sparkles className="size-8" />
              </div>
              <h2 className="text-2xl font-bold">{operationsT('chat.welcomeTitle')}</h2>
              <p className="mt-2 text-sm leading-relaxed text-muted-foreground">
                {operationsT('chat.welcomePrimary')}<br />{operationsT('chat.welcomeSecondary')}
              </p>

              {!modelReady && (
                <div className="mt-6 flex w-full max-w-lg items-center gap-4 rounded-xl border border-warning/30 bg-warning-weak px-4 py-3 text-left">
                  <div className="flex-1">
                    <strong className="block text-sm text-warning-fg">{operationsT('chat.modelRequiredTitle')}</strong>
                    <span className="text-xs text-warning-fg/80">{operationsT('chat.modelRequiredDescription')}</span>
                    {modelError !== null && (
                      <span className="mt-1 block text-xs text-danger-fg">
                        {localizeApiError(modelError, commonT, language, 'errors.loadFailed')}
                      </span>
                    )}
                  </div>
                  <Button size="sm" onClick={() => navigate('/models')}>{operationsT('chat.configure')}</Button>
                </div>
              )}

              <div className="mt-8 grid w-full max-w-lg grid-cols-2 gap-3">
                {quickActions.map((a) => {
                  const Icon = a.icon
                  const label = operationsT(`quickActions.${a.key}.label`)
                  const prompt = operationsT(`quickActions.${a.key}.prompt`)
                  return (
                    <button key={a.key} onClick={() => runQuick(prompt)}
                      className="flex items-center gap-3 rounded-xl border border-border bg-card px-4 py-3.5 text-left transition-colors hover:border-primary hover:bg-primary/5">
                      <Icon className="size-[18px] text-primary" />
                      <strong className="text-sm font-semibold">{label}</strong>
                    </button>
                  )
                })}
              </div>
            </div>
          ) : (
            <div className="space-y-5">
              {messages.map((m, i) => (
                <div key={i} className={cn('flex gap-3', m.role === 'user' && 'flex-row-reverse')}>
                  <div className={cn('grid size-9 shrink-0 place-items-center rounded-full text-white', m.role === 'user' ? 'bg-muted-foreground' : '')}
                    style={m.role !== 'user' ? { background: 'var(--gradient-brand)' } : undefined}>
                    {m.role === 'user' ? <UserIcon className="size-4" /> : <span className="text-sm">◆</span>}
                  </div>
                  <div className={cn('min-w-0 max-w-[80%] rounded-2xl px-4 py-3 text-sm', m.role === 'user' ? 'bg-primary text-primary-foreground' : 'border border-border bg-card')}>
                    {m.steps && m.steps.length > 0 && (
                      <div className="mb-2 space-y-1">
                        {m.steps.map((s, j) => (
                          <div key={j} className="flex items-center gap-2 rounded-md bg-muted/60 px-2 py-1 font-mono text-[11px]">
                            <span className="truncate text-muted-foreground">{s.command || s.tool}</span>
                            {s.duration !== null
                              ? <span className="ml-auto text-success-fg">{operationsT('chat.toolCompleted', { duration: s.duration })}</span>
                              : <span className="ml-auto text-warning-fg">{operationsT('chat.toolRunning')}</span>}
                          </div>
                        ))}
                      </div>
                    )}
                    {m.role === 'assistant' && m.thinking && !m.content ? (
                      <div className="animate-pulse text-muted-foreground">{operationsT('chat.thinking')}</div>
                    ) : m.role === 'assistant' ? (
                      <div className="md-body leading-relaxed" dangerouslySetInnerHTML={{ __html: renderMd(m.content) }} />
                    ) : (
                      <div className="whitespace-pre-wrap">{m.content}</div>
                    )}
                    {m.role === 'assistant' && m.totalMs ? (
                      <div className="mt-2 text-[11px] text-muted-foreground">
                        {operationsT('chat.totalDuration', { seconds: (m.totalMs / 1000).toFixed(1) })}
                      </div>
                    ) : null}
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>
      </div>

      {/* 输入栏 */}
      <div className="border-t border-border bg-card/60 px-6 py-4 backdrop-blur">
        <div className="mx-auto flex max-w-3xl gap-2">
          <Input value={input} onChange={(e) => setInput(e.target.value)} onKeyUp={(e) => e.key === 'Enter' && submit()}
            placeholder={modelReady ? operationsT('chat.inputPlaceholder') : operationsT('chat.modelRequiredPlaceholder')}
            disabled={sending || !modelReady} className="h-11" />
          <Button className="h-11 px-5" onClick={submit} disabled={sending}>
            <Send className="size-4" /> {!modelReady
              ? operationsT('chat.configure')
              : sending ? operationsT('chat.sending') : operationsT('chat.send')}
          </Button>
        </div>
      </div>
    </div>
  )
}
