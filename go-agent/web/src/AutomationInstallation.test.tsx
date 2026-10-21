import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, expect, it, vi } from 'vitest'
import { App } from './App'

const operation = { id: 'operation-1', suggestion_id: 'sample', suggestion_version: 'sample', status: 'unknown', phase: 'reload', created_at: '2026-10-15T00:00:00Z', updated_at: '2026-10-15T00:00:00Z', config_status: 'saved', mapping_status: 'unknown', load_status: 'unknown', archive_status: 'not_ready', ownership: 'unverified', can_reconcile: true, legacy_unverified: false }
const base = { id: 'sample', status: 'unknown', created_at: '2026-10-15T00:00:00Z', title: '自动化建议', confidence: .8, time_zone: 'UTC', entity_ids: ['light.room'], entities: [], rule: { triggers: [{ platform: 'time', at: '18:00' }], conditions: [], actions: [{ service: 'light.turn_on', target: { entity_id: 'light.room' } }] }, missing_bindings: [], unsupported_code: null, shared: false, can_bind: false, can_confirm: false, can_ignore: false, can_revise: false, evidence: { sample_size: null, period_days: null }, installation_state: 'unknown', installation: operation }
function show(options: { legacy?: boolean; stale?: boolean; fail?: boolean; applying?: boolean; deleted?: boolean; readStatus?:()=>number; afterReconcile?:()=>void; empty?:()=>boolean } = {}) {
 let item: any = options.legacy ? { ...base, status: 'confirmed', installation: undefined, installation_state: 'legacy_unverified' } : options.stale ? { ...base, status: 'pending', can_bind: true, can_revise: true, can_confirm: true, can_ignore: true, intent: { schema_version: 1, kind: 'time', entity_id: 'light.room', at: '18:00', time_zone: 'UTC', required_conditions: [] } } : base
 if (options.applying) item = { ...base, status: 'applying', installation: { ...operation, status: 'applying', phase: 'create', config_status: 'unknown' } }
 if (options.deleted) item = { ...base, status:'confirmed', installation_state:'deleted', installation:{...operation,status:'confirmed',historical:true,deleted_at:'2026-10-15T02:00:00Z',can_reconcile:false} }
 const writes: string[] = []
 vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
  if (url.endsWith('/auth/me')) return Response.json({ user: { id: 'admin', username: 'admin', display_name: '管理员', role: 'admin', must_change_password: false }, capabilities: ['suggestions:read', 'suggestions:manage'], csrf_token: 'test' })
  if (url.endsWith('/suggestions')) {
   const status=options.readStatus?.()??200
   if(status===0)throw new Error('network')
   if(status!==200)return Response.json({error:{code:'unavailable'}},{status})
   return Response.json({ suggestions: options.empty?.()?[]:[item], count: options.empty?.()?0:1 })
  }
  if (url.endsWith('/admin/sharing')) return Response.json({ revision: 0, suggestion_ids: [] })
  if (init?.method === 'POST') {
   writes.push(url)
   if (url.endsWith('/install-operations/operation-1/reconcile')) {
    if (options.fail) return Response.json({ error: { code: 'unavailable' } }, { status: 503 })
    const next = { ...operation, status: 'confirmed', phase: 'archive', config_status: 'matching', mapping_status: 'verified', load_status: 'verified', archive_status: 'pending', archive_error_code: 'install_archive_pending', ownership: 'managed', observed_state: 'off', observed_at: '2026-10-15T01:00:00Z' }
    item = { ...base, status: 'confirmed', installation_state: 'verified', installation: next }
    options.afterReconcile?.()
    return Response.json({ operation: next })
   }
  }
  return Response.json({ error: { code: 'not_found' } }, { status: 404 })
 }))
 render(<MemoryRouter initialEntries={['/app/automations?id=sample']}><App/></MemoryRouter>)
 return writes
}
afterEach(() => vi.unstubAllGlobals())
it('separates saved configuration, current load, local archive and disabled state with read-only reconciliation', async () => {
 const writes=show()
 expect(await screen.findByText('配置已保存，加载结果待核对')).toBeVisible()
 fireEvent.click(screen.getByRole('button',{name:'只读核对安装状态'}))
 expect(await screen.findByText('安装已核实，本地归档待完成')).toBeVisible()
 expect(screen.getByText('已停用（最近观察）')).toBeVisible()
 expect(writes).toHaveLength(1)
 expect(writes[0]).toContain('/install-operations/operation-1/reconcile')
 expect(screen.queryByRole('button',{name:'明确重试创建自动化'})).not.toBeInTheDocument()
})
it('hides new-write and revision controls when durable operation overrides stale pending capabilities',async()=>{
 const writes=show({stale:true})
 await screen.findByText('配置已保存，加载结果待核对')
 for(const name of ['确认创建自动化','修订建议','补齐有人在家条件','忽略未执行建议'])expect(screen.queryByRole('button',{name})).not.toBeInTheDocument()
 expect(writes).toHaveLength(0)
})
it('describes legacy confirmed records as unverified without enabled or ownership claims',async()=>{
 show({legacy:true})
 expect(await screen.findByText('历史安装记录，当前归属与加载未核实')).toBeVisible()
 expect(screen.queryByRole('button',{name:'只读核对安装状态'})).not.toBeInTheDocument()
 expect(screen.queryByText('已确认安装并归档')).not.toBeInTheDocument()
})
it('does not automatically replay reconciliation or confirmation when a response fails',async()=>{
 const writes=show({fail:true})
 fireEvent.click(await screen.findByRole('button',{name:'只读核对安装状态'}))
 await waitFor(()=>expect(screen.getByRole('alert')).toBeVisible())
 expect(writes).toHaveLength(1)
 expect(writes.some(url=>url.endsWith('/confirm'))).toBe(false)
})
it('offers only read-only recovery for a durable applying operation after the install request ends', async () => {
 const writes=show({applying:true})
 expect(await screen.findByText('安装进行中，尚未核实')).toBeVisible()
 for (const name of ['确认创建自动化','修订建议','补齐有人在家条件','忽略未执行建议']) expect(screen.queryByRole('button',{name})).not.toBeInTheDocument()
 const reconcile=screen.getByRole('button',{name:'只读核对安装状态'})
 expect(reconcile).toBeEnabled()
 fireEvent.click(reconcile)
 await screen.findByText('安装已核实，本地归档待完成')
 expect(writes).toEqual(['/api/console/v1/admin/install-operations/operation-1/reconcile'])
})
it('shows tombstoned installation detail as historical without reconciliation',async()=>{
 const writes=show({deleted:true})
 expect(await screen.findByText('已删除，保留历史记录')).toBeVisible()
 expect(screen.getByText(/不能据此判断当前实体的归属或启用状态/)).toBeVisible()
 expect(screen.queryByRole('button',{name:'只读核对安装状态'})).not.toBeInTheDocument()
 expect(writes).toHaveLength(0)
})
it('clears suggestion details and mutation overrides until a new authorized success after revocation',async()=>{
 let status=200,empty=false
 show({readStatus:()=>status,afterReconcile:()=>{status=409},empty:()=>empty})
 fireEvent.click(await screen.findByRole('button',{name:'只读核对安装状态'}))
 await screen.findByRole('button',{name:'重新读取结果'})
 for(const next of [503,0]){
  status=next
  const calls=vi.mocked(fetch).mock.calls.length
  fireEvent.click(screen.getByRole('button',{name:'重新读取结果'}))
  await waitFor(()=>expect(vi.mocked(fetch).mock.calls.length).toBeGreaterThan(calls))
  await waitFor(()=>expect(screen.queryByRole('button',{name:'只读核对安装状态'})).not.toBeInTheDocument())
  expect(screen.queryByText('安装已核实，本地归档待完成')).not.toBeInTheDocument()
  expect(screen.queryByText('配置已保存，加载结果待核对')).not.toBeInTheDocument()
 }
 status=200;empty=true
 fireEvent.click(screen.getByRole('button',{name:'重新读取结果'}))
 expect(await screen.findByText('未找到这个建议或没有读取权限。')).toBeVisible()
 expect(screen.queryByRole('button',{name:'只读核对安装状态'})).not.toBeInTheDocument()
})
