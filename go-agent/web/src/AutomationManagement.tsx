import { useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { ApiError, ManagementPartialError, errorText, requestConsole, requestManagementAction, requestManagementPrepare, type ManagementView, type ManagementOperation, type ManagementPartial, type ManagementAction, type ManagedVersion, type ManagedDraft, type SuggestionRule } from './api'
import { useAuth } from './auth'
import { readAutomationEntityDirectory, type AutomationDirectoryEntity } from './automationEntityDirectory'
import { AutomationRevisionForm } from './AutomationRevisionForm'
import { AutomationRuleView, AutomationRevisionDiff } from './AutomationRuleView'
import { AutomationInstallation } from './AutomationInstallation'
import { dateLabel } from './familyViews'
import { ActionGroup, Alert, Breadcrumb, Button, Card, Field, KeyValueList, LoadingState, Select, Stack } from './ui'

const labels: Record<ManagementAction,string> = { enable:'启用',disable:'停用',update:'更新',delete:'删除' }
const statuses: Record<string,string> = { prepared:'待独立确认',applying:'已记录尝试，等待核对',unknown:'结果尚未核实',confirmed:'操作已核对',failed:'操作未完成',expired:'确认已过期',cancelled:'本地确认已取消' }
function rule(v: ManagedVersion): SuggestionRule {
 const i=v.intent, target=i.kind==='time'?i.entity_id:i.related_id ?? ''
 return { triggers:i.kind==='time'?[{platform:'time',at:i.at}]:[{platform:'state',entity_id:i.entity_id,from:'off',to:'on'}],conditions:i.kind==='time'&&v.presence_binding?[{condition:'state',...v.presence_binding}]:[{condition:'sun',after:'sunset'}],actions:[{service:target.split('.')[0]+'.turn_on',target:{entity_id:target}}] }
}
export function AutomationManagement({ id, onChanged }: { id:string; onChanged:()=>void }) {
 const auth=useAuth()
 if(auth.identity?.user.role!=='admin'||!auth.identity.capabilities.includes('suggestions:manage'))return <Alert tone="warning">只有管理员可以管理受管规则。</Alert>
 return <ManagementDetails key={`${id}:${auth.epoch}`} id={id} onChanged={onChanged}/>
}
function ManagementDetails({id,onChanged}:{id:string;onChanged:()=>void}) {
 const auth=useAuth()
 const [data,setData]=useState<ManagementView|null>(null)
 const [operation,setOperation]=useState<ManagementOperation|null>(null)
 const [partial,setPartial]=useState<ManagementPartial|null>(null)
 const [draft,setDraft]=useState<ManagedDraft|null>(null)
 const [error,setError]=useState('')
 const [denied,setDenied]=useState(false)
 const [busy,setBusy]=useState(false)
 const [selection,setSelection]=useState<ManagementAction|null>(null)
 const [stopActions,setStopActions]=useState('')
 const [editing,setEditing]=useState(false)
 const [directory,setDirectory]=useState<AutomationDirectoryEntity[]>([])
 const [directoryError,setDirectoryError]=useState('')
 const [now,setNow]=useState(Date.now())
 const local=useRef(new AbortController()),running=useRef(false),blocked=useRef(false)
 const epoch=auth.epoch
 const current=(generation=local.current)=>generation===local.current&&!generation.signal.aborted&&!auth.signal.aborted&&auth.isCurrent(epoch)&&!blocked.current
 const signal=(generation=local.current)=>AbortSignal.any([generation.signal,auth.signal])
 const base='/admin/automations/'+encodeURIComponent(id)
 function fail(cause:unknown) {
  if(!current())return
  if(cause instanceof ApiError&&cause.status===401){auth.expire();return}
  if(cause instanceof ApiError&&cause.status===403){blocked.current=true;setDenied(true);setData(null);setOperation(null);setDraft(null);setPartial(null)}
  if(cause instanceof ApiError&&cause.status===409){setData(null);setOperation(null);setDraft(null)}
  if(cause instanceof ManagementPartialError&&cause.operation.managed_id===id){setPartial(cause.operation);setOperation(null)}
  setError(errorText(cause))
 }
 async function read(generation=local.current) {
  const result=await requestConsole<{management:ManagementView}>(base+'/management',{signal:signal(generation)})
  if(!current(generation))return
  if(result.management.managed_id!==id)throw new ApiError(502,'invalid_response')
  setData(result.management);setOperation(result.management.latest_operation);setDraft(result.management.draft)
 }
 async function run(work:(generation:AbortController)=>Promise<void>) {
  if(running.current||!current())return
  const generation=local.current
  running.current=true;setBusy(true);setError('')
  try{await work(generation)}catch(cause){if(current(generation))fail(cause)}finally{if(local.current===generation){running.current=false;if(current(generation))setBusy(false)}}
 }
 useEffect(()=>{
  const generation=new AbortController()
  local.current=generation;running.current=false
  void run(read)
  void readAutomationEntityDirectory(signal(generation)).then(entities=>{if(current(generation))setDirectory([...entities.values()])}).catch(cause=>{if(current(generation)){if(cause instanceof ApiError&&[401,403].includes(cause.status))fail(cause);else setDirectoryError('名称暂无法核对；可读取本地记录，修订时需要重新读取实体。')}})
  const timer=window.setInterval(()=>setNow(Date.now()),1000)
  return()=>{generation.abort();window.clearInterval(timer)}
 },[])
 const entities=new Map(directory.map(entity=>[entity.entity_id,entity]))
 const locked=busy||Boolean(error)||Boolean(partial)||denied
 const op=partial?null:operation
 const expired=op?Date.parse(op.expires_at)<=now:false
 async function prepare() {
  if(!data||!selection||!data.can_prepare||locked||selection==='disable'&&!stopActions||selection==='update'&&!draft)return
  const requested=selection
  await run(async()=>{
   const result=await requestManagementPrepare(id,{action:requested,expected_digest:data.expected_digest,...(requested==='disable'?{stop_actions:stopActions==='stop'}:{}),...(requested==='update'?{revision_id:draft!.id}:{})},auth.identity!.csrf_token,signal())
   if(!current())return
   if(result.operation.managed_id!==id||result.operation.action!==requested)throw new ApiError(502,'invalid_response')
   setOperation(result.operation);setSelection(null);setData({...data,can_prepare:false})
  })
 }
 async function operate(action:'confirm'|'cancel'|'reconcile') {
  if(!op||locked||(action==='confirm'&&(!op.can_confirm||expired))||(action==='cancel'&&!op.can_cancel)||(action==='reconcile'&&!op.can_reconcile))return
  await run(async()=>{
   const result=await requestManagementAction(op.id,action,auth.identity!.csrf_token,signal())
   if(!current())return
   if(result.operation.managed_id!==id)throw new ApiError(502,'invalid_response')
   setOperation(result.operation)
   if(result.operation.status==='confirmed'){onChanged();setEditing(false)}
   // The operation response is retained even if refreshing its local current
   // version fails. Refresh never dispatches an external write.
   await read()
  })
 }
 return <Stack gap={24}>
  <Breadcrumb><Link to="/app/automations">返回自动化结果</Link></Breadcrumb>
  <Card heading="受管规则管理"><Stack gap={16}>
   <p>准备后需要单独确认，有效期五分钟。任何写入都不会自动重试；未知结果只能显式只读核对。</p>
   <Button variant="secondary" disabled={busy||denied} onClick={()=>void run(read)}>读取本地管理记录</Button>
   {error&&<Alert tone="danger" live="assertive">{error} 请求可能已留下记录，请先读取本地管理记录。本页不会自动重试。</Alert>}
   {!data&&!error&&<LoadingState label="正在读取管理记录…"/>}
   {data&&<><Link className="jx-action-link" to={'/app/automations?id='+encodeURIComponent(data.suggestion_id)}>查看来源建议与安装记录</Link><KeyValueList items={[{id:'version',label:'当前记录版本',value:<code>{data.current_version}</code>},{id:'sharing',label:'来源共享设置',value:data.shared?'已共享；成员仍需具备当前全部实体权限':'未共享'}]}/>
    {data.deleted_at?<Alert tone="warning">已删除，保留历史记录。删除时间：{dateLabel(data.deleted_at)}。没有自动恢复操作。</Alert>:<p>这里展示本地当前版本；不代表此刻启用、运行或物理设备效果。</p>}
    {directoryError&&<Alert tone="warning">{directoryError}</Alert>}
    <AutomationRuleView rule={rule(data.current)} entities={entities}/>
    {!data.deleted_at&&data.can_prepare&&!partial&&<ActionGroup>{(['enable','disable','update','delete'] as const).map(action=><Button key={action} variant="secondary" disabled={locked} onClick={()=>{setSelection(action);setStopActions('');setEditing(action==='update'&&!draft)}}>{labels[action]}规则</Button>)}</ActionGroup>}
   </>}
  </Stack></Card>
  {selection&&data&&!partial&&<Card heading={labels[selection]+'操作审阅'}><Stack gap={16}>
   {selection==='enable'&&<p>启用后，符合触发与条件时可能执行规则中的设备动作。</p>}
   {selection==='disable'&&<><p>停用阻止后续触发。已经发生的物理效果无法撤销。</p><Field id="managed-stop-actions" label="正在运行的动作"><Select id="managed-stop-actions" value={stopActions} disabled={busy} onChange={event=>setStopActions(event.target.value)}><option value="">请明确选择</option><option value="continue">允许已经运行的动作继续</option><option value="stop">请求停止已经运行的动作</option></Select></Field></>}
   {selection==='delete'&&<Alert tone="warning">删除配置不会停止正在运行的动作，也不能撤销已发生的物理效果。删除前会保存完整本地恢复材料；不会自动停用或自动恢复。</Alert>}
   {selection==='update'&&<><p>更新使用原规则标识，成功核对后才切换当前版本。保存草稿不会更改已安装规则。</p><Button variant="secondary" disabled={busy} onClick={()=>setEditing(true)}>编辑更新草稿</Button></>}
   {selection==='update'&&draft&&<><AutomationRevisionDiff changes={draft.version.diff} entities={entities}/><AutomationRuleView rule={rule(draft.version)} entities={entities}/></>}
   <Button disabled={locked||selection==='disable'&&!stopActions||selection==='update'&&(!draft||editing)} onClick={()=>void prepare()}>{'准备'+labels[selection]+'确认'}</Button>
  </Stack></Card>}
  {editing&&data&&!partial&&<AutomationRevisionForm key={data.current_version} managed version={data.current} enabled={data.can_prepare} pending={busy} error={error} onCancel={()=>setEditing(false)} onSave={async body=>{await run(async()=>{
   const result=await requestConsole<{draft:ManagedDraft}>(base+'/revisions',{method:'POST',body,csrfToken:auth.identity!.csrf_token,signal:signal()})
   if(current()&&result.draft.managed_id===id){setDraft(result.draft);setEditing(false)}
  })}}/>}
  {op&&<Card heading="独立操作确认"><Stack gap={16}>
   <p>{labels[op.action]}规则 · {statuses[op.status]??'状态待核对'}</p>
   <p>{op.status==='unknown'||op.status==='applying'?'结果尚未核实，恢复记录不证明丢失响应的请求已成功。请核对以下操作编号和时间。':'以下为已保存的操作记录，请核对操作编号、状态和时间。'}</p>
   <KeyValueList items={[{id:'id',label:'操作编号',value:<code>{op.id}</code>},{id:'created',label:'准备时间',value:dateLabel(op.created_at)},{id:'expires',label:'确认截止时间',value:dateLabel(op.expires_at)}]}/>
   {op.action==='disable'&&<p>{op.stop_actions?'明确请求停止运行中的动作':'明确允许运行中的动作继续'}；无法撤销已发生的物理效果。</p>}
   {op.action==='delete'&&<p>删除不会停止运行中的动作。将先保存完整本地恢复材料，无自动恢复。</p>}
   {op.revision&&<><AutomationRevisionDiff changes={op.revision.diff} entities={entities}/><AutomationRuleView rule={rule(op.revision)} entities={entities}/></>}
   {op.write_status==='acknowledged'&&<p>HA 已确认接收写入</p>}
   <p>备份：{op.backup_status==='saved'?'已保存本地恢复材料':op.action==='delete'?'尚未保存':'此操作不需要'}；归档：{op.archive_status==='complete'?'已完成':op.archive_status==='pending'?'等待本地归档':op.action==='update'?'尚未完成':'此操作不需要'}。</p>
   {op.installation&&op.status==='confirmed'&&<AutomationInstallation operation={op.installation}/>}
   {op.action==='update'&&(op.status==='unknown'||op.status==='applying')&&<Alert tone="warning">更新后的运行加载尚未核对，当前安装版本保持原记录。请显式只读核对；不会重发更新。</Alert>}
   {expired&&op.status==='prepared'&&<Alert tone="warning">五分钟确认已过期，请取消此记录后重新准备。</Alert>}
   <ActionGroup>{op.can_confirm&&!expired&&<Button disabled={locked} onClick={()=>void operate('confirm')}>{'确认'+labels[op.action]+'规则'}</Button>}{op.can_cancel&&<Button variant="secondary" disabled={locked} onClick={()=>void operate('cancel')}>取消本地确认</Button>}{op.can_reconcile&&<Button variant="secondary" disabled={locked} onClick={()=>void operate('reconcile')}>只读核对操作结果</Button>}</ActionGroup>
   <p>核对后至写入前仍可能发生外部编辑，不能保证跨系统原子修改。操作确认不证明设备物理效果。</p>
  </Stack></Card>}
  {partial&&<Card heading="本次响应观察"><Stack gap={16}><Alert tone="warning">本地持久化尚未核实。以下是本次响应保留的观察，不是已保存的确认记录；所有操作能力均已关闭。</Alert><p>{partial.write_status==='acknowledged'?'HA 已确认接收写入':'HA 写入结果尚未核实'}</p><KeyValueList items={[{id:'id',label:'响应对应操作',value:<code>{partial.id}</code>},{id:'attempt',label:'尝试时间',value:dateLabel(partial.attempted_at ?? null)}]}/><p>读取到较早的 applying／unknown 记录也不代表 HA 写入失败。请保留本次回执；恢复存储后重新进入详情并只读核对，不要重放确认。</p></Stack></Card>}
 </Stack>
}
