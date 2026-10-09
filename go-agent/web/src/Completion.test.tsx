import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { App } from './App'

const member = { id: 'm1', username: 'mei', display_name: '梅', role: 'member', must_change_password: false }
const admin = { ...member, id: 'a1', username: 'owner', display_name: '家长', role: 'admin' }
const caps = ['sessions:read', 'chat:use', 'areas:read', 'knowledge:read', 'suggestions:read']
const meta = { observed_at: '2026-10-09T00:00:00Z', last_attempt_at: '2026-10-09T01:00:00Z', last_success_at: '2026-10-09T00:00:00Z', freshness: 'fresh', connection: 'connected', error_code: null }
const areas = { meta, totals: { devices: 1, entities: 1, standalone_entities: 0 }, areas: [{ id: 'living', name: '客厅', device_count: 1, entity_count: 1, standalone_entity_count: 0, matches: [] }] }
const presence = { entity_id: 'binary_sensor.home', name: '全家占用', domain: 'binary_sensor', state: 'on', unit: null, device_class: 'occupancy', last_changed: null, last_updated: null, disabled: false, hidden: false, category: null }
const devices = { meta, area: { id: 'living', name: '客厅' }, devices: [{ id: 'controller', kind: 'device', name: '全家控制器', area_id: 'living', direct_area_id: 'living', membership: 'direct', entity_count: 1, domains: ['binary_sensor'], entities: [presence], load_location_verified: false }] }
const pending = { id: 's-old', status: 'pending', created_at: '2026-10-09T00:00:00Z', title: '晚间灯光建议', confidence: 0.8, time_zone: 'Asia/Shanghai', entity_ids: ['light.room'], rule: null, missing_bindings: ['presence'], unsupported_code: null, shared: false, can_bind: true, can_confirm: false, can_ignore: true, evidence: { sample_size: null, period_days: 14 } }
const bound = { ...pending, id: 's-new', source_suggestion_id: 's-old', missing_bindings: [], can_bind: false, can_confirm: true, rule: { triggers: [{ platform: 'time', at: '18:00:00' }], conditions: [{ condition: 'state', entity_id: 'binary_sensor.home', state: 'on' }], actions: [{ service: 'light.turn_on', target: { entity_id: 'light.room' } }] } }
const confirmed = { ...bound, status: 'confirmed', can_confirm: false, can_ignore: false }
const collection = { phase: 'failed', last_attempt_at: '2026-10-09T01:00:00Z', last_success_at: null, last_failure_at: '2026-10-09T01:00:01Z', error_code: 'ha_timeout', snapshot_at: '2026-10-09T01:00:00Z', window_start: '2026-10-08T01:00:00Z', window_end: '2026-10-09T01:00:00Z', completed_batches: 2, total_batches: 4, checkpoint: '2026-10-08T01:00:00Z' }
const failure = (code: string, status: number) => Response.json({ error: { code, message: 'SECRET RAW ERROR', request_id: 'req-test' } }, { status })
type Transport = (path: string, options: RequestInit) => Response | Promise<Response> | undefined
function show(path: string, role: 'member' | 'admin' = 'member', transport: Transport = () => undefined) {
  const fetcher = vi.fn(async (url: string, options: RequestInit = {}) => {
    const target = url.replace('/api/console/v1', '')
    const custom = await transport(target, options)
    if (custom) return custom
    if (target === '/auth/me') return Response.json({ user: role === 'admin' ? admin : member, capabilities: [...caps, ...(role === 'admin' ? ['knowledge:manage', 'suggestions:manage', 'collection:read', 'members:manage'] : [])], csrf_token: 'csrf-test' })
    if (target === '/auth/logout') return new Response(null, { status: 204 })
    if (target === '/sessions') return Response.json({ sessions: [] })
    if (target === '/areas') return Response.json(areas)
    if (target === '/areas/living/devices') return Response.json(devices)
    if (target === '/integrations/status') return Response.json({ ha: meta, ollama: { connection: 'connected', checked_at: '2026-10-09T02:00:00Z', models: [{ name: 'local-chat', available: true }], error_code: null } })
    if (target === '/suggestions') return Response.json({ suggestions: [pending], count: 1 })
    if (target === '/admin/sharing') return Response.json({ revision: 3, suggestion_ids: [] })
    if (target === '/admin/vault/status') return Response.json({ vault: 'personal', page_count: 2, total_bytes: 123 })
    if (target === '/admin/collection') return Response.json(collection)
    return failure('not_found', 404)
  })
  vi.stubGlobal('fetch', fetcher)
  render(<MemoryRouter initialEntries={[path]}><App/></MemoryRouter>)
  return fetcher
}
afterEach(() => vi.unstubAllGlobals())

