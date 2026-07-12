import { useTranslation } from 'react-i18next'
import { normalizeLanguage, type SupportedLanguage } from '@/i18n'
import { cn } from '@/lib/utils'

const languages: SupportedLanguage[] = ['zh', 'en']

// 登录页与顶栏共用的紧凑语言切换器。
export function LanguageSwitcher({ className }: { className?: string }) {
  const { t, i18n } = useTranslation('common')
  const currentLanguage = normalizeLanguage(i18n.resolvedLanguage ?? i18n.language)
  const labels: Record<SupportedLanguage, string> = {
    zh: t('language.zhShort'),
    en: t('language.enShort'),
  }
  const accessibleLabels: Record<SupportedLanguage, string> = {
    zh: t('language.zh'),
    en: t('language.en'),
  }

  return (
    <div
      role="group"
      aria-label={t('language.label')}
      className={cn('inline-flex items-center rounded-lg border border-border bg-card/80 p-0.5 text-xs font-semibold backdrop-blur', className)}
    >
      {languages.map((language) => (
        <button
          key={language}
          type="button"
          onClick={() => {
            void i18n.changeLanguage(language).catch(() => undefined)
          }}
          aria-label={accessibleLabels[language]}
          aria-pressed={currentLanguage === language}
          title={accessibleLabels[language]}
          className={cn(
            'rounded-md px-2 py-1 transition-colors',
            currentLanguage === language
              ? 'bg-primary text-primary-foreground shadow-sm'
              : 'text-muted-foreground hover:text-foreground',
          )}
        >
          {labels[language]}
        </button>
      ))}
    </div>
  )
}
