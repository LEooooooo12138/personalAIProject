import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import ChatPage from '../pages/ChatPage'

const context = vi.hoisted(() => ({ signal: new AbortController().signal, sent: [] as string[], handshakeError: null as Error | null, frames: null as ((event: MessageEvent) => void) | null }))
vi.mock('../auth', () => ({ useAuth: () => ({ identity: { user: { id: 'alice' }, capabilities: ['chat:use', 'sessions:read'], csrf_token: 'csrf' }, epoch: 1, signal: context.signal, isCurrent: () => !context.signal.aborted, expire: vi.fn() }) }))
vi.mock('../coordinator', () => ({ coordinatedFetch: (url: string, options: RequestInit) => fetch(url, options), openChatSocket: async () => { if (context.handshakeError) throw context.handshakeError; return ({ send: (value: string) => context.sent.push(value), close: () => {}, set onmessage(value: (event: MessageEvent) => void) { context.frames = value } }) } }))
class Recorder {
  static instance: Recorder
  static isTypeSupported = (mime: string) => mime === 'audio/webm;codecs=opus'
  state = 'inactive'; mimeType = 'audio/webm;codecs=opus'
  ondataavailable: ((event: { data: Blob }) => void) | null = null
  onstop: (() => void) | null = null
  onerror: (() => void) | null = null
  constructor() { Recorder.instance = this }
  start() { this.state = 'recording' }
  stop() { this.state = 'inactive'; this.ondataavailable?.({ data: new Blob(['audio'], { type: this.mimeType }) }); this.onstop?.() }
}
let stopTrack: ReturnType<typeof vi.fn>
let requests: { path: string; body: Record<string, unknown> }[]
class Player {
  static instances: Player[] = []
  onended: (() => void) | null = null
  onerror: (() => void) | null = null
  play = vi.fn(async () => {})
  pause = vi.fn()
  removeAttribute = vi.fn()
  constructor() { Player.instances.push(this) }
}
beforeEach(async () => {
  const { webcrypto } = await vi.importActual<{ webcrypto: Crypto }>('node:crypto')
  context.signal = new AbortController().signal; context.sent = []; context.frames = null; context.handshakeError = null
  stopTrack = vi.fn(); requests = []
  Player.instances = []
  vi.stubGlobal('crypto', webcrypto)
  vi.stubGlobal('Audio', Player)
  vi.stubGlobal('URL', class extends URL { static createObjectURL = vi.fn(() => 'blob:test-audio'); static revokeObjectURL = vi.fn() })
  vi.stubGlobal('MediaRecorder', Recorder)
  vi.stubGlobal('AudioContext', class { decodeAudioData = async () => ({ duration: 1 }); close = async () => {} })
  vi.stubGlobal('OfflineAudioContext', class { destination = {}; createBufferSource = () => ({ buffer: null, connect: () => {}, start: () => {} }); startRendering = async () => ({ getChannelData: () => new Float32Array(16000) }) })
  Object.defineProperty(navigator, 'mediaDevices', { configurable: true, value: { getUserMedia: vi.fn(async () => ({ getTracks: () => [{ stop: stopTrack }] })) } })
  vi.stubGlobal('fetch', vi.fn(async (path: string, options?: RequestInit) => {
    if (options?.method === 'POST') {
      const body = JSON.parse(options.body as string); requests.push({ path, body })
      if (body.kind === 'synthesize') return Response.json({ id: 'job1', status: 'succeeded', audio_base64: 'YXVkaW8=', mime_type: 'audio/wav' })
      return Response.json({ id: 'job1', status: 'succeeded', text: '语音消息' })
    }
    if (path.endsWith('/speech/status')) return Response.json({ asr_mode: 'local', tts_mode: 'local', cloud_configured: false, local_asr_configured: true, local_tts_configured: true })
    if (path.includes('/messages')) return Response.json({ id: 'one', messages: [{ role: 'assistant', content: '旧回答', timestamp: '2026-01-01' }] })
    return Response.json({ sessions: [] })
  }))
})
afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals() })
const show = (url = '/app/chat') => render(<MemoryRouter initialEntries={[url]}><ChatPage/></MemoryRouter>)
it('ends recording, sends one transcript and preserves the typed draft', async () => {
  show()
  fireEvent.change(screen.getByLabelText('消息内容'), { target: { value: '未发送草稿' } })
  fireEvent.click(await screen.findByRole('button', { name: '开始录音' }))
  fireEvent.click(await screen.findByRole('button', { name: '结束并发送' }))
  await waitFor(() => expect(context.sent).toHaveLength(1))
  expect(JSON.parse(context.sent[0])).toMatchObject({ content: '语音消息' })
  expect(screen.getByLabelText('消息内容')).toHaveValue('未发送草稿')
  expect(stopTrack).toHaveBeenCalled()
  expect(requests[0].body).toMatchObject({ kind: 'transcribe', mime_type: 'audio/wav' })
  expect(atob(requests[0].body.audio_base64 as string).slice(0, 4)).toBe('RIFF')
})
it('releases a microphone permission result arriving after unmount without uploading', async () => {
  let resolve!: (stream: unknown) => void
  navigator.mediaDevices.getUserMedia = vi.fn(() => new Promise((done) => { resolve = done })) as typeof navigator.mediaDevices.getUserMedia
  const view = show()
  fireEvent.click(await screen.findByRole('button', { name: '开始录音' }))
  view.unmount()
  await act(async () => resolve({ getTracks: () => [{ stop: stopTrack }] }))
  expect(stopTrack).toHaveBeenCalledOnce()
  expect(requests).toHaveLength(0)
})
it('cancels recording on a new conversation without sending audio', async () => {
  show('/app/chat?sid=one')
  await screen.findByText('旧回答')
  fireEvent.click(await screen.findByRole('button', { name: '开始录音' }))
  await screen.findByRole('button', { name: '结束并发送' })
  fireEvent.click(screen.getByRole('button', { name: '新对话' }))
  await waitFor(() => expect(stopTrack).toHaveBeenCalled())
  expect(requests).toHaveLength(0)
})
it('stops at sixty seconds and waits for an explicit send', async () => {
  let limit: (() => void) | undefined
  const realTimeout = globalThis.setTimeout
  const timeout = vi.spyOn(globalThis, 'setTimeout').mockImplementation(((callback: () => void, ms?: number, ...args: unknown[]) => {
    if (ms === 60000) limit = callback
    return realTimeout(callback, ms, ...args)
  }) as typeof setTimeout)
  show()
  fireEvent.click(await screen.findByRole('button', { name: '开始录音' }))
  await screen.findByRole('button', { name: '结束并发送' })
  expect(limit).toBeDefined()
  act(() => limit!())
  expect(screen.getByRole('button', { name: '发送录音' })).toBeInTheDocument()
  expect(requests).toHaveLength(0)
  expect(stopTrack).toHaveBeenCalled()
  timeout.mockRestore()
})
it('does not synthesize restored history when automatic playback is enabled', async () => {
  show('/app/chat?sid=one')
  await screen.findByText('旧回答')
  const toggle = await screen.findByLabelText('自动朗读新回答')
  expect(toggle).not.toBeChecked()
  fireEvent.click(toggle)
  expect(requests).toHaveLength(0)
  expect(screen.getByRole('button', { name: '朗读回答' })).toBeInTheDocument()
})
it('cancels a recording when new conversation is clicked before a first message exists', async () => {
  show()
  fireEvent.click(await screen.findByRole('button', { name: '开始录音' }))
  await screen.findByRole('button', { name: '结束并发送' })
  fireEvent.click(screen.getByRole('button', { name: '新对话' }))
  expect(stopTrack).toHaveBeenCalled()
  expect(screen.getByRole('button', { name: '开始录音' })).toBeInTheDocument()
})
it('keeps typed text from racing an active recording', async () => {
  show()
  fireEvent.change(screen.getByLabelText('消息内容'), { target: { value: '草稿' } })
  fireEvent.click(await screen.findByRole('button', { name: '开始录音' }))
  await screen.findByRole('button', { name: '结束并发送' })
  fireEvent.keyDown(screen.getByLabelText('消息内容'), { key: 'Enter' })
  expect(screen.getByRole('button', { name: '发送消息' })).toBeDisabled()
  expect(screen.getByLabelText('消息内容')).toHaveValue('草稿')
  expect(context.sent).toHaveLength(0)
})
it('binds manual synthesis to the persisted index and content digest, releasing playback on recording', async () => {
  show('/app/chat?sid=one')
  fireEvent.click(await screen.findByRole('button', { name: '朗读回答' }))
  await screen.findByRole('button', { name: '停止朗读' })
  await waitFor(() => expect(Player.instances[0]?.play).toHaveBeenCalledOnce())
  expect(requests[0].body).toEqual({ kind: 'synthesize', session_id: 'one', message_index: 0, content_revision: '70574f28e923fba6ab1fd0ae15c32fb7f7722988975b4b6b8967d560a14e6638' })
  fireEvent.click(screen.getByRole('button', { name: '开始录音' }))
  await screen.findByRole('button', { name: '结束并发送' })
  expect(Player.instances[0].pause).toHaveBeenCalledOnce()
  expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:test-audio')
})
it('synthesizes only a new final response when auto playback was explicitly enabled', async () => {
  show('/app/chat?sid=one')
  await screen.findByText('旧回答')
  fireEvent.click(screen.getByLabelText('自动朗读新回答'))
  fireEvent.change(screen.getByLabelText('消息内容'), { target: { value: '新问题' } })
  fireEvent.click(screen.getByRole('button', { name: '发送消息' }))
  await waitFor(() => expect(context.frames).toBeTruthy())
  act(() => context.frames!({ data: JSON.stringify({ type: 'response', content: '新回答' }) } as MessageEvent))
  await waitFor(() => expect(Player.instances[0]?.play).toHaveBeenCalledOnce())
  expect(requests).toHaveLength(1)
  expect(requests[0].body).toMatchObject({ kind: 'synthesize', session_id: 'one', message_index: 2 })
})
it('offers a manual retry after autoplay rejection without generating a second speech job', async () => {
  vi.stubGlobal('Audio', class extends Player { play = vi.fn().mockRejectedValueOnce(new DOMException('Blocked', 'NotAllowedError')).mockResolvedValue(undefined) })
  show('/app/chat?sid=one')
  fireEvent.click(await screen.findByRole('button', { name: '朗读回答' }))
  fireEvent.click(await screen.findByRole('button', { name: '播放已生成语音' }))
  await waitFor(() => expect(Player.instances[0].play).toHaveBeenCalledTimes(2))
  expect(requests).toHaveLength(1)
  fireEvent.click(screen.getByRole('button', { name: '停止朗读' }))
  expect(Player.instances[0].pause).toHaveBeenCalledOnce()
})
it('cancels a pending speech job on conversation change and never sends its late transcript', async () => {
  const original = vi.mocked(fetch).getMockImplementation()!
  vi.mocked(fetch).mockImplementation(async (path, options) => {
    if (String(path).endsWith('/speech/jobs') && options?.method === 'POST') { requests.push({ path: String(path), body: JSON.parse(options.body as string) }); return Response.json({ id: 'pending', status: 'queued' }) }
    return original(path, options)
  })
  show('/app/chat?sid=one')
  await screen.findByText('旧回答')
  fireEvent.click(screen.getByRole('button', { name: '开始录音' }))
  fireEvent.click(await screen.findByRole('button', { name: '结束并发送' }))
  await waitFor(() => expect(requests).toHaveLength(1))
  fireEvent.click(screen.getByRole('button', { name: '新对话' }))
  await waitFor(() => expect(requests.some((request) => request.path.endsWith('/pending/cancel'))).toBe(true))
  expect(context.sent).toHaveLength(0)
})
it('stops oversized recording before any upload', async () => {
  show()
  fireEvent.click(await screen.findByRole('button', { name: '开始录音' }))
  await screen.findByRole('button', { name: '结束并发送' })
  act(() => Recorder.instance.ondataavailable!({ data: new Blob([new Uint8Array(8 * 1024 * 1024 + 1)]) }))
  expect(screen.getByText(/录音不能超过 8 MiB/)).toBeInTheDocument()
  expect(stopTrack).toHaveBeenCalled()
  expect(requests).toHaveLength(0)
})

