import type { TFunction } from 'i18next'
import type { SupportedLanguage } from './types'
import type { zhCommon } from './locales/zh/common'

type ErrorResourceKey = keyof typeof zhCommon.errors
export type CommonErrorKey = `errors.${ErrorResourceKey}`

interface ParsedApiError {
  serverMessage?: string
  message?: string
  status?: number
  code?: string
  hasRequest: boolean
  hasResponse: boolean
}

const HAN_SCRIPT = /\p{Script=Han}/u

const knownMessageKeys = {
  '请先登录': 'errors.loginRequired',
  '会话过期': 'errors.sessionExpired',
  '登录已过期，请重新登录': 'errors.sessionExpired',
  '会话已过期，请重新登录': 'errors.sessionExpired',
  '登录会话无效或已过期': 'errors.sessionExpired',
  '用户名或密码不正确': 'errors.invalidCredentials',
  '用户名或密码错误': 'errors.invalidCredentials',
  '凭据错误': 'errors.invalidCredentials',
  '用户停用': 'errors.userDisabled',
  '用户已停用': 'errors.userDisabled',
  '用户已禁用': 'errors.userDisabled',
  '账号已禁用': 'errors.userDisabled',
  '用户已过期': 'errors.userExpired',
  '用户已删除': 'errors.userDeleted',
  '权限不足': 'errors.forbidden',
  '当前用户没有权限执行该操作': 'errors.forbidden',
  '当前用户没有权限操作该集群': 'errors.forbidden',
  '该操作仅全局管理员可执行': 'errors.forbidden',
  '参数错误': 'errors.badRequest',
  '请求参数错误': 'errors.badRequest',
  '参数无效': 'errors.badRequest',
  '请求体不正确': 'errors.badRequest',
  '请求体格式错误': 'errors.badRequest',
  '请求格式错误': 'errors.badRequest',
  '资源不存在': 'errors.notFound',
  '集群不存在': 'errors.clusterNotFound',
  '用户不存在': 'errors.userNotFound',
  '告警不存在': 'errors.alertNotFound',
  '工单不存在': 'errors.approvalNotFound',
  '报告不存在': 'errors.reportNotFound',
  '规则不存在': 'errors.ruleNotFound',
  '模型配置不存在': 'errors.modelNotFound',
  '静默规则不存在': 'errors.silenceNotFound',
  '记录不存在': 'errors.notFound',
  '不能删除当前登录用户': 'errors.cannotDeleteCurrentUser',
  '不能删除最后一个全局管理员': 'errors.cannotDeleteLastGlobalAdmin',
  '该角色必须至少授权一个集群': 'errors.clusterGrantRequired',
} as const satisfies Readonly<Record<string, CommonErrorKey>>

const messageKeys: Readonly<Record<string, CommonErrorKey>> = knownMessageKeys

const statusKeys: Readonly<Record<number, CommonErrorKey>> = {
  400: 'errors.httpBadRequest',
  401: 'errors.httpUnauthorized',
  403: 'errors.httpForbidden',
  404: 'errors.httpNotFound',
  409: 'errors.httpConflict',
  500: 'errors.httpServer',
  502: 'errors.httpBadGateway',
  503: 'errors.httpUnavailable',
}

const codeKeys: Readonly<Record<string, CommonErrorKey>> = {
  BAD_REQUEST: 'errors.httpBadRequest',
  UNAUTHORIZED: 'errors.httpUnauthorized',
  FORBIDDEN: 'errors.httpForbidden',
  NOT_FOUND: 'errors.httpNotFound',
  CONFLICT: 'errors.httpConflict',
  INTERNAL_SERVER_ERROR: 'errors.httpServer',
  BAD_GATEWAY: 'errors.httpBadGateway',
  SERVICE_UNAVAILABLE: 'errors.httpUnavailable',
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null
}

function nonEmptyString(value: unknown): string | undefined {
  return typeof value === 'string' && value.trim() !== '' ? value : undefined
}

function numericStatus(value: unknown): number | undefined {
  if (typeof value === 'number' && Number.isInteger(value)) return value
  if (typeof value !== 'string' || !/^\d{3}$/u.test(value.trim())) return undefined
  return Number(value)
}

function parseApiError(error: unknown): ParsedApiError {
  const root = isRecord(error) ? error : undefined
  const response = root && isRecord(root.response) ? root.response : undefined
  const data = response && isRecord(response.data) ? response.data : undefined
  const directMessage = typeof error === 'string'
    ? nonEmptyString(error)
    : error instanceof Error ? nonEmptyString(error.message) : undefined

  return {
    serverMessage: nonEmptyString(data?.error) ?? nonEmptyString(data?.message),
    message: directMessage ?? nonEmptyString(root?.message),
    status: numericStatus(response?.status) ?? numericStatus(data?.status),
    code: nonEmptyString(data?.code) ?? nonEmptyString(root?.code),
    hasRequest: Boolean(root && Object.hasOwn(root, 'request') && root.request),
    hasResponse: response !== undefined,
  }
}

function normalizedKnownMessage(message: string): string {
  return message.trim().replace(/[。！!]$/u, '')
}

function knownMessageKey(message: string): CommonErrorKey | undefined {
  return messageKeys[normalizedKnownMessage(message)]
}

function networkErrorKey(error: ParsedApiError): CommonErrorKey | undefined {
  const code = error.code?.toUpperCase()
  const message = error.message?.toLowerCase()

  if (code === 'ECONNABORTED' || code === 'ETIMEDOUT' || message?.includes('timed out')
    || message?.includes('timeout')) {
    return 'errors.timeout'
  }
  if (code === 'ERR_NETWORK' || message === 'network error' || message === 'failed to fetch') {
    return 'errors.network'
  }
  if (error.hasRequest && !error.hasResponse) return 'errors.noResponse'
  return undefined
}

export function localizeApiError(
  error: unknown,
  t: TFunction<'common'>,
  language: SupportedLanguage,
  fallbackKey: CommonErrorKey,
): string {
  const parsed = parseApiError(error)

  if (parsed.serverMessage) {
    if (language === 'zh') return parsed.serverMessage

    const key = knownMessageKey(parsed.serverMessage)
    if (key) return t(key)
    if (HAN_SCRIPT.test(parsed.serverMessage)) return t(fallbackKey)
    return parsed.serverMessage
  }

  if (language === 'zh' && parsed.message && HAN_SCRIPT.test(parsed.message)) {
    return parsed.message
  }

  if (parsed.status) {
    const key = statusKeys[parsed.status]
    if (key) return t(key)
  }

  if (parsed.code) {
    const key = codeKeys[parsed.code.toUpperCase()]
    if (key) return t(key)
  }

  const networkKey = networkErrorKey(parsed)
  if (networkKey) return t(networkKey)

  if (parsed.message) {
    const key = knownMessageKey(parsed.message)
    if (key) return t(key)
    if (language === 'zh') return parsed.message
    return HAN_SCRIPT.test(parsed.message) ? t(fallbackKey) : parsed.message
  }

  return parsed.code ?? t(fallbackKey)
}
