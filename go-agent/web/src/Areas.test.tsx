import { act, fireEvent, render, screen, within } from '@testing-library/react'
import { MemoryRouter, useLocation, useNavigate } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { App } from './App'

const meta = { observed_at: '2026-10-08T07:00:00Z', last_attempt_at: '2026-10-08T07:00:00Z', last_success_at: '2026-10-08T07:00:00Z', freshness: 'fresh', connection: 'connected', error_code: null }
const owner = { id: 'a1', username: 'owner', display_name: '家长', role: 'admin', must_change_password: false }
const member = { ...owner, id: 'm1', username: 'mei', display_name: '梅', role: 'member' }
const identity = (user = member) => ({ user, csrf_token: 'csrf-1', capabilities: ['chat:use', 'sessions:read', 'areas:read', ...(user.role === 'admin' ? ['members:manage'] : [])] })
const areas = { meta, totals: { devices: 2, entities: 4, standalone_entities: 1 }, areas: [
  { id: 'a_bGl2aW5n', name: '客厅', device_count: 1, entity_count: 2, standalone_entity_count: 0, matches: [] },
  { id: 'u_other', name: '其他', device_count: 1, entity_count: 2, standalone_entity_count: 1, matches: [] },
  { id: 'a_ZW1wdHk', name: '空房间', device_count: 0, entity_count: 0, standalone_entity_count: 0, matches: [] },
] }
const entity = (id: string, changes: object = {}) => ({ entity_id: id, name: '开关 1', domain: 'switch', state: 'on', unit: null, last_changed: '2026-10-08T06:00:00Z', last_updated: '2026-10-08T06:50:00Z', disabled: false, hidden: false, category: null, ...changes })
const controller = { id: 'd_Y29udHJvbGxlcg', kind: 'device', name: '客厅三路开关', area_id: 'a_bGl2aW5n', direct_area_id: 'a_bGl2aW5n', membership: 'both', entity_count: 2, domains: ['switch'], entities: [entity('switch.channel_1'), entity('switch.channel_2', { name: '开关 2', state: 'unavailable' })], load_location_verified: false }
const loose = { id: 'e_c2Vuc29yLnRlbXBlcmF0dXJl', kind: 'entity', name: '独立温度计', area_id: 'u_other', direct_area_id: null, membership: 'entity', entity_count: 1, domains: ['sensor'], entities: [entity('sensor.temperature', { name: '温度', domain: 'sensor', state: '23.5', unit: '°C' })], load_location_verified: false }
const integrations = { ha: meta, ollama: { connection: 'connected', checked_at: '2026-10-08T07:00:00Z', models: [{ name: 'gemma4:12b', available: true }, { name: 'bge-m3:latest', available: false }], error_code: null } }
function json(value: unknown, status = 200) { return Response.json(value, { status }) }
function Jump() { const navigate = useNavigate(); return <button onClick={() => navigate('/app/areas/a_a2l0Y2hlbg')}>测试切换厨房</button> }
function show(path = '/app/areas', jump = false) { return render(<MemoryRouter initialEntries={[path]}>{jump && <Jump/>}<App/></MemoryRouter>) }
type Transport = (url: string, options: RequestInit) => Promise<Response>
function transport(custom?: Transport, user = member) {
  vi.stubGlobal('fetch', vi.fn(async (url: string, options: RequestInit) => {
    if (url.endsWith('/auth/me')) return json(identity(user))
    if (url.endsWith('/sessions')) return json({ sessions: [] })
    if (url.endsWith('/integrations/status')) return json(integrations)
    if (custom) return custom(url, options)
    if (url.endsWith('/areas')) return json(areas)
    if (url.endsWith('/areas/a_bGl2aW5n/devices/d_Y29udHJvbGxlcg')) return json({ meta, area: { id: 'a_bGl2aW5n', name: '客厅' }, device: controller })
    if (url.endsWith('/areas/a_bGl2aW5n/devices')) return json({ meta, area: { id: 'a_bGl2aW5n', name: '客厅' }, devices: [controller] })
    if (url.endsWith('/areas/u_other/devices')) return json({ meta, area: { id: 'u_other', name: '其他' }, devices: [loose] })
    if (url.endsWith('/areas/a_ZW1wdHk/devices')) return json({ meta, area: { id: 'a_ZW1wdHk', name: '空房间' }, devices: [] })
    throw new Error(`unexpected family request ${url}`)
  }))
}
afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals(); Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' }) })

