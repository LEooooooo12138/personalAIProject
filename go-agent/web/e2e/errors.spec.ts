import { expect, test } from '@playwright/test'

// Real browser -> SharedWorker -> Go WebSocket -> offline inference adapter.
// No cloud endpoint or saved real credential is used by this fixture.
test('model authentication failure is explicit and the same conversation remains usable', async ({ page }) => {
  await page.goto('/app/')
  await page.getByLabel('用户名').fill('owner')
  await page.getByLabel('密码', { exact: true }).fill('fixture-password-123')
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await expect(page.getByRole('heading', { name: '家庭概况' })).toBeVisible()
  await expect(page.getByRole('main')).toHaveCount(1)
  await page.goto('/app/chat')
  const input = page.getByRole('textbox', { name: '消息内容' })
  await input.fill('E2E_INFERENCE_AUTH_FAILURE')
  await page.getByRole('button', { name: '发送消息' }).click()
  await expect(page.getByRole('alert')).toContainText('认证失败')
  await expect(page.getByRole('alert')).not.toContainText('重发可能产生重复')
  await expect(input).toBeEnabled()
  await input.fill('你好，请继续对话')
  await page.getByRole('button', { name: '发送消息' }).click()
  await expect(page.getByRole('log')).toContainText('离线测试回复')
  await expect(page.getByRole('alert')).toHaveCount(0)
})
