import { afterEach, expect, it, vi } from 'vitest'
import { encodeWav, recordingToWav } from './audio'

afterEach(() => vi.unstubAllGlobals())
it('encodes mono 16k PCM16 with correct lengths and clamped signed samples', () => {
  const wav = encodeWav(new Float32Array([-2, 0, 2]))
  const bytes = new Uint8Array(wav)
  const view = new DataView(wav)
  expect(new TextDecoder().decode(bytes.subarray(0, 4))).toBe('RIFF')
  expect(view.getUint32(4, true)).toBe(42)
  expect(view.getUint16(20, true)).toBe(1)
  expect(view.getUint16(22, true)).toBe(1)
  expect(view.getUint32(24, true)).toBe(16000)
  expect(view.getUint16(34, true)).toBe(16)
  expect(view.getUint32(40, true)).toBe(6)
  expect([view.getInt16(44, true), view.getInt16(46, true), view.getInt16(48, true)]).toEqual([-32768, 0, 32767])
})
it('rejects more than sixty seconds of PCM', () => {
  expect(() => encodeWav(new Float32Array(960001))).toThrow(/60/)
})
it('closes the decode context and ignores a decode result after cancellation', async () => {
  let finish!: (buffer: unknown) => void
  const close = vi.fn(async () => {})
  let active = true
  const decode = vi.fn(() => new Promise((resolve) => { finish = resolve }))
  vi.stubGlobal('AudioContext', class { decodeAudioData = decode; close = close })
  const offline = vi.fn()
  vi.stubGlobal('OfflineAudioContext', offline)
  const task = recordingToWav(new Blob(['recorded']), () => active)
  await vi.waitFor(() => expect(decode).toHaveBeenCalled())
  active = false
  finish({ duration: 1 })
  await expect(task).rejects.toMatchObject({ name: 'AbortError' })
  expect(close).toHaveBeenCalledOnce()
  expect(offline).not.toHaveBeenCalled()
})
it('trims recording packet padding to sixty seconds before rendering and encoding', async () => {
  const close = vi.fn(async () => {})
  const sizes: number[] = []
  vi.stubGlobal('AudioContext', class { decodeAudioData = async () => ({ duration: 60.05 }); close = close })
  vi.stubGlobal('OfflineAudioContext', class {
    destination = {}
    constructor(channels: number, frames: number, rate: number) { expect(channels).toBe(1); expect(rate).toBe(16000); sizes.push(frames) }
    createBufferSource = () => ({ buffer: null, connect: () => {}, start: () => {} })
    startRendering = async () => ({ getChannelData: () => new Float32Array(sizes[0]) })
  })
  const wav = await recordingToWav(new Blob(['recorded']), () => true)
  expect(sizes).toEqual([960000])
  expect(wav.size).toBe(1920044)
  expect(wav.type).toBe('audio/wav')
  expect(close).toHaveBeenCalledOnce()
})
