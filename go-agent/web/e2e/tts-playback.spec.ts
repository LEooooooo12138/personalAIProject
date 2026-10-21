import { expect, test } from '@playwright/test'
import { createServer } from 'node:http'
import type { AddressInfo } from 'node:net'
import fs from 'node:fs/promises'
import path from 'node:path'

function sampleWav() {
  const samples = 24000
  const wav = Buffer.alloc(44 + samples * 2)
  wav.write('RIFF'); wav.writeUInt32LE(wav.length - 8, 4); wav.write('WAVEfmt ', 8)
  wav.writeUInt32LE(16, 16); wav.writeUInt16LE(1, 20); wav.writeUInt16LE(1, 22)
  wav.writeUInt32LE(24000, 24); wav.writeUInt32LE(48000, 28); wav.writeUInt16LE(2, 32); wav.writeUInt16LE(16, 34)
  wav.write('data', 36); wav.writeUInt32LE(samples * 2, 40)
  for (let i = 0; i < samples; i++) wav.writeInt16LE(Math.round(Math.sin(i * 2 * Math.PI * 330 / 24000) * 500), 44 + i * 2)
  return wav
}
type MediaEvent = { type: string; duration?: number; time?: number; code?: number; name?: string; directive?: string }

test('saved fixture reply plays generated PCM audio through real speech jobs and native browser Audio', async ({ page }, testInfo) => {
  let localCalls = 0
  const wav = sampleWav()
  const speech = createServer(async (request, response) => {
    const chunks: Buffer[] = []
    for await (const chunk of request) chunks.push(Buffer.from(chunk))
    const body = JSON.parse(Buffer.concat(chunks).toString())
    if (request.method !== 'POST' || request.url !== '/v1/audio/speech' || body.model !== 'tts-1' || body.voice !== 'alloy' || body.response_format !== 'wav') {
      response.writeHead(400); response.end(); return
    }
    localCalls++
    response.setHeader('Content-Type', 'audio/wav'); response.end(wav)
  })
  await new Promise<void>(resolve => speech.listen(0, '127.0.0.1', resolve))
  await page.addInitScript(() => {
    const events: MediaEvent[] = []
    Object.defineProperty(window, '__voiceMediaEvents', { value: events })
    document.addEventListener('securitypolicyviolation', event => { if (event.effectiveDirective === 'media-src') events.push({ type: 'csp', directive: event.effectiveDirective }) })
    // Observe the original native Audio and its original play promise; do not mock playback.
    window.Audio = new Proxy(window.Audio, { construct(Target, args) {
      const audio = Reflect.construct(Target, args) as HTMLAudioElement
      audio.addEventListener('ended', () => events.push({ type: 'ended', duration: audio.duration, time: audio.currentTime }))
      audio.addEventListener('playing', () => events.push({ type: 'playing', duration: audio.duration, time: audio.currentTime }))
      audio.addEventListener('error', () => events.push({ type: 'error', code: audio.error?.code }))
      const originalPlay = audio.play.bind(audio)
      audio.play = () => {
        const promise = originalPlay()
        promise.then(() => events.push({ type: 'play-resolved', duration: audio.duration }), error => events.push({ type: 'play-rejected', name: error.name }))
        return promise
      }
      return audio
    } })
  })
  try {
    await page.goto('/app/')
    await page.getByLabel('用户名').fill('owner')
    await page.getByLabel('密码', { exact: true }).fill('fixture-password-123')
    await page.getByRole('button', { name: '登录', exact: true }).click()
    await expect(page.getByRole('heading', { name: '家庭概况' })).toBeVisible()
    const actor = await (await page.request.get('/api/console/v1/auth/me')).json()
    const old = await (await page.request.get('/api/console/v1/admin/assistant')).json()
    const save = await page.request.put('/api/console/v1/admin/assistant', { headers: { Origin: new URL(page.url()).origin, 'X-CSRF-Token': actor.csrf_token }, data: {
      expected_revision: old.revision, llm_mode: 'local', asr_mode: 'local', tts_mode: 'local', local_asr_url: '',
      local_tts_url: `http://127.0.0.1:${(speech.address() as AddressInfo).port}/v1/audio/speech`,
    } })
    expect(save.status()).toBe(200)
    expect((await save.json()).key_configured).toBe(false)
    await page.goto('/app/chat')
    await page.getByLabel('消息内容').fill('浏览器合成语音播放测试')
    await page.getByRole('button', { name: '发送消息', exact: true }).click()
    await expect(page.getByRole('log', { name: '对话消息' }).getByText('离线测试回复')).toBeVisible()
    await page.getByRole('button', { name: '朗读回答', exact: true }).click()
    await expect.poll(() => page.evaluate(() => (window as unknown as { __voiceMediaEvents: MediaEvent[] }).__voiceMediaEvents.some(event => event.type === 'ended'))).toBe(true)
    const events = await page.evaluate(() => (window as unknown as { __voiceMediaEvents: MediaEvent[] }).__voiceMediaEvents)
    expect(localCalls).toBe(1)
    expect(events).toContainEqual({ type: 'play-resolved', duration: 1 })
    expect(events).toContainEqual({ type: 'ended', duration: 1, time: 1 })
    expect(events.some(event => ['csp', 'error', 'play-rejected'].includes(event.type))).toBe(false)
    await expect(page.getByText('无法播放返回的音频。', { exact: true })).toHaveCount(0)
    await expect(page.getByRole('button', { name: '停止朗读', exact: true })).toHaveCount(0)
  } finally {
    const events = await page.evaluate(() => (window as unknown as { __voiceMediaEvents?: MediaEvent[] }).__voiceMediaEvents ?? []).catch(() => [])
    const outputDir = process.env.CONSOLE_TTS_EVIDENCE_DIR || testInfo.outputDir
    await fs.mkdir(outputDir, { recursive: true })
    const reportPath = path.join(outputDir, 'tts-native-playback.json')
    await fs.writeFile(reportPath, JSON.stringify({ localCalls, events }, null, 2))
    await testInfo.attach('native-audio-events', { path: reportPath, contentType: 'application/json' })
    speech.closeAllConnections()
    await new Promise<void>((resolve, reject) => speech.close(error => error ? reject(error) : resolve()))
  }
})
