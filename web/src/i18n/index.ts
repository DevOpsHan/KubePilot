import i18next, { type i18n } from 'i18next'
import { initReactI18next } from 'react-i18next'
import { resources } from './resources'
import type { SupportedLanguage } from './types'

export type { SupportedLanguage } from './types'

export const LANGUAGE_STORAGE_KEY = 'aiops-lang'

export function normalizeLanguage(value?: string | null): SupportedLanguage {
  return value === 'en' ? 'en' : 'zh'
}

export function readStoredLanguage(storage?: Pick<Storage, 'getItem'>): string | null {
  try {
    return (storage ?? globalThis.localStorage).getItem(LANGUAGE_STORAGE_KEY)
  } catch {
    return null
  }
}

export function writeStoredLanguage(
  language: SupportedLanguage,
  storage?: Pick<Storage, 'setItem'>,
): void {
  try {
    (storage ?? globalThis.localStorage).setItem(LANGUAGE_STORAGE_KEY, language)
  } catch {
    // 禁用存储时仍允许当前页面继续切换语言。
  }
}

async function initializeAppI18n(
  instance: i18n,
  initialLanguage: SupportedLanguage,
): Promise<i18n> {
  await instance.init({
    resources,
    lng: initialLanguage,
    defaultNS: 'common',
    fallbackLng: 'zh',
    supportedLngs: ['zh', 'en'],
    returnNull: false,
    interpolation: { escapeValue: false },
    react: { useSuspense: false },
  })
  return instance
}

export async function createAppI18n(initialLanguage: SupportedLanguage = 'zh'): Promise<i18n> {
  return initializeAppI18n(i18next.createInstance(), initialLanguage)
}

function createRegisteredAppI18n(initialLanguage: SupportedLanguage): Promise<i18n> {
  const instance = i18next.createInstance()
  instance.use(initReactI18next)
  return initializeAppI18n(instance, initialLanguage)
}

export function bindLanguageSideEffects(instance: i18n): () => void {
  const applyLanguage = (language?: string) => {
    const normalized = normalizeLanguage(language)
    writeStoredLanguage(normalized)

    if (typeof document === 'undefined') return
    document.documentElement.lang = normalized === 'zh' ? 'zh-CN' : 'en'
    document.title = instance.getFixedT(normalized, 'common')('meta.title')
  }

  instance.on('languageChanged', applyLanguage)
  applyLanguage(instance.resolvedLanguage ?? instance.language)

  return () => instance.off('languageChanged', applyLanguage)
}

export const appI18n: i18n = await createRegisteredAppI18n(
  normalizeLanguage(readStoredLanguage()),
)

bindLanguageSideEffects(appI18n)
