import { expect, test, type Page, type TestInfo } from '@playwright/test'
import fs from 'node:fs/promises'
import { automationLifecycle, expectWrites, keyboardSelect } from './helpers/automationLifecycle'
import { startAuthProxy } from './helpers/auth-proxy'
import type { AnalysisRun, BackfillRun, CollectionView, SuggestionList, AutomationList, ManagementView } from '../src/api'

const api = '/api/console/v1'
async function read<T>(page: Page, route: string): Promise<T> {
  const response = await page.request.get(api + route)
  expect(response.status()).toBe(200)
  return response.json()
}
async function login(page: Page, name = 'owner', origin = '') {
  await page.goto(origin + '/app/')
  await page.getByLabel('用户名').fill(name)
  await page.getByLabel('密码', { exact: true }).fill('fixture-password-123')
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await expect(page.getByRole('heading', { name: '家庭概况' })).toBeVisible()
}
async function headers(page: Page) {
  const me = await read<{ csrf_token: string }>(page, '/auth/me')
  return { Origin: new URL(page.url()).origin, 'X-CSRF-Token': me.csrf_token }
}
async function suggestion(page: Page, id: string) {
  const item = (await read<SuggestionList>(page, '/suggestions')).suggestions.find(item => item.id === id)
  expect(item).toBeDefined()
  return item!
}
async function currentID(page: Page, previous: string) {
  await expect(page).toHaveURL(url => Boolean(url.searchParams.get('id')) && url.searchParams.get('id') !== previous)
  return new URL(page.url()).searchParams.get('id')!
}
async function fit(page: Page, width: 305 | 320, label: string, info: TestInfo) {
  await page.setViewportSize({ width, height: 900 })
  const layout = await page.evaluate(() => ({ viewport: innerWidth, clientWidth: document.documentElement.clientWidth, scrollWidth: document.documentElement.scrollWidth }))
  expect(layout.viewport).toBe(width)
  expect(layout.clientWidth).toBeGreaterThan(0)
  expect(layout.clientWidth).toBeLessThanOrEqual(width)
  expect(layout.scrollWidth).toBeLessThanOrEqual(layout.clientWidth)
  await info.attach(label + '-layout', { body: JSON.stringify(layout), contentType: 'application/json' })
  await page.screenshot({ path: info.outputPath(label + '.png'), fullPage: true })
}

