import { enResource } from './locales/en'
import { zhResource } from './locales/zh'
import type { LocaleShape, SupportedLanguage } from './types'

export const resources = {
  zh: zhResource,
  en: enResource,
} as const satisfies Record<SupportedLanguage, LocaleShape<typeof zhResource>>