// These assertions fail if the public/personal boundary or explicit-action workflow is lost.
describe('knowledge workflows', () => {
  it('searches public agent pages and renders safe body instead of HTML or remote images', async () => {
    const fetcher = show('/app/knowledge', 'member', (path) => path === '/knowledge/search?q=%E6%B8%A9%E5%BA%A6' ? Response.json({ results: [{ path: 'comfort.md', title: '舒适温度', snippet: '白天建议' }], count: 1 }) : path === '/knowledge/page?path=comfort.md' ? Response.json({ path: 'comfort.md', title: '舒适温度', body: '唯一公开事实\n\n<script>danger()</script> ![remote](https://example.org/pixel)' }) : undefined)
    fireEvent.change(await screen.findByLabelText('搜索公开知识'), { target: { value: '温度' } })
    fireEvent.click(screen.getByRole('button', { name: '搜索知识' }))
    fireEvent.click(await screen.findByRole('link', { name: /舒适温度/ }))
    expect(await screen.findByText('唯一公开事实')).toBeInTheDocument()
    expect(document.querySelector('script')).toBeNull()
    expect(document.querySelector('img')).toBeNull()
    expect(fetcher.mock.calls.some(([url]) => url.includes('/admin/'))).toBe(false)
  })
  it('separates no results from a failed search and never renders raw errors', async () => {
    let fail = false
    show('/app/knowledge', 'member', (path) => path.startsWith('/knowledge/search') ? fail ? failure('unavailable', 503) : Response.json({ results: [], count: 0 }) : undefined)
    const input = await screen.findByLabelText('搜索公开知识')
    fireEvent.change(input, { target: { value: '不存在' } }); fireEvent.click(screen.getByRole('button', { name: '搜索知识' }))
    expect(await screen.findByText('没有找到可读取的公开资料。')).toBeInTheDocument()
    fail = true; fireEvent.click(screen.getByRole('button', { name: '搜索知识' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('服务暂时不可用')
    expect(screen.queryByText('没有找到可读取的公开资料。')).not.toBeInTheDocument()
    expect(screen.queryByText('SECRET RAW ERROR')).not.toBeInTheDocument()
  })
  it('shows an unreadable document as a missing page with a return action', async () => {
    show('/app/knowledge?path=internal.md', 'member', (path) => path.startsWith('/knowledge/page') ? failure('not_found', 404) : undefined)
    expect(await screen.findByRole('alert')).toHaveTextContent('未找到')
    expect(screen.getByRole('link', { name: '返回公开知识' })).toBeInTheDocument()
  })
  it('denies members opening private knowledge directly without private requests', async () => {
    const fetcher = show('/app/admin/knowledge')
    expect(await screen.findByRole('heading', { name: '无权查看私人知识' })).toBeInTheDocument()
    expect(fetcher.mock.calls.some(([url]) => url.includes('/admin/vault') || url.includes('/admin/knowledge'))).toBe(false)
  })
  it('runs a page-local personal question, links sources, and never posts ordinary chat', async () => {
    const fetcher = show('/app/admin/knowledge', 'admin', (path, options) => path === '/admin/knowledge/query' ? JSON.parse(options!.body as string).query === '私人事实是什么？' ? Response.json({ answer: '私人唯一事实是晨间散步。', sources: [{ path: 'habit.md', title: '私人习惯', score: 0.9 }] }) : failure('invalid_request', 422) : path === '/admin/vault/page?path=habit.md' ? Response.json({ path: 'habit.md', title: '私人习惯', body: '仅私人正文' }) : undefined)
    fireEvent.change(await screen.findByLabelText('私人问题'), { target: { value: '私人事实是什么？' } })
    fireEvent.click(screen.getByRole('button', { name: '单次问答' }))
    expect(await screen.findByText('私人唯一事实是晨间散步。')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('link', { name: '私人习惯' }))
    expect(await screen.findByText('仅私人正文')).toBeInTheDocument()
    const [, options] = fetcher.mock.calls.find(([url]) => url.endsWith('/admin/knowledge/query'))!
    expect(options!.headers).toMatchObject({ 'X-CSRF-Token': 'csrf-test' })
    expect(fetcher.mock.calls.some(([url]) => url.includes('/chat/'))).toBe(false)
  })
  it('preserves failed ingest input without retrying or claiming public publication', async () => {
    const fetcher = show('/app/admin/knowledge', 'admin', (path) => path === '/admin/wiki/ingest' ? failure('invalid_request', 422) : undefined)
    fireEvent.change(await screen.findByLabelText('来源标题'), { target: { value: '私人记录' } })
    fireEvent.change(screen.getByLabelText('导入文本'), { target: { value: '仅属于管理员的文本' } })
    fireEvent.click(screen.getByRole('button', { name: '导入到 personal' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('检查输入')
    expect(screen.getByLabelText('导入文本')).toHaveValue('仅属于管理员的文本')
    expect(fetcher.mock.calls.filter(([url]) => url.endsWith('/admin/wiki/ingest'))).toHaveLength(1)
  })
  it('discards a delayed personal answer after sign-out', async () => {
    let release!: (response: Response) => void
    const delayed = new Promise<Response>((resolve) => { release = resolve })
    show('/app/admin/knowledge', 'admin', (path) => path === '/admin/knowledge/query' ? delayed : undefined)
    fireEvent.change(await screen.findByLabelText('私人问题'), { target: { value: '隐私问题' } })
    fireEvent.click(screen.getByRole('button', { name: '单次问答' }))
    fireEvent.click(screen.getAllByRole('button', { name: '退出登录' })[0])
    await screen.findByRole('button', { name: '登录' })
    await act(async () => release(Response.json({ answer: '旧身份秘密', sources: [] })))
    expect(screen.queryByText('旧身份秘密')).not.toBeInTheDocument()
  })
})

describe('automation workflows', () => {
  it('binds to a new ID and requires a separate confirmation action', async () => {
    const fetcher = show('/app/automations?id=s-old', 'admin', (path) => path.endsWith('/s-old/bindings') ? Response.json({ suggestion: bound }) : path.endsWith('/s-new/confirm') ? Response.json({ suggestion: confirmed }) : undefined)
    fireEvent.click(await screen.findByRole('button', { name: '补齐有人在家条件' }))
    fireEvent.change(await screen.findByLabelText('代表全家的占用实体'), { target: { value: 'binary_sensor.home' } })
    fireEvent.change(screen.getByLabelText('有人在家时的状态'), { target: { value: 'on' } })
    fireEvent.click(screen.getByRole('button', { name: '保存绑定并查看新版本' }))
    expect(await screen.findByText('新版本：s-new')).toBeInTheDocument()
    expect(screen.getByText('light.turn_on')).toBeInTheDocument()
    expect(fetcher.mock.calls.filter(([url]) => url.endsWith('/confirm'))).toHaveLength(0)
    fireEvent.click(screen.getByRole('button', { name: '确认创建自动化' }))
    expect(await screen.findByText('已确认安装并归档')).toBeInTheDocument()
    expect(fetcher.mock.calls.filter(([url]) => url.endsWith('/s-new/confirm'))).toHaveLength(1)
  })
  it('keeps bindings on validation failure without automatic resubmission', async () => {
    const fetcher = show('/app/automations?id=s-old', 'admin', (path) => path.endsWith('/bindings') ? failure('invalid_request', 422) : undefined)
    fireEvent.click(await screen.findByRole('button', { name: '补齐有人在家条件' }))
    fireEvent.change(await screen.findByLabelText('代表全家的占用实体'), { target: { value: 'binary_sensor.home' } })
    fireEvent.change(screen.getByLabelText('有人在家时的状态'), { target: { value: 'on' } })
    fireEvent.click(screen.getByRole('button', { name: '保存绑定并查看新版本' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('检查输入')
    expect(screen.getByLabelText('有人在家时的状态')).toHaveValue('on')
    expect(fetcher.mock.calls.filter(([url]) => url.endsWith('/bindings'))).toHaveLength(1)
  })
  it('shows member results without administrator controls or version-chain fields', async () => {
    const fetcher = show('/app/automations?id=s-new', 'member', (path) => path === '/suggestions' ? Response.json({ suggestions: [{ ...confirmed, shared: true, source_suggestion_id: undefined }], count: 1 }) : undefined)
    expect(await screen.findByText('light.turn_on')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '确认创建自动化' })).not.toBeInTheDocument()
    expect(screen.queryByText('s-old')).not.toBeInTheDocument()
    expect(fetcher.mock.calls.some(([url]) => url.includes('/admin/sharing'))).toBe(false)
  })
  it('requires refresh before explicitly retrying an applying result', async () => {
    const fetcher = show('/app/automations?id=s-new', 'admin', (path) => path === '/suggestions' ? Response.json({ suggestions: [{ ...bound, status: 'applying' }], count: 1 }) : path.endsWith('/confirm') ? Response.json({ suggestion: confirmed }) : undefined)
    expect(await screen.findByRole('button', { name: '明确重试创建自动化' })).toBeDisabled()
    fireEvent.click(screen.getByRole('button', { name: '核对最新建议状态' }))
    await waitFor(() => expect(screen.getByRole('button', { name: '明确重试创建自动化' })).toBeEnabled())
    expect(fetcher.mock.calls.some(([url]) => url.endsWith('/confirm'))).toBe(false)
  })
  it('reports a sharing revision conflict without silently overwriting', async () => {
    const fetcher = show('/app/automations', 'admin', (path, options) => path === '/suggestions' ? Response.json({ suggestions: [confirmed], count: 1 }) : path === '/admin/sharing' && options?.method === 'PUT' ? failure('conflict', 409) : undefined)
    fireEvent.click(await screen.findByLabelText('共享：晚间灯光建议'))
    fireEvent.click(screen.getByRole('button', { name: '保存结果共享' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('刷新')
    const writes = fetcher.mock.calls.filter(([url, options]) => url.endsWith('/admin/sharing') && options?.method === 'PUT')
    expect(writes).toHaveLength(1)
    expect(JSON.parse(writes[0][1]!.body as string)).toEqual({ revision: 3, suggestion_ids: ['s-new'] })
  })
  it('shows service failure rather than an empty member results page', async () => {
    show('/app/automations', 'member', (path) => path === '/suggestions' ? failure('ha_unavailable', 503) : undefined)
    expect(await screen.findByRole('alert')).toHaveTextContent('HA')
    expect(screen.queryByText('暂无已共享结果')).not.toBeInTheDocument()
  })
})

describe('accurate family status and collection', () => {
  it('shows history batch failure separately from a successful snapshot and unknown complete success', async () => {
    show('/app/admin/collection', 'admin')
    expect(await screen.findByText('2 / 4 批')).toBeInTheDocument()
    expect(screen.getByText('历史补采失败')).toBeInTheDocument()
    expect(screen.getByText('状态快照已保存')).toBeInTheDocument()
    expect(screen.getByText('尚无完整采集成功记录')).toBeInTheDocument()
  })
  it('prevents repeated analysis clicks while waiting and only requests 14 days', async () => {
    let release!: (response: Response) => void
    const task = new Promise<Response>((resolve) => { release = resolve })
    const fetcher = show('/app/admin/collection', 'admin', (path) => path === '/admin/analyze' ? task : undefined)
    fireEvent.click(await screen.findByRole('button', { name: '分析最近 14 天' })); fireEvent.click(screen.getByRole('button', { name: '分析中…' }))
    expect(fetcher.mock.calls.filter(([url]) => url.endsWith('/admin/analyze'))).toHaveLength(1)
    await act(async () => release(Response.json({ period_start: '2026-09-25T00:00:00Z', period_end: '2026-10-09T00:00:00Z', suggestion_count: 2 })))
    expect(await screen.findByText('分析完成，产生 2 条建议；尚未安装自动化。')).toBeInTheDocument()
    const options = fetcher.mock.calls.find(([url]) => url.endsWith('/admin/analyze'))![1]
    expect(JSON.parse(options!.body as string)).toEqual({ days: 14 })
  })
  it('keeps family login on HA auth failure and leaves model installation unknown', async () => {
    show('/app/', 'member', (path) => path === '/integrations/status' ? Response.json({ ha: { ...meta, connection: 'unavailable', freshness: 'stale', error_code: 'ha_auth_required' }, ollama: { connection: 'unavailable', checked_at: '2026-10-09T02:00:00Z', models: [{ name: 'local-chat', available: null }], error_code: 'ollama_unavailable' } }) : undefined)
    expect(await screen.findByText('家庭设备服务授权失效，请联系管理员。')).toBeInTheDocument()
    expect(screen.getByText('local-chat 暂无法确认')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '登录' })).not.toBeInTheDocument()
    expect(screen.queryByText('local-chat 未安装')).not.toBeInTheDocument()
  })
  it('explains door and tamper while preserving unknown binary values', async () => {
    show('/app/areas/living/devices/controller', 'member', (path) => path.endsWith('/devices/controller') ? Response.json({ meta, area: devices.area, device: { ...devices.devices[0], entities: [{ ...presence, entity_id: 'binary_sensor.door', name: '门磁', device_class: 'door' }, { ...presence, entity_id: 'binary_sensor.tamper', name: '防拆', device_class: 'tamper' }, { ...presence, entity_id: 'binary_sensor.unknown', name: '未知传感器', device_class: null }] } }) : undefined)
    expect(await screen.findByText('打开')).toBeInTheDocument()
    expect(screen.getByText('检测到防拆')).toBeInTheDocument()
    expect(screen.getByText('on')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /开启|关闭|控制/ })).not.toBeInTheDocument()
  })
})
it('does not navigate to a delayed binding version after the administrator chose another suggestion', async () => {
  let release!: (response: Response) => void
  const task = new Promise<Response>((resolve) => { release = resolve })
  show('/app/automations?id=s-old', 'admin', (path) => path === '/suggestions' ? Response.json({ suggestions: [pending, { ...pending, id: 's-other', title: '另一条建议' }], count: 2 }) : path.endsWith('/bindings') ? task : undefined)
  fireEvent.click(await screen.findByRole('button', { name: '补齐有人在家条件' }))
  fireEvent.change(await screen.findByLabelText('代表全家的占用实体'), { target: { value: 'binary_sensor.home' } })
  fireEvent.change(screen.getByLabelText('有人在家时的状态'), { target: { value: 'on' } })
  fireEvent.click(screen.getByRole('button', { name: '保存绑定并查看新版本' }))
  fireEvent.click(screen.getByRole('link', { name: '返回自动化结果' }))
  fireEvent.click(await screen.findByRole('link', { name: /另一条建议/ }))
  await act(async () => release(Response.json({ suggestion: bound })))
  expect(screen.getByRole('heading', { name: '另一条建议' })).toBeInTheDocument()
  expect(screen.queryByText('新版本：s-new')).not.toBeInTheDocument()
})

it('freezes the sharing revision together with the edited selection across polling', async () => {
  let revision = 3
  const fetcher = show('/app/automations', 'admin', (path, options) => path === '/suggestions' ? Response.json({ suggestions: [confirmed], count: 1 }) : path === '/admin/sharing' ? options.method === 'PUT' ? failure('conflict', 409) : Response.json({ revision, suggestion_ids: [] }) : undefined)
  fireEvent.click(await screen.findByLabelText('共享：晚间灯光建议'))
  revision = 4
  fireEvent(document, new Event('visibilitychange'))
  await waitFor(() => expect(fetcher.mock.calls.filter(([url, options]) => url.endsWith('/admin/sharing') && options?.method !== 'PUT').length).toBeGreaterThan(1))
  fireEvent.click(screen.getByRole('button', { name: '保存结果共享' }))
  const write = fetcher.mock.calls.find(([url, options]) => url.endsWith('/admin/sharing') && options?.method === 'PUT')!
  expect(JSON.parse(write[1]!.body as string)).toEqual({ revision: 3, suggestion_ids: ['s-new'] })
})

it('allows a fresh server result to replace a locally returned binding version', async () => {
  let updated = false
  show('/app/automations?id=s-old', 'admin', (path) => path === '/suggestions' ? Response.json({ suggestions: updated ? [{ ...bound, status: 'ignored', can_confirm: false, can_ignore: false }] : [pending], count: 1 }) : path.endsWith('/bindings') ? Response.json({ suggestion: bound }) : undefined)
  fireEvent.click(await screen.findByRole('button', { name: '补齐有人在家条件' }))
  fireEvent.change(await screen.findByLabelText('代表全家的占用实体'), { target: { value: 'binary_sensor.home' } })
  fireEvent.change(screen.getByLabelText('有人在家时的状态'), { target: { value: 'on' } })
  fireEvent.click(screen.getByRole('button', { name: '保存绑定并查看新版本' }))
  await screen.findByText('新版本：s-new')
  updated = true
  fireEvent(document, new Event('visibilitychange'))
  expect(await screen.findByText('已忽略，未执行')).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: '确认创建自动化' })).not.toBeInTheDocument()
})

it('does not label an old snapshot as evidence that the current failure was in history', async () => {
  show('/app/admin/collection', 'admin', (path) => path === '/admin/collection' ? Response.json({ ...collection, snapshot_at: '2026-10-08T01:00:00Z' }) : undefined)
  expect(await screen.findByRole('heading', { name: '采集失败' })).toBeInTheDocument()
  expect(screen.queryByText('历史补采失败')).not.toBeInTheDocument()
})


it('clears administrator content when the binding entity read reports a revoked login', async () => {
  let revoked = false
  show('/app/automations?id=s-old', 'admin', (path) => revoked && path === '/areas' ? failure('unauthenticated', 401) : undefined)
  await screen.findByRole('button', { name: '补齐有人在家条件' })
  revoked = true
  fireEvent.click(screen.getByRole('button', { name: '补齐有人在家条件' }))
  expect(await screen.findByRole('button', { name: '登录' })).toBeInTheDocument()
  expect(screen.queryByText('晚间灯光建议')).not.toBeInTheDocument()
})


it('requires a successful version check after a confirmation conflict before another explicit confirmation', async () => {
  let writes = 0
  const fetcher = show('/app/automations?id=s-new', 'admin', (path) => path === '/suggestions' ? Response.json({ suggestions: [bound], count: 1 }) : path.endsWith('/confirm') ? ++writes === 1 ? failure('conflict', 409) : Response.json({ suggestion: confirmed }) : undefined)
  fireEvent.click(await screen.findByRole('button', { name: '确认创建自动化' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('刷新')
  expect(screen.getByRole('button', { name: '确认创建自动化' })).toBeDisabled()
  fireEvent.click(screen.getByRole('button', { name: '核对最新建议状态' }))
  await waitFor(() => expect(screen.getByRole('button', { name: '确认创建自动化' })).toBeEnabled())
  expect(fetcher.mock.calls.filter(([url]) => url.endsWith('/confirm'))).toHaveLength(1)
  fireEvent.click(screen.getByRole('button', { name: '确认创建自动化' }))
  expect(await screen.findByText('已确认安装并归档')).toBeInTheDocument()
})


it('shows administrator collection failure and its detail entry on the home page', async () => {
  show('/app/', 'admin')
  expect(await screen.findByRole('heading', { name: '采集状态与异常' })).toBeInTheDocument()
  expect(await screen.findByText('本轮采集未完成，请查看详情。')).toBeInTheDocument()
  expect(screen.getByRole('link', { name: '采集详情与分析' })).toHaveAttribute('href', '/app/admin/collection')
})

it('does not infer current process collection success from a checkpoint on the home page', async () => {
  show('/app/', 'admin', (path) => path === '/admin/collection' ? Response.json({ ...collection, phase: 'idle', last_attempt_at: null, last_failure_at: null, snapshot_at: null, error_code: null }) : undefined)
  expect(await screen.findByText('尚无本进程完整采集成功记录')).toBeInTheDocument()
  expect(screen.queryByText('本轮采集已完成')).not.toBeInTheDocument()
})

it('never reads administrator collection from the member home even with an unexpected capability', async () => {
  const fetcher = show('/app/', 'member', (path) => path === '/auth/me' ? Response.json({ user: member, capabilities: [...caps, 'collection:read'], csrf_token: 'csrf-test' }) : undefined)
  await screen.findByRole('heading', { name: '家庭概况' })
  expect(fetcher.mock.calls.some(([url]) => url.endsWith('/admin/collection'))).toBe(false)
  expect(screen.queryByRole('link', { name: '采集详情与分析' })).not.toBeInTheDocument()
})


it.each(['broken', 'missing'])('allows withdrawal of a %s legacy shared ID while preserving the edited revision', async (kind) => {
  let revision = 3
  const legacyId = kind === 'broken' ? 's-broken' : 's-missing'
  const legacy = { ...confirmed, id: legacyId, title: '旧结果', rule: null, unsupported_code: 'invalid_rule' }
  const fetcher = show('/app/automations', 'admin', (path, options) => path === '/suggestions' ? Response.json({ suggestions: kind === 'broken' ? [confirmed, legacy] : [confirmed], count: kind === 'broken' ? 2 : 1 }) : path === '/admin/sharing' ? options.method === 'PUT' ? Response.json({ revision: 5, suggestion_ids: ['s-new'] }) : Response.json({ revision, suggestion_ids: ['s-new', legacyId] }) : undefined)
  fireEvent.click(await screen.findByRole('button', { name: '撤销旧授权：' + legacyId }))
  expect(screen.getByRole('button', { name: '撤销旧授权：' + legacyId })).toBeDisabled()
  expect(screen.getByLabelText('共享：晚间灯光建议')).toBeChecked()
  revision = 4
  fireEvent(document, new Event('visibilitychange'))
  await waitFor(() => expect(fetcher.mock.calls.filter(([url, options]) => url.endsWith('/admin/sharing') && options?.method !== 'PUT').length).toBeGreaterThan(1))
  fireEvent.click(screen.getByRole('button', { name: '保存结果共享' }))
  const write = fetcher.mock.calls.find(([url, options]) => url.endsWith('/admin/sharing') && options?.method === 'PUT')!
  expect(JSON.parse(write[1]!.body as string)).toEqual({ revision: 3, suggestion_ids: ['s-new'] })
  expect(fetcher.mock.calls.some(([url]) => url.endsWith('/confirm'))).toBe(false)
})


it('removes a previous successful analysis result when a new analysis attempt fails', async () => {
  let attempts = 0
  show('/app/admin/collection', 'admin', (path) => path === '/admin/analyze' ? ++attempts === 1 ? Response.json({ period_start: '2026-09-25T00:00:00Z', period_end: '2026-10-09T00:00:00Z', suggestion_count: 2 }) : failure('ha_unavailable', 503) : undefined)
  fireEvent.click(await screen.findByRole('button', { name: '分析最近 14 天' }))
  await screen.findByText('分析完成，产生 2 条建议；尚未安装自动化。')
  fireEvent.click(screen.getByRole('button', { name: '分析最近 14 天' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('HA')
  expect(screen.queryByText('分析完成，产生 2 条建议；尚未安装自动化。')).not.toBeInTheDocument()
})
