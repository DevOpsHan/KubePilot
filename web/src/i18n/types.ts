export type SupportedLanguage = 'zh' | 'en'

export type LocaleShape<T> = T extends string
  ? string
  : { readonly [Key in keyof T]: LocaleShape<T[Key]> }
