import { fireEvent, render, screen, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, expect, it, vi } from 'vitest'
import { App } from './App'
import type { SharedAutomationResult } from './api'

const entity = { entity_id: 'switch.channel_1', name: '开关 1', domain: 'switch', device_class: null, area_name: '客厅', device_name: '客厅三路开关' }
const suggestion = {
  id: 'sample', status: 'confirmed', created_at: '2026-10-12T00:00:00Z', title: '定时开启 switch.channel_1（18:00:00）',
  confidence: .9, time_zone: 'Asia/Shanghai', entity_ids: [entity.entity_id], entities: [entity],
  rule: { triggers: [{ platform: 'time', at: '18:00:00' }], conditions: [], actions: [{ service: 'switch.turn_on', target: { entity_id: entity.entity_id } }] },
  missing_bindings: [], unsupported_code: null, shared: true, can_bind: false, can_confirm: false, can_ignore: false,
  evidence: { sample_size: null, period_days: null },
}
const sharedResult: SharedAutomationResult = {
  id: suggestion.id, created_at: suggestion.created_at,
  title: '定时开启 客厅 · 客厅三路开关 · 开关 1（18:00:00）',
  time_zone: suggestion.time_zone, rule: suggestion.rule,
  entity_ids: [entity.entity_id], entities: [entity], evidence: suggestion.evidence,
  shared: true, historical: true, history_status: 'legacy_unverified',
}
function show(path: string, items = [suggestion], sharedResults: SharedAutomationResult[] = []) {
  const fetcher = vi.fn(async (url: string) => {
    if (url.endsWith('/auth/me')) return Response.json({ user: { id: 'member', username: 'member', display_name: '家人', role: 'member', must_change_password: false }, capabilities: ['suggestions:read'], csrf_token: 'test' })
    if (url.endsWith('/suggestions')) return Response.json({ suggestions: items, count: items.length, shared_results: sharedResults })
    if (url.endsWith('/automations')) return Response.json({ automations: [{ id: 'managed-sample', suggestion_id: 'sample', title: '定时开启 客厅 · 客厅三路开关 · 开关 1（18:00:00）', ownership: 'managed', installation_state: 'verified', observed_state: 'unknown', stale: false, can_diagnose: false }], external_candidates: [], counts: { installed: 1, attention: 1, unverified: 0 } })
    return Response.json({ error: { code: 'not_found' } }, { status: 404 })
  })
  vi.stubGlobal('fetch', fetcher)
  render(<MemoryRouter initialEntries={[path]}><App/></MemoryRouter>)
  return fetcher
}
afterEach(() => vi.unstubAllGlobals())

it('shows verified names in list titles without needing directory permission or a manual action', async () => {
  const requests = show('/app/automations', [suggestion], [sharedResult])
  const title = await screen.findByRole('heading', { name: /定时开启/ })
  expect(title).toHaveTextContent('客厅 · 客厅三路开关 · 开关 1')
  expect(title).not.toHaveTextContent('switch.channel_1')
  expect(requests.mock.calls.some(([url]) => url.includes('/areas'))).toBe(false)
})

it('uses the same readable names in details and rule flow while keeping IDs in collapsed technical data', async () => {
  show('/app/automations?id=sample')
  const title = await screen.findByRole('heading', { name: /定时开启/ })
  expect(title).not.toHaveTextContent('switch.channel_1')
  const flow = screen.getByRole('list', { name: '规则关系' })
  expect(flow).toHaveTextContent('开启 客厅 · 客厅三路开关 · 开关 1')
  expect(flow).not.toHaveTextContent('switch.channel_1')
  const disclosure = screen.getByText('原始规则', { exact: true }).closest('details')!
  expect(disclosure).not.toHaveAttribute('open')
  fireEvent.click(within(disclosure).getByText('原始规则', { exact: true }))
  expect(disclosure).toHaveTextContent('switch.channel_1')
})

it('does not invent a load name or expose an entity ID when names cannot be verified', async () => {
  show('/app/automations?id=sample', [{ ...suggestion, entities: [] }])
  const title = await screen.findByRole('heading', { name: /定时开启/ })
  expect(title).toHaveTextContent('名称待核对')
  expect(title).not.toHaveTextContent('switch.channel_1')
  const flow = screen.getByRole('list', { name: '规则关系' })
  expect(flow).toHaveTextContent('名称待核对')
  expect(flow).not.toHaveTextContent('switch.channel_1')
})