test('actual discovery evidence reaches unknown installation, read-only convergence, diagnosis and independent management', async ({ page, browser }, info) => {
  test.setTimeout(180_000)
  const fixture = await automationLifecycle()
  await fixture.mode({ history: 'empty' })
  const baseline = (await fixture.state()).writes
  expect(Object.values(baseline).every(value => value === 0)).toBe(true)
  await login(page)
  await page.goto('/app/admin/collection')
  await expect(page.getByText('模拟预览 · 当前设备和自动化均为测试数据', { exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: '分析现有数据' })).toBeEnabled()
  const before = await read<CollectionView>(page, '/admin/collection')
  expect(Object.values(before.coverage?.entities ?? {}).every(item => item.valid_state_count === 0)).toBe(true)
  const originalIDs = new Set(before.analysis_runs?.map(run => run.id))
  const originalSuggestions = new Set((await read<SuggestionList>(page, '/suggestions')).suggestions.map(item => item.id))
  await page.getByRole('button', { name: '分析现有数据' }).click()
  await expect.poll(async () => (await read<CollectionView>(page, '/admin/collection')).analysis_runs?.filter(run => !originalIDs.has(run.id)).length).toBe(1)
  const insufficientID = (await read<CollectionView>(page, '/admin/collection')).analysis_runs!.find(run => !originalIDs.has(run.id))!.id
  await expect.poll(async () => (await read<AnalysisRun>(page, '/admin/analysis-runs/' + insufficientID)).status).toBe('completed')
  const insufficient = await read<AnalysisRun>(page, '/admin/analysis-runs/' + insufficientID)
  expect(insufficient.outcome).toBe('insufficient_data')
  await expect(page.getByText(/数据不足，当前算法无法给出结论。.*尚未安装自动化。/).first()).toBeVisible()
  expectWrites(baseline, (await fixture.state()).writes)
  await fit(page, 320, 'empty-discovery-320', info)

  // Cancel a real delayed HA read before publishing observations. Modes cannot
  // reset stored history, so this branch deliberately precedes successful data.
  await fixture.mode({ history: 'empty', history_delay_ms: 5000 })
  const reads = (await fixture.state()).history_reads
  const target = (await read<CollectionView>(page, '/admin/collection')).requested_window!
  await page.getByRole('button', { name: '补采最近 14 天', exact: true }).click()
  await expect.poll(async () => (await fixture.state()).history_reads).toBeGreaterThan(reads)
  await expect(page.getByRole('button', { name: '取消补采', exact: true })).toBeEnabled()
  const backfillID = (await read<CollectionView>(page, '/admin/collection')).backfill_runs![0].id
  expect((await read<BackfillRun>(page, '/admin/backfills/' + backfillID)).requested_window).toEqual(target)
  await page.getByRole('button', { name: '取消补采', exact: true }).click()
  await expect.poll(async () => (await read<BackfillRun>(page, '/admin/backfills/' + backfillID)).status).toBe('cancelled')
  expectWrites(baseline, (await fixture.state()).writes)

  await fixture.mode({ history: 'pattern' })
  await page.getByRole('button', { name: '刷新采集显示', exact: true }).click()
  await expect(page.getByRole('button', { name: '补采最近 14 天', exact: true })).toBeEnabled()
  await page.getByRole('button', { name: '补采最近 14 天', exact: true }).click()
  await expect.poll(async () => (await read<BackfillRun>(page, '/admin/backfills/' + backfillID)).status).toBe('completed')
  const acquired = await read<BackfillRun>(page, '/admin/backfills/' + backfillID)
  expect(acquired.requested_window).toEqual(target)
  expect(acquired.retention).toBe('unknown')
  await page.getByRole('button', { name: '刷新采集显示', exact: true }).click()
  await expect(page.getByRole('button', { name: '分析现有数据', exact: true })).toBeEnabled()
  const priorRunIDs = new Set((await read<CollectionView>(page, '/admin/collection')).analysis_runs!.map(run => run.id))
  await page.getByRole('button', { name: '分析现有数据', exact: true }).click()
  await expect.poll(async () => (await read<CollectionView>(page, '/admin/collection')).analysis_runs!.filter(run => !priorRunIDs.has(run.id)).length).toBe(1)
  const analyzedID = (await read<CollectionView>(page, '/admin/collection')).analysis_runs!.find(run => !priorRunIDs.has(run.id))!.id
  await expect.poll(async () => (await read<AnalysisRun>(page, '/admin/analysis-runs/' + analyzedID)).status).toBe('completed')
  const analyzed = await read<AnalysisRun>(page, '/admin/analysis-runs/' + analyzedID)
  expect(analyzed.outcome).toBe('matched')
  expect(analyzed.requested_window).toEqual(target)
  const discovered = (await read<SuggestionList>(page, '/suggestions')).suggestions.filter(item => !originalSuggestions.has(item.id))
  expect(discovered).toHaveLength(1)
  const original = discovered[0]
  expect(original.id).not.toBe('fixture_time')
  expect(original.intent?.entity_id).toBe('switch.channel_1')
  expect(original.evidence).toMatchObject({ hit_days: 11, eligible_days: 14, period_days: 14, denominator: 14, denominator_type: 'requested_days', window: target, algorithm_version: analyzed.algorithm_version })
  expect(original.evidence.event_refs?.length).toBeGreaterThan(0)
  expectWrites(baseline, (await fixture.state()).writes)
  await page.goto('/app/automations?id=' + original.id)
  await expect(page.getByText('11 / 14 请求天数', { exact: true })).toBeVisible()
  await page.getByText('建议证据详情', { exact: true }).click()
  await expect(page.getByText(analyzed.algorithm_version, { exact: true })).toBeVisible()
  await fit(page, 305, 'discovered-evidence-305', info)

  await page.getByRole('button', { name: '补齐有人在家条件', exact: true }).click()
  await keyboardSelect(page.getByLabel('房间', { exact: true }), '其他')
  await keyboardSelect(page.getByLabel('设备', { exact: true }), '独立实体 · 全家有人（模拟聚合）')
  await keyboardSelect(page.getByLabel('实体', { exact: true }), '全家有人（模拟聚合）')
  await keyboardSelect(page.getByLabel('有人在家时的状态', { exact: true }), '开启')
  await page.getByRole('button', { name: '保存绑定并查看新版本', exact: true }).click()
  const boundID = await currentID(page, original.id)
  const bound = await suggestion(page, boundID)
  expect(bound.presence_binding).toEqual({ entity_id: 'group.family_home', state: 'on' })
  expect(bound.source_suggestion_id).toBe(original.id)
  expect((await suggestion(page, original.id)).evidence).toEqual(original.evidence)
  await page.getByRole('button', { name: '修订建议', exact: true }).click()
  await page.getByLabel('触发时间', { exact: true }).fill('invalid')
  await page.getByRole('button', { name: '保存修订并查看差异', exact: true }).click()
  await expect(page.getByLabel('触发时间', { exact: true })).toBeFocused()
  await page.getByLabel('触发时间', { exact: true }).fill('19:30')
  const targetPicker = page.getByRole('group', { name: '动作目标', exact: true })
  await keyboardSelect(targetPicker.getByLabel('房间', { exact: true }), '客厅')
  await keyboardSelect(targetPicker.getByLabel('设备', { exact: true }), '客厅三路开关')
  await keyboardSelect(targetPicker.getByLabel('实体', { exact: true }), '开关 1')
  const presence = page.getByRole('group', { name: '家庭占用条件', exact: true })
  await keyboardSelect(presence.getByLabel('房间', { exact: true }), '其他')
  await keyboardSelect(presence.getByLabel('设备', { exact: true }), '独立实体 · 全家有人（模拟聚合）')
  await keyboardSelect(presence.getByLabel('实体', { exact: true }), '全家有人（模拟聚合）')
  await keyboardSelect(page.getByLabel('有人在家时的状态', { exact: true }), '开启')
  await page.getByRole('button', { name: '保存修订并查看差异', exact: true }).click()
  const revisionID = await currentID(page, boundID)
  await expect(page.getByRole('heading', { name: '版本差异', exact: true })).toBeVisible()
  const revised = await suggestion(page, revisionID)
  expect(revised.intent?.at).toBe('19:30')
  expect((await suggestion(page, boundID)).intent).toEqual(bound.intent)
  expect((await suggestion(page, boundID)).status).toBe('superseded')
  expectWrites(baseline, (await fixture.state()).writes)
  await fit(page, 320, 'revision-diff-320', info)

  await fixture.mode({ history: 'pattern', profile: 'unknown' })
  await page.getByRole('button', { name: '确认创建自动化', exact: true }).click()
  await expect(page.getByRole('button', { name: '核对最新建议状态', exact: true })).toBeVisible()
  expect((await suggestion(page, revisionID)).status).toBe('pending')
  expect((await suggestion(page, revisionID)).installation).toBeUndefined()
  expectWrites(baseline, (await fixture.state()).writes)
  await fixture.mode({ history: 'pattern', fault: 'lose_reload' })
  await page.reload()
  const confirm = page.getByRole('button', { name: '确认创建自动化', exact: true })
  await expect(confirm).toBeEnabled()
  await confirm.focus(); await confirm.press('Enter')
  await expect.poll(async () => (await suggestion(page, revisionID)).installation?.status).toBe('unknown')
  const unknown = await suggestion(page, revisionID)
  await expect(page.getByRole('button', { name: '确认创建自动化', exact: true })).toHaveCount(0)
  expectWrites(baseline, (await fixture.state()).writes, { total: 2, config: 1, reload: 1 })
  const installedWrites = (await fixture.state()).writes
  await fit(page, 305, 'install-unknown-305', info)
  const repeated = await page.request.post(api + '/admin/suggestions/' + revisionID + '/confirm', { headers: await headers(page), data: {} })
  expect(repeated.status()).toBe(200)
  expect((await repeated.json()).suggestion.installation.status).toBe('unknown')
  expectWrites(installedWrites, (await fixture.state()).writes)
  const reconcile = page.getByRole('button', { name: '只读核对安装状态', exact: true })
  await reconcile.focus(); await reconcile.press('Enter')
  await expect(page.getByText('已核实安装并归档', { exact: true })).toBeVisible()
  const installed = await suggestion(page, revisionID)
  expect(installed.installation?.id).toBe(unknown.installation?.id)
  expect(installed.installation).toMatchObject({ status: 'confirmed', mapping_status: 'verified', load_status: 'verified', archive_status: 'complete' })
  expectWrites(installedWrites, (await fixture.state()).writes)
  await reconcile.click()
  expectWrites(installedWrites, (await fixture.state()).writes)

  const list = await read<AutomationList>(page, '/automations')
  const managed = list.automations.find(item => item.suggestion_id === revisionID)!
  expect(managed.ownership).toBe('managed')
  await page.goto('/app/automations')
  await page.getByRole('button', { name: /^已安装/ }).click()
  const diagnose = page.getByRole('button', { name: '只读诊断：' + managed.title, exact: true })
  await diagnose.focus(); await diagnose.press('Enter')
  await expect.poll(async () => (await read<AutomationList>(page, '/automations')).automations.find(item => item.id === managed.id)?.diagnostic?.id).toBeTruthy()
  await expect(page.locator('body')).not.toContainText('LIFECYCLE_PRIVATE')
  const diagnostic = (await read<AutomationList>(page, '/automations')).automations.find(item => item.id === managed.id)!.diagnostic!
  expect(diagnostic.checks).toEqual(expect.arrayContaining([expect.objectContaining({ reason: 'completed', status: 'pass' })]))
  const diagnosticCard = page.getByRole('region', { name: managed.title, exact: true })
  await expect(diagnosticCard.getByRole('listitem').filter({ hasText: '历史运行报告完成；不证明物理设备效果' })).toBeVisible()
  expectWrites(installedWrites, (await fixture.state()).writes)
  await fit(page, 320, 'diagnostic-320', info)
  await page.getByRole('link', { name: '管理受管规则', exact: true }).click()
  await expect(page.getByRole('heading', { name: '受管规则管理', exact: true })).toBeVisible()
  const management = async () => (await read<{ management: ManagementView }>(page, '/admin/automations/' + managed.id + '/management')).management
  expect((await management()).current_version).toBe(revisionID)
  expectWrites(installedWrites, (await fixture.state()).writes)
  async function prepareDisable() {
    await page.getByRole('button', { name: '停用规则', exact: true }).click()
    await expect(page.getByRole('button', { name: '准备停用确认', exact: true })).toBeDisabled()
    await keyboardSelect(page.getByLabel('正在运行的动作', { exact: true }), '允许已经运行的动作继续')
    const prepare = page.getByRole('button', { name: '准备停用确认', exact: true })
    await prepare.focus(); await prepare.press('Enter')
    await expect(page.getByRole('button', { name: '确认停用规则', exact: true })).toBeEnabled()
    expect((await management()).latest_operation?.stop_actions).toBe(false)
    expectWrites(installedWrites, (await fixture.state()).writes)
  }
  await prepareDisable()
  const cancelledOperation = (await management()).latest_operation!.id
  await page.getByRole('button', { name: '取消本地确认', exact: true }).click()
  await expect(page.getByText('停用规则 · 本地确认已取消', { exact: true })).toBeVisible()
  expect((await management()).latest_operation?.status).toBe('cancelled')
  expectWrites(installedWrites, (await fixture.state()).writes)
  await prepareDisable()
  expect((await management()).latest_operation?.id).not.toBe(cancelledOperation)
  const finish = page.getByRole('button', { name: '确认停用规则', exact: true })
  await fit(page, 305, 'management-review-305', info)
  await finish.focus(); await finish.press('Enter')
  await expect(page.getByText('停用规则 · 操作已核对', { exact: true })).toBeVisible()
  expectWrites(installedWrites, (await fixture.state()).writes, { total: 1, disable: 1 })
  const final = (await fixture.state()).writes

  const memberContext = await browser.newContext({ baseURL: new URL(page.url()).origin })
  try {
    const member = await memberContext.newPage()
    await login(member, 'familyreader')
    expect((await member.request.get(api + '/admin/collection')).status()).toBe(403)
    expect((await member.request.get(api + '/admin/automations/' + managed.id + '/management')).status()).toBe(403)
    const hidden = await read<SuggestionList>(member, '/suggestions')
    expect(hidden.suggestions.some(item => item.id === revisionID || item.id === original.id)).toBe(false)
    await member.goto('/app/automations?managed=' + managed.id)
    await expect(member.getByRole('button', { name: /^(启用|停用|更新|删除|确认|准备).*规则/ })).toHaveCount(0)
    await member.goto('/app/automations?id=' + revisionID)
    await expect(member.getByText('未找到这个建议或没有读取权限。', { exact: true })).toBeVisible()
    const denied = await member.request.post(api + '/admin/suggestions/' + revisionID + '/confirm', { headers: await headers(member), data: {} })
    expect(denied.status()).toBe(403)
    await fit(member, 320, 'member-hidden-320', info)
  } finally { await memberContext.close() }
  expectWrites(final, (await fixture.state()).writes)
  const evidence = { provider: 'controlled_fixture', analysis_id: analyzedID, backfill_id: backfillID, requested_window: target, evidence: original.evidence, suggestion_id: original.id, bound_id: boundID, revised_id: revisionID, install_operation_id: installed.installation?.id, managed_id: managed.id, baseline, installed_writes: installedWrites, final_writes: final, physical_effect: 'not_tested' }
  await fs.writeFile(info.outputPath('lifecycle-evidence.json'), JSON.stringify(evidence, null, 2))
  await info.attach('lifecycle-evidence', { body: JSON.stringify(evidence), contentType: 'application/json' })
})