describe('family directory pages', () => {
  it('navigates from areas to a controller and channels without counting channels as devices', async () => {
    transport(); show()
    expect(await screen.findByRole('heading', { name: '家庭区域' })).toBeInTheDocument()
    expect(await screen.findByText('2 个设备注册项')).toBeInTheDocument(); expect(screen.getByText('4 个实体')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('link', { name: /客厅.*1 个设备注册项.*2 个实体/ }))
    expect(await screen.findByRole('heading', { name: '客厅' })).toBeInTheDocument()
    fireEvent.click(screen.getByRole('link', { name: /客厅三路开关/ }))
    expect(await screen.findByRole('heading', { name: '客厅三路开关' })).toBeInTheDocument()
    expect(screen.getByText('switch.channel_1')).toBeInTheDocument(); expect(screen.getByText('不可用')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /开灯|关灯|开启|关闭设备/ })).not.toBeInTheDocument()
  })
  it('keeps other and empty areas real and labels standalone entities separately', async () => {
    transport(); show(); fireEvent.click(await screen.findByRole('link', { name: /空房间.*0 个设备注册项/ }))
    expect(await screen.findByText('这个区域还没有设备或独立实体。')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('link', { name: '全部区域' })); fireEvent.click(await screen.findByRole('link', { name: /其他.*1 个设备注册项/ }))
    expect(await screen.findByRole('heading', { name: '独立实体' })).toBeInTheDocument(); expect(screen.getByRole('link', { name: /独立温度计/ })).toBeInTheDocument()
  })
  it('searches the server and keeps whole-house totals separate from matching areas', async () => {
    transport(async (url) => {
      if (url.endsWith('/areas')) return json(areas)
      if (url.endsWith('/areas?q=switch.channel_1')) return json({ ...areas, areas: [{ ...areas.areas[0], matches: [{ id: controller.id, name: '客厅三路开关', kind: 'device' }] }] })
      throw new Error(`unexpected query ${url}`)
    }); show(); await screen.findByRole('heading', { name: '家庭区域' })
    fireEvent.change(screen.getByRole('searchbox', { name: '搜索区域、设备或实体' }), { target: { value: 'switch.channel_1' } }); fireEvent.click(screen.getByRole('button', { name: '搜索' }))
    expect(await screen.findByRole('link', { name: /客厅三路开关/ })).toBeInTheDocument(); expect(screen.getByText('2 个设备注册项')).toBeInTheDocument(); expect(screen.getByText('匹配 1 个区域')).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: /其他.*设备注册项/ })).not.toBeInTheDocument()
  })
  it('marks entity overrides and does not claim a verified physical load location', async () => {
    transport(async () => json({ meta, area: { id: 'a_bGl2aW5n', name: '客厅' }, device: { ...controller, membership: 'entity', direct_area_id: 'a_a2l0Y2hlbg' } })); show('/app/areas/a_bGl2aW5n/devices/d_Y29udHJvbGxlcg')
    expect(await screen.findByText('实体覆盖到此区域')).toBeInTheDocument(); expect(screen.getByText(/控制器归属；负载地点未验证/)).toBeInTheDocument()
  })
  it('keeps a directly assigned controller with no local entities', async () => {
    transport(async () => json({ meta, area: { id: 'a_bGl2aW5n', name: '客厅' }, device: { ...controller, membership: 'direct', entities: [], entity_count: 0, domains: [] } })); show('/app/areas/a_bGl2aW5n/devices/d_Y29udHJvbGxlcg')
    expect(await screen.findByText('此区域没有该设备的实体，实体可能已覆盖到其他区域。')).toBeInTheDocument(); expect(screen.queryByText('switch.channel_1')).not.toBeInTheDocument()
  })
  it('preserves null and disabled/hidden states with expandable diagnostic entities', async () => {
    const diagnostic = entity('sensor.diagnostic', { name: '诊断信息', domain: 'sensor', category: 'diagnostic', state: null, last_changed: null, last_updated: null, disabled: true, hidden: true })
    transport(async () => json({ meta, area: { id: 'a_bGl2aW5n', name: '客厅' }, device: { ...controller, entities: [diagnostic], entity_count: 1, domains: ['sensor'] } })); show('/app/areas/a_bGl2aW5n/devices/d_Y29udHJvbGxlcg')
    const details = await screen.findByText('配置与诊断实体（1）'); expect(details.closest('details')).not.toHaveAttribute('open'); fireEvent.click(details); expect(details.closest('details')).toHaveAttribute('open')
    expect(screen.getByText('暂无状态')).toBeInTheDocument(); expect(screen.getByText('已禁用')).toBeInTheDocument(); expect(screen.getByText('已隐藏')).toBeInTheDocument(); expect(screen.getAllByText('暂无记录')).toHaveLength(2)
  })
  it('shows dated stale data and never claims a failed HA check is connected', async () => {
    transport(async () => json({ ...areas, meta: { ...meta, freshness: 'stale', connection: 'unavailable', last_attempt_at: '2026-10-08T07:01:00Z', error_code: 'ha_unavailable' } })); show()
    expect(await screen.findByText('正在显示旧数据')).toBeInTheDocument(); expect(screen.getByText('HA 暂时无法连接')).toBeInTheDocument(); expect(screen.getByRole('link', { name: /客厅.*设备注册项/ })).toBeInTheDocument(); expect(screen.queryByText('HA 最近检查成功')).not.toBeInTheDocument()
  })
  it('retries initial HA failure without inventing cards', async () => {
    let succeed = false; transport(async () => succeed ? json(areas) : json({ error: { code: 'ha_unavailable', request_id: 'safe-1' } }, 503)); show()
    expect(await screen.findByText(/家庭目录暂时不可用/)).toBeInTheDocument(); expect(screen.queryByRole('link', { name: /客厅.*设备注册项/ })).not.toBeInTheDocument(); succeed = true; fireEvent.click(screen.getByRole('button', { name: '重新加载' }))
    expect(await screen.findByRole('link', { name: /客厅.*设备注册项/ })).toBeInTheDocument()
  })
  it('reports an unknown area/device combination on direct navigation', async () => {
    transport(async () => json({ error: { code: 'not_found' } }, 404)); show('/app/areas/a_bGl2aW5n/devices/d_d3Jvbmc')
    expect(await screen.findByText('未找到这个区域或设备。')).toBeInTheDocument(); expect(screen.getByRole('link', { name: '全部区域' })).toBeInTheDocument()
  })
  it('drops late old-account directory data after sign-out', async () => {
    let release!: (response: Response) => void; const delayed = new Promise<Response>((resolve) => { release = resolve })
    transport(async (url) => { if (url.endsWith('/areas')) return delayed; if (url.endsWith('/auth/logout')) return new Response(null, { status: 204 }); throw new Error(`unexpected ${url}`) }); show()
    fireEvent.click((await screen.findAllByRole('button', { name: '退出登录' }))[0]); await screen.findByRole('button', { name: '登录' }); await act(async () => release(json({ ...areas, areas: [{ ...areas.areas[0], name: 'OLD-ACCOUNT-AREA' }] })))
    expect(screen.queryByText('OLD-ACCOUNT-AREA')).not.toBeInTheDocument()
  })
  it('pauses hidden polling and aborts the departing route', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    const requests: { path: string; signal?: AbortSignal | null }[] = []
    transport(async (url, options) => { requests.push({ path: url, signal: options.signal }); return json(areas) }); show(); await screen.findByRole('link', { name: /客厅.*设备注册项/ }); const initial = requests.length
    await act(async () => vi.advanceTimersByTimeAsync(30000)); expect(requests.length).toBe(initial + 1)
    Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'hidden' }); fireEvent(document, new Event('visibilitychange')); await act(async () => vi.advanceTimersByTimeAsync(60000)); expect(requests.length).toBe(initial + 1)
    Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' }); fireEvent(document, new Event('visibilitychange')); await act(async () => Promise.resolve()); expect(requests.length).toBe(initial + 2)
    fireEvent.click(within(screen.getByRole('navigation', { name: '主导航' })).getByRole('link', { name: '我的' })); expect(requests.at(-1)?.signal?.aborted).toBe(true)
  })
  it('shows service/model checks and moves mobile member management into account', async () => {
    transport(undefined, owner); show('/app/'); expect(await screen.findByText('家庭概况')).toBeInTheDocument(); expect(await screen.findByText('bge-m3:latest 未安装')).toBeInTheDocument(); expect(screen.getByText('安装检查不代表实际生成已验证。')).toBeInTheDocument()
    const bottom = within(screen.getByRole('navigation', { name: '底部导航' })); expect(bottom.queryByRole('link', { name: '成员' })).not.toBeInTheDocument(); expect(bottom.getByRole('link', { name: '设备' })).toBeInTheDocument(); fireEvent.click(bottom.getByRole('link', { name: '我的' })); expect(await screen.findByRole('link', { name: /成员管理/ })).toBeInTheDocument()
  })
})