import userEvent from '@testing-library/user-event'
import { waitFor } from '@testing-library/react'

const revisionIntent = { schema_version: 1, kind: 'time', entity_id: entity.entity_id, at: '18:00', time_zone: 'Asia/Shanghai', required_conditions: ['presence_home'] }
const pendingRevision = { ...suggestion, id: 'pending', status: 'pending', shared: false, can_revise: true, can_confirm: true, can_ignore: true, intent: revisionIntent, presence_binding: { entity_id: 'binary_sensor.home', state: 'on' } }
function showRevision(options: { stale?: boolean; conflict?: boolean; unknown?: boolean } = {}) {
  let items = [{ ...pendingRevision, can_revise: !options.unknown, intent: options.unknown ? undefined : revisionIntent }]
  const writes: { url: string; body: any }[] = []
  const directoryEntities = [
    { entity_id: entity.entity_id, name: '同名灯', domain: 'switch', state: 'off', hidden: false, disabled: false },
    { entity_id: 'light.other', name: '同名灯', domain: 'light', state: 'off', hidden: false, disabled: false },
    { entity_id: 'light.hidden', name: '隐藏灯', domain: 'light', state: 'on', hidden: true, disabled: false },
    { entity_id: 'light.unavailable', name: '离线灯', domain: 'light', state: 'unavailable', hidden: false, disabled: false },
    { entity_id: 'binary_sensor.home', name: '家庭占用', domain: 'binary_sensor', state: 'on', hidden: false, disabled: false },
  ]
  const meta = { freshness: options.stale ? 'stale' : 'fresh', connection: 'connected' }
  const fetcher = vi.fn(async (url: string, init?: RequestInit) => {
    if (url.endsWith('/auth/me')) return Response.json({ user: { id: 'admin', username: 'admin', display_name: '管理员', role: 'admin', must_change_password: false }, capabilities: ['suggestions:read', 'suggestions:manage'], csrf_token: 'test' })
    if (url.endsWith('/suggestions')) return Response.json({ suggestions: items, count: items.length })
    if (url.endsWith('/admin/sharing')) return Response.json({ revision: 0, suggestion_ids: [] })
    if (url.endsWith('/areas')) return Response.json({ meta, areas: [{ id: 'room', name: '客厅' }] })
    if (url.endsWith('/areas/room/devices')) return Response.json({ meta, area: { id: 'room', name: '客厅' }, devices: [{ id: 'device', name: '控制器', kind: 'device', entities: directoryEntities }] })
    if (init?.method === 'POST') {
      const body = JSON.parse(String(init.body)); writes.push({ url, body })
      if (url.endsWith('/revisions')) {
        if (options.conflict) return Response.json({ error: { code: 'conflict' } }, { status: 409 })
        const revised = { ...pendingRevision, id: 'new-version', source_suggestion_id: 'pending', intent: body.intent, presence_binding: body.presence_binding, revision_diff: [{ field: 'time', before: '18:00', after: body.intent.at }, { field: 'target', before: entity.entity_id, after: body.intent.entity_id }], rule: { ...suggestion.rule, triggers: [{ platform: 'time', at: body.intent.at }], actions: [{ service: 'light.turn_on', target: { entity_id: body.intent.entity_id } }] } }
        items = [{ ...pendingRevision, status: 'superseded', can_revise: false, can_confirm: false }, revised] as typeof items
        return Response.json({ suggestion: revised, diff: revised.revision_diff })
      }
      if (url.endsWith('/confirm')) return Response.json({ suggestion: { ...items[1], status: 'confirmed', can_confirm: false, can_revise: false } })
    }
    return Response.json({ error: { code: 'not_found' } }, { status: 404 })
  })
  vi.stubGlobal('fetch', fetcher)
  render(<MemoryRouter initialEntries={['/app/automations?id=pending']}><App/></MemoryRouter>)
  return { writes, fetcher }
}
async function selectRevisionEntity(user: ReturnType<typeof userEvent.setup>, title: string, id: string) {
  const section = screen.getByRole('group', { name: title })
  await user.selectOptions(within(section).getByLabelText('房间'), 'room')
  await user.selectOptions(within(section).getByLabelText('设备'), 'device')
  await user.selectOptions(within(section).getByLabelText('实体'), id)
}
it('saves an explicit immutable revision, displays differences and waits for separate confirmation', async () => {
  const user = userEvent.setup()
  const { writes } = showRevision()
  await user.click(await screen.findByRole('button', { name: '修订建议' }))
  await screen.findByRole('group', { name: '动作目标' })
  const target = screen.getByRole('group', { name: '动作目标' })
  expect(within(target).getByLabelText('实体')).toHaveValue('')
  await user.click(screen.getByRole('button', { name: '保存修订并查看差异' }))
  expect(writes).toHaveLength(0)
  expect(screen.getByLabelText('触发时间')).toHaveFocus()
  await user.type(screen.getByLabelText('触发时间'), '19:30')
  await selectRevisionEntity(user, '动作目标', 'light.other')
  expect(within(target).queryByRole('option', { name: '隐藏灯' })).not.toBeInTheDocument()
  expect(within(target).queryByRole('option', { name: '离线灯' })).not.toBeInTheDocument()
  expect(within(target).getAllByRole('option', { name: /同名灯/ })).toHaveLength(2)
  await selectRevisionEntity(user, '家庭占用条件', 'binary_sensor.home')
  await user.selectOptions(screen.getByLabelText('有人在家时的状态'), 'on')
  await user.click(screen.getByRole('button', { name: '保存修订并查看差异' }))
  await screen.findByRole('heading', { name: '版本差异' })
  expect(writes).toHaveLength(1)
  expect(writes[0].body.expected_version).toBe('pending')
  expect(writes[0].body.intent.required_conditions).toEqual(['presence_home'])
  expect(writes[0].body.presence_binding).toEqual({ entity_id: 'binary_sensor.home', state: 'on' })
  await user.click(screen.getByRole('button', { name: '确认创建自动化' }))
  expect(writes[1].url).toMatch(/new-version\/confirm$/)
})
it('keeps unknown rules read-only and refuses stale directory choices', async () => {
  const user = userEvent.setup()
  const { writes } = showRevision({ stale: true })
  await user.click(await screen.findByRole('button', { name: '修订建议' }))
  await screen.findByRole('alert')
  expect(screen.queryByRole('button', { name: '保存修订并查看差异' })).not.toBeInTheDocument()
  expect(writes).toHaveLength(0)
})
it('does not expose revision controls for unsupported legacy intent', async () => {
  showRevision({ unknown: true })
  await screen.findByRole('heading', { name: /定时开启/ })
  expect(screen.queryByRole('button', { name: '修订建议' })).not.toBeInTheDocument()
})



