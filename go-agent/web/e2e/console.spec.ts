import { expect, test, type Page } from '@playwright/test'
import { createServer } from 'node:http'
import type { AddressInfo } from 'node:net'

import fs from 'node:fs/promises'
import path from 'node:path'
const evidenceDir = path.resolve('../../docs/assets/frontend-redesign-2026-10-10')
async function capture(page: Page, name: string) { await fs.mkdir(evidenceDir, {recursive:true}); await page.screenshot({path:path.join(evidenceDir, name + '.png'), fullPage:true}) }
const password = 'fixture-password-123'
const unique = () => `member_${Date.now().toString(36)}`

async function login(page: Page, username: string, secret = password) {
  await page.goto('/app/')
  await expect(page.getByRole('main')).toHaveCount(1)
  await expect(page.getByRole('button', { name: '登录', exact: true })).toHaveAttribute('type', 'submit')
  await expect(page.getByLabel('密码', { exact: true })).toHaveAttribute('autocomplete', 'current-password')
  await page.getByLabel('用户名').fill(username)
  await page.getByLabel('密码', { exact: true }).fill(secret)
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await expect(page.getByRole('navigation', { name: '底部导航' }).or(page.getByRole('navigation', { name: '主导航' }))).toBeVisible()
  const response = await page.request.get('/api/console/v1/auth/me')
  expect(response.ok()).toBeTruthy()
  const identity = await response.json() as { csrf_token: string; user: { username: string } }
  expect(identity.user.username).toBe(username)
  return identity
}

async function send(page: Page, content: string) {
  await page.goto('/app/chat')
  await page.getByRole('textbox', { name: '消息内容' }).fill(content)
  await page.getByRole('button', { name: '发送消息' }).click()
  await expect(page.getByRole('log', { name: '对话消息' }).getByText(content)).toBeVisible()
  await expect(page.getByRole('log', { name: '对话消息' }).getByText('离线测试回复')).toBeVisible()
  await expect.poll(() => new URL(page.url()).searchParams.get('sid')).not.toBeNull()
  return new URL(page.url()).searchParams.get('sid')!
}

test('assistant settings save independent modes, reload and upload a fixed personality file', async ({ page }) => {
  await login(page, 'owner')
  await page.goto('/app/account')
  await page.getByRole('link', { name: '助手设置', exact: true }).click()
  await expect(page.getByRole('heading', { name: '助手设置', exact: true })).toBeVisible()
  await page.getByLabel('ASR 模式').selectOption('hybrid')
  await page.getByLabel('TTS 模式').selectOption('cloud')
  await page.getByRole('button', { name: '保存服务配置' }).click()
  await expect(page.getByText('服务配置已保存。')).toBeVisible()
  await expect(page.getByLabel('MiMo API Key')).toHaveValue('')
  await page.reload()
  await expect(page.getByLabel('LLM 模式')).toHaveValue('local')
  await expect(page.getByLabel('ASR 模式')).toHaveValue('hybrid')
  await expect(page.getByLabel('TTS 模式')).toHaveValue('cloud')
  // Keep the shared disposable fixture local for other browser regressions.
  await page.getByLabel('ASR 模式').selectOption('local')
  await page.getByLabel('TTS 模式').selectOption('local')
  await page.getByRole('button', { name: '保存服务配置' }).click()
  await expect(page.getByText('服务配置已保存。')).toBeVisible()
  const content = Buffer.from('# 家序\n保持温和、准确。\n', 'utf8')
  await page.getByLabel('人格 Markdown 文件').setInputFiles({ name: 'assistant-character.md', mimeType: 'text/markdown', buffer: content })
  await page.getByRole('button', { name: '上传人格文件' }).click()
  await expect(page.getByText('人格文件已保存。')).toBeVisible()
  await page.reload()
  await expect(page.getByText(`当前文件：已配置 · ${content.length} 字节`)).toBeVisible()
  await page.getByText('人格文件存储详情', { exact: true }).click()
  await expect(page.getByText(/personality\/default.md/)).toBeVisible()
  const metadata = await page.request.get('/api/console/v1/admin/personality')
  expect(await metadata.json()).toMatchObject({ exists: true, size_bytes: content.length })
  await page.setViewportSize({ width: 390, height: 900 })
  await expect(page.getByRole('button', { name: '重新读取配置' })).toBeVisible()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  await page.screenshot({ path: test.info().outputPath('assistant-settings.png'), fullPage: true })
})

