import { Moon } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { useTheme } from '@/providers/ThemeProvider'
import { cn } from '@/lib/utils'

// 单个昼夜切换按钮:浅色显示暖色太阳,点一下切到深色并显示蓝色月牙,再点切回。
export function ThemeSwitcher({ className }: { className?: string }) {
  const { mode, toggle } = useTheme()
  const { t } = useTranslation('common')
  const isDark = mode === 'dark'
  return (
    <button
      type="button"
      onClick={toggle}
      title={t(isDark ? 'theme.dark' : 'theme.light')}
      aria-label={t(isDark ? 'theme.dark' : 'theme.light')}
      className={cn(
        'grid size-9 place-items-center rounded-xl border transition-colors active:scale-95',
        isDark ? 'border-slate-700 bg-slate-900' : 'border-border bg-card',
        className,
      )}
    >
      {isDark ? (
        // 蓝色月牙
        <Moon className="size-[18px] text-[#bfdbfe]" fill="#60a5fa" />
      ) : (
        // 暖色太阳(橙→粉渐变球,带柔光)
        <span
          className="block size-[18px] rounded-full shadow-[0_0_10px_rgba(251,146,60,0.55)]"
          style={{ background: 'radial-gradient(circle at 35% 30%, #fcd34d 0%, #fb923c 45%, #fb7185 100%)' }}
        />
      )}
    </button>
  )
}
