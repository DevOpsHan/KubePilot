import type { CSSProperties } from 'react'

// 等距立方体(纯 SVG,三面不同明度的靛蓝,玻璃质感)
function IsoCube({ size, className, style }: { size: number; className?: string; style?: CSSProperties }) {
  return (
    <svg width={size} height={size} viewBox="0 0 100 100" className={className} style={style} aria-hidden>
      <polygon points="50,6 94,31 50,56 6,31" fill="#c7d2fe" fillOpacity="0.9" stroke="#6366f1" strokeOpacity="0.25" />
      <polygon points="6,31 50,56 50,98 6,73" fill="#a5b4fc" fillOpacity="0.8" stroke="#6366f1" strokeOpacity="0.25" />
      <polygon points="94,31 50,56 50,98 94,73" fill="#818cf8" fillOpacity="0.75" stroke="#6366f1" strokeOpacity="0.25" />
    </svg>
  )
}

// 登录页自绘动态背景:渐变底 + 移动网格 + 渐变光斑 + 浮动等距立方体。
// 不使用任何外部图片素材,纯 CSS/SVG;prefers-reduced-motion 下动画关闭。
export function LoginBackground() {
  return (
    <div aria-hidden className="pointer-events-none absolute inset-0 -z-10 overflow-hidden">
      {/* 渐变底(浅色柔和靛蓝,深色由页面 bg 兜底) */}
      <div className="absolute inset-0 dark:opacity-0" style={{ background: 'linear-gradient(135deg,#eef2ff 0%,#f5f3ff 45%,#eef2ff 100%)' }} />
      {/* 移动网格 */}
      <div className="login-grid absolute inset-0" />
      {/* 渐变光斑 */}
      <div className="login-blob absolute -left-32 top-8 size-[440px] rounded-full opacity-40 blur-3xl"
        style={{ background: 'radial-gradient(circle,#6366f1 0%,transparent 70%)' }} />
      <div className="login-blob login-blob-2 absolute bottom-[-6rem] left-[30%] size-[480px] rounded-full opacity-30 blur-3xl"
        style={{ background: 'radial-gradient(circle,#8b5cf6 0%,transparent 70%)' }} />
      {/* 浮动等距立方体 */}
      <IsoCube size={120} className="login-float absolute left-[14%] top-[22%]" style={{ animationDelay: '0s', animationDuration: '9s' }} />
      <IsoCube size={72} className="login-float absolute left-[30%] top-[58%]" style={{ animationDelay: '1.2s', animationDuration: '7.5s' }} />
      <IsoCube size={56} className="login-float absolute left-[8%] bottom-[16%]" style={{ animationDelay: '0.6s', animationDuration: '8.5s' }} />
      <IsoCube size={44} className="login-float absolute left-[44%] top-[16%] opacity-80" style={{ animationDelay: '2s', animationDuration: '10s' }} />
    </div>
  )
}
