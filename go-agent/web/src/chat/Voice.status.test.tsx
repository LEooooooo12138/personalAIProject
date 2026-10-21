import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { useVoice } from './useVoice'
import VoiceControls from './VoiceControls'

const context = vi.hoisted(() => ({ signal: new AbortController().signal, epoch: 1 }))
vi.mock('../auth', () => ({ useAuth: () => ({ identity: { user: { id: 'owner' }, csrf_token: 'csrf' }, epoch: context.epoch, signal: context.signal, isCurrent: (epoch: number) => epoch === context.epoch && !context.signal.aborted, expire: vi.fn() }) }))
vi.mock('../coordinator', () => ({ coordinatedFetch: (url: string, options: RequestInit) => fetch(url, options) }))
const configured = { asr_mode: 'local', tts_mode: 'local', local_asr_configured: true, local_tts_configured: true, cloud_configured: false }
let statusResponse: () => Promise<Response>
let voice: ReturnType<typeof useVoice>
let limit: (() => void) | undefined
let transcript = vi.fn<(text: string) => void>()
let uploads: Record<string, unknown>[]
let stopTrack: ReturnType<typeof vi.fn>
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
class Player {
 static instances: Player[] = []
 onended: (() => void) | null = null
 onerror: (() => void) | null = null
 play = vi.fn(async () => {})
 pause = vi.fn()
 removeAttribute = vi.fn()
 constructor() { Player.instances.push(this) }
}
function Harness({ enabled = true }: { enabled?: boolean }) {
 voice = useVoice('one', 0, enabled, transcript)
 return <VoiceControls voice={voice} disabled={false}/>
}
async function ready() { await waitFor(() => expect(voice.status.data).toEqual(configured)) }
async function refreshFailure() {
 statusResponse = async () => Response.json({ error: 'speech_unavailable' }, { status: 503 })
 act(() => document.dispatchEvent(new Event('visibilitychange')))
 await waitFor(() => expect(voice.status.error?.status).toBe(503))
}
beforeEach(async () => {
 const { webcrypto } = await vi.importActual<{ webcrypto: Crypto }>('node:crypto')
 context.signal = new AbortController().signal; context.epoch = 1
 statusResponse = async () => Response.json(configured)
 limit = undefined; uploads = []; transcript = vi.fn<(text: string) => void>(); stopTrack = vi.fn(); Player.instances = []
 vi.stubGlobal('crypto', webcrypto)
 vi.stubGlobal('Audio', Player)
 vi.stubGlobal('URL', class extends URL { static createObjectURL = vi.fn(() => 'blob:voice-status'); static revokeObjectURL = vi.fn() })
 vi.stubGlobal('MediaRecorder', Recorder)
 vi.stubGlobal('AudioContext', class { decodeAudioData = async () => ({ duration: 1 }); close = async () => {} })
 vi.stubGlobal('OfflineAudioContext', class { destination = {}; createBufferSource = () => ({ buffer: null, connect: () => {}, start: () => {} }); startRendering = async () => ({ getChannelData: () => new Float32Array(16000) }) })
 Object.defineProperty(navigator, 'mediaDevices', { configurable: true, value: { getUserMedia: vi.fn(async () => ({ getTracks: () => [{ stop: stopTrack }] })) } })
 const realTimeout = globalThis.setTimeout
 vi.spyOn(globalThis, 'setTimeout').mockImplementation(((callback: () => void, ms?: number, ...args: unknown[]) => {
  if (ms === 60000) limit = callback
  return realTimeout(callback, ms, ...args)
 }) as typeof setTimeout)
 vi.stubGlobal('fetch', vi.fn(async (path: string, options?: RequestInit) => {
  if (path.endsWith('/speech/status')) return statusResponse()
  if (options?.method === 'POST') {
   const body = JSON.parse(options.body as string); uploads.push(body)
   return body.kind === 'synthesize' ? Response.json({ id: 'speech', status: 'succeeded', mime_type: 'audio/wav', audio_base64: 'UklGRg==' }) : Response.json({ id: 'speech', status: 'succeeded', text: '识别后的文字' })
  }
  throw new Error(`Unexpected endpoint: ${path}`)
 }))
})
afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals() })

it('holds capture and reading before the first status response and explains unknown destinations', async () => {
 statusResponse = () => new Promise(() => {})
 render(<Harness/> )
 expect(screen.getByRole('button', { name: '开始录音' })).toBeDisabled()
 expect(screen.getByLabelText('自动朗读新回答')).toBeDisabled()
 expect(screen.getByText(/尚未确认.*录音.*回答文字.*云端/)).toBeInTheDocument()
 await act(async () => { await voice.start(); await voice.speak('one', 0, '回答'); voice.setAuto(true); voice.onFinal('one', 1, '自动回答') })
 expect(navigator.mediaDevices.getUserMedia).not.toHaveBeenCalled()
 expect(uploads).toHaveLength(0)
})

