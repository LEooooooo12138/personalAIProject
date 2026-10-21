import { useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import type { AutomationDiagnostic, AutomationList, AutomationView, TimeWindow } from './api'
import { ApiError, errorText } from './api'
import { useConsoleAction } from './consoleActions'
import { AutomationRuleView } from './AutomationRuleView'
import { dateLabel } from './familyViews'
import { ActionGroup, Alert, Button, Card, Disclosure, EmptyState, Field, KeyValueList, LoadingState, Select, Stack, StatusBadge } from './ui'

const summaryLabels = { attention: '需关注', partial: '部分无法核对', pass: '已检查项目正常', unknown: '无法判断' }
const reasons: Record<string, string> = {
 existence_unavailable: '暂无法读取配置是否存在', config_exists: '已读取到配置', config_missing: '当前未找到配置', state_unavailable: '暂无法核对启用状态', enabled_observed: '观察时已启用', disabled_observed: '观察时已停用', automation_unavailable: '自动化实体不可用', installed_version_unproven: '安装版本尚无法核对', installed_version_matches: '配置与受管版本相符', config_drift: '配置与受管版本不同，请审阅外部修改', runtime_version_unproven: '运行时加载版本尚无法核对', runtime_version_matches: '观察时运行时加载版本相符', references_unavailable: '引用实体尚无法核对', entity_state_unavailable: '引用实体状态尚无法核对', entity_available: '引用实体可用', entity_unavailable: '引用实体不可用，可能影响规则执行', entity_missing: '未找到引用实体，可能影响规则执行', current_condition_unavailable: '当前条件尚无法核对', current_condition_not_met: '观察时条件不满足；不代表运行失败', current_condition_met: '观察时条件满足；不代表曾经触发', reference_scope_incomplete: '仅核对了部分引用实体', run_outcome_unknown: '运行结果尚无法判断', action_failed: '历史运行报告动作错误；请在 HA 核对', execution_error: '历史运行报告执行错误；请在 HA 核对', completed: '历史运行报告完成；不证明物理设备效果', conditions_not_met: '历史运行因条件不满足未继续动作', not_triggered: '历史记录报告未触发', running: '记录中的运行尚未结束', cancelled: '记录中的运行已取消', concurrency_blocked: '记录中的运行受并发限制', run_evidence_missing: '暂无可核对的运行记录；不能据此断言未执行', expectation_contract_missing: '缺少预期运行约定，无法判断是否漏执行', timezone_unavailable: '家庭时区暂无法核对', schedule_not_comparable: '当前计划不支持确定性比较', state_trigger_history_unproven: '缺少历史状态触发证据', schedule_dst_ambiguous: '夏令时导致计划时间存在歧义', schedule_prerequisites_unproven: '缺少历史前置条件，无法判断是否漏执行', causality_unproven: '无法证明自动化与设备变化的因果关系', comparison_unavailable: '冲突比较证据不足', no_conflict_in_comparison: '本次可比较范围内未发现冲突', possible_conflict: '规则可能关联，但证据不足以确认冲突', opposite_actions_same_scope: '可比较条件下存在相反动作，请审阅两条规则', peer_scope_incomplete: '仅检查了有限范围的其他规则', diagnostic_unavailable: '此项暂无法判断',
}
const sourceLabels: Record<string,string> = { state:'启用状态', config:'存储配置', runtime:'运行加载', trace:'运行记录', activity:'活动归因', timezone:'家庭时区', entities:'引用实体', peers:'规则比较' }
const availability: Record<string,string> = { available:'已读取', unsupported:'当前不支持', forbidden:'无读取权限', retention_gap:'记录已过期或存在缺口', unavailable:'暂不可用' }
function tone(summary: AutomationDiagnostic['summary']) { return summary === 'pass' ? 'success' as const : summary === 'attention' ? 'danger' as const : 'warning' as const }
function diagnosticWindow(days: number): TimeWindow { const end = new Date(); return { start: new Date(end.getTime() - days*86400000).toISOString(), end: end.toISOString() } }

export function AutomationDiagnosticEvidence({ diagnostic }: { diagnostic: AutomationDiagnostic }) {
 return <Stack gap={16}>
  {diagnostic.historical && <Alert tone="warning">历史诊断，当前版本尚未核对。以下时间仅属于记录中的诊断。</Alert>}
  <StatusBadge tone={tone(diagnostic.summary)} label={summaryLabels[diagnostic.summary] ?? summaryLabels.unknown}/>
  <KeyValueList items={[{ id:'checked', label:'诊断核对时间', value:dateLabel(diagnostic.checked_at) }, { id:'window', label:'运行证据窗口', value:dateLabel(diagnostic.window.start)+' 至 '+dateLabel(diagnostic.window.end) }, { id:'counts', label:'可见检查', value:`${diagnostic.counts.finding} 项需关注 · ${diagnostic.counts.unknown} 项无法判断 · ${diagnostic.counts.pass} 项通过` }]}/>
  <ul>{diagnostic.checks.map((check,index)=><li key={index}><p><strong>{check.status === 'finding' ? '需关注' : check.status === 'unknown' ? '无法判断' : '已核对'}：</strong>{reasons[check.reason] ?? reasons.diagnostic_unavailable}</p></li>)}</ul>
  {diagnostic.changes.length>0 && <Disclosure title={`本次可见变化（${diagnostic.changes.length}）`}><ul>{diagnostic.changes.map((change,index)=><li key={index}>{change.kind==='new'?'新发现':change.kind==='recovered'?'已有证据确认恢复':'严重程度变化'}：{reasons[change.check.reason] ?? reasons.diagnostic_unavailable}</li>)}</ul></Disclosure>}
  <Disclosure title="来源与证据详情"><Stack gap={16}><KeyValueList items={Object.entries(diagnostic.sources).map(([key,source])=>({id:key,label:sourceLabels[key] ?? '证据来源',value:availability[source.status] ?? '暂不可用'}))}/><ul>{diagnostic.checks.flatMap((check,index)=>check.evidence_refs.map((evidence,offset)=><li key={`${index}-${offset}`}>{sourceLabels[evidence.source] ?? '规则证据'} · {dateLabel(evidence.observed_at)}{evidence.entity_ids.length>0 && <p><code>{evidence.entity_ids.join(' · ')}</code></p>}</li>))}</ul></Stack></Disclosure>
  <p className="jx-muted">运行记录可能被清理。诊断不会安装、接管、启停或触发自动化；安装、启用、运行记录和物理设备效果分别核对。</p>
 </Stack>
}
function AutomationDiagnosticCard({ item, stale, days, reload, manage }: { item:AutomationView; stale:boolean; days:number; reload:()=>void; manage:boolean }) {
 const action = useConsoleAction()
 const [result,setResult] = useState<AutomationDiagnostic | undefined>()
 useEffect(()=>setResult(undefined),[item.diagnostic?.id])
 const diagnostic = result ?? item.diagnostic
 const isStale = stale || item.stale || Boolean(action.error)
 const prior = useRef({state:item.observed_state,at:item.observed_at})
 if (!item.deleted_at && (!prior.current.at || (!isStale && item.observed_at && item.observed_at >= prior.current.at))) prior.current={state:item.observed_state,at:item.observed_at}
 const observation = item.deleted_at ? {state:item.observed_state,at:item.observed_at} : prior.current
 async function diagnose() {
  const response = await action.run<{diagnostic:AutomationDiagnostic}>(`/admin/automations/${encodeURIComponent(item.id)}/diagnostics`,{window:diagnosticWindow(days)})
  if(response){setResult(response.diagnostic);reload()}
 }
 return <Card heading={item.title}><Stack gap={16}>
  <StatusBadge tone={item.installation_state==='verified'?'neutral':'warning'} label={item.deleted_at?'已删除，保留历史记录':item.installation_state==='verified'?'安装记录已核实':'历史或安装结果待核对'}/>
  <p>{item.ownership==='managed'?'受管规则':item.deleted_at?'已删除的历史版本':'历史记录，当前归属未核实'} · {observation.state==='on'?'已启用（记录中的观察）':observation.state==='off'?'已停用（记录中的观察）':'启用状态无法判断'}</p>
  <p>记录中的观察时间：<time aria-label={'记录中的观察时间：'+item.title} dateTime={observation.at}>{observation.at?dateLabel(observation.at):'暂无观察'}</time>{isStale && <strong> · 已过期，暂无法更新核对</strong>}</p>
  {item.deleted_at?<p>删除记录时间：{dateLabel(item.deleted_at)}。此历史记录不能诊断或管理当前实体。</p>:diagnostic?<AutomationDiagnosticEvidence diagnostic={diagnostic}/>:<Alert tone="warning">暂无运行诊断，无法判断。请由管理员运行只读诊断。</Alert>}
  {item.rule && <Disclosure title={item.deleted_at?'删除前记录的规则':'当前受管版本的规则'}><AutomationRuleView rule={item.rule} entities={new Map((item.entities??[]).map(entity=>[entity.entity_id,entity]))}/><p>记录版本：<code>{item.current_version}</code></p></Disclosure>}
  {!item.deleted_at && <ActionGroup><Link className="jx-action-link" to={`/app/automations?id=${encodeURIComponent(item.suggestion_id)}`}>查看来源建议与安装记录</Link>{item.can_diagnose && <Button variant="secondary" disabled={action.pending} aria-label={'只读诊断：'+item.title} onClick={()=>void diagnose()}>运行只读诊断</Button>}{item.ha_url && <a className="jx-action-link" href={item.ha_url} target="_blank" rel="noopener noreferrer">在 Home Assistant 中查看</a>}</ActionGroup>}
  {action.error && <Alert tone="warning" live="assertive">{action.error} 保留上次诊断，请重新核对。</Alert>}
  {manage && item.ownership==='managed' && !item.deleted_at && <Link className="jx-action-link" to={'/app/automations?managed='+encodeURIComponent(item.id)}>管理受管规则</Link>}
 </Stack></Card>
}
export function AutomationDiagnostics({ data,error,loading,reload,manage,attention,archived=false }: {data:AutomationList|null;error:unknown;loading:boolean;reload:()=>void;manage:boolean;attention:boolean;archived?:boolean}) {
 const [days,setDays]=useState(7)
 const [external,setExternal]=useState('')
 const externalRef=useRef(external);externalRef.current=external
 const action=useConsoleAction()
 const [externalResult,setExternalResult]=useState<AutomationDiagnostic|null>(null)
 const denied=error instanceof ApiError && [401,403,409].includes(error.status)
 const projection=useRef(data);projection.current=data
 useEffect(()=>{setExternal('');externalRef.current='';setExternalResult(null)},[data?.sharing_revision,denied])
 const selected=denied ? undefined : data?.external_candidates.find(item=>item.ref.id===external)
 const rows=(denied?[]:data?.automations??[]).filter(item=>archived ? Boolean(item.deleted_at) : !item.deleted_at && (attention ? item.installation_state!=='verified'||!item.diagnostic||item.diagnostic.summary!=='pass' : item.installation_state==='verified'))
 async function diagnoseExternal(){
  if(!selected)return
  const expected=external
  const expectedProjection=data
  const response=await action.run<{diagnostic:AutomationDiagnostic}>('/admin/automation-diagnostics',{ref:selected.ref,window:diagnosticWindow(days)},'POST',()=>externalRef.current===expected && projection.current===expectedProjection)
  if(response)setExternalResult(response.diagnostic)
 }
 return <Stack gap={24}>
  <ActionGroup><Button variant="secondary" onClick={reload} disabled={loading}>刷新诊断列表</Button></ActionGroup>
  {manage && !archived && <Field id="diagnostic-days" label="诊断窗口"><Select id="diagnostic-days" value={days} onChange={event=>setDays(Number(event.target.value))}><option value={1}>最近 1 天</option><option value={7}>最近 7 天</option><option value={14}>最近 14 天</option></Select></Field>}
  {error ? <Alert tone="warning" live="assertive">{denied?'共享范围或会话已变化，已清除先前结果。请刷新重新核对。':'读取失败，保留上次观察并标为已过期。'}{errorText(error)}</Alert> : !data && <LoadingState label="正在读取自动化诊断…"/>}
  {data && !denied && rows.length===0 && <EmptyState icon="workflow" title={archived?'暂无已删除的受管规则':attention?'当前没有可见的待核对项目':'暂无已核实安装的规则'}/>}
  {rows.map(item=><AutomationDiagnosticCard key={`${item.id}:${item.current_version}:${item.observation_version}:${data?.sharing_revision}:${item.deleted_at}`} item={item} stale={Boolean(error)} days={days} reload={reload} manage={manage}/>)}
  {manage && data && !archived && !denied && <Card heading="外部自动化只读诊断" description="仅选择服务端读取到的规则；诊断不会安装或接管。"><Stack gap={16}>
   <Field id="external-automation" label="外部自动化"><Select id="external-automation" value={external} disabled={action.pending||Boolean(error)} onChange={event=>{setExternal(event.target.value);setExternalResult(null);action.clearError()}}><option value="">请选择已读取的外部规则</option>{data?.external_candidates.map(item=><option key={item.ref.id} value={item.ref.id}>{item.title} · {item.ref.id}</option>)}</Select></Field>
   <ActionGroup><Button variant="secondary" disabled={!selected||action.pending||Boolean(error)} onClick={()=>void diagnoseExternal()}>只读诊断所选外部规则</Button>{selected?.ha_url && <a className="jx-action-link" href={selected.ha_url} target="_blank" rel="noopener noreferrer">在 Home Assistant 中查看</a>}</ActionGroup>
   {action.error && <Alert tone="warning" live="assertive">{action.error}</Alert>}{externalResult && <AutomationDiagnosticEvidence diagnostic={externalResult}/>}
  </Stack></Card>}
 </Stack>
}
