import type { SupportedLanguage } from './types'

const NOT_AVAILABLE = '—'
const MINUTE_MS = 60_000
const HOUR_MS = 60 * MINUTE_MS
const DAY_MS = 24 * HOUR_MS
const CALENDAR_DATE_PATTERN = /^(\d{4})-(\d{2})-(\d{2})$/u

export type CalendarDateStyle = 'short' | 'long'

const locales = {
  zh: 'zh-CN',
  en: 'en-US',
} as const satisfies Record<SupportedLanguage, string>

const dateTimeFormatters: Record<SupportedLanguage, Intl.DateTimeFormat> = {
  zh: new Intl.DateTimeFormat(locales.zh, {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  }),
  en: new Intl.DateTimeFormat(locales.en, {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  }),
}

const dateFormatters: Record<SupportedLanguage, Intl.DateTimeFormat> = {
  zh: new Intl.DateTimeFormat(locales.zh, {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  }),
  en: new Intl.DateTimeFormat(locales.en, {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  }),
}

const calendarDateFormatters: Record<
  SupportedLanguage,
  Record<CalendarDateStyle, Intl.DateTimeFormat>
> = {
  zh: {
    short: new Intl.DateTimeFormat(locales.zh, {
      month: '2-digit',
      day: '2-digit',
      timeZone: 'UTC',
    }),
    long: new Intl.DateTimeFormat(locales.zh, {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      timeZone: 'UTC',
    }),
  },
  en: {
    short: new Intl.DateTimeFormat(locales.en, {
      month: '2-digit',
      day: '2-digit',
      timeZone: 'UTC',
    }),
    long: new Intl.DateTimeFormat(locales.en, {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      timeZone: 'UTC',
    }),
  },
}

const relativeFormatters: Record<SupportedLanguage, Intl.RelativeTimeFormat> = {
  zh: new Intl.RelativeTimeFormat(locales.zh, { numeric: 'always' }),
  en: new Intl.RelativeTimeFormat(locales.en, { numeric: 'always' }),
}

const currentFormatters: Record<SupportedLanguage, Intl.RelativeTimeFormat> = {
  zh: new Intl.RelativeTimeFormat(locales.zh, { numeric: 'auto' }),
  en: new Intl.RelativeTimeFormat(locales.en, { numeric: 'auto' }),
}

function timestampOf(value: string | number | null | undefined): number | undefined {
  if (value === null || value === undefined) return undefined
  if (typeof value === 'string' && value.trim() === '') return undefined

  const timestamp = typeof value === 'number' ? value : new Date(value).getTime()
  return Number.isFinite(timestamp) ? timestamp : undefined
}

function calendarTimestampOf(value: string | null | undefined): number | undefined {
  if (value === null || value === undefined) return undefined
  const match = CALENDAR_DATE_PATTERN.exec(value.trim())
  if (!match) return undefined

  const year = Number(match[1])
  const month = Number(match[2])
  const day = Number(match[3])
  const date = new Date(0)
  date.setUTCHours(0, 0, 0, 0)
  date.setUTCFullYear(year, month - 1, day)

  if (date.getUTCFullYear() !== year
    || date.getUTCMonth() !== month - 1
    || date.getUTCDate() !== day) {
    return undefined
  }
  return date.getTime()
}

function normalizeRelativeParts(parts: Intl.RelativeTimeFormatPart[]): string {
  return parts.reduce((result, part, index) => {
    const previous = parts[index - 1]
    const needsSeparator = previous?.type === 'integer'
      && part.type === 'literal'
      && !/^\s/u.test(part.value)
    return `${result}${needsSeparator ? ' ' : ''}${part.value}`
  }, '').replace(/\s+/gu, ' ').trim()
}

function formatRelative(
  language: SupportedLanguage,
  value: number,
  unit: Intl.RelativeTimeFormatUnit,
): string {
  return normalizeRelativeParts(relativeFormatters[language].formatToParts(value, unit))
}

export function formatDateTime(
  value: string | number | null | undefined,
  language: SupportedLanguage,
): string {
  const timestamp = timestampOf(value)
  return timestamp === undefined ? NOT_AVAILABLE : dateTimeFormatters[language].format(timestamp)
}

export function formatCalendarDate(
  value: string | null | undefined,
  language: SupportedLanguage,
  style: CalendarDateStyle = 'long',
): string {
  const timestamp = calendarTimestampOf(value)
  return timestamp === undefined
    ? NOT_AVAILABLE
    : calendarDateFormatters[language][style].format(timestamp)
}

export function formatRelativeTime(
  value: string | number | null | undefined,
  language: SupportedLanguage,
  now = Date.now(),
): string {
  const timestamp = timestampOf(value)
  if (timestamp === undefined) return NOT_AVAILABLE

  const current = Number.isFinite(now) ? now : Date.now()
  const difference = timestamp - current
  const absoluteDifference = Math.abs(difference)

  if (difference === 0) {
    return normalizeRelativeParts(currentFormatters[language].formatToParts(0, 'second'))
  }
  if (absoluteDifference < MINUTE_MS) {
    return formatRelative(language, Math.round(difference / 1000), 'second')
  }
  if (absoluteDifference < HOUR_MS) {
    return formatRelative(language, Math.round(difference / MINUTE_MS), 'minute')
  }
  if (absoluteDifference < DAY_MS) {
    return formatRelative(language, Math.round(difference / HOUR_MS), 'hour')
  }
  if (absoluteDifference <= 7 * DAY_MS) {
    return formatRelative(language, Math.round(difference / DAY_MS), 'day')
  }
  return dateFormatters[language].format(timestamp)
}
