import { expect, test, type Page } from '@playwright/test'
import { startAuthProxy } from './helpers/auth-proxy'

const api = '/api/console/v1'
async function login(page: Page, origin = '') {
 await page.goto(origin + '/app/')
 await page.getByLabel('用户名').fill('owner')
 await page.getByLabel('密码', { exact: true }).fill('fixture-password-123')
 await page.getByRole('button', { name: '登录', exact: true }).click()
 await expect(page.getByRole('heading', { name: '家庭概况' })).toBeVisible()
}

for (const kind of ['asr', 'tts'] as const) {
 test(`Go rejects ${kind} URL through the Worker and reveals the exact collapsed field`, async ({ page }) => {
  await login(page)
  await page.goto('/app/admin/assistant')
  const before = await (await page.request.get(api + '/admin/assistant')).json()
  const address = page.getByLabel(`本地 ${kind.toUpperCase()} 服务地址`, { exact: true })
  const details = page.locator('details').filter({ has: address })
  await details.locator('summary').click()
  await address.fill('http://example.invalid/voice')
  await details.locator('summary').click()
  await expect(address).not.toBeVisible()
  await page.getByRole('button', { name: '保存服务配置', exact: true }).click()
  await expect(address).toBeVisible()
  await expect(address).toBeFocused()
  await expect(address).toHaveAttribute('aria-invalid', 'true')
  await expect(address).toHaveAttribute('aria-describedby', `local-${kind}-error`)
  await expect(page.locator(`#local-${kind}-error`)).toContainText('HTTP 回环 IP')
  await expect(address).toHaveValue('http://example.invalid/voice')
  const rejected = await (await page.request.get(api + '/admin/assistant')).json()
  expect(rejected.revision).toBe(before.revision)
  expect(rejected[`local_${kind}_url`]).toBe(before[`local_${kind}_url`])
  await address.fill('http://127.0.0.1:19999/unused-fixture')
  await expect(address).not.toHaveAttribute('aria-invalid', 'true')
  await page.getByRole('button', { name: '保存服务配置', exact: true }).click()
  await expect(page.getByText('服务配置已保存。', { exact: true })).toBeVisible()
  await page.reload()
  await expect(address).toHaveValue('http://127.0.0.1:19999/unused-fixture')
 })
}

test('failed voice status blocks new tasks and retry restores controls through the real Worker', async ({ page, baseURL }) => {
 const proxy = await startAuthProxy(baseURL!)
 try {
  await login(page, proxy.origin)
  const failed = proxy.gate(api + '/speech/status', 'fail')
  await page.goto(proxy.origin + '/app/chat')
  await failed.completed
  const voice = page.getByRole('region', { name: '语音对话' })
  await expect(voice.getByRole('alert')).toContainText('语音配置读取失败')
  await expect(voice).toContainText('录音和回答文字可能发送至云端')
  await expect(voice.getByRole('button', { name: '开始录音', exact: true })).toBeDisabled()
  await expect(voice.getByLabel('自动朗读新回答')).toBeDisabled()
  await page.getByLabel('消息内容').fill('语音配置失败时仍可文字对话')
  await page.getByRole('button', { name: '发送消息', exact: true }).click()
  await expect(page.getByRole('log', { name: '对话消息' })).toContainText('离线测试回复')
  await expect(page.getByRole('button', { name: '朗读回答', exact: true })).toBeDisabled()
  expect(proxy.counts(api + '/speech/jobs')).toBe(0)
  await voice.getByRole('button', { name: '重新读取', exact: true }).click()
  await expect(voice.getByRole('alert')).toHaveCount(0)
  await expect(voice.getByRole('button', { name: '开始录音', exact: true })).toBeEnabled()
  await expect(voice.getByLabel('自动朗读新回答')).toBeEnabled()
  await expect(page.getByRole('button', { name: '朗读回答', exact: true })).toBeEnabled()
  expect(proxy.counts(api + '/speech/jobs')).toBe(0)
 } finally {
  await page.close()
  await proxy.close()
 }
})
