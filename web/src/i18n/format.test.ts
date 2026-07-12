import { describe, expect, it } from 'vitest'
import { formatCalendarDate, formatDateTime, formatRelativeTime } from './format'

const DATE_TIME_OPTIONS: Intl.DateTimeFormatOptions = {
  year: 'numeric',
  month: '2-digit',
  day: '2-digit',
  hour: '2-digit',
  minute: '2-digit',
  second: '2-digit',
  hour12: false,
}

const DATE_OPTIONS: Intl.DateTimeFormatOptions = {
  year: 'numeric',
  month: '2-digit',
  day: '2-digit',
}

const CALENDAR_DATE_OPTIONS = {
  short: {
    month: '2-digit',
    day: '2-digit',
    timeZone: 'UTC',
  },
  long: {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    timeZone: 'UTC',
  },
} as const satisfies Record<'short' | 'long', Intl.DateTimeFormatOptions>

describe('formatCalendarDate', () => {
  it.each([null, undefined, '', 'not-a-date', '2026/07/10', '2026-02-30'])(
    '空值、非法格式或不存在的日期返回不可用占位符：%s',
    (value) => {
      expect(formatCalendarDate(value, 'zh')).toBe('—')
      expect(formatCalendarDate(value, 'en')).toBe('—')
    },
  )

  it.each([
    { language: 'zh', locale: 'zh-CN' },
    { language: 'en', locale: 'en-US' },
  ] as const)('按 $locale 输出不含时间的长短日历日期', ({ language, locale }) => {
    const value = '2026-07-10'
    const timestamp = Date.UTC(2026, 6, 10)

    expect(formatCalendarDate(value, language, 'short')).toBe(
      new Intl.DateTimeFormat(locale, CALENDAR_DATE_OPTIONS.short).format(timestamp),
    )
    expect(formatCalendarDate(value, language, 'long')).toBe(
      new Intl.DateTimeFormat(locale, CALENDAR_DATE_OPTIONS.long).format(timestamp),
    )
  })

  it.each(['2025-12-31', '2026-01-01'])(
    '保持 YYYY-MM-DD 的日历日期不发生前后偏移：%s',
    (value) => {
      const [year, month, day] = value.split('-').map(Number)
      const timestamp = Date.UTC(year, month - 1, day)

      expect(formatCalendarDate(value, 'zh', 'long')).toBe(
        new Intl.DateTimeFormat('zh-CN', CALENDAR_DATE_OPTIONS.long).format(timestamp),
      )
      expect(formatCalendarDate(value, 'en', 'long')).toBe(
        new Intl.DateTimeFormat('en-US', CALENDAR_DATE_OPTIONS.long).format(timestamp),
      )
    },
  )
})

describe('formatDateTime', () => {
  it.each([null, undefined, '', 'not-a-date'])(
    '空值或无效值返回不可用占位符：%s',
    (value) => {
      expect(formatDateTime(value, 'zh')).toBe('—')
      expect(formatDateTime(value, 'en')).toBe('—')
    },
  )

  it.each([
    { language: 'zh', locale: 'zh-CN' },
    { language: 'en', locale: 'en-US' },
  ] as const)('按 $locale 格式化日期时间且不依赖固定时区', ({ language, locale }) => {
    const value = new Date(2026, 0, 2, 3, 4, 5).getTime()
    const expected = new Intl.DateTimeFormat(locale, DATE_TIME_OPTIONS).format(value)

    expect(formatDateTime(value, language)).toBe(expected)
  })

  it('将 Unix 纪元视为有效时间而不是空值', () => {
    expect(formatDateTime(0, 'en')).not.toBe('—')
  })
})

describe('formatRelativeTime', () => {
  const now = Date.UTC(2026, 6, 11, 8, 0, 0)

  it.each([null, undefined, '', 'not-a-date'])(
    '空值或无效值返回不可用占位符：%s',
    (value) => {
      expect(formatRelativeTime(value, 'zh', now)).toBe('—')
      expect(formatRelativeTime(value, 'en', now)).toBe('—')
    },
  )

  it('稳定格式化当前、过去和未来分钟', () => {
    expect(formatRelativeTime(now, 'zh', now)).toBe('现在')
    expect(formatRelativeTime(now, 'en', now)).toBe('now')
    expect(formatRelativeTime(now - 3 * 60_000, 'zh', now)).toBe('3 分钟前')
    expect(formatRelativeTime(now - 3 * 60_000, 'en', now)).toBe('3 minutes ago')
    expect(formatRelativeTime(now + 2 * 60_000, 'zh', now)).toBe('2 分钟后')
    expect(formatRelativeTime(now + 2 * 60_000, 'en', now)).toBe('in 2 minutes')
  })

  it('按小时和天选择相对时间单位', () => {
    expect(formatRelativeTime(now - 2 * 60 * 60_000, 'zh', now)).toBe('2 小时前')
    expect(formatRelativeTime(now - 2 * 60 * 60_000, 'en', now)).toBe('2 hours ago')
    expect(formatRelativeTime(now + 2 * 24 * 60 * 60_000, 'zh', now)).toBe('2 天后')
    expect(formatRelativeTime(now + 2 * 24 * 60 * 60_000, 'en', now)).toBe('in 2 days')
  })

  it.each([
    { language: 'zh', locale: 'zh-CN' },
    { language: 'en', locale: 'en-US' },
  ] as const)('超过七天时按 $locale 回退为日期', ({ language, locale }) => {
    const value = now - 8 * 24 * 60 * 60_000
    const expected = new Intl.DateTimeFormat(locale, DATE_OPTIONS).format(value)

    expect(formatRelativeTime(value, language, now)).toBe(expected)
  })
})