it('reports a completed first read failure and restores capture after explicit reload', async () => {
 statusResponse = async () => Response.json({ error: 'speech_unavailable' }, { status: 503 })
 render(<Harness/> )
 expect(await screen.findByRole('alert')).toHaveTextContent('语音配置读取失败')
 expect(screen.queryByText('正在读取配置')).toBeNull()
 expect(screen.getByRole('button', { name: '开始录音' })).toBeDisabled()
 await act(async () => { await voice.start(); await voice.speak('one', 0, '回答'); voice.onFinal('one', 1, '自动回答') })
 expect(navigator.mediaDevices.getUserMedia).not.toHaveBeenCalled()
 expect(uploads).toHaveLength(0)
 statusResponse = async () => Response.json(configured)
 fireEvent.click(screen.getByRole('button', { name: '重新读取' }))
 await ready()
 expect(screen.queryByRole('alert')).toBeNull()
 fireEvent.click(screen.getByRole('button', { name: '开始录音' }))
 await screen.findByRole('button', { name: '结束并发送' })
 fireEvent.click(screen.getByRole('button', { name: '取消录音' }))
 expect(stopTrack).toHaveBeenCalledOnce()
})

it('labels stale configuration, blocks sending and preserves recorded audio until a successful reread', async () => {
 render(<Harness/> ); await ready()
 fireEvent.click(screen.getByRole('button', { name: '开始录音' }))
 await screen.findByRole('button', { name: '结束并发送' })
 await refreshFailure()
 expect(screen.getByRole('alert')).toHaveTextContent('上次配置')
 expect(screen.getByRole('button', { name: '结束并发送' })).toBeDisabled()
 expect(voice.phase).toBe('recording')
 expect(stopTrack).not.toHaveBeenCalled()
 act(() => voice.endAndSend())
 expect(uploads).toHaveLength(0)
 act(() => limit!())
 expect(voice.phase).toBe('ready')
 expect(screen.getByRole('button', { name: '发送录音' })).toBeDisabled()
 act(() => voice.sendReady())
 await act(async () => {})
 expect(uploads).toHaveLength(0)
 expect(voice.phase).toBe('ready')
 statusResponse = async () => Response.json(configured)
 fireEvent.click(screen.getByRole('button', { name: '重新读取' }))
 await waitFor(() => expect(screen.getByRole('button', { name: '发送录音' })).toBeEnabled())
 fireEvent.click(screen.getByRole('button', { name: '发送录音' }))
 await waitFor(() => expect(transcript).toHaveBeenCalledExactlyOnceWith('识别后的文字'))
 expect(uploads).toHaveLength(1)
 expect(uploads[0]).toMatchObject({ kind: 'transcribe', session_id: 'one', mime_type: 'audio/wav' })
})

it('does not interrupt trusted capture or playback during a background read or failure and keeps cleanup available', async () => {
 render(<Harness/> ); await ready()
 await act(async () => { await voice.speak('one', 0, '回答'); voice.setAuto(true) })
 expect(voice.playback).toBe('playing')
 let resolve!: (response: Response) => void
 statusResponse = () => new Promise(done => { resolve = done })
 act(() => document.dispatchEvent(new Event('visibilitychange')))
 await waitFor(() => expect(voice.status.loading).toBe(true))
 expect(screen.getByRole('button', { name: '开始录音' })).toBeEnabled()
 expect(Player.instances[0].pause).not.toHaveBeenCalled()
 await act(async () => resolve(Response.json({ error: 'speech_unavailable' }, { status: 503 })))
 expect(voice.playback).toBe('playing')
 await act(async () => { await voice.speak('one', 1, '另一个回答'); voice.onFinal('one', 2, '自动回答') })
 expect(uploads).toHaveLength(1)
 expect(Player.instances[0].pause).not.toHaveBeenCalled()
 fireEvent.click(screen.getByRole('button', { name: '停止朗读' }))
 expect(Player.instances[0].pause).toHaveBeenCalledOnce()
 expect(URL.revokeObjectURL).toHaveBeenCalledExactlyOnceWith('blob:voice-status')
})

it('rechecks trust after asynchronous audio conversion before creating a speech job', async () => {
 let decode!: (value: { duration: number }) => void
 vi.stubGlobal('AudioContext', class { decodeAudioData = () => new Promise(resolve => { decode = resolve }); close = async () => {} })
 render(<Harness/> ); await ready()
 await act(async () => { await voice.start() })
 act(() => voice.endAndSend())
 await waitFor(() => expect(decode).toBeDefined())
 await refreshFailure()
 await act(async () => decode({ duration: 1 }))
 await waitFor(() => expect(voice.phase).not.toBe('transcribing'))
 expect(uploads).toHaveLength(0)
 expect(transcript).not.toHaveBeenCalled()
})

it('rechecks trust after asynchronous content hashing before generating speech', async () => {
 let digest!: (value: ArrayBuffer) => void
 vi.stubGlobal('crypto', { subtle: { digest: () => new Promise(resolve => { digest = resolve }) } })
 render(<Harness/> ); await ready()
 let pending!: Promise<void>
 act(() => { pending = voice.speak('one', 0, '回答') })
 await refreshFailure()
 await act(async () => { digest(new Uint8Array(32).buffer); await pending })
 expect(uploads).toHaveLength(0)
 expect(voice.playback).toBe('idle')
})

