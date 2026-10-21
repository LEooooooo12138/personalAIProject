import { expect, test, type Page, type TestInfo } from '@playwright/test'
import fs from 'node:fs/promises'
import path from 'node:path'

type ReferenceMetadata = { exists: boolean; size_bytes: number; updated_at: string | null; revision: string }
const api = '/api/console/v1'

// Synthetic PCM16 tones are local fixtures, never a person's voice or a cloud probe.
function pcm16Wav(samples: number, frequency: number) {
  const buffer = Buffer.alloc(44 + samples * 2)
  buffer.write('RIFF', 0); buffer.writeUInt32LE(buffer.length - 8, 4); buffer.write('WAVE', 8)
  buffer.write('fmt ', 12); buffer.writeUInt32LE(16, 16); buffer.writeUInt16LE(1, 20)
  buffer.writeUInt16LE(1, 22); buffer.writeUInt32LE(16000, 24); buffer.writeUInt32LE(32000, 28)
  buffer.writeUInt16LE(2, 32); buffer.writeUInt16LE(16, 34)
  buffer.write('data', 36); buffer.writeUInt32LE(samples * 2, 40)
  for (let i = 0; i < samples; i++) buffer.writeInt16LE(Math.round(Math.sin(i * 2 * Math.PI * frequency / 16000) * 1000), 44 + i * 2)
  return buffer
}

async function readReference(page: Page) {
  const response = await page.request.get(`${api}/admin/tts-reference`)
  expect(response.status()).toBe(200)
  const metadata = await response.json() as ReferenceMetadata
  expect(Object.keys(metadata).sort()).toEqual(['exists', 'revision', 'size_bytes', 'updated_at'])
  return metadata
}

async function capture(page: Page, testInfo: TestInfo, name: string) {
  const outputDir = process.env.CONSOLE_TTS_EVIDENCE_DIR || testInfo.outputDir
  await fs.mkdir(outputDir, { recursive: true })
  const file = path.join(outputDir, `${name}.png`)
  await page.screenshot({ path: file, fullPage: true })
  await testInfo.attach(name, { path: file, contentType: 'image/png' })
}