it('filters by domain and resets that filter when another area is opened', async () => {
  transport(async (url) => url.endsWith('/areas/a_a2l0Y2hlbg/devices') ? json({ meta, area: { id: 'a_a2l0Y2hlbg', name: '厨房' }, devices: [{ ...controller, name: '厨房继承开关', area_id: 'a_a2l0Y2hlbg' }] }) : json({ meta, area: { id: 'a_bGl2aW5n', name: '客厅' }, devices: [controller, { ...loose, area_id: 'a_bGl2aW5n' }] }))
  show('/app/areas/a_bGl2aW5n', true)
  await screen.findByRole('link', { name: /客厅三路开关/ })
  fireEvent.change(screen.getByRole('combobox', { name: '设备类型筛选' }), { target: { value: 'sensor' } })
  expect(screen.queryByRole('link', { name: /客厅三路开关/ })).not.toBeInTheDocument()
  expect(screen.getByRole('link', { name: /独立温度计/ })).toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', { name: '测试切换厨房' }))
  expect(await screen.findByRole('link', { name: /厨房继承开关/ })).toBeInTheDocument()
  expect(screen.getByRole('combobox', { name: '设备类型筛选' })).toHaveValue('')
})

it('rejects a late departing-area response after the new area has loaded', async () => {
  let release!: (response: Response) => void
  let departedSignal: AbortSignal | null | undefined
  const delayed = new Promise<Response>((resolve) => { release = resolve })
  transport(async (url, options) => {
    if (url.endsWith('/areas/a_bGl2aW5n/devices')) { departedSignal = options.signal; return delayed }
    return json({ meta, area: { id: 'a_a2l0Y2hlbg', name: '厨房' }, devices: [{ ...controller, name: 'CURRENT-KITCHEN' }] })
  })
  show('/app/areas/a_bGl2aW5n', true)
  await screen.findByText('正在读取家庭目录…')
  fireEvent.click(screen.getByRole('button', { name: '测试切换厨房' }))
  await screen.findByRole('link', { name: /CURRENT-KITCHEN/ })
  expect(departedSignal?.aborted).toBe(true)
  await act(async () => release(json({ meta, area: { id: 'a_bGl2aW5n', name: 'OLD-AREA' }, devices: [{ ...controller, name: 'OLD-CONTROLLER' }] })))
  expect(screen.queryByText('OLD-AREA')).not.toBeInTheDocument()
  expect(screen.queryByText('OLD-CONTROLLER')).not.toBeInTheDocument()
  expect(screen.getByRole('heading', { name: '厨房' })).toBeInTheDocument()
})