test('a real delayed collection result cannot refill private UI after logout and member login', async ({ browser, baseURL }, info) => {
  const fixture = await automationLifecycle()
  const before = (await fixture.state()).writes
  const proxy = await startAuthProxy(baseURL!)
  const context = await browser.newContext()
  let gate: ReturnType<typeof proxy.gate> | undefined
  try {
    const page = await context.newPage()
    await login(page, 'owner', proxy.origin)
    const privateResponse = await context.request.get(proxy.origin + api + '/admin/collection')
    expect(privateResponse.status()).toBe(200)
    const privateRuns = ((await privateResponse.json()) as CollectionView).analysis_runs!.map(run => run.id)
    expect(privateRuns.length).toBeGreaterThan(0)
    gate = proxy.gate(api + '/admin/collection', 'response')
    await page.goto(proxy.origin + '/app/admin/collection')
    await gate.seen
    expect(await gate.status).toBe(200)
    await page.getByRole('button', { name: '退出登录', exact: true }).last().click()
    await expect(page.getByRole('button', { name: '登录', exact: true })).toBeVisible()
    await login(page, 'familyreader', proxy.origin)
    gate.release()
    await gate.completed
    await expect(page.getByRole('heading', { name: '家庭概况' })).toBeVisible()
    for (const id of privateRuns) await expect(page.locator('body')).not.toContainText(id)
    await page.goto(proxy.origin + '/app/admin/collection')
    await expect(page.getByRole('button', { name: '分析现有数据', exact: true })).toHaveCount(0)
    for (const id of privateRuns) await expect(page.locator('body')).not.toContainText(id)
    const denied = await context.request.get(proxy.origin + api + '/admin/collection')
    expect(denied.status()).toBe(403)
    expect(proxy.counts(api + '/admin/analyze')).toBe(0)
    expect(proxy.counts(api + '/admin/backfills')).toBe(0)
    expectWrites(before, (await fixture.state()).writes)
    await fit(page, 305, 'late-result-member-305', info)
  } finally { gate?.release(); await context.close(); await proxy.close() }
})
