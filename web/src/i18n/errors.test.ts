import { describe, expect, it } from 'vitest'
import { createAppI18n } from './index'
import { localizeApiError, type CommonErrorKey } from './errors'

const fallbackKey: CommonErrorKey = 'errors.loadFailed'

async function translators() {
  const instance = await createAppI18n('zh')
  return {
    tEn: instance.getFixedT('en', 'common'),
    tZh: instance.getFixedT('zh', 'common'),
  }
}

describe('localizeApiError', () => {
  it.each([
    ['请先登录', 'Please sign in to continue'],
    ['会话过期', 'Your session has expired. Please sign in again'],
    ['登录会话无效或已过期', 'Your session has expired. Please sign in again'],
    ['用户名或密码不正确', 'Incorrect username or password'],
    ['用户停用', 'This account is disabled'],
    ['用户已停用', 'This account is disabled'],
    ['用户已过期', 'This account has expired'],
    ['当前用户没有权限执行该操作', 'You do not have permission to perform this action'],
    ['请求格式错误', 'The request is invalid'],
    ['集群不存在', 'The cluster was not found'],
    ['资源不存在', 'The requested resource was not found'],
    ['告警不存在', 'The alert was not found'],
    ['不能删除当前登录用户', 'The currently signed-in user cannot be deleted'],
    ['不能删除最后一个全局管理员', 'The last global administrator cannot be deleted'],
    ['集群管理员不能创建或修改全局管理员', 'A cluster administrator cannot create or modify a global administrator'],
    ['集群管理员不能管理全局管理员', 'A cluster administrator cannot manage a global administrator'],
    ['该角色必须至少授权一个集群', 'This role must be authorized for at least one cluster'],
  ])('将已知服务端中文错误映射为具体英文：%s', async (message, expected) => {
    const { tEn } = await translators()
    const error = { response: { data: { error: message } } }

    expect(localizeApiError(error, tEn, 'en', fallbackKey)).toBe(expected)
  })

  it('中文优先保留具体服务端原始错误', async () => {
    const { tZh } = await translators()
    const message = '集群证书已失效，请重新导入'
    const error = { response: { status: 500, data: { error: message } } }

    expect(localizeApiError(error, tZh, 'zh', fallbackKey)).toBe(message)
  })

  it('读取 Echo response.data.message 并按当前语言处理具体错误', async () => {
    const { tEn, tZh } = await translators()
    const knownError = {
      response: { status: 401, data: { message: '用户名或密码不正确' } },
    }
    const unknownError = {
      response: { status: 500, data: { message: '集群证书已失效，请重新导入' } },
    }

    expect(localizeApiError(knownError, tZh, 'zh', fallbackKey))
      .toBe('用户名或密码不正确')
    expect(localizeApiError(knownError, tEn, 'en', fallbackKey))
      .toBe('Incorrect username or password')
    expect(localizeApiError(unknownError, tZh, 'zh', fallbackKey))
      .toBe('集群证书已失效，请重新导入')
    expect(localizeApiError(unknownError, tEn, 'en', fallbackKey))
      .toBe('Failed to load data')
  })

  it('英文环境用场景 fallback 隔离未知汉字服务端错误', async () => {
    const { tEn } = await translators()
    const error = {
      response: {
        status: 500,
        data: { error: '集群证书已失效，请重新导入' },
      },
    }

    expect(localizeApiError(error, tEn, 'en', fallbackKey)).toBe('Failed to load data')
  })

  it.each([
    [400, 'The request is invalid'],
    [401, 'Your session has expired. Please sign in again'],
    [403, 'You do not have permission to perform this action'],
    [404, 'The requested resource was not found'],
    [409, 'The request conflicts with the current state'],
    [500, 'The server encountered an error'],
    [502, 'The upstream service returned an invalid response'],
    [503, 'The service is temporarily unavailable'],
  ])('无消息时为 HTTP %i 提供稳定英文', async (status, expected) => {
    const { tEn } = await translators()

    expect(localizeApiError({ response: { status } }, tEn, 'en', fallbackKey)).toBe(expected)
  })

  it('从 response.data.status 和 response.data.code 提取语义', async () => {
    const { tEn } = await translators()

    expect(localizeApiError(
      { response: { data: { status: '503' } } },
      tEn,
      'en',
      fallbackKey,
    )).toBe('The service is temporarily unavailable')
    expect(localizeApiError(
      { response: { data: { code: 'FORBIDDEN' } } },
      tEn,
      'en',
      fallbackKey,
    )).toBe('You do not have permission to perform this action')
  })

  it.each([
    [{ code: 'ECONNABORTED', message: 'timeout of 1000ms exceeded' }, 'The request timed out'],
    [{ code: 'ERR_NETWORK', message: 'Network Error' }, 'Unable to reach the server'],
    [{ request: {}, message: '' }, 'The server did not respond'],
    [new TypeError('Failed to fetch'), 'Unable to reach the server'],
  ])('稳定处理网络、超时和无响应错误', async (error, expected) => {
    const { tEn } = await translators()

    expect(localizeApiError(error, tEn, 'en', fallbackKey)).toBe(expected)
  })

  it.each([
    [{ response: { status: 503 } }, '服务暂时不可用'],
    [{ code: 'ECONNABORTED', message: 'timeout of 1000ms exceeded' }, '请求超时'],
    [{ code: 'ERR_NETWORK', message: 'Network Error' }, '无法连接服务器'],
    [{ request: {}, message: '' }, '服务器未响应'],
    [new TypeError('Failed to fetch'), '无法连接服务器'],
  ])('中文环境稳定处理 HTTP、网络、超时和无响应错误', async (error, expected) => {
    const { tZh } = await translators()

    expect(localizeApiError(error, tZh, 'zh', fallbackKey)).toBe(expected)
  })

  it('保留非汉字技术错误与 Kubernetes 原文', async () => {
    const { tEn } = await translators()

    expect(localizeApiError(new Error('ECONNREFUSED'), tEn, 'en', fallbackKey))
      .toBe('ECONNREFUSED')
    expect(localizeApiError(
      { response: { data: { error: 'pods "api-0" not found' } } },
      tEn,
      'en',
      fallbackKey,
    )).toBe('pods "api-0" not found')
  })

  it('支持字符串错误且空错误回退到场景文案', async () => {
    const { tEn, tZh } = await translators()

    expect(localizeApiError('ECONNRESET', tEn, 'en', fallbackKey)).toBe('ECONNRESET')
    expect(localizeApiError(null, tEn, 'en', fallbackKey)).toBe('Failed to load data')
    expect(localizeApiError(undefined, tZh, 'zh', fallbackKey)).toBe('加载数据失败')
  })
})