it('rejects an old response after a different account has signed in and loaded new directory data', async () => {
  let release!: (response: Response) => void
  let count = 0
  const delayed = new Promise<Response>((resolve) => { release = resolve })
  transport(async (url) => {
    if (url.endsWith('/areas')) return ++count === 1 ? delayed : json({ ...areas, areas: [{ ...areas.areas[0], name: 'NEW-ACCOUNT-AREA' }] })
    if (url.endsWith('/auth/logout')) return new Response(null, { status: 204 })
    if (url.endsWith('/auth/login')) return json(identity({ ...member, id: 'm2', display_name: '新成员' }))
    throw new Error(`unexpected ${url}`)
  }); show()
  await screen.findByText('正在读取家庭目录…')
  fireEvent.click(screen.getAllByRole('button', { name: '退出登录' })[0])
  fireEvent.change(await screen.findByLabelText('用户名'), { target: { value: 'new-member' } })
  fireEvent.change(screen.getByLabelText('密码'), { target: { value: 'new-password-123' } })
  fireEvent.click(screen.getByRole('button', { name: '登录' }))
  expect(await screen.findByRole('link', { name: /NEW-ACCOUNT-AREA/ })).toBeInTheDocument()
  await act(async () => release(json({ ...areas, areas: [{ ...areas.areas[0], name: 'OLD-ACCOUNT-AREA' }] })))
  expect(screen.queryByText('OLD-ACCOUNT-AREA')).not.toBeInTheDocument()
  expect(screen.getByRole('link', { name: /NEW-ACCOUNT-AREA/ })).toBeInTheDocument()
})