test('real Chrome saves MiMo voices and manages reference audio through Go handlers', async ({ page }, testInfo) => {
  await page.setViewportSize({ width: 1280, height: 960 })
  await page.goto('/app/')
  await page.getByLabel('用户名').fill('owner')
  await page.getByLabel('密码', { exact: true }).fill('fixture-password-123')
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await expect(page.getByRole('heading', { name: '家庭概况' })).toBeVisible()
  await page.goto('/app/admin/assistant')
  await expect(page.getByText('模拟预览 · 当前设备和自动化均为测试数据')).toBeVisible()
  await expect(page.getByLabel('MiMo 朗读模型')).toHaveValue('mimo-v2.5-tts')
  await expect(page.getByLabel('MiMo 内置音色')).toHaveValue('冰糖')
  const missing = await readReference(page)
  expect(missing).toMatchObject({ exists: false, size_bytes: 0 })

  await page.getByLabel('TTS 模式', { exact: true }).selectOption('cloud')
  await page.getByLabel('MiMo 内置音色').selectOption('Mia')
  await page.getByRole('button', { name: '保存服务配置', exact: true }).click()
  await expect(page.getByText('服务配置已保存。', { exact: true })).toBeVisible()
  const savedVoice = await page.request.get(`${api}/admin/assistant`)
  expect(savedVoice.status()).toBe(200)
  expect(await savedVoice.json()).toMatchObject({ tts_mode: 'cloud', tts_model: 'mimo-v2.5-tts', tts_voice: 'Mia', key_configured: false })
  await page.reload()
  await expect(page.getByLabel('MiMo 内置音色')).toHaveValue('Mia')
  await expect(page.getByLabel('TTS 模式', { exact: true })).toHaveValue('cloud')
  await page.getByLabel('MiMo 朗读模型').selectOption('mimo-v2.5-tts-voiceclone')
  await expect(page.getByText(/使用 MiMo 克隆朗读前，请先上传参考音频/)).toBeVisible()
  await expect(page.getByLabel('MiMo 内置音色')).toBeDisabled()
  await page.getByRole('button', { name: '保存服务配置', exact: true }).click()
  await expect(page.getByText('服务配置已保存。', { exact: true })).toBeVisible()
  const savedClone = await page.request.get(`${api}/admin/assistant`)
  expect(savedClone.status()).toBe(200)
  expect(await savedClone.json()).toMatchObject({ tts_model: 'mimo-v2.5-tts-voiceclone', tts_voice: 'Mia' })
  await page.reload()
  await expect(page.getByLabel('MiMo 朗读模型')).toHaveValue('mimo-v2.5-tts-voiceclone')
  await expect(page.getByText(/使用 MiMo 克隆朗读前，请先上传参考音频/)).toBeVisible()
  await capture(page, testInfo, 'tts-clone-reference-missing')

  const sample = pcm16Wav(1600, 330)
  await page.getByLabel('克隆参考音频', { exact: true }).setInputFiles({ name: 'synthetic-reference.wav', mimeType: 'audio/wav', buffer: sample })
  await page.getByRole('button', { name: '上传参考音频', exact: true }).click()
  await expect(page.getByText('参考音频已保存。上传未调用 MiMo。', { exact: true })).toBeVisible()
  const first = await readReference(page)
  expect(first).toMatchObject({ exists: true, size_bytes: sample.length })
  expect(first.updated_at).toBeTruthy()
  expect(first.revision).not.toBe(missing.revision)
  await page.reload()
  await expect(page.getByText(`参考音频：已配置 · ${sample.length} 字节`, { exact: true })).toBeVisible()
  await expect(page.getByText(/使用 MiMo 克隆朗读前，请先上传参考音频/)).toHaveCount(0)
  await capture(page, testInfo, 'tts-clone-reference-saved')

  const replacement = pcm16Wav(3200, 440)
  await page.getByLabel('克隆参考音频', { exact: true }).setInputFiles({ name: 'synthetic-replacement.wav', mimeType: 'audio/wav', buffer: replacement })
  await page.getByRole('button', { name: '替换参考音频', exact: true }).click()
  await expect(page.getByText('参考音频已保存。上传未调用 MiMo。', { exact: true })).toBeVisible()
  const second = await readReference(page)
  expect(second).toMatchObject({ exists: true, size_bytes: replacement.length })
  expect(second.revision).not.toBe(first.revision)
  await expect(page.getByText(`参考音频：已配置 · ${replacement.length} 字节`, { exact: true })).toBeVisible()

  await page.getByRole('button', { name: '移除参考音频', exact: true }).click()
  await expect(page.getByText('参考音频已移除。', { exact: true })).toBeVisible()
  const last = await readReference(page)
  expect(last).toMatchObject({ exists: false, size_bytes: 0 })
  expect(last.revision).not.toBe(second.revision)
  await page.reload()
  await expect(page.getByText('参考音频：尚未上传', { exact: true })).toBeVisible()
  await expect(page.getByText(/使用 MiMo 克隆朗读前，请先上传参考音频/)).toBeVisible()
  await expect(page.getByRole('button', { name: '移除参考音频', exact: true })).toHaveCount(0)
  const status = await page.request.get(`${api}/speech/status`)
  expect(status.status()).toBe(200)
  expect(await status.json()).toMatchObject({ tts_model: 'mimo-v2.5-tts-voiceclone', tts_voice: 'Mia', voice_reference_configured: false, cloud_configured: false })
  await capture(page, testInfo, 'tts-clone-reference-removed')
  await testInfo.attach('reference-metadata-revisions', { body: JSON.stringify({ missing, uploaded: first, replaced: second, removed: last }, null, 2), contentType: 'application/json' })
})
