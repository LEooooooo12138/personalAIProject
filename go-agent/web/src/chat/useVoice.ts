import { useCallback, useEffect, useRef, useState } from 'react'
import { ApiError, errorText, requestConsole, type SpeechJob, type SpeechStatus } from '../api'
import { useAuth } from '../auth'
import { useFamilyQuery } from '../familyQuery'
import { recordingToWav } from './audio'

type CapturePhase = 'idle' | 'permission' | 'recording' | 'ready' | 'transcribing'
const abortError = () => new DOMException('Cancelled', 'AbortError')
function delay(signal: AbortSignal) {
  return new Promise<void>((resolve, reject) => {
    if (signal.aborted) { reject(abortError()); return }
    const abort = () => { clearTimeout(timer); reject(abortError()) }
    const timer = setTimeout(() => { signal.removeEventListener('abort', abort); resolve() }, 1000)
    signal.addEventListener('abort', abort, { once: true })
  })
}
function base64(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result).split(',')[1])
    reader.onerror = () => reject(new Error('audio_read_failed'))
    reader.readAsDataURL(blob)
  })
}

export function useVoice(sessionId: string | null, selection: number, enabled: boolean, onTranscript: (text: string) => void) {
  const auth = useAuth()
  const current = useRef({ auth, sessionId, onTranscript })
  current.current = { auth, sessionId, onTranscript }
  const status = useFamilyQuery<SpeechStatus>('/speech/status', enabled)
  // Keep a confirmed configuration during background reads, but never trust a failed read.
  const available = enabled && status.data !== null && !status.error
  const availableRef = useRef(available)
  availableRef.current = available
  const [phase, setPhase] = useState<CapturePhase>('idle')
  const [error, setError] = useState('')
  const [recordedSeconds, setRecordedSeconds] = useState(0)
  const recordingStarted = useRef<number | null>(null)
  const recordingClock = useRef<ReturnType<typeof setInterval> | null>(null)
  const [auto, setAutoState] = useState(false)
  const autoRef = useRef(false)
  const [playback, setPlayback] = useState<'idle' | 'loading' | 'playing' | 'ready'>('idle')
  const generation = useRef(0)
  const mounted = useRef(true)
  const recorder = useRef<MediaRecorder | null>(null)
  const stream = useRef<MediaStream | null>(null)
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const audioBlob = useRef<Blob | null>(null)
  const operation = useRef<{ cancel: () => void } | null>(null)
  const audio = useRef<HTMLAudioElement | null>(null)
  const audioURL = useRef<string | null>(null)
  const releaseTracks = useCallback(() => {
    if (timer.current) clearTimeout(timer.current)
    timer.current = null
    if (recordingClock.current !== null) clearInterval(recordingClock.current)
    recordingClock.current = null
    stream.current?.getTracks().forEach((track) => track.stop())
    stream.current = null
  }, [])
  const stopAudio = useCallback(() => {
    const player = audio.current
    if (player) { player.onended = null; player.onerror = null; player.pause(); player.removeAttribute('src') }
    audio.current = null
    if (audioURL.current) URL.revokeObjectURL(audioURL.current)
    audioURL.current = null
  }, [])
  const cancel = useCallback(() => {
    generation.current++
    const capture = recorder.current
    recorder.current = null
    if (capture) { capture.onstop = null; capture.ondataavailable = null; capture.onerror = null; if (capture.state !== 'inactive') capture.stop() }
    releaseTracks(); audioBlob.current = null
    const task = operation.current
    operation.current = null
    task?.cancel(); stopAudio()
    if (mounted.current) { setPhase('idle'); setPlayback('idle'); setRecordedSeconds(0); recordingStarted.current = null }
  }, [releaseTracks, stopAudio])
  useEffect(() => {
    mounted.current = true
    const signal = auth.signal
    cancel(); setError('')
    signal.addEventListener('abort', cancel, { once: true })
    return () => { mounted.current = false; cancel(); signal.removeEventListener('abort', cancel) }
  }, [auth.epoch, auth.signal, selection, cancel])

  const active = (token: number) => mounted.current && token === generation.current && !current.current.auth.signal.aborted
  function failure(cause: unknown, token: number) {
    if (!active(token)) return
    if (cause instanceof ApiError && cause.status === 401) current.current.auth.expire()
    else setError(errorText(cause))
  }
  async function job(body: object, token: number) {
    const actor = current.current.auth.identity
    if (!actor || !active(token) || !availableRef.current) throw abortError()
    const controller = new AbortController()
    const authSignal = current.current.auth.signal
    const signal = AbortSignal.any([controller.signal, authSignal])
    const expected = { userId: actor.user.id, csrfToken: actor.csrf_token }
    let id: string | null = null
    const cancelRemote = () => {
      if (id && !authSignal.aborted) void requestConsole(`/speech/jobs/${encodeURIComponent(id)}/cancel`, { method: 'POST', body: {}, csrfToken: actor.csrf_token, expected, signal: authSignal }).catch(() => {})
    }
    const task = { cancel: () => { controller.abort(); cancelRemote() } }
    operation.current = task
    try {
      let result = await requestConsole<SpeechJob>('/speech/jobs', { method: 'POST', body, csrfToken: actor.csrf_token, expected, signal })
      id = result.id
      if (!active(token) || signal.aborted) { cancelRemote(); throw abortError() }
      while (result.status === 'queued' || result.status === 'running') {
        await delay(signal)
        result = await requestConsole<SpeechJob>(`/speech/jobs/${encodeURIComponent(id)}`, { signal, expected })
        if (!active(token) || signal.aborted) throw abortError()
      }
      if (result.status !== 'succeeded') throw new ApiError(502, result.error_code ?? 'speech_unavailable')
      return result
    } finally { if (operation.current === task) operation.current = null }
  }
  async function transcribe(blob: Blob, token: number) {
    if (!active(token) || !availableRef.current) return
    setPhase('transcribing'); setError('')
    const sid = current.current.sessionId
    try {
      let wav: Blob
      try { wav = await recordingToWav(blob, () => active(token)) }
      catch (cause) { if (active(token)) { setError(cause instanceof Error ? cause.message : '录音转换失败。'); setPhase('idle'); audioBlob.current = null }; return }
      const encoded = await base64(wav)
      if (!active(token)) return
      if (!availableRef.current) { setPhase('ready'); return }
      const result = await job({ kind: 'transcribe', audio_base64: encoded, mime_type: wav.type, ...(sid ? { session_id: sid } : {}) }, token)
      if (active(token)) {
        audioBlob.current = null; setPhase('idle')
        if (result.text?.trim()) current.current.onTranscript(result.text)
        else setError('没有识别到文字，请重新录音。')
      }
    } catch (cause) { failure(cause, token); if (active(token)) { audioBlob.current = null; setPhase('idle') } }
  }
  function stopCapture(send: boolean) {
    if (send && !availableRef.current) return
    const capture = recorder.current
    if (!capture || capture.state === 'inactive') return
    const original = capture.onstop
    const token = generation.current
    capture.onstop = (event) => { original?.call(capture, event); const blob = audioBlob.current; if (send && blob && active(token)) void transcribe(blob, token) }
    if (recordingStarted.current !== null) setRecordedSeconds(Math.min(60, Math.floor((performance.now() - recordingStarted.current) / 1000)))
    capture.stop(); releaseTracks()
  }
  async function start() {
    if (!availableRef.current || (phase !== 'idle' && phase !== 'ready')) return
    cancel(); setError('')
    const token = generation.current
    if (!navigator.mediaDevices?.getUserMedia || typeof MediaRecorder === 'undefined') { setError('当前浏览器不支持录音，请使用 HTTPS 或 localhost 下的受支持浏览器。'); return }
    const mime = ['audio/webm;codecs=opus', 'audio/ogg;codecs=opus', 'audio/mp4'].find((value) => MediaRecorder.isTypeSupported(value))
    if (!mime) { setError('当前浏览器没有支持的录音格式。'); return }
    setPhase('permission')
    try {
      const media = await navigator.mediaDevices.getUserMedia({ audio: true })
      if (!active(token)) { media.getTracks().forEach((track) => track.stop()); return }
      if (!availableRef.current) { media.getTracks().forEach((track) => track.stop()); setPhase('idle'); return }
      stream.current = media
      const capture = new MediaRecorder(media, { mimeType: mime })
      recorder.current = capture
      const chunks: Blob[] = []
      let size = 0
      capture.ondataavailable = (event) => {
        if (!active(token)) return
        size += event.data.size
        if (size > 8 * 1024 * 1024) { cancel(); setError('录音不能超过 8 MiB，请缩短录音。'); return }
        if (event.data.size) chunks.push(event.data)
      }
      capture.onstop = () => {
        releaseTracks(); recorder.current = null
        if (!active(token)) return
        const blob = new Blob(chunks, { type: capture.mimeType || mime })
        if (!blob.size) { setPhase('idle'); setError('没有录到音频，请检查麦克风。'); return }
        audioBlob.current = blob; setPhase('ready')
      }
      capture.onerror = () => { if (active(token)) { cancel(); setError('录音失败，请检查麦克风后重试。') } }
      capture.start(1000)
      recordingStarted.current = performance.now(); setRecordedSeconds(0); setPhase('recording')
      recordingClock.current = setInterval(() => {
        if (active(token) && recordingStarted.current !== null) setRecordedSeconds(Math.min(60, Math.floor((performance.now() - recordingStarted.current) / 1000)))
      }, 250)
      timer.current = setTimeout(() => { if (active(token)) stopCapture(false) }, 60000)
    } catch { if (active(token)) { cancel(); setError('无法使用麦克风，请检查浏览器权限和设备连接。') } }
  }
  async function resumePlayback() {
    const player = audio.current
    if (!player || !availableRef.current) return
    try { await player.play(); if (audio.current === player) { setPlayback('playing'); setError('') } }
    catch (cause) {
      // onerror or cancellation may already have released this player.
      if (audio.current !== player) return
      if (cause instanceof DOMException && cause.name === 'NotAllowedError') {
        setPlayback('ready'); setError('浏览器阻止了自动播放，请点击播放已生成语音。')
      } else {
        stopAudio(); setPlayback('idle'); setError('无法播放返回的音频。')
      }
    }
  }
  async function speak(sid: string, index: number, content: string) {
    if (!availableRef.current || !content.trim() || phase === 'recording' || phase === 'permission' || phase === 'transcribing') return
    cancel(); setError(''); setPlayback('loading')
    const token = generation.current
    try {
      const bytes = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(content))
      if (!active(token)) return
      if (!availableRef.current) { setPlayback('idle'); return }
      const content_revision = Array.from(new Uint8Array(bytes), (value) => value.toString(16).padStart(2, '0')).join('')
      const result = await job({ kind: 'synthesize', session_id: sid, message_index: index, content_revision }, token)
      if (!active(token)) return
      if (!result.audio_base64 || result.audio_base64.length > 24 * 1024 * 1024 || !/^audio\/(wav|wave|x-wav|mpeg|mp3|ogg|webm|mp4)(;.*)?$/.test(result.mime_type ?? '')) throw new ApiError(502, 'invalid_audio')
      const audioBytes = Uint8Array.from(atob(result.audio_base64), (value) => value.charCodeAt(0))
      const url = URL.createObjectURL(new Blob([audioBytes], { type: result.mime_type }))
      audioURL.current = url
      const player = new Audio(url)
      audio.current = player
      player.onended = () => { if (audio.current === player) { stopAudio(); setPlayback('idle') } }
      player.onerror = () => { if (audio.current === player) { stopAudio(); setPlayback('idle'); setError('无法播放返回的音频。') } }
      if (!availableRef.current) { setPlayback('ready'); return }
      await resumePlayback()
    } catch (cause) { failure(cause, token); if (active(token)) setPlayback('idle') }
  }
  return {
    status, available, phase, error, auto, playback, start, cancel, recordedSeconds, maxRecordingSeconds: 60 as const,
    setAuto: (value: boolean) => { if (value && !availableRef.current) return; autoRef.current = value; setAutoState(value) },
    endAndSend: () => stopCapture(true),
    sendReady: () => { if (audioBlob.current && phase === 'ready') void transcribe(audioBlob.current, generation.current) },
    speak, resumePlayback,
    onFinal: (sid: string, index: number, content: string) => { if (autoRef.current) void speak(sid, index, content) },
  }
}