it('preserves both the typed draft and voice transcript when the chat handshake fails', async () => {
  context.handshakeError = new TypeError('Failed to fetch')
  show()
  fireEvent.change(screen.getByLabelText('消息内容'), { target: { value: '未发送草稿' } })
  fireEvent.click(await screen.findByRole('button', { name: '开始录音' }))
  fireEvent.click(await screen.findByRole('button', { name: '结束并发送' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('消息尚未发送')
  expect(screen.getByLabelText('消息内容')).toHaveValue('未发送草稿\n语音消息')
  expect(context.sent).toHaveLength(0)
  expect(screen.getByRole('log')).not.toHaveTextContent('语音消息')
})


it.each([false, true])('rollover_tts_uses_new_session_index_one, auto=%s', async (auto) => {
  show('/app/chat?sid=one')
  await screen.findByText('旧回答')
  if (auto) fireEvent.click(screen.getByLabelText('自动朗读新回答'))
  fireEvent.change(screen.getByLabelText('消息内容'), { target: { value: '本轮问题' } })
  fireEvent.click(screen.getByRole('button', { name: '发送消息' }))
  await waitFor(() => expect(context.frames).toBeTruthy())
  act(() => context.frames!({ data: JSON.stringify({ type: 'session', session_id: 'new' }) } as MessageEvent))
  act(() => context.frames!({ data: JSON.stringify({ type: 'response', content: '本轮回答' }) } as MessageEvent))
  expect(screen.queryByText('旧回答')).toBeNull()
  if (!auto) fireEvent.click(screen.getByRole('button', { name: '朗读回答' }))
  await waitFor(() => expect(Player.instances[0]?.play).toHaveBeenCalledOnce())
  expect(requests.filter((request) => request.body.kind === 'synthesize')).toHaveLength(1)
  expect(requests[0].body).toMatchObject({ session_id: 'new', message_index: 1 })
})

it('counts only actual recording time, freezes at sixty seconds without sending, and clears on cancel', async () => {
 let grant!: (stream: unknown) => void
 let now = 0; let tick: (() => void) | undefined; let limit: (() => void) | undefined
 navigator.mediaDevices.getUserMedia = vi.fn(() => new Promise(resolve => { grant = resolve })) as typeof navigator.mediaDevices.getUserMedia
 show()
 const startButton = await screen.findByRole('button', { name: '开始录音' })
 const clock = vi.spyOn(performance, 'now')
 const realInterval = globalThis.setInterval
 const interval = vi.spyOn(globalThis, 'setInterval').mockImplementation(((callback: () => void, ms?: number) => { if (ms === 250) tick = callback; return realInterval(callback, ms) }) as typeof setInterval)
 const realTimeout = globalThis.setTimeout
 const timeout = vi.spyOn(globalThis, 'setTimeout').mockImplementation(((callback: () => void, ms?: number, ...args: unknown[]) => {
  if (ms === 60000) { limit = callback; return 124 }
  return realTimeout(callback, ms, ...args)
 }) as typeof setTimeout)
 try {
  fireEvent.click(startButton)
  now = 5000
  expect(screen.queryByLabelText('录音计时')).toBeNull()
  grant({ getTracks: () => [{ stop: stopTrack }] })
  await screen.findByRole('button', { name: '结束并发送' })
  const started = performance.now(); clock.mockImplementation(() => started + now - 5000)
  expect(screen.getByLabelText('录音计时')).toHaveTextContent('0 / 60 秒')
  now = 8000; act(() => tick!())
  expect(screen.getByLabelText('录音计时')).toHaveTextContent('3 / 60 秒')
  now = 65000; act(() => limit!())
  expect(screen.getByRole('button', { name: '发送录音' })).toBeInTheDocument()
  expect(screen.getByLabelText('录音计时')).toHaveTextContent('60 / 60 秒')
  expect(requests).toHaveLength(0); expect(context.sent).toHaveLength(0)
  fireEvent.click(screen.getByRole('button', { name: '取消录音' }))
  expect(screen.queryByLabelText('录音计时')).toBeNull()
 } finally { interval.mockRestore(); timeout.mockRestore(); clock.mockRestore() }
})