test('native browser recording converts to WAV and sends one transcript while preserving draft', async ({ page }) => {
  const identity = await login(page, 'owner')
  let uploads = 0
  let recorded: Buffer | null = null
  const speech = createServer(async (request, response) => {
    const chunks: Buffer[] = []
    for await (const chunk of request) chunks.push(Buffer.from(chunk))
    const multipart = Buffer.concat(chunks)
    const start = multipart.indexOf(Buffer.from('RIFF'))
    if (start >= 0) recorded = multipart.subarray(start, start + multipart.readUInt32LE(start + 4) + 8)
    uploads++
    response.setHeader('Content-Type', 'application/json')
    response.end(JSON.stringify({ text: '浏览器语音测试' }))
  })
  await new Promise<void>((resolve) => speech.listen(0, '127.0.0.1', resolve))
  const oldSettings = await (await page.request.get('/api/console/v1/admin/assistant')).json()
  const headers = { Origin: new URL(page.url()).origin, 'X-CSRF-Token': identity.csrf_token }
  const update = async (settings: typeof oldSettings, local_asr_url: string) => {
    const current = await (await page.request.get('/api/console/v1/admin/assistant')).json()
    const saved = await page.request.put('/api/console/v1/admin/assistant', { headers, data: {
      expected_revision: current.revision, llm_mode: settings.llm_mode, asr_mode: settings.asr_mode, tts_mode: settings.tts_mode,
      local_asr_url, local_tts_url: settings.local_tts_url,
    } })
    expect(saved.status()).toBe(200)
  }
  try {
  await update({ ...oldSettings, asr_mode: 'local' }, `http://127.0.0.1:${(speech.address() as AddressInfo).port}/v1/audio/transcriptions`)
  await page.goto('/app/chat')
  // Supply a generated audio track, keeping the real MediaRecorder and Web Audio codecs.
  await page.evaluate(() => {
    Object.defineProperty(navigator.mediaDevices, 'getUserMedia', { configurable: true, value: async () => {
      const context = new AudioContext()
      await context.resume()
      const oscillator = context.createOscillator()
      const destination = context.createMediaStreamDestination()
      oscillator.connect(destination); oscillator.start()
      Object.defineProperty(window, 'voiceTestSource', { configurable: true, value: { context, track: destination.stream.getAudioTracks()[0] } })
      return destination.stream
    } })
  })
  await page.getByRole('textbox', { name: '消息内容' }).fill('保留这段未发送草稿')
  await page.getByRole('button', { name: '开始录音', exact: true }).click()
  await expect(page.getByRole('button', { name: '结束并发送', exact: true })).toBeVisible()
  await capture(page, 'voice-recording-synthetic-track')
  await expect.poll(() => page.evaluate(() => (window as unknown as { voiceTestSource: { context: AudioContext } }).voiceTestSource.context.currentTime)).toBeGreaterThan(0.25)
  await page.getByRole('button', { name: '结束并发送', exact: true }).click()
  const messages = page.getByRole('log', { name: '对话消息' })
  await expect(messages.getByText('浏览器语音测试', { exact: true })).toHaveCount(1)
  await expect(messages.getByText('离线测试回复', { exact: true })).toBeVisible()
  await expect(page.getByRole('textbox', { name: '消息内容' })).toHaveValue('保留这段未发送草稿')
  expect(uploads).toBe(1)
  expect(recorded).not.toBeNull()
  const wav = recorded as unknown as Buffer
  expect(wav.subarray(8, 12).toString()).toBe('WAVE')
  expect(wav.readUInt16LE(22)).toBe(1); expect(wav.readUInt32LE(24)).toBe(16000)
  expect(wav.readUInt16LE(34)).toBe(16); expect(wav.length).toBeGreaterThan(3200)
  expect(await page.evaluate(() => (window as unknown as { voiceTestSource: { track: MediaStreamTrack } }).voiceTestSource.track.readyState)).toBe('ended')
  await page.evaluate(() => (window as unknown as { voiceTestSource: { context: AudioContext } }).voiceTestSource.context.close())
  } finally {
    await update(oldSettings, oldSettings.local_asr_url)
    speech.closeAllConnections()
    await new Promise<void>((resolve) => speech.close(() => resolve()))
  }
})

