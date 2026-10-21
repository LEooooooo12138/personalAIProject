const RATE = 16000
export function encodeWav(samples: Float32Array): ArrayBuffer {
  if (!samples.length || samples.length > RATE * 60) throw new Error('录音必须为 0 至 60 秒。')
  const buffer = new ArrayBuffer(44 + samples.length * 2)
  const view = new DataView(buffer)
  const text = (offset: number, value: string) => { for (let i = 0; i < value.length; i++) view.setUint8(offset + i, value.charCodeAt(i)) }
  text(0, 'RIFF'); view.setUint32(4, 36 + samples.length * 2, true); text(8, 'WAVE')
  text(12, 'fmt '); view.setUint32(16, 16, true); view.setUint16(20, 1, true); view.setUint16(22, 1, true)
  view.setUint32(24, RATE, true); view.setUint32(28, RATE * 2, true); view.setUint16(32, 2, true); view.setUint16(34, 16, true)
  text(36, 'data'); view.setUint32(40, samples.length * 2, true)
  samples.forEach((sample, index) => { const value = Math.max(-1, Math.min(1, sample)); view.setInt16(44 + index * 2, value < 0 ? value * 32768 : value * 32767, true) })
  return buffer
}
function readBuffer(blob: Blob): Promise<ArrayBuffer> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(reader.result as ArrayBuffer)
    reader.onerror = () => reject(new Error('无法读取录音。'))
    reader.readAsArrayBuffer(blob)
  })
}
export async function recordingToWav(blob: Blob, active: () => boolean): Promise<Blob> {
  const check = () => { if (!active()) throw new DOMException('Cancelled', 'AbortError') }
  check()
  if (blob.size > 8 * 1024 * 1024) throw new Error('录音不能超过 8 MiB。')
  if (typeof AudioContext === 'undefined' || typeof OfflineAudioContext === 'undefined') throw new Error('浏览器不支持音频转换，请更换支持 Web Audio 的浏览器。')
  const data = await readBuffer(blob)
  check()
  const context = new AudioContext()
  try {
    const decoded = await context.decodeAudioData(data)
    check()
    if (!Number.isFinite(decoded.duration) || decoded.duration <= 0 || decoded.duration > 65) throw new Error('录音时长超过 60 秒或音频无效，请重新录制。')
    // Recorder timers and encoded packets can finish slightly after the limit.
    // Render only the first sixty seconds; never upload those trailing frames.
    const offline = new OfflineAudioContext(1, Math.min(60 * RATE, Math.ceil(decoded.duration * RATE)), RATE)
    const source = offline.createBufferSource()
    source.buffer = decoded; source.connect(offline.destination); source.start()
    const rendered = await offline.startRendering()
    check()
    return new Blob([encodeWav(rendered.getChannelData(0))], { type: 'audio/wav' })
  } catch (cause) {
    if (cause instanceof DOMException && cause.name === 'AbortError') throw cause
    if (cause instanceof Error && cause.message.includes('60 秒')) throw cause
    throw new Error('浏览器无法转换此录音，请更换支持 Web Audio 的浏览器或录音格式。')
  } finally { await context.close().catch(() => {}) }
}
