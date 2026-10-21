import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, expect, it, vi } from 'vitest'
import { App } from '../App'

const observed = '2026-10-15T00:00:00Z'
const snapshot = { id: 'diagnostic', ref: { kind: 'managed', id: 'managed' }, checked_at: observed, window: { start: '2026-10-14T00:00:00Z', end: observed }, summary: 'unknown', checks: [{ status: 'unknown', category: 'execution', severity: 'info', reason: 'run_evidence_missing', next_action: 'refresh_sources', evidence_refs: [] }], changes: [], counts: { pass: 0, finding: 0, unknown: 1, changes: 0 }, sources: { trace: { status: 'unsupported', observed_at: observed } } }
const managed = { id: 'managed', suggestion_id: 'installed', ref: { kind: 'managed', id: 'managed' }, title: '客厅灯定时开启', ownership: 'managed', installation_state: 'verified', observed_state: 'off', observed_at: observed, stale: false, diagnostic: snapshot, can_diagnose: true, ha_url: 'http://ha.local/config/automation/dashboard' }
const legacy = { id: 'legacy', suggestion_id: 'legacy', title: '旧安装记录', ownership: 'unverified', installation_state: 'unknown', observed_state: 'unknown', stale: true, can_diagnose: false }
const ignored = { id: 'ignored', status: 'ignored', created_at: observed, title: '已忽略的建议', entity_ids: [], entities: [], rule: null, evidence: {}, shared: false, can_bind: false, can_confirm: false, can_ignore: false, missing_bindings: [], unsupported_code: null }
const sharedResult = {id:'installed',title:'原始客厅共享结果',created_at:observed,time_zone:'UTC',historical:true,history_status:'updated',shared:true,entity_ids:['light.original'],entities:[],evidence:{sample_size:null,period_days:14},rule:{triggers:[{platform:'time',at:'18:00'}],conditions:[],actions:[{service:'light.turn_on',target:{entity_id:'light.original'}}]}}
function show(member = false) {
 const posts: string[] = []
 let failRead = false
 let failStatus = 503
 let failNetwork = false
 let override: Record<string,unknown> | undefined
 let revision = 1
 let shared:Record<string,unknown>[]=[sharedResult]
 vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
  if (url.endsWith('/auth/me')) return Response.json({ user: { id: member ? 'member' : 'admin', username: 'user', display_name: '用户', role: member ? 'member' : 'admin', must_change_password: false }, capabilities: member ? ['suggestions:read'] : ['suggestions:read', 'suggestions:manage'], csrf_token: 'test' })
  if (url.endsWith('/auth/logout')) return new Response(null,{status:204})
  if ((url.endsWith('/suggestions') || url.endsWith('/automations')) && failNetwork) throw new Error('network')
  if (url.endsWith('/suggestions') && failRead) return Response.json({error:{code:'unavailable'}},{status:failStatus})
  if (url.endsWith('/suggestions')) return Response.json({ suggestions: [ignored], count: 1, shared_results:shared })
  if (url.endsWith('/admin/sharing')) return Response.json({ revision: 0, suggestion_ids: [] })
  if (url.endsWith('/automations')) return failRead ? Response.json({ error: { code: 'unavailable' } }, { status: failStatus }) : Response.json({ sharing_revision:revision, automations: override ? [{...managed,...override}] : member ? [{ ...managed, can_diagnose: false, ha_url: undefined }] : [managed, legacy], external_candidates: member ? [] : [{ ref: { kind: 'external', id: 'automation.external' }, title: '外部走廊规则', ha_url: 'http://ha.local/config/automation/dashboard' }], counts: { installed: 1, attention: 2, unverified: 1 } })
  if (init?.method === 'POST') { posts.push(url); if (url.endsWith('/diagnostics') || url.endsWith('/automation-diagnostics')) return Response.json({ diagnostic: snapshot, partial: true }) }
  return Response.json({ error: { code: 'not_found' } }, { status: 404 })
 }))
 render(<MemoryRouter initialEntries={['/app/automations']}><App/></MemoryRouter>)
 return { posts, share:(rows:Record<string,unknown>[])=>{shared=rows}, fail: (status=503) => { failRead = true;failStatus=status }, network:()=>{failNetwork=true}, recover:()=>{failNetwork=false;failRead=false}, replace:(row:Record<string,unknown>,newRevision=revision)=>{override=row;revision=newRevision} }
}
afterEach(() => vi.unstubAllGlobals())
it('opens an explicitly shared immutable result separately from current installation counts and controls',async()=>{
 const fixture=show(true)
 fireEvent.click(await screen.findByRole('link',{name:/原始客厅共享结果/}))
 expect(await screen.findByText(/管理员共享的历史确认结果/)).toBeVisible()
 expect(screen.getByText(/当前规则已更新/)).toBeVisible()
 expect(screen.getByText('每天 18:00')).toBeVisible()
 expect(screen.queryByText('客厅灯定时开启')).not.toBeInTheDocument()
 expect(screen.queryByRole('button',{name:/确认|只读诊断|准备|启用|停用|删除/})).not.toBeInTheDocument()
 expect(fixture.posts).toHaveLength(0)
 fireEvent.click(screen.getByRole('link',{name:'返回共享结果'}))
 expect(screen.getByRole('button',{name:'已共享结果（1）'})).toBeVisible()
 expect(screen.getByRole('button',{name:'已安装（1）'})).toBeVisible()
})
it('keeps historical reads stale on availability failure but clears them through denial, network failure and removal',async()=>{
 const fixture=show(true)
 fireEvent.click(await screen.findByRole('link',{name:/原始客厅共享结果/}))
 fixture.fail(503);fireEvent(document,new Event('visibilitychange'))
 expect(await screen.findByText(/共享结果可能已过期/)).toBeVisible()
 expect(screen.getByText(/管理员共享的历史确认结果/)).toBeVisible()
 for(const fail of [()=>fixture.fail(409),()=>fixture.fail(503),()=>fixture.network()]){
  fail();fireEvent(document,new Event('visibilitychange'))
  await waitFor(()=>expect(screen.queryByText(/管理员共享的历史确认结果/)).not.toBeInTheDocument())
  expect(screen.queryByText('原始客厅共享结果')).not.toBeInTheDocument()
 }
 fixture.recover();fixture.share([{...sharedResult,history_status:'deleted',deleted_at:observed}]);fireEvent(document,new Event('visibilitychange'))
 expect(await screen.findByText(/已删除，保留共享历史/)).toBeVisible()
 fixture.share([]);fireEvent(document,new Event('visibilitychange'))
 expect(await screen.findByText('未找到这个共享结果或没有读取权限。')).toBeVisible()
 expect(fixture.posts).toHaveLength(0)
})
it('removes selected shared history when the session ends',async()=>{
 show(true)
 fireEvent.click(await screen.findByRole('link',{name:/原始客厅共享结果/}))
 expect(await screen.findByText(/管理员共享的历史确认结果/)).toBeVisible()
 fireEvent.click(screen.getAllByRole('button',{name:'退出登录'})[0])
 await screen.findByRole('button',{name:'登录'})
 expect(screen.queryByText('原始客厅共享结果')).not.toBeInTheDocument()
 expect(screen.queryByText(/管理员共享的历史确认结果/)).not.toBeInTheDocument()
})
it('offers the exact historical payload as the admin sharing candidate',async()=>{
 show()
 expect(await screen.findByLabelText('共享：原始客厅共享结果')).toBeVisible()
 expect(screen.queryByLabelText('共享：客厅灯定时开启')).not.toBeInTheDocument()
 fireEvent.click(screen.getByRole('link',{name:'审阅将共享的原始结果：原始客厅共享结果'}))
 expect(await screen.findByText(/管理员共享的历史确认结果/)).toBeVisible()
 expect(screen.queryByRole('button',{name:'保存结果共享'})).not.toBeInTheDocument()
 expect(screen.queryByRole('button',{name:'确认创建自动化'})).not.toBeInTheDocument()
})
it('labels an older diagnostic as historical without current-version success', async()=>{
 const fixture=show()
 fixture.replace({diagnostic:{...snapshot,historical:true,checks:[],changes:[],sources:{},counts:{pass:0,finding:0,unknown:0,changes:0}},stale:true})
 fireEvent.click(await screen.findByRole('button',{name:/^已安装/}))
 expect(await screen.findByText(/历史诊断，当前版本尚未核对/)).toBeVisible()
 expect(screen.queryByText('已检查项目正常')).not.toBeInTheDocument()
})
it('separates installed, unknown legacy and archive without treating unknown evidence as success', async () => {
 show()
 fireEvent.click(await screen.findByRole('button', { name: /^已安装/ }))
 expect(await screen.findByText('客厅灯定时开启')).toBeVisible()
 expect(screen.getByText('无法判断')).toBeVisible()
 expect(screen.getByText(/已停用（记录中的观察）/)).toBeVisible()
 expect(screen.queryByText('旧安装记录')).not.toBeInTheDocument()
 expect(screen.queryByText('已检查项目正常')).not.toBeInTheDocument()
 fireEvent.click(screen.getByRole('button', { name: /^需关注/ }))
 expect(screen.getByText('旧安装记录')).toBeVisible()
 expect(screen.getAllByText(/待核对/).length).toBeGreaterThan(0)
 fireEvent.click(screen.getByRole('button', { name: /^归档/ }))
 expect(screen.getByText('已忽略，未执行')).toBeVisible()
})
it('retains the prior observation time and marks it stale when list refresh fails', async () => {
 const fixture = show()
 fireEvent.click(await screen.findByRole('button', { name: /^已安装/ }))
 await screen.findByText('客厅灯定时开启')
 const timeBefore = screen.getByLabelText('记录中的观察时间：客厅灯定时开启').textContent
 fixture.fail()
 fireEvent.click(screen.getByRole('button', { name: '刷新诊断列表' }))
 expect(await screen.findByText(/读取失败，保留上次观察/)).toBeVisible()
 expect(screen.getByLabelText('记录中的观察时间：客厅灯定时开启').textContent).toBe(timeBefore)
 expect(screen.getAllByText(/已过期/).length).toBeGreaterThan(0)
})
it('runs only explicit readonly managed or selected external diagnostics and never installs', async () => {
 const fixture = show()
 fireEvent.click(await screen.findByRole('button', { name: /^已安装/ }))
 fireEvent.click(await screen.findByRole('button', { name: '只读诊断：客厅灯定时开启' }))
 await waitFor(() => expect(fixture.posts).toEqual(['/api/console/v1/admin/automations/managed/diagnostics']))
 fireEvent.change(screen.getByRole('combobox', { name: '外部自动化' }), { target: { value: 'automation.external' } })
 fireEvent.click(screen.getByRole('button', { name: '只读诊断所选外部规则' }))
 await waitFor(() => expect(fixture.posts).toHaveLength(2))
 expect(fixture.posts[1]).toBe('/api/console/v1/admin/automation-diagnostics')
 expect(screen.queryByRole('button', { name: /接管|安装所选/ })).not.toBeInTheDocument()
})
it('offers members evidence without admin selection or actions', async () => {
 const fixture = show(true)
 fireEvent.click(await screen.findByRole('button',{name:/^已安装/}))
 await screen.findByText('客厅灯定时开启')
 expect(screen.queryByRole('combobox', { name: '外部自动化' })).not.toBeInTheDocument()
 expect(screen.queryByRole('button', { name: /只读诊断/ })).not.toBeInTheDocument()
 expect(fixture.posts).toHaveLength(0)
})

