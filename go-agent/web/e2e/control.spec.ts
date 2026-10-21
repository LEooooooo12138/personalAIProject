import { expect, test, type Locator, type Page } from '@playwright/test'

// Full browser → Go Console → ControlService → disposable loopback HA fixture.
// No transport interception, production model, HA credential or real device.
import fs from 'node:fs/promises'
import path from 'node:path'
const evidenceDir = path.resolve(process.env.CONSOLE_EVIDENCE_DIR || '../../docs/assets/frontend-redesign-2026-10-10')
async function capture(page: Page, name: string, subject?: Locator) {
 await fs.mkdir(evidenceDir, {recursive:true})
 if (subject) {
  // A full-page image does not reveal content clipped by the transcript scroller.
  // Use a taller real viewport and prove the whole card fits its scrollport.
  await page.setViewportSize({ width: page.viewportSize()!.width, height: 1440 })
  let geometry: unknown
  await expect.poll(async () => {
   // Centering avoids a fractional top-edge clip caused by integer scroll offsets.
   await subject.evaluate(element => element.scrollIntoView({ block: 'center', inline: 'nearest', behavior: 'instant' }))
   geometry = await subject.evaluate(element => {
   const card = element.getBoundingClientRect()
   const viewport = element.closest('.chat-transcript')!.getBoundingClientRect()
   return { fits: card.top >= viewport.top && card.bottom <= viewport.bottom, cardTop: card.top, cardBottom: card.bottom, viewportTop: viewport.top, viewportBottom: viewport.bottom, cardHeight: card.height, viewportHeight: viewport.height, scrollTop: element.closest('.chat-transcript')!.scrollTop }
   })
   return geometry
  }).toMatchObject({ fits: true }).catch(async cause => {
   console.error('capture geometry', JSON.stringify(geometry))
   await page.screenshot({ path: path.join(evidenceDir, name + '-failed.png'), fullPage: true })
   throw cause
  })
 }
 await page.screenshot({path:path.join(evidenceDir, name + '.png'), fullPage:true})
}
const password = 'fixture-password-123'
async function login(page: Page, username: 'owner' | 'familyreader') {
  await page.goto('/app/')
  await page.getByLabel('用户名').fill(username)
  await page.getByLabel('密码', { exact: true }).fill(password)
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await expect(page.getByRole('heading', { name: '家庭概况' })).toBeVisible()
  const response = await page.request.get('/api/console/v1/auth/me')
  expect(response.status()).toBe(200)
  const identity = await response.json() as { user: { username: string }; capabilities: string[]; csrf_token: string }
  expect(identity.user.username).toBe(username)
  return identity
}
async function send(page: Page, content: string) {
  await page.goto('/app/chat')
  await page.getByRole('textbox', { name: '消息内容' }).fill(content)
  await page.getByRole('button', { name: '发送消息' }).click()
  await expect(page.getByRole('log', { name: '对话消息' }).getByText(content, { exact: true })).toBeVisible()
  await expect.poll(() => new URL(page.url()).searchParams.get('sid')).not.toBeNull()
  await expect(page.getByRole('textbox', { name: '消息内容' })).toBeEnabled()
  return new URL(page.url()).searchParams.get('sid')!
}
// Mutations use SharedWorker fetch and are not page response events. Read the
// persisted proposal after the UI settles; this GET cannot deliver a body to UI.
async function savedProposal(page: Page, sid: string) {
  const historyResponse = await page.request.get('/api/console/v1/sessions/' + sid + '/messages')
  expect(historyResponse.status()).toBe(200)
  const history = await historyResponse.json() as { messages: { attachments?: { proposal_id: string }[] }[] }
  const id = history.messages.flatMap(message => message.attachments ?? []).at(-1)!.proposal_id
  const proposalResponse = await page.request.get('/api/console/v1/control/proposals/' + id)
  expect(proposalResponse.status()).toBe(200)
  return proposalResponse.json()
}
function card(page: Page) { return page.getByRole('region', { name: '开关 1控制提议' }) }
async function query(page: Page, state: '开启' | '关闭') {
  await send(page, '开关 1状态')
  const result = page.getByRole('region', { name: '开关 1设备状态' })
  await expect(result).toContainText(`当前状态：${state}`)
  await expect(result).toContainText('观察时间：')
}
async function ensureOn(page: Page) {
  await send(page, '打开开关 1')
  const confirm = card(page).getByRole('button', { name: '确认打开开关 1' })
  if (await confirm.count()) {
    await confirm.click()
    await expect(card(page)).toContainText('已观察到目标状态')
  } else {
    await expect(page.getByRole('region', { name: '开关 1设备状态' })).toContainText('当前状态：开启')
  }
}
async function assertFits(page: Page, controlCard: Locator) {
  const layout = await page.evaluate(() => ({ width: document.documentElement.clientWidth, scroll: document.documentElement.scrollWidth }))
  expect(layout.scroll, JSON.stringify(layout)).toBeLessThanOrEqual(layout.width)
  const bounds = await controlCard.boundingBox()
  expect(bounds).not.toBeNull()
  expect(bounds!.x).toBeGreaterThanOrEqual(0)
  expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(layout.width)
  const geometry = await controlCard.evaluate((element) => {
    const parent = element.getBoundingClientRect()
    const measure = (node: Element) => {
      const box = node.getBoundingClientRect(); const style = getComputedStyle(node)
      return { tag: node.tagName, class: node.className, text: node.textContent?.slice(0, 40), x: box.x, width: box.width, client: node.clientWidth, scroll: node.scrollWidth, display: style.display, minWidth: style.minWidth, maxWidth: style.maxWidth, whiteSpace: style.whiteSpace, overflowWrap: style.overflowWrap, wordBreak: style.wordBreak, flex: style.flex, grid: style.gridTemplateColumns, overflow: style.overflow, padding: style.padding, boxSizing: style.boxSizing }
    }
    const ancestors: ReturnType<typeof measure>[] = []
    for (let node = element.parentElement; node; node = node.parentElement) ancestors.push(measure(node))
    return { viewport: window.innerWidth, card: measure(element), ancestors, overflow: [...element.querySelectorAll('*')].filter((node) => { const box = node.getBoundingClientRect(); return node.scrollWidth > node.clientWidth || box.right > parent.right + 1 || box.left < parent.left - 1 }).map(measure) }
  })
  expect(geometry.card.scroll, JSON.stringify(geometry, null, 2)).toBeLessThanOrEqual(geometry.card.client)
}

