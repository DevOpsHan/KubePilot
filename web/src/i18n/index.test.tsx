import { useEffect, useState } from 'react'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { I18nextProvider, getI18n, useTranslation } from 'react-i18next'
import { describe, expect, it, vi } from 'vitest'
import { LanguageSwitcher } from '@/components/LanguageSwitcher'
import {
  navItems,
  navTranslationKeys,
  titleTranslationKeys,
  viewTitleKeys,
  type NavKey,
  type TitleKey,
} from '@/components/layout/nav'
import {
  LANGUAGE_STORAGE_KEY,
  appI18n,
  bindLanguageSideEffects,
  createAppI18n,
  normalizeLanguage,
  readStoredLanguage,
  writeStoredLanguage,
} from './index'
import { resources } from './resources'

function collectResourceLeaves(value: unknown, path: string[] = []): Array<[string, string]> {
  if (typeof value === 'string') return [[path.join('.'), value]]
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    throw new TypeError(`资源叶子必须是字符串：${path.join('.')}`)
  }

  return Object.entries(value).flatMap(([key, child]) => (
    collectResourceLeaves(child, [...path, key])
  ))
}

function Probe({ onMount }: { onMount: () => void }) {
  const { t } = useTranslation('common')
  const [count, setCount] = useState(0)

  useEffect(onMount, [onMount])

  return (
    <div>
      <output data-testid="probe-translation">{t('actions.save')}</output>
      <button type="button" onClick={() => setCount((value) => value + 1)}>
        业务计数 {count}
      </button>
    </div>
  )
}