it('keeps existing data dated and marks a failed browser refresh as stale', async () => {
  let count = 0
  transport(async () => ++count === 1 ? json(areas) : json({ error: { code: 'ha_unavailable' } }, 503))
  vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] }); show()
  await screen.findByRole('link', { name: /客厅.*设备注册项/ })
  await act(async () => vi.advanceTimersByTimeAsync(30000))
  expect(screen.getByText('正在显示旧数据')).toBeInTheDocument()
  expect(screen.getByText('HA 暂时无法连接')).toBeInTheDocument()
  expect(screen.getByRole('link', { name: /客厅.*设备注册项/ })).toBeInTheDocument()
  expect(screen.queryByText('HA 最近检查成功')).not.toBeInTheDocument()
})

it('denies a direct family URL when the identity has no areas capability', async () => {
  vi.stubGlobal('fetch', vi.fn(async (url: string) => {
    if (url.endsWith('/auth/me')) return json({ ...identity(), capabilities: [] })
    throw new Error(`must not read family data ${url}`)
  }))
  show('/app/areas/a_bGl2aW5n/devices/d_Y29udHJvbGxlcg')
  expect(await screen.findByRole('heading', { name: '当前无法查看家庭目录' })).toBeInTheDocument()
  expect(screen.queryByRole('link', { name: '设备' })).not.toBeInTheDocument()
})

it('retains existing dated directory data when an explicit refresh fails', async () => {
  let count = 0
  transport(async () => ++count === 1 ? json(areas) : json({ error: { code: 'unavailable' } }, 503)); show()
  await screen.findByRole('link', { name: /客厅.*设备注册项/ })
  fireEvent.click(screen.getByRole('button', { name: '重新检查' }))
  expect(await screen.findByText('正在显示旧数据')).toBeInTheDocument()
  expect(screen.getByRole('link', { name: /客厅.*设备注册项/ })).toBeInTheDocument()
})

it('synchronizes the search draft and actual results when router history moves back and forward', async () => {
  function HistoryNavigation() {
    const navigate = useNavigate()
    const location = useLocation()
    return <><button onClick={() => navigate(-1)}>测试历史返回</button><button onClick={() => navigate(1)}>测试历史前进</button><output aria-label="测试搜索位置">{location.pathname}{location.search}</output></>
  }
  transport(async (url) => {
    const query = new URL(url, 'http://fixture.local').searchParams.get('q')
    return json({ ...areas, areas: [{ ...areas.areas[0], name: query ? `RESULT-${query}` : '客厅' }] })
  })
  render(<MemoryRouter initialEntries={['/app/areas']}><HistoryNavigation/><App/></MemoryRouter>)
  const search = await screen.findByRole('searchbox', { name: '搜索区域、设备或实体' })
  fireEvent.change(search, { target: { value: 'A' } }); fireEvent.click(screen.getByRole('button', { name: /^搜索$/ }))
  await screen.findByRole('link', { name: /RESULT-A/ })
  fireEvent.change(search, { target: { value: 'B' } }); fireEvent.click(screen.getByRole('button', { name: /^搜索$/ }))
  await screen.findByRole('link', { name: /RESULT-B/ })
  fireEvent.click(screen.getByRole('button', { name: '测试历史返回' }))
  await screen.findByRole('link', { name: /RESULT-A/ })
  expect(screen.getByLabelText('测试搜索位置')).toHaveTextContent('/app/areas?q=A')
  expect(search).toHaveValue('A')
  fireEvent.click(screen.getByRole('button', { name: '测试历史前进' }))
  await screen.findByRole('link', { name: /RESULT-B/ })
  expect(screen.getByLabelText('测试搜索位置')).toHaveTextContent('/app/areas?q=B')
  expect(search).toHaveValue('B')
  fireEvent.click(screen.getByRole('button', { name: '测试历史返回' }))
  await screen.findByRole('link', { name: /RESULT-A/ })
  fireEvent.click(screen.getByRole('button', { name: /^搜索$/ }))
  expect(search).toHaveValue('A')
  expect(screen.getByLabelText('测试搜索位置')).toHaveTextContent('/app/areas?q=A')
  expect(screen.queryByRole('link', { name: /RESULT-B/ })).not.toBeInTheDocument()
})