test('owner cancels without state change, then keyboard confirms and restores current result from history', async ({ page }) => {
  const identity = await login(page, 'owner')
  expect(identity.capabilities).toContain('devices:control')
  await ensureOn(page)
  const cancelledSid = await send(page, '关闭开关 1')
  await expect(card(page)).toContainText('客厅')
  await expect(card(page)).toContainText('当前状态：开启')
  await expect(card(page)).toContainText('目标动作：关闭')
  await expect(card(page)).toContainText('有效期至：')
  await capture(page, 'control-pending')
  await card(page).getByRole('button', { name: '取消提议' }).click()
  await expect(card(page)).toContainText('提议已取消')
  const cancelled = await savedProposal(page, cancelledSid)
  expect(cancelled.assistant_message.message.event_id).toBeTruthy()
  await expect(page.getByRole('log').getByText(cancelled.assistant_message.message.content, { exact: true })).toHaveCount(1)
  await expect(card(page)).toContainText('提议已取消')
  await expect(card(page).getByRole('button', { name: /确认|取消/ })).toHaveCount(0)
  await page.reload()
  await expect(card(page)).toContainText('提议已取消')
  await expect(page.getByRole('log').getByText(cancelled.assistant_message.message.content, { exact: true })).toHaveCount(1)
  await query(page, '开启') // cancellation must not switch the mock load off
  const confirmedSid = await send(page, '关闭开关 1')
  const confirm = card(page).getByRole('button', { name: '确认关闭开关 1' })
  await confirm.focus()
  await expect(confirm).toBeFocused()
  await page.keyboard.press('Enter')
  await expect(card(page)).toContainText('已观察到目标状态')
  const confirmed = await savedProposal(page, confirmedSid)
  expect(confirmed.assistant_message.message.event_id).toBeTruthy()
  await expect(page.getByRole('log').getByText(confirmed.assistant_message.message.content, { exact: true })).toHaveCount(1)
  await expect(card(page)).toContainText('已观察到目标状态')
  await expect(card(page)).toContainText('核对状态：关闭')
  await expect(card(page)).toContainText('现场核验')
  await expect(card(page).getByRole('button')).toHaveCount(0)
  await page.reload()
  await expect(card(page)).toContainText('已观察到目标状态')
  await expect(page.getByRole('log').getByText(confirmed.assistant_message.message.content, { exact: true })).toHaveCount(1)
  const restored = await (await page.request.get('/api/console/v1/sessions/' + confirmedSid + '/messages')).json()
  expect(restored.messages[confirmed.assistant_message.index].content).toBe(confirmed.assistant_message.message.content)
  expect(restored.messages[confirmed.assistant_message.index].event_id).toBe(confirmed.assistant_message.message.event_id)
  expect(restored.messages.filter((message: { event_id?: string }) => message.event_id === confirmed.assistant_message.message.event_id)).toHaveLength(1)
  expect(restored.messages[confirmed.assistant_message.index].device_result.state).toBe('off')
  await expect(card(page)).toContainText('核对状态：关闭')
  await expect(card(page).getByRole('button')).toHaveCount(0)
  expect(new URL(page.url()).searchParams.get('sid')).toBe(confirmedSid)
  expect(confirmedSid).not.toBe(cancelledSid)
  await query(page, '关闭')
  await ensureOn(page)
})

