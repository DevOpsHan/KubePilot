import { describe, expect, it } from 'vitest'

import { appendBoundedText, decodeUtf8Chunk, flushUtf8Decoder } from './stream-text'

describe('流式文本处理', () => {
  it('跨消息拆分的 UTF-8 字符不会被解码成替换字符', () => {
    const bytes = new TextEncoder().encode('日志')
    const decoder = new TextDecoder()

    const first = decodeUtf8Chunk(decoder, bytes.slice(0, 2).buffer)
    const second = decodeUtf8Chunk(decoder, bytes.slice(2).buffer)
    const tail = flushUtf8Decoder(decoder)

    expect(first + second + tail).toBe('日志')
    expect(first + second + tail).not.toContain('\uFFFD')
  })

  it('追加内容超过上限时只保留最新文本', () => {
    expect(appendBoundedText('1234', '5678', 6)).toBe('345678')
  })
})
