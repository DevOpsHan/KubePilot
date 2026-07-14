import { useTranslation } from 'react-i18next'
import { BRAND_BOLT_PATH } from '@/lib/brand'

// 首页主插画:品牌闪电为核心中枢,外环轨道旋转(象征多集群绕智能中枢运转),
// 叠加呼吸光晕与漂浮粒子。纯自绘 SVG + CSS 动画,prefers-reduced-motion 下静止。
function HomeIllustration() {
  return (
    <div className="relative aspect-square w-full max-w-[380px]">
      {/* 呼吸光晕 */}
      <div aria-hidden
        className="home-glow absolute left-1/2 top-1/2 size-[72%] -translate-x-1/2 -translate-y-1/2 rounded-full blur-3xl"
        style={{ background: 'radial-gradient(circle,#8b5cf6 0%,transparent 68%)' }} />

      {/* 外轨道(顺时针) */}
      <div aria-hidden className="home-orbit-cw absolute inset-0">
        <svg viewBox="0 0 100 100" className="size-full">
          <circle cx="50" cy="50" r="47" fill="none" stroke="#8b5cf6" strokeOpacity="0.28" strokeWidth="0.5" strokeDasharray="2 3" />
          <circle cx="97" cy="50" r="2.4" fill="#8b5cf6" />
          <circle cx="50" cy="3" r="1.8" fill="#47bfff" />
          <circle cx="15" cy="75" r="1.4" fill="#a855f7" />
        </svg>
      </div>

      {/* 内轨道(逆时针) */}
      <div aria-hidden className="home-orbit-ccw absolute inset-[15%]">
        <svg viewBox="0 0 100 100" className="size-full">
          <circle cx="50" cy="50" r="46" fill="none" stroke="#6366f1" strokeOpacity="0.24" strokeWidth="0.6" strokeDasharray="1.5 3.5" />
          <circle cx="96" cy="50" r="2.2" fill="#6366f1" />
          <circle cx="20" cy="30" r="1.6" fill="#47bfff" />
        </svg>
      </div>

      {/* 中心品牌闪电(脉冲发光) */}
      <svg viewBox="0 0 48 46" aria-hidden
        className="home-pulse absolute left-1/2 top-1/2 w-[33%] -translate-x-1/2 -translate-y-1/2">
        <defs>
          <linearGradient id="home-bolt" x1="0.2" y1="0" x2="0.5" y2="1">
            <stop offset="0" stopColor="#c4b5fd" />
            <stop offset="0.5" stopColor="#8b5cf6" />
            <stop offset="1" stopColor="#6d28d9" />
          </linearGradient>
        </defs>
        <path d={BRAND_BOLT_PATH} fill="url(#home-bolt)" />
        {/* 左缘高光,增强立体玻璃质感 */}
        <path d={BRAND_BOLT_PATH} fill="none" stroke="#ede9fe" strokeOpacity="0.55" strokeWidth="0.6" />
      </svg>

      {/* 漂浮粒子 */}
      <span aria-hidden className="login-float absolute left-[6%] top-[30%] size-2 rounded-full bg-primary/60" style={{ animationDelay: '0s' }} />
      <span aria-hidden className="login-float absolute right-[8%] top-[20%] size-1.5 rounded-full" style={{ animationDelay: '1.3s', background: '#47bfff' }} />
      <span aria-hidden className="login-float absolute bottom-[16%] right-[16%] size-2.5 rounded-full" style={{ animationDelay: '0.7s', background: '#a855f7', opacity: 0.7 }} />
      <span aria-hidden className="login-float absolute bottom-[24%] left-[14%] size-1.5 rounded-full bg-primary/50" style={{ animationDelay: '2s' }} />
    </div>
  )
}

// 登录后默认落地的首页:纯欢迎页,风格与登录页统一。
export function HomePage() {
  const { t } = useTranslation('home')
  return (
    <div className="flex min-h-full flex-col items-center justify-center gap-7 px-8 py-12 text-center">
      <div className="space-y-3">
        <h1 className="text-3xl font-extrabold tracking-tight sm:text-4xl"
          style={{ backgroundImage: 'var(--gradient-brand)', WebkitBackgroundClip: 'text', backgroundClip: 'text', color: 'transparent' }}>
          {t('welcome')}
        </h1>
        <p className="mx-auto max-w-xl text-sm text-muted-foreground sm:text-base">{t('subtitle')}</p>
      </div>

      <HomeIllustration />

      <div className="text-xs font-medium tracking-wide text-muted-foreground">{t('brand')}</div>
    </div>
  )
}