it('keeps a stale save visible, focuses missing choices and never retries or installs automatically', async () => {
  const user = userEvent.setup()
  const { writes } = showRevision({ conflict: true })
  await user.click(await screen.findByRole('button', { name: '修订建议' }))
  await screen.findByRole('group', { name: '动作目标' })
  await user.type(screen.getByLabelText('触发时间'), '19:30')
  await user.click(screen.getByRole('button', { name: '保存修订并查看差异' }))
  expect(within(screen.getByRole('group', { name: '动作目标' })).getByLabelText('房间')).toHaveFocus()
  await user.selectOptions(within(screen.getByRole('group', { name: '动作目标' })).getByLabelText('房间'), 'room')
  await user.tab()
  expect(within(screen.getByRole('group', { name: '动作目标' })).getByLabelText('设备')).toHaveFocus()
  await user.selectOptions(within(screen.getByRole('group', { name: '动作目标' })).getByLabelText('设备'), 'device')
  await user.tab()
  expect(within(screen.getByRole('group', { name: '动作目标' })).getByLabelText('实体')).toHaveFocus()
  await user.selectOptions(within(screen.getByRole('group', { name: '动作目标' })).getByLabelText('实体'), 'light.other')
  await selectRevisionEntity(user, '家庭占用条件', 'binary_sensor.home')
  await user.selectOptions(screen.getByLabelText('有人在家时的状态'), 'on')
  await user.click(screen.getByRole('button', { name: '保存修订并查看差异' }))
  await waitFor(() => expect(screen.getByRole('button', { name: '保存修订并查看差异' })).toBeDisabled())
  expect(screen.getByRole('alert')).toHaveTextContent('不会自动重试')
  expect(writes).toHaveLength(1)
  expect(screen.queryByRole('button', { name: '确认创建自动化' })).not.toBeInTheDocument()
})