describe.sequential('应用国际化初始化', () => {
  it.each([
    { value: 'zh', expected: 'zh' },
    { value: 'en', expected: 'en' },
    { value: 'en-US', expected: 'zh' },
    { value: 'ZH', expected: 'zh' },
    { value: '', expected: 'zh' },
    { value: null, expected: 'zh' },
    { value: undefined, expected: 'zh' },
  ] as const)('只接受受支持的语言值：$value', ({ value, expected }) => {
    expect(normalizeLanguage(value)).toBe(expected)
  })

  it('创建测试实例后仍保留生产实例作为 react-i18next 全局 fallback', async () => {
    expect(getI18n()).toBe(appI18n)

    const instance = await createAppI18n('en')

    expect(instance).not.toBe(appI18n)
    expect(getI18n()).toBe(appI18n)
  })

  it('中英文资源具有完全相同的递归叶子键', () => {
    const zhKeys = collectResourceLeaves(resources.zh).map(([key]) => key).sort()
    const enKeys = collectResourceLeaves(resources.en).map(([key]) => key).sort()

    expect(enKeys).toEqual(zhKeys)
  })

  it('英文资源的所有叶子均不包含汉字', () => {
    const englishLeaves = collectResourceLeaves(resources.en)

    expect(englishLeaves.every(([, value]) => !/\p{Script=Han}/u.test(value))).toBe(true)
  })

  it('导航配置使用完整且同名的类型化翻译映射', () => {
    const typedNavMap: { readonly [Key in NavKey]: `nav.${Key}` } = navTranslationKeys
    const typedTitleMap: { readonly [Key in TitleKey]: `title.${Key}` } = titleTranslationKeys

    expect(Object.entries(typedNavMap).every(([key, value]) => value === `nav.${key}`)).toBe(true)
    expect(Object.entries(typedTitleMap).every(([key, value]) => value === `title.${key}`)).toBe(true)
    expect(navItems.every((item) => navTranslationKeys[item.key] === `nav.${item.key}`)).toBe(true)
    expect(Object.values(viewTitleKeys).every(
      (key) => titleTranslationKeys[key] === `title.${key}`,
    )).toBe(true)
  })

  it('创建中文实例并提供默认命名空间翻译', async () => {
    const instance = await createAppI18n('zh')

    expect(instance.language).toBe('zh')
    expect(instance.t('actions.save')).toBe('保存')
  })

  it('存储读取或写入异常时使用安全回退', () => {
    const readErrorStorage = {
      getItem(): string | null {
        throw new DOMException('禁止读取', 'SecurityError')
      },
    }
    const writeErrorStorage = {
      setItem(): void {
        throw new DOMException('禁止写入', 'SecurityError')
      },
    }

    expect(normalizeLanguage(readStoredLanguage(readErrorStorage))).toBe('zh')
    expect(() => writeStoredLanguage('en', writeErrorStorage)).not.toThrow()
  })

  it('真实切换语言、同步文档副作用并保留业务状态', async () => {
    const instance = await createAppI18n('zh')
    const cleanupSideEffects = bindLanguageSideEffects(instance)
    const onMount = vi.fn()
    const user = userEvent.setup()

    try {
      render(
        <I18nextProvider i18n={instance}>
          <LanguageSwitcher />
          <Probe onMount={onMount} />
        </I18nextProvider>,
      )

      expect(screen.getByTestId('probe-translation')).toHaveTextContent('保存')
      expect(document.documentElement.lang).toBe('zh-CN')
      expect(document.title).toBe('KubePilot · Kubernetes 智能运维')

      await user.click(screen.getByRole('button', { name: '业务计数 0' }))
      expect(screen.getByRole('button', { name: '业务计数 1' })).toBeInTheDocument()

      const englishButton = screen.getByRole('button', { name: 'English' })
      expect(englishButton).toHaveAttribute('title', 'English')
      await user.click(englishButton)

      await waitFor(() => {
        expect(screen.getByTestId('probe-translation')).toHaveTextContent('Save')
        expect(localStorage.getItem(LANGUAGE_STORAGE_KEY)).toBe('en')
        expect(document.documentElement.lang).toBe('en')
        expect(document.title).toBe('KubePilot · Kubernetes AIOps')
      })
      const englishSwitcher = screen.getByRole('group', { name: 'Language' })
      expect(screen.getByRole('button', { name: 'Chinese' })).toHaveTextContent('ZH')
      expect(screen.getByRole('button', { name: 'English' })).toHaveTextContent('EN')
      expect(englishSwitcher).not.toHaveTextContent(/\p{Script=Han}/u)
      expect(screen.getByRole('button', { name: '业务计数 1' })).toBeInTheDocument()
      expect(onMount).toHaveBeenCalledTimes(1)

      await user.click(screen.getByRole('button', { name: 'Chinese' }))

      await waitFor(() => {
        expect(document.documentElement.lang).toBe('zh-CN')
        expect(document.title).toBe('KubePilot · Kubernetes 智能运维')
      })
      expect(screen.getByRole('button', { name: '业务计数 1' })).toBeInTheDocument()
      expect(onMount).toHaveBeenCalledTimes(1)
    } finally {
      cleanupSideEffects()
    }
  })

  it('清理后不再响应语言变化', async () => {
    const instance = await createAppI18n('zh')
    const cleanupSideEffects = bindLanguageSideEffects(instance)

    expect(document.documentElement.lang).toBe('zh-CN')
    cleanupSideEffects()

    await instance.changeLanguage('en')

    expect(document.documentElement.lang).toBe('zh-CN')
    expect(document.title).toBe('KubePilot · Kubernetes 智能运维')
  })

  it('语言切换失败时捕获拒绝并保持当前语言', async () => {
    const instance = await createAppI18n('zh')
    const rejectedChange = Promise.reject(new Error('静态资源切换失败'))
    const catchRejection = vi.spyOn(rejectedChange, 'catch')
    const settledRejection = rejectedChange.catch(() => undefined)
    catchRejection.mockClear()
    const changeLanguage = vi.spyOn(instance, 'changeLanguage')
      .mockReturnValueOnce(rejectedChange)
    const user = userEvent.setup()

    render(
      <I18nextProvider i18n={instance}>
        <LanguageSwitcher />
      </I18nextProvider>,
    )

    await user.click(screen.getByRole('button', { name: 'English' }))
    await waitFor(() => expect(changeLanguage).toHaveBeenCalledWith('en'))
    await settledRejection

    expect(catchRejection).toHaveBeenCalledTimes(1)
    expect(instance.language).toBe('zh')
    expect(screen.getByRole('group', { name: '语言' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '中文' })).toHaveAttribute('aria-pressed', 'true')
  })
})