test('owner creates a member; forced password change and member denial use real Cookie/CSRF', async ({ browser, page }) => {
  await login(page, 'owner')
  await page.getByRole('navigation', { name: '主导航' }).getByRole('link', { name: '我的', exact: true }).click()
  await page.getByRole('link', { name: '成员管理' }).click()
  await page.getByRole('button', { name: '创建成员' }).click()
  const name = unique()
  await page.getByLabel('登录名').fill(name)
  await page.getByLabel('显示名称').fill('新成员')
  await page.getByRole('button', { name: '确认创建' }).click()
  const temporary = (await page.locator('.jx-sensitive-result code').textContent())?.trim()
  expect(temporary).toBeTruthy()
  await page.getByRole('button', { name: '关闭', exact: true }).last().click()
  await page.getByRole('button', { name: '退出登录' }).last().click()
  const member = await browser.newContext()
  try {
    const memberPage = await member.newPage()
    await memberPage.goto('/app/')
    await memberPage.getByLabel('用户名').fill(name)
    await memberPage.getByLabel('密码', { exact: true }).fill(temporary!)
    await memberPage.getByRole('button', { name: '登录', exact: true }).click()
    await expect(memberPage.getByRole('heading', { name: '设置新密码' })).toBeVisible()
    await memberPage.getByLabel('当前密码').fill(temporary!)
    await memberPage.getByLabel('新密码', { exact: true }).fill('changed-fixture-password-123')
    await memberPage.getByLabel('确认新密码').fill('changed-fixture-password-123')
    await memberPage.getByRole('button', { name: '保存并重新登录' }).click()
    await expect(memberPage.getByRole('button', { name: '登录', exact: true })).toBeVisible()
    const identity = await login(memberPage, name, 'changed-fixture-password-123')
    await expect(memberPage.getByRole('link', { name: '成员' })).toHaveCount(0)
    await memberPage.goto('/app/members')
    await expect(memberPage.getByText('无权查看成员管理')).toBeVisible()
    const denied = await memberPage.request.post('/api/console/v1/admin/members', {
      headers: { Origin: 'http://127.0.0.1:18081', 'X-CSRF-Token': identity.csrf_token },
      data: { username: unique(), display_name: '无权创建' },
    })
    expect(denied.status()).toBe(403)
  } finally { await member.close() }
})

test('two accounts stay isolated and the same account can resume on another device', async ({ browser }) => {
  const alice = await browser.newContext()
  const bobby = await browser.newContext()
  const aliceOther = await browser.newContext()
  try {
    const alicePage = await alice.newPage()
    const bobbyPage = await bobby.newPage()
    const otherPage = await aliceOther.newPage()
    await login(alicePage, 'alice')
    await login(bobbyPage, 'bobby')
    const secret = `Alice 私人问题 ${Date.now()}`
    const sid = await send(alicePage, secret)
    await bobbyPage.goto(`/app/chat?sid=${encodeURIComponent(sid)}`)
    await expect(bobbyPage.getByText('该对话已不存在。')).toBeVisible()
    await expect(bobbyPage.getByText(secret)).toHaveCount(0)
    await expect.poll(() => new URL(bobbyPage.url()).searchParams.get('sid')).toBeNull()
    const bobbyHistory = await bobbyPage.request.get('/api/console/v1/sessions')
    expect(bobbyHistory.status()).toBe(200)
    const bobbySessions = (await bobbyHistory.json()).sessions as { id: string; preview: string }[]
    expect(bobbySessions.some((session) => session.id === sid || session.preview.includes(secret))).toBe(false)
    await login(otherPage, 'alice')
    await otherPage.goto(`/app/chat?sid=${encodeURIComponent(sid)}`)
    await expect(otherPage.getByRole('log', { name: '对话消息' }).getByText(secret)).toBeVisible()
    await otherPage.reload()
    await expect(otherPage.getByRole('log', { name: '对话消息' }).getByText(secret)).toBeVisible()
    await otherPage.getByRole('button', { name: '退出登录' }).last().click()
    await expect(otherPage.getByRole('button', { name: '登录', exact: true })).toBeVisible()
    await expect(otherPage.getByText(secret)).toHaveCount(0)
  } finally { await alice.close(); await bobby.close(); await aliceOther.close() }
})

for (const width of [390, 1440]) {
  test(`chat fits ${width}px without horizontal overflow`, async ({ browser }) => {
    const context = await browser.newContext({ viewport: { width, height: 900 } })
    try {
      const page = await context.newPage()
      await login(page, 'alice')
      await page.goto('/app/chat')
      await expect(page.getByRole('textbox', { name: '消息内容' })).toBeVisible()
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBeTruthy()
      if (width === 390) {
        const transcript = page.getByRole('log', { name: '对话消息' })
        for (let turn = 1; turn <= 4; turn++) {
          await page.getByRole('textbox', { name: '消息内容' }).fill(`滚动检查 ${turn}`)
          await page.getByRole('button', { name: '发送消息' }).click()
          await expect(transcript.getByText('离线测试回复')).toHaveCount(turn)
          await expect(page.getByRole('textbox', { name: '消息内容' })).toBeEnabled()
        }
        const scroll = await transcript.evaluate((area) => ({ height: area.scrollHeight, top: area.scrollTop, viewport: area.clientHeight }))
        expect(scroll.height - scroll.top - scroll.viewport, JSON.stringify(scroll)).toBeLessThan(3)
      }
    } finally { await context.close() }
  })
}