it('releases a permission result granted after configuration became untrusted', async () => {
 let grant!: (stream: unknown) => void
 navigator.mediaDevices.getUserMedia = vi.fn(() => new Promise(done => { grant = done })) as typeof navigator.mediaDevices.getUserMedia
 render(<Harness/> ); await ready()
 let pending!: Promise<void>
 act(() => { pending = voice.start() })
 await refreshFailure()
 await act(async () => { grant({ getTracks: () => [{ stop: stopTrack }] }); await pending })
 expect(voice.phase).toBe('idle')
 expect(stopTrack).toHaveBeenCalledOnce()
 expect(uploads).toHaveLength(0)
})

it('rejects speech when the hook is disabled even if called directly', async () => {
 render(<Harness enabled={false}/> )
 await act(async () => { await voice.start(); await voice.speak('one', 0, '回答'); voice.setAuto(true); voice.onFinal('one', 1, '自动回答') })
 expect(navigator.mediaDevices.getUserMedia).not.toHaveBeenCalled()
 expect(uploads).toHaveLength(0)
})

it('preserves generated audio when an existing job completes after status failure and waits for trusted manual playback', async () => {
 let finish!: (response: Response) => void
 const original = vi.mocked(fetch).getMockImplementation()!
 vi.mocked(fetch).mockImplementation(async (path, options) => {
  if (String(path).endsWith('/speech/jobs')) { uploads.push(JSON.parse(options!.body as string)); return new Promise(resolve => { finish = resolve }) }
  return original(path, options)
 })
 render(<Harness/> ); await ready()
 let pending!: Promise<void>
 act(() => { pending = voice.speak('one', 0, '回答') })
 await waitFor(() => expect(finish).toBeDefined())
 await refreshFailure()
 await act(async () => { finish(Response.json({ id: 'speech', status: 'succeeded', mime_type: 'audio/wav', audio_base64: 'UklGRg==' })); await pending })
 expect(voice.playback).toBe('ready')
 expect(screen.getByRole('button', { name: '播放已生成语音' })).toBeDisabled()
 await act(async () => { await voice.resumePlayback() })
 expect(Player.instances[0].play).not.toHaveBeenCalled()
 statusResponse = async () => Response.json(configured)
 fireEvent.click(screen.getByRole('button', { name: '重新读取' }))
 await waitFor(() => expect(screen.getByRole('button', { name: '播放已生成语音' })).toBeEnabled())
 fireEvent.click(screen.getByRole('button', { name: '播放已生成语音' }))
 await waitFor(() => expect(voice.playback).toBe('playing'))
 expect(uploads).toHaveLength(1)
})

it('pauses enabled automatic reading on failure and resumes only for future answers after recovery', async () => {
 render(<Harness/> ); await ready()
 fireEvent.click(screen.getByLabelText('自动朗读新回答'))
 await refreshFailure()
 await act(async () => voice.onFinal('one', 0, '失败期间的回答'))
 expect(voice.playback).toBe('idle')
 expect(uploads).toHaveLength(0)
 expect(screen.getByLabelText('自动朗读新回答')).toBeEnabled()
 fireEvent.click(screen.getByLabelText('自动朗读新回答'))
 expect(voice.auto).toBe(false)
 statusResponse = async () => Response.json(configured)
 fireEvent.click(screen.getByRole('button', { name: '重新读取' }))
 await waitFor(() => expect(voice.status.error).toBeNull())
 expect(uploads).toHaveLength(0)
 fireEvent.click(screen.getByLabelText('自动朗读新回答'))
 await act(async () => voice.onFinal('one', 1, '恢复后的回答'))
 await waitFor(() => expect(voice.playback).toBe('playing'))
 expect(uploads).toHaveLength(1)
 expect(uploads[0]).toMatchObject({ kind: 'synthesize', message_index: 1 })
})

it('keeps active capture during background status loading and releases it on explicit cancel after failure', async () => {
 render(<Harness/> ); await ready()
 await act(async () => { await voice.start() })
 let resolve!: (response: Response) => void
 statusResponse = () => new Promise(done => { resolve = done })
 act(() => document.dispatchEvent(new Event('visibilitychange')))
 await waitFor(() => expect(voice.status.loading).toBe(true))
 expect(voice.phase).toBe('recording')
 expect(screen.getByRole('button', { name: '结束并发送' })).toBeEnabled()
 expect(stopTrack).not.toHaveBeenCalled()
 await act(async () => resolve(Response.json({ error: 'speech_unavailable' }, { status: 503 })))
 expect(voice.phase).toBe('recording')
 expect(stopTrack).not.toHaveBeenCalled()
 fireEvent.click(screen.getByRole('button', { name: '取消录音' }))
 expect(voice.phase).toBe('idle')
 expect(stopTrack).toHaveBeenCalledOnce()
 expect(uploads).toHaveLength(0)
})
