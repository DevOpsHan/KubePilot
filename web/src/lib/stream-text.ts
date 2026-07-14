export const MAX_STREAM_TEXT_LENGTH = 1_000_000

export function decodeUtf8Chunk(decoder: TextDecoder, chunk: ArrayBuffer): string {
  return decoder.decode(chunk, { stream: true })
}

export function flushUtf8Decoder(decoder: TextDecoder): string {
  return decoder.decode()
}

export function appendBoundedText(
  current: string,
  chunk: string,
  limit = MAX_STREAM_TEXT_LENGTH,
): string {
  const next = current + chunk
  return next.length > limit ? next.slice(next.length - limit) : next
}