test('member queries the live fixture state with no control capability or confirm button', async ({ page }) => {
  const identity = await login(page, 'familyreader')
  expect(identity.capabilities).toContain('devices:query')
  expect(identity.capabilities).not.toContain('devices:control')
  await query(page, '开启')
  await expect(page.getByRole('button', { name: /确认打开|确认关闭|取消提议/ })).toHaveCount(0)
})

test('pending controls and long display names fit 320, 390, 768 and 1440px without horizontal overflow', async ({ page }) => {
  await login(page, 'owner')
  await ensureOn(page)
  for (const width of [320, 390, 768, 1440]) {
    await page.setViewportSize({ width, height: 1000 })
    await send(page, '关闭开关 1')
    await expect(card(page).getByRole('button', { name: '确认关闭开关 1' })).toBeVisible()
    await assertFits(page, card(page))
    // Only extend display text for a layout stress check; the real API request,
    // proposal ID, action callbacks and state remain the fixture's authority.
    await card(page).locator('header strong').evaluate((element) => { element.textContent = '客厅靠窗长名称多路控制器主灯'.repeat(12) })
    await card(page).locator('header span').evaluate((element) => { element.textContent = '客厅与餐厅连接处的已核验负载位置'.repeat(6) })
    await assertFits(page, card(page))
    await test.info().attach(`control-${width}px`, { body: await page.screenshot({ fullPage: true }), contentType: 'image/png' })
    await card(page).getByRole('button', { name: '取消提议' }).click()
    await expect(card(page)).toContainText('提议已取消')
  }
  await query(page, '开启')
})

// Requires fixture -extended-controls; all entities and writes are disposable.
test('door query shares verified binary sensor semantics with the area view', async ({ page }) => {
 test.skip(process.env.CONSOLE_EXTENDED_CONTROLS !== '1', 'requires independent extended fixture')
 await login(page, 'familyreader')
 await send(page, '审计门磁状态')
 const result = page.getByRole('region', { name: '审计门磁设备状态' })
 await expect(result).toContainText('当前状态：打开')
 await result.getByText('技术详情', { exact: true }).click()
 await expect(result).toContainText('原始状态on')
 await page.goto('/app/areas/a_b3RoZXI')
 await page.getByRole('link', { name: /审计门磁/ }).click()
 const entity = page.locator('.jx-family-entity').filter({ has: page.getByRole('heading', { name: '审计门磁', exact: true }) })
 await expect(entity).toContainText('打开')
})

for (const [name, terminal] of [['延迟成功开关', '已观察到目标状态'], ['延迟待核开关', '结果尚未确认，请核对状态']] as const) {
 test(`restored executing ${name} restores its terminal result after a reload`, async ({ page }) => {
  test.skip(process.env.CONSOLE_EXTENDED_CONTROLS !== '1', 'requires independent extended fixture')
  const identity = await login(page, 'owner')
  await send(page, `打开${name}`)
  const region = page.getByRole('region', { name: `${name}控制提议` })
  await expect(region.getByRole('button', { name: `确认打开${name}` })).toBeVisible()
  const messages = await page.request.get('/api/console/v1/sessions/' + new URL(page.url()).searchParams.get('sid') + '/messages')
  const history = await messages.json() as { messages: { attachments?: { proposal_id: string }[] }[] }
  const id = history.messages.flatMap(message => message.attachments ?? []).at(-1)!.proposal_id
  const confirmation = page.request.post(`/api/console/v1/control/proposals/${id}/confirm`, { data: {}, headers: { 'X-CSRF-Token': identity.csrf_token, Origin: new URL(page.url()).origin } })
  await expect.poll(async () => (await (await page.request.get(`/api/console/v1/control/proposals/${id}`)).json()).status).toBe('executing')
  await page.reload()
  await expect(region).toContainText('正在执行，无法撤回')
  await capture(page, name === '延迟成功开关' ? 'control-executing-success' : 'control-executing-unknown', region)
  await expect(region).toContainText(terminal)
  expect((await confirmation).status()).toBe(200)
  await capture(page, name === '延迟成功开关' ? 'control-succeeded' : 'control-unknown', region)
  await test.info().attach(`restored-${name}`, { body: await page.screenshot({ fullPage: true }), contentType: 'image/png' })
  if (name === '延迟成功开关') {
   await send(page, `关闭${name}`)
   await page.getByRole('region', { name: `${name}控制提议` }).getByRole('button', { name: `确认关闭${name}` }).click()
   await expect(page.getByRole('region', { name: `${name}控制提议` })).toContainText('已观察到目标状态')
  }
 })
}
