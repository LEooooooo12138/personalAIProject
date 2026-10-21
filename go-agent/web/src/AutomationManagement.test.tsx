import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, useNavigate } from 'react-router-dom'
import { afterEach, expect, it, vi } from 'vitest'
import { App } from './App'
import { requestManagementAction, requestManagementPrepare, ManagementPartialError } from './api'
import { AuthProvider, useAuth } from './auth'
import { AutomationManagement } from './AutomationManagement'
import { StrictMode } from 'react'

const identity = { user: { id: 'owner', role: 'admin', username: 'owner', display_name: '管理员', must_change_password: false }, capabilities: ['suggestions:read','suggestions:manage'], csrf_token: 'csrf' }
const version = { id:'v1',intent:{schema_version:1,kind:'time',entity_id:'light.a',at:'18:00',time_zone:'UTC',required_conditions:['presence_home']},presence_binding:{entity_id:'group.home',state:'on'},diff:[] }
const operation = { id:'op1',managed_id:'m1',action:'disable',status:'prepared',current_operation_id:'i1',current_version:'v1',expected_digest:'abc',created_at:new Date().toISOString(),updated_at:new Date().toISOString(),expires_at:new Date(Date.now()+300000).toISOString(),write_status:'not_attempted',backup_status:'not_required',archive_status:'not_required',can_confirm:true,can_cancel:true,can_reconcile:false,stop_actions:false,concurrency_risk:'external_edit_after_read' }
const management = { managed_id:'m1',suggestion_id:'anchor',current_operation_id:'i1',current_version:'v1',expected_digest:'abc',current:version,shared:false,can_prepare:true,latest_operation:null,draft:null }
const partial = { id:'op1',managed_id:'m1',action:'disable',status:'unknown',observed_status:'unknown',write_status:'acknowledged',attempted_at:new Date().toISOString(),created_at:new Date().toISOString(),expires_at:new Date().toISOString(),backup_status:'not_required',archive_status:'not_required',local_persistence:'unverified',result_source:'response_observation',can_confirm:false,can_cancel:false,can_reconcile:false }
afterEach(()=>vi.unstubAllGlobals())
it('replays the initial StrictMode read without letting an old completion release the new guard',async()=>{
 const releases:((r:Response)=>void)[]=[]
 const fetcher=vi.fn(async(url:string,_init?:RequestInit)=>{
  if(url.endsWith('/auth/me'))return Response.json(identity)
  if(url.endsWith('/management'))return new Promise<Response>(resolve=>releases.push(resolve))
  return Response.json({error:{code:'unavailable'}},{status:503})
 });vi.stubGlobal('fetch',fetcher)
 render(<MemoryRouter><AuthProvider><StrictMode><AutomationManagement id="m1" onChanged={()=>{}}/></StrictMode></AuthProvider></MemoryRouter>)
 await waitFor(()=>expect(releases).toHaveLength(2))
 await act(async()=>releases[0](Response.json({management:{...management,current_version:'OLD'}})))
 expect(screen.queryByText('OLD')).not.toBeInTheDocument()
 expect(screen.getByRole('button',{name:'读取本地管理记录'})).toBeDisabled()
 await act(async()=>releases[1](Response.json({management})))
 expect(await screen.findByText('v1')).toBeVisible()
 expect(screen.getByRole('button',{name:'读取本地管理记录'})).toBeEnabled()
 expect(screen.queryByText(/请求可能已留下记录/)).not.toBeInTheDocument()
 expect(fetcher.mock.calls.some(([,init])=>init?.method==='POST')).toBe(false)
})
it('shows confirmed deleted history without a spurious lost-response warning',async()=>{
 show(url=>url.endsWith('/management')?Promise.resolve(Response.json({management:{...management,deleted_at:operation.created_at,can_prepare:false,latest_operation:{...operation,action:'delete',status:'confirmed',can_confirm:false,can_cancel:false,backup_status:'saved'}}})):undefined)
 expect(await screen.findByText(/已删除，保留历史记录/)).toBeVisible()
 expect(screen.getByText('删除规则 · 操作已核对')).toBeVisible()
 expect(screen.queryByText(/丢失响应/)).not.toBeInTheDocument()
})
function show(extra:(url:string,init?:RequestInit)=>Promise<Response>|undefined=()=>undefined,role='admin') {
 const writes:string[]=[]
 let latest:typeof operation|null=null
 const fetcher=vi.fn(async(url:string,init?:RequestInit)=>{
  if(init?.method==='POST')writes.push(url)
  const supplied=extra(url,init);if(supplied)return supplied
  if(url.endsWith('/auth/me'))return Response.json({...identity,user:{...identity.user,role}})
  if(url.endsWith('/management'))return Response.json({management:{...management,latest_operation:latest,can_prepare:!latest||latest.status==='confirmed'}})
  if(url.endsWith('/actions')){latest=operation;return Response.json({operation})}
  if(url.endsWith('/confirm')){latest={...operation,status:'confirmed',write_status:'acknowledged',can_confirm:false,can_cancel:false};return Response.json({operation:latest})}
  if(url.endsWith('/suggestions'))return Response.json({suggestions:[],count:0})
  if(url.endsWith('/automations'))return Response.json({automations:[],external_candidates:[],counts:{installed:0,attention:0,unverified:0}})
  if(url.endsWith('/admin/sharing'))return Response.json({revision:0,suggestion_ids:[]})
  return Response.json({error:{code:'unavailable'}},{status:503})
 });vi.stubGlobal('fetch',fetcher)
 const rendered=render(<MemoryRouter initialEntries={['/app/automations?managed=m1']}><App/><SwitchSelection/></MemoryRouter>)
 return {writes,fetcher,...rendered}
}
function SwitchSelection(){const navigate=useNavigate();return <button onClick={()=>navigate('/app/automations?managed=m2')}>选择另一规则</button>}
it('discards late confirmation when the managed selection changes',async()=>{
 let release!:(response:Response)=>void
 show(url=>url.endsWith('/m2/management')?Promise.resolve(Response.json({management:{...management,managed_id:'m2',current_version:'second'}})):url.endsWith('/management')?Promise.resolve(Response.json({management:{...management,can_prepare:false,latest_operation:operation}})):url.endsWith('/confirm')?new Promise(resolve=>{release=resolve}):undefined)
 fireEvent.click(await screen.findByRole('button',{name:'确认停用规则'}))
 await waitFor(()=>expect(release).toBeDefined())
 fireEvent.click(screen.getByRole('button',{name:'选择另一规则'}));await screen.findByText('second')
 await act(async()=>release(Response.json({operation:{...operation,status:'confirmed',write_status:'acknowledged'}})))
 expect(screen.queryByText('HA 已确认接收写入',{exact:true})).not.toBeInTheDocument()
})
it('drops late authorized data after the actual auth identity epoch changes',async()=>{
 let role='admin',release!:(response:Response)=>void
 vi.stubGlobal('fetch',vi.fn(async(url:string)=>{
  if(url.endsWith('/auth/me'))return Response.json({...identity,user:{...identity.user,role,id:role==='admin'?'owner':'other'}})
  if(url.endsWith('/management'))return Response.json({management:{...management,latest_operation:operation,can_prepare:false}})
  if(url.endsWith('/confirm'))return new Promise<Response>(resolve=>{release=resolve})
  return Response.json({error:{code:'unavailable'}},{status:503})
 }))
 function IdentityHarness(){const auth=useAuth();return <><button onClick={()=>void auth.refresh()}>切换身份</button><AutomationManagement id="m1" onChanged={()=>{}}/></>}
 render(<MemoryRouter><AuthProvider><IdentityHarness/></AuthProvider></MemoryRouter>)
 fireEvent.click(await screen.findByRole('button',{name:'确认停用规则'}));await waitFor(()=>expect(release).toBeDefined())
 role='member';fireEvent.click(screen.getByRole('button',{name:'切换身份'}));await screen.findByText('只有管理员可以管理受管规则。')
 await act(async()=>release(Response.json({error:{code:'management_persistence_unverified'},operation:partial},{status:503})))
 expect(screen.queryByText('本次响应观察')).not.toBeInTheDocument()
})
it('requires explicit stop-actions choice and independent confirmation',async()=>{
 const {writes}=show()
 fireEvent.click(await screen.findByRole('button',{name:'停用规则'}))
 const prepare=screen.getByRole('button',{name:'准备停用确认'});expect(prepare).toBeDisabled()
 fireEvent.change(screen.getByLabelText('正在运行的动作'),{target:{value:'continue'}})
 fireEvent.click(prepare);await screen.findByRole('heading',{name:'独立操作确认'})
 expect(writes).toHaveLength(1);expect(writes[0]).toMatch(/\/actions$/)
 fireEvent.click(screen.getByRole('button',{name:'确认停用规则'}))
 await screen.findByText('HA 已确认接收写入',{exact:true})
 expect(screen.queryByText(/丢失响应/)).not.toBeInTheDocument()
 expect(writes.filter(url=>url.endsWith('/confirm'))).toHaveLength(1)
})
it('retains authorized 503 acknowledgment without enabling replay after recovery',async()=>{
 show((url)=>url.endsWith('/management')?Promise.resolve(Response.json({management:{...management,can_prepare:false,latest_operation:operation}})):url.endsWith('/confirm')?Promise.resolve(Response.json({error:{code:'management_persistence_unverified'},operation:partial},{status:503})):undefined)
 fireEvent.click(await screen.findByRole('button',{name:'确认停用规则'}))
 await screen.findByText('HA 已确认接收写入',{exact:true})
 expect(screen.getByText(/本地持久化尚未核实/)).toBeVisible()
 expect(screen.queryByRole('button',{name:'确认停用规则'})).not.toBeInTheDocument()
 fireEvent.click(screen.getByRole('button',{name:'读取本地管理记录'}))
 await waitFor(()=>expect(screen.getByText('HA 已确认接收写入',{exact:true})).toBeVisible())
 expect(screen.queryByRole('button',{name:'确认停用规则'})).not.toBeInTheDocument()
})
it('does not request admin management for members',async()=>{
 const {fetcher}=show(()=>undefined,'member')
 await screen.findByText('只有管理员可以管理受管规则。')
 expect(fetcher.mock.calls.some(([url])=>url.endsWith('/management'))).toBe(false)
})
it('narrow client retains only the approved partial response and safe error text',async()=>{
 vi.stubGlobal('fetch',vi.fn(async()=>Response.json({error:{code:'management_persistence_unverified',message:'RAW_SECRET'},operation:{...partial,user_id:'PRIVATE'}},{status:503})))
 try {await requestManagementAction('op1','confirm','csrf');throw new Error('expected failure')}catch(error){expect(error).toBeInstanceOf(ManagementPartialError);expect(JSON.stringify(error)).not.toContain('PRIVATE');expect((error as Error).message).not.toContain('RAW_SECRET');expect((error as ManagementPartialError).operation.write_status).toBe('acknowledged')}
})
it('does not infer replay or a replaced version from an unknown update candidate',async()=>{
 const {writes}=show(url=>url.endsWith('/management')?Promise.resolve(Response.json({management:{...management,can_prepare:false,latest_operation:{...operation,action:'update',status:'unknown',write_status:'acknowledged',can_confirm:false,can_cancel:false,can_reconcile:true,installation:{historical:true,suggestion_version:'candidate'}}}})):undefined)
 await screen.findByText(/更新后的运行加载尚未核对/)
 expect(screen.getByText(/恢复记录不证明丢失响应的请求已成功/)).toBeVisible()
 expect(screen.queryByText(/此安装版本已被更新/)).not.toBeInTheDocument()
 expect(writes).toHaveLength(0)
 fireEvent.click(screen.getByRole('button',{name:'只读核对操作结果'}))
 await waitFor(()=>expect(writes).toEqual([expect.stringMatching(/\/reconcile$/)]))
})
it('hides expired confirmation and keeps permission denial sticky',async()=>{
 let denied=false
 const {fetcher}=show(url=>url.endsWith('/management')?Promise.resolve(denied?Response.json({error:{code:'forbidden'}},{status:403}):Response.json({management:{...management,can_prepare:false,latest_operation:{...operation,expires_at:'2020-01-01T00:00:00Z'}}})):undefined)
 await screen.findByText(/五分钟确认已过期/)
 expect(screen.queryByRole('button',{name:'确认停用规则'})).not.toBeInTheDocument()
 denied=true;fireEvent.click(screen.getByRole('button',{name:'读取本地管理记录'}))
 await waitFor(()=>expect(screen.getByRole('button',{name:'读取本地管理记录'})).toBeDisabled())
 expect(screen.queryByRole('heading',{name:'独立操作确认'})).not.toBeInTheDocument()
 const calls=fetcher.mock.calls.length;fireEvent.click(screen.getByRole('button',{name:'读取本地管理记录'}));expect(fetcher.mock.calls).toHaveLength(calls)
})
it('preserves preparation partial results but rejects forged capability envelopes',async()=>{
 const fetcher=vi.fn(async()=>Response.json({error:{code:'management_persistence_unverified'},operation:partial},{status:503}));vi.stubGlobal('fetch',fetcher)
 await expect(requestManagementPrepare('m1',{action:'disable',expected_digest:'abc',stop_actions:false},'csrf')).rejects.toBeInstanceOf(ManagementPartialError)
 fetcher.mockImplementation(async()=>Response.json({error:{code:'management_persistence_unverified'},operation:{...partial,can_confirm:true}},{status:503}))
 try{await requestManagementAction('op1','confirm','csrf')}catch(error){expect(error).not.toBeInstanceOf(ManagementPartialError)}
})
