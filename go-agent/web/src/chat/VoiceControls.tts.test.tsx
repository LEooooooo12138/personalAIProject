import { render, screen, within } from '@testing-library/react'
import { expect, it, vi } from 'vitest'
import type { SpeechStatus } from '../api'
import { ApiError, errorText } from '../api'
import type { useVoice } from './useVoice'
import VoiceControls from './VoiceControls'

function show(overrides: Partial<SpeechStatus> = {}, error = '') {
  const data: SpeechStatus = { asr_mode: 'local', tts_mode: 'cloud', cloud_configured: true, local_asr_configured: true, local_tts_configured: false, ...overrides }
  const voice = { status: { data, error: null, loading: false, reload: vi.fn() }, available: true, phase: 'idle', playback: 'idle', auto: false, error, recordedSeconds: 0, maxRecordingSeconds: 60, start: vi.fn(), endAndSend: vi.fn(), sendReady: vi.fn(), cancel: vi.fn(), setAuto: vi.fn(), resumePlayback: vi.fn() } as unknown as ReturnType<typeof useVoice>
  render(<VoiceControls voice={voice} disabled={false}/>)
  return within(screen.getByText('回答朗读').parentElement!)
}
it('identifies the builtin voice with backward compatible defaults without claiming readiness', () => {
  const reading = show()
  expect(reading.getByText(/MiMo 内置音色 · 冰糖/)).toBeInTheDocument()
  expect(reading.queryByText(/连接成功|已就绪/)).toBeNull()
})
it('reports missing clone audio and discloses that cloning sends the sample and text', () => {
  const reading = show({ tts_model: 'mimo-v2.5-tts-voiceclone', voice_reference_configured: false })
  expect(reading.getByText(/MiMo 克隆音色 · 参考音频未配置/)).toBeInTheDocument()
  expect(reading.getByText(/参考音频和本次朗读文字会发送到 MiMo/)).toBeInTheDocument()
})
it('does not imply that cloud clone settings apply to local playback', () => {
  const reading = show({ tts_mode: 'local', local_tts_configured: true, tts_model: 'mimo-v2.5-tts-voiceclone', voice_reference_configured: false })
  expect(reading.queryByText(/MiMo 克隆|参考音频未配置|发送到 MiMo/)).toBeNull()
})
it('explains the missing reference error without falling back to another voice', () => {
  show({}, errorText(new ApiError(409, 'voice_reference_required')))
  expect(screen.getByRole('alert')).toHaveTextContent('克隆朗读需要参考音频')
  expect(screen.getByRole('alert')).toHaveTextContent('助手设置')
})
it('explains the server MP3 decoder limitation with an available file format', () => {
  show({}, errorText(new ApiError(503, 'voice_reference_decoder_unavailable')))
  expect(screen.getByRole('alert')).toHaveTextContent('PCM16 WAV')
})

it.each(['cloud', 'hybrid'] as const)('explains local-only replies when %s TTS has no local service', (tts_mode) => {
 const reading=show({tts_mode,local_tts_configured:false})
 expect(reading.getByText(/设备、知识及部分历史回复仍需本地朗读/)).toBeInTheDocument()
 expect(reading.getByText(/当前未配置本地朗读服务，这些回复暂时无法朗读/)).toBeInTheDocument()
})
it('does not warn about missing local service when it is configured', () => {
 const reading=show({tts_mode:'cloud',local_tts_configured:true})
 expect(reading.getByText(/设备、知识及部分历史回复仍需本地朗读/)).toBeInTheDocument()
 expect(reading.queryByText(/这些回复暂时无法朗读/)).toBeNull()
})
it.each([
 ['speech_local_required','此回复需本地朗读','本地语音服务尚未配置'],
 ['speech_local_unconfigured','当前选择本地朗读','本地语音服务尚未配置'],
])('explains %s as a local configuration requirement', (code,reason,configuration) => {
 show({},errorText(new ApiError(409,code)))
 expect(screen.getByRole('alert')).toHaveTextContent(reason)
 expect(screen.getByRole('alert')).toHaveTextContent(configuration)
 expect(screen.getByRole('alert')).toHaveTextContent('助手设置')
})
