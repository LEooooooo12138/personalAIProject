import { act, renderHook, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { useVoice } from './useVoice'

const context = vi.hoisted(() => ({ signal: new AbortController().signal }))
vi.mock('../auth', () => ({ useAuth: () => ({ identity: { user: { id: 'owner' }, csrf_token: 'csrf' }, epoch: 1, signal: context.signal, isCurrent: () => !context.signal.aborted, expire: vi.fn() }) }))
vi.mock('../coordinator', () => ({ coordinatedFetch: (url: string, options: RequestInit) => fetch(url, options) }))
class Player {
  static instances: Player[] = []
  onended: (() => void) | null = null
  onerror: (() => void) | null = null
  play = vi.fn<() => Promise<void>>(async () => {})
  pause = vi.fn()
  removeAttribute = vi.fn()
  constructor() { Player.instances.push(this) }
}
beforeEach(async () => {
  const { webcrypto } = await vi.importActual<{ webcrypto: Crypto }>('node:crypto')
  context.signal = new AbortController().signal; Player.instances = []
  vi.stubGlobal('crypto', webcrypto)
  vi.stubGlobal('Audio', Player)
  let urls = 0
  vi.stubGlobal('URL', class extends URL { static createObjectURL = vi.fn(() => `blob:sample-${++urls}`); static revokeObjectURL = vi.fn() })
  vi.stubGlobal('fetch', vi.fn(async (_url: string, options?: RequestInit) => options?.method === 'POST'
    ? Response.json({ id: 'job', status: 'succeeded', mime_type: 'audio/wav', audio_base64: 'UklGRg==' })
    : Response.json({ asr_mode: 'local', tts_mode: 'local', local_asr_configured: false, local_tts_configured: true, cloud_configured: false })))
})
afterEach(() => vi.unstubAllGlobals())

it('releases unplayable media and does not label decoding errors as autoplay permission failures', async () => {
  vi.stubGlobal('Audio', class extends Player { play = vi.fn(async () => { throw new DOMException('Unsupported media', 'NotSupportedError') }) })
  const { result } = renderHook(() => useVoice('one', 0, true, () => {}))
  await waitFor(() => expect(result.current.available).toBe(true))
  await act(async () => { await result.current.speak('one', 0, 'reply') })
  expect(result.current.playback).toBe('idle')
  expect(result.current.error).toBe('无法播放返回的音频。')
  expect(URL.revokeObjectURL).toHaveBeenCalledExactlyOnceWith('blob:sample-1')
  expect(Player.instances[0].removeAttribute).toHaveBeenCalledWith('src')
})

it('retains generated audio for a direct retry only when the browser denies autoplay permission', async () => {
  vi.stubGlobal('Audio', class extends Player { play = vi.fn<() => Promise<void>>().mockRejectedValueOnce(new DOMException('Denied', 'NotAllowedError')).mockResolvedValue(undefined) })
  const { result } = renderHook(() => useVoice('one', 0, true, () => {}))
  await waitFor(() => expect(result.current.available).toBe(true))
  await act(async () => { await result.current.speak('one', 0, 'reply') })
  expect(result.current.playback).toBe('ready')
  expect(result.current.error).toContain('浏览器阻止了自动播放')
  expect(URL.revokeObjectURL).not.toHaveBeenCalled()
  await act(async () => { await result.current.resumePlayback() })
  expect(result.current.playback).toBe('playing')
  expect(result.current.error).toBe('')
  expect(Player.instances).toHaveLength(1)
})

it('cleans up exactly once when media error fires before the play rejection', async () => {
  vi.stubGlobal('Audio', class extends Player { play = vi.fn(async () => { this.onerror?.(); throw new DOMException('Unsupported media', 'NotSupportedError') }) })
  const { result } = renderHook(() => useVoice('one', 0, true, () => {}))
  await waitFor(() => expect(result.current.available).toBe(true))
  await act(async () => { await result.current.speak('one', 0, 'reply') })
  expect(result.current.playback).toBe('idle')
  expect(result.current.error).toBe('无法播放返回的音频。')
  expect(URL.revokeObjectURL).toHaveBeenCalledExactlyOnceWith('blob:sample-1')
})

it('ignores a cancelled players late rejection after a new player starts', async () => {
  let reject!: (cause: unknown) => void
  vi.stubGlobal('Audio', class extends Player { play = vi.fn(() => Player.instances.length === 1 ? new Promise<void>((_resolve, fail) => { reject = fail }) : Promise.resolve()) })
  const { result } = renderHook(() => useVoice('one', 0, true, () => {}))
  await waitFor(() => expect(result.current.available).toBe(true))
  let first!: Promise<void>
  act(() => { first = result.current.speak('one', 0, 'first') })
  await waitFor(() => expect(Player.instances).toHaveLength(1))
  await act(async () => { result.current.cancel(); await result.current.speak('one', 1, 'second') })
  expect(result.current.playback).toBe('playing')
  await act(async () => { reject(new DOMException('Interrupted by pause', 'AbortError')); await first })
  expect(result.current.playback).toBe('playing')
  expect(result.current.error).toBe('')
  expect(URL.revokeObjectURL).toHaveBeenCalledExactlyOnceWith('blob:sample-1')
  expect(Player.instances[1].pause).not.toHaveBeenCalled()
})
