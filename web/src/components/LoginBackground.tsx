import type { CSSProperties } from 'react'
import { BRAND_BOLT_PATH } from '@/lib/brand'

// 半透明品牌闪电(漂浮装饰),复用 logo 轮廓,替代此前的等距立方体。
function FloatBolt({
  size, className, style, opacity = 0.5, color = '#8b5cf6',
}: {
  size: number; className?: string; style?: CSSProperties; opacity?: number; color?: string
}) {
  return (
    <svg width={size} height={(size * 46) / 48} viewBox="0 0 48 46" className={className} style={style} aria-hidden>
      <path d={BRAND_BOLT_PATH} fill={color} fillOpacity={opacity} />
    </svg>
  )
}

// 浅色极光:紫/青/靛蓝多点径向叠加 + 底色,呼应品牌闪电三色(#863bff / #7e14ff / #47bfff)。
const AURORA = [
  'radial-gradient(46% 42% at 15% 18%, rgba(139,92,246,0.50) 0%, transparent 72%)',
  'radial-gradient(40% 38% at 85% 14%, rgba(71,191,255,0.36) 0%, transparent 72%)',
  'radial-gradient(52% 48% at 80% 82%, rgba(124,58,237,0.46) 0%, transparent 74%)',
  'radial-gradient(42% 42% at 24% 88%, rgba(99,102,241,0.40) 0%, transparent 74%)',
  'linear-gradient(135deg,#e9e7fb 0%,#efe9ff 48%,#e7ebff 100%)',
].join(',')

// 登录页自绘动态背景:品牌极光 + 移动网格 + 渐变光斑 + 漂浮品牌闪电。
// 纯 CSS/SVG,不使用外部图片;prefers-reduced-motion 下动画关闭。
export function LoginBackground() {
  return (
    <div aria-hidden className="pointer-events-none absolute inset-0 z-0 overflow-hidden">
      {/* 浅色极光底(深色下隐藏) */}
      <div className="absolute inset-0 dark:opacity-0" style={{ background: AURORA }} />
      {/* 深色底光(仅深色显示,顶部紫色辉光) */}
      <div className="absolute inset-0 opacity-0 dark:opacity-100"
        style={{ background: 'radial-gradient(60% 55% at 50% -5%, rgba(124,58,237,0.28) 0%, transparent 70%)' }} />

      {/* 移动网格 */}
      <div className="login-grid absolute inset-0" />

      {/* 渐变光斑(紫 / 青 / 靛蓝,缓慢漂浮) */}
      <div className="login-blob absolute -left-32 top-4 size-[460px] rounded-full opacity-60 blur-3xl"
        style={{ background: 'radial-gradient(circle,#8b5cf6 0%,transparent 70%)' }} />
      <div className="login-blob login-blob-2 absolute -bottom-24 left-[28%] size-[500px] rounded-full opacity-55 blur-3xl"
        style={{ background: 'radial-gradient(circle,#7c3aed 0%,transparent 70%)' }} />
      <div className="login-blob absolute -right-24 top-[36%] size-[420px] rounded-full opacity-45 blur-3xl"
        style={{ background: 'radial-gradient(circle,#47bfff 0%,transparent 72%)', animationDuration: '16s' }} />

      {/* 漂浮品牌闪电 */}
      <FloatBolt size={104} color="#8b5cf6" opacity={0.42} className="login-float absolute left-[12%] top-[18%]" style={{ animationDelay: '0s', animationDuration: '9s' }} />
      <FloatBolt size={64} color="#7c3aed" opacity={0.38} className="login-float absolute left-[32%] top-[62%]" style={{ animationDelay: '1.2s', animationDuration: '7.5s' }} />
      <FloatBolt size={46} color="#6366f1" opacity={0.4} className="login-float absolute left-[7%] bottom-[16%]" style={{ animationDelay: '0.6s', animationDuration: '8.5s' }} />
      <FloatBolt size={54} color="#8b5cf6" opacity={0.32} className="login-float absolute right-[10%] top-[14%]" style={{ animationDelay: '2s', animationDuration: '10s' }} />

      {/* 光点粒子 */}
      <span className="login-float absolute left-[20%] top-[40%] size-2 rounded-full bg-primary/50" style={{ animationDelay: '0.4s' }} />
      <span className="login-float absolute right-[26%] bottom-[22%] size-1.5 rounded-full" style={{ animationDelay: '1.6s', background: '#47bfff' }} />
      <span className="login-float absolute left-[46%] top-[26%] size-2.5 rounded-full" style={{ animationDelay: '2.4s', background: '#a855f7', opacity: 0.6 }} />
    </div>
  )
}
