import { expect, test } from '@playwright/test'
import fs from 'node:fs/promises'
import path from 'node:path'
import { startAuthProxy } from './helpers/auth-proxy'

// Presentation fixtures exercise exceptional DTOs, not real collector failures.
test('dated service failures and partial collection keep evidence and limits visible', async ({ browser }) => {
  const fixtures = new Map<string, unknown>([
    ['/api/console/v1/integrations/status', {
      ha: { connection: 'unavailable', freshness: 'stale', error_code: 'ha_auth_required', observed_at: '2026-10-09T00:00:00Z', last_attempt_at: '2026-10-10T01:00:00Z', last_success_at: '2026-10-09T00:00:00Z' },
      ollama: { connection: 'connected', checked_at: '2026-10-10T01:00:00Z', models: [{ name: 'gemma4:12b', available: true }], error_code: null },
    }],
    ['/api/console/v1/admin/collection', {
      phase: 'failed', snapshot_at: '2026-10-10T01:00:01Z', last_attempt_at: '2026-10-10T01:00:00Z', last_success_at: null,
      last_failure_at: '2026-10-10T01:00:02Z', error_code: 'ha_timeout', completed_batches: 2, total_batches: 4,
      window_start: '2026-10-09T01:00:00Z', window_end: '2026-10-10T01:00:00Z', checkpoint: '2026-10-09T12:00:00Z',
    }],
  ])
  const proxy = await startAuthProxy('http://127.0.0.1:18081', fixtures)
  const context = await browser.newContext()
  const dir = path.resolve('../../docs/assets/frontend-redesign-2026-10-10')
  try {
    const page = await context.newPage(); await page.goto(`${proxy.origin}/app/`)
    await page.getByLabel('用户名').fill('owner'); await page.getByLabel('密码', { exact: true }).fill('fixture-password-123')
    await page.getByRole('button', { name: '登录', exact: true }).click()
    await expect(page.getByText('家庭设备服务授权失效，请联系管理员。')).toBeVisible()
    await fs.mkdir(dir, { recursive: true })
    for (const width of [390, 1440]) {
      await page.setViewportSize({ width, height: 900 }); await page.goto(`${proxy.origin}/app/`)
      await expect(page.getByText('本轮采集未完成，请查看详情。')).toBeVisible()
      await page.screenshot({ path: path.join(dir, `home-service-failure-fixture-${width}.png`), fullPage: true })
      await page.goto(`${proxy.origin}/app/admin/collection`)
      await expect(page.getByRole('heading', { name: '历史补采失败', exact: true })).toBeVisible()
      await expect(page.getByText('checkpoint 只代表已保存进度，不能证明本进程采集成功。')).toBeVisible()
      await expect(page.getByRole('progressbar')).toHaveAttribute('value', '2')
      await expect(page.getByText('尚无完整采集成功记录')).toBeVisible()
      await page.getByText('采集记录与 checkpoint', { exact: true }).click()
      await expect(page.getByText('2026-10-09T12:00:00Z', { exact: true })).toBeVisible()
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
      await page.screenshot({ path: path.join(dir, `collection-history-failure-fixture-${width}.png`), fullPage: true })
    }
  } finally { await context.close(); await proxy.close() }
})