it('keeps the freshest recorded observation on a source failure and clears it on projection revision', async()=>{
 const fixture=show()
 fireEvent.click(await screen.findByRole('button',{name:/^已安装/}))
 await screen.findByText('客厅灯定时开启')
 const oldTime=screen.getByLabelText('记录中的观察时间：客厅灯定时开启').getAttribute('datetime')
 fixture.replace({observed_at:'2026-10-01T00:00:00Z',observed_state:'on',stale:true})
 fireEvent.click(screen.getByRole('button',{name:'刷新诊断列表'}))
 await screen.findByText(/已过期/)
 expect(screen.getByLabelText('记录中的观察时间：客厅灯定时开启')).toHaveAttribute('datetime',oldTime)
 expect(screen.getByText(/已停用（记录中的观察）/)).toBeVisible()
 fixture.replace({observed_at:'2026-10-01T00:00:00Z',observed_state:'on',stale:true},2)
 fireEvent.click(screen.getByRole('button',{name:'刷新诊断列表'}))
 await waitFor(()=>expect(screen.getByLabelText('记录中的观察时间：客厅灯定时开启')).toHaveAttribute('datetime','2026-10-01T00:00:00Z'))
})
it('removes retained rows when publication reports a sharing revision conflict',async()=>{
 const fixture=show(true)
 fireEvent.click(await screen.findByRole('button',{name:/^已安装/}))
 await screen.findByText('客厅灯定时开启')
 fixture.fail(409)
 fireEvent.click(screen.getByRole('button',{name:'刷新诊断列表'}))
 await waitFor(()=>expect(screen.queryByText('客厅灯定时开启')).not.toBeInTheDocument())
 expect(screen.getByRole('button',{name:'已安装（…）'})).toBeVisible()
})
it('does not resurrect rows or counts across success, sharing conflict, server failure and network failure',async()=>{
 const fixture=show()
 fireEvent.click(await screen.findByRole('button',{name:/^已安装/}))
 await screen.findByText('客厅灯定时开启')
 expect(screen.getByRole('button',{name:'归档（1）'})).toBeVisible()
 fireEvent.change(screen.getByRole('combobox',{name:'外部自动化'}),{target:{value:'automation.external'}})
 fireEvent.click(screen.getByRole('button',{name:'只读诊断所选外部规则'}))
 await waitFor(()=>expect(screen.getAllByText('无法判断')).toHaveLength(2))
 for (const failure of [()=>fixture.fail(409),()=>fixture.fail(503),()=>fixture.network()]) {
  failure()
  const calls=vi.mocked(fetch).mock.calls.length
  fireEvent(document,new Event('visibilitychange'))
  await waitFor(()=>expect(vi.mocked(fetch).mock.calls.length).toBeGreaterThan(calls))
  await waitFor(()=>expect(screen.queryByText('客厅灯定时开启')).not.toBeInTheDocument())
  expect(screen.getByRole('button',{name:'已安装（…）'})).toBeVisible()
  expect(screen.getByRole('button',{name:'归档（0）'})).toBeVisible()
  expect(screen.queryByText('已忽略的建议')).not.toBeInTheDocument()
  expect(screen.queryByRole('combobox',{name:'外部自动化'})).not.toBeInTheDocument()
 }
 fixture.recover()
 fireEvent(document,new Event('visibilitychange'))
 expect(await screen.findByText('客厅灯定时开启')).toBeVisible()
 expect(screen.getByRole('button',{name:'已安装（1）'})).toBeVisible()
 expect(screen.getAllByText('无法判断')).toHaveLength(1)
 await waitFor(()=>expect(screen.getByRole('button',{name:'归档（1）'})).toBeVisible())
})
it.each(['current_version','observation_version'])('clears retained observation when %s changes',async field=>{
 const fixture=show()
 fireEvent.click(await screen.findByRole('button',{name:/^已安装/}))
 await screen.findByText('客厅灯定时开启')
 fixture.replace({[field]:'replacement',observed_at:'2026-10-01T00:00:00Z',observed_state:'on',stale:true})
 fireEvent.click(screen.getByRole('button',{name:'刷新诊断列表'}))
 await waitFor(()=>expect(screen.getByLabelText('记录中的观察时间：客厅灯定时开启')).toHaveAttribute('datetime','2026-10-01T00:00:00Z'))
 expect(screen.getByText(/已启用（记录中的观察）/)).toBeVisible()
})
it('moves deleted managed records into history without diagnostic or native controls',async()=>{
 const fixture=show()
 fixture.replace({deleted_at:observed,installation_state:'deleted',ownership:'historical',can_diagnose:false,ha_url:undefined})
 fireEvent.click(await screen.findByRole('button',{name:/^归档/}))
 fireEvent.click(screen.getByRole('button',{name:'刷新诊断列表'}))
 expect(await screen.findByText('已删除，保留历史记录')).toBeVisible()
 expect(screen.queryByRole('button',{name:/只读诊断/})).not.toBeInTheDocument()
 expect(screen.queryByRole('link',{name:'在 Home Assistant 中查看'})).not.toBeInTheDocument()
 expect(screen.queryByRole('link',{name:'查看来源建议与安装记录'})).not.toBeInTheDocument()
})
