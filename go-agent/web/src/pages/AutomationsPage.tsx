import { ActionGroup, Alert, Breadcrumb, Button, Card, Checkbox, Disclosure, EmptyState, Field, KeyValueList, ListRow, LoadingState, PageFrame, PageHeader, Select, Stack, StatusBadge } from '../ui'
import { AutomationRevisionEditor } from '../AutomationRevisionEditor'
import { AutomationInstallation, installationLabel, installationTone } from '../AutomationInstallation'
import { AutomationDiagnostics } from '../AutomationDiagnostics'
import { AutomationManagement } from '../AutomationManagement'
import { AutomationRuleView, AutomationRevisionDiff } from '../AutomationRuleView'
import { readAutomationEntityDirectory, type AutomationDirectoryEntity } from '../automationEntityDirectory'
import { automationTitle } from '../automationPresentation'
import { PresenceEntityPicker, isPresenceCandidate } from '../PresenceEntityPicker'
import { useEffect, useRef, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { ApiError, requestConsole, errorText, type AutomationList, type InstallOperationView, type SharingView, type SuggestionList, type SuggestionView } from '../api'
import { useAuth } from '../auth'
import { useFamilyQuery } from '../familyQuery'
import { useConsoleAction } from '../consoleActions'
import { dateLabel } from '../familyViews'
import { Icon } from '../ui'

export default function AutomationsPage() {
  const auth = useAuth()
  const manage = auth.identity?.user.role === 'admin' && auth.identity.capabilities.includes('suggestions:manage')
  const [params, setParams] = useSearchParams()
  const id = params.get('id') ?? ''
  const resultId = params.get('result') ?? ''
  const managedId = params.get('managed') ?? ''
  const idRef = useRef(id)
  idRef.current = id
  const suggestions = useFamilyQuery<SuggestionList>('/suggestions',!managedId,true)
  const suggestionsRef = useRef(suggestions.data)
  suggestionsRef.current = suggestions.data
  const automations = useFamilyQuery<AutomationList>('/automations',!managedId,true)
  const automationCounts = automations.error instanceof ApiError && [401,403,409].includes(automations.error.status) ? undefined : automations.data?.counts
  const [section, setSection] = useState<'review'|'shared'|'installed'|'attention'|'archive'>(manage ? 'review' : 'shared')
  const sharing = useFamilyQuery<SharingView>('/admin/sharing', manage && !managedId)
  const action = useConsoleAction()
  const shareAction = useConsoleAction()
  const [overrides, setOverrides] = useState<Record<string, SuggestionView>>({})
  const [binding, setBinding] = useState(false)
  const [revising, setRevising] = useState(false)
  const [entities, setEntities] = useState<AutomationDirectoryEntity[] | null>(null)
  const [entityError, setEntityError] = useState('')
  const [entityId, setEntityId] = useState('')
  const [presenceState, setPresenceState] = useState('')
  const [checking, setChecking] = useState(false)
  const [sharingDraft, setSharingDraft] = useState<SharingView & { original_ids: string[] }>({ revision: 0, suggestion_ids: [], original_ids: [] })
  const selectedShares = sharingDraft.suggestion_ids
  const [shareDirty, setShareDirty] = useState(false)
  const dirtyRef = useRef(false)
  dirtyRef.current = shareDirty
  const local = useRef(new AbortController())
  const entityRead = useRef<AbortController | null>(null)
  useEffect(() => { local.current = new AbortController(); const active = local.current; return () => active.abort() }, [])
  useEffect(() => { if (sharing.data && !dirtyRef.current) setSharingDraft({ ...sharing.data, original_ids: [...sharing.data.suggestion_ids] }) }, [sharing.data])
  useEffect(() => {
    if (suggestions.invalidated) { setOverrides({}); return }
    if (!suggestions.data) return
    setOverrides({})
  }, [suggestions.data,suggestions.invalidated])
  useEffect(() => {
    entityRead.current?.abort()
    setRevising(false); setBinding(false); setEntities(null); setEntityId(''); setPresenceState(''); setEntityError(''); action.clearError()
    return () => entityRead.current?.abort()
  }, [id])
  const suggestionsDenied = suggestions.invalidated || suggestions.error instanceof ApiError && [401,403,409].includes(suggestions.error.status)
  const all = (suggestionsDenied ? [] : suggestions.data?.suggestions ?? []).map((item) => overrides[item.id] ?? item)
  if (!suggestionsDenied) for (const item of Object.values(overrides)) if (!all.some((existing) => existing.id === item.id)) all.push(item)
  const displayed = all.map(item => ({ ...item, title: automationTitle(item) }))
  const reviewItems = displayed.filter(item => item.status === 'pending' && !item.installation)
  const archivedItems = displayed.filter(item => item.status === 'ignored' || item.status === 'superseded')
  const listItems = section === 'archive' ? archivedItems : reviewItems
  const sharedResults = suggestionsDenied ? [] : suggestions.data?.shared_results ?? []
  const shareable = sharedResults
  const selectedResult = sharedResults.find(item=>item.id===resultId)
  const unverifiableShares = [...new Set([...sharingDraft.original_ids, ...selectedShares])].filter((sharedId) => !shareable.some((item) => item.id === sharedId))
  const selected = displayed.find((item) => item.id === id)
  const presenceEntity = entities?.find(entity => entity.entity_id === entityId && isPresenceCandidate(entity))
  const validPresenceState = Boolean(presenceEntity && (presenceState === 'on' || presenceEntity.domain === 'group' && presenceState === 'home'))
  async function readEntities() {
    entityRead.current?.abort()
    const controller = new AbortController()
    entityRead.current = controller
    setBinding(true); setEntityError(''); setEntities(null); setEntityId(''); setPresenceState('')
    const expected = auth.epoch
    const expectedId = id
    const signal = AbortSignal.any([auth.signal, local.current.signal, controller.signal])
    try {
      const directory = await readAutomationEntityDirectory(signal)
      if (signal.aborted || !auth.isCurrent(expected) || idRef.current !== expectedId) return
      setEntities([...directory.values()])
    } catch (cause) { if (!signal.aborted && auth.isCurrent(expected) && idRef.current === expectedId) { if (cause instanceof ApiError && cause.status === 401) auth.expire(); else setEntityError(errorText(cause)) } }
  }
  async function bind() {
    if (!selected?.can_bind || !presenceEntity || !validPresenceState || entityError) return
    const result = await action.run<{ suggestion: SuggestionView }>(`/admin/suggestions/${encodeURIComponent(selected.id)}/bindings`, { presence: { entity_id: entityId, state: presenceState } }, 'POST', () => idRef.current === selected.id && suggestionsRef.current === suggestions.data)
    if (!result) return
    setOverrides((old) => ({ ...old, [selected.id]: { ...selected, status: 'superseded', can_bind: false, can_revise: false, can_confirm: false, can_ignore: false, superseded_by: result.suggestion.id }, [result.suggestion.id]: result.suggestion }))
    setParams({ id: result.suggestion.id }); setBinding(false); suggestions.reload()
  }
  async function decide(kind: 'confirm' | 'ignore') {
    if (!selected) return
    const result = await action.run<{ suggestion: SuggestionView }>(`/admin/suggestions/${encodeURIComponent(selected.id)}/${kind}`, {}, 'POST', () => idRef.current === selected.id && suggestionsRef.current === suggestions.data)
    if (result) { setOverrides((old) => ({ ...old, [result.suggestion.id]: result.suggestion })); suggestions.reload() }
  }
  async function reconcile() {
    if (!selected?.installation?.can_reconcile) return
    const result = await action.run<{ operation: InstallOperationView }>(`/admin/install-operations/${encodeURIComponent(selected.installation.id)}/reconcile`, {}, 'POST', () => idRef.current === selected.id && suggestionsRef.current === suggestions.data)
    if (result) {
      const op = result.operation
      setOverrides(old => ({ ...old, [selected.id]: { ...selected, status: op.status, installation: op, installation_state: op.legacy_unverified ? 'legacy_unverified' : op.status === 'confirmed' ? 'verified' : 'unknown', can_confirm: false, can_bind: false, can_revise: false, can_ignore: false } }))
      suggestions.reload()
    }
  }
  async function checkLatest() {
    if (checking || !selected) return
    setChecking(true); setEntityError('')
    const expected = auth.epoch
    const signal = AbortSignal.any([auth.signal, local.current.signal])
    try {
      const latest = await requestConsole<SuggestionList>('/suggestions', { signal })
      if (signal.aborted || !auth.isCurrent(expected) || idRef.current !== selected.id || suggestionsRef.current !== suggestions.data) return
      const found = latest.suggestions.find((item) => item.id === selected.id)
      if (found) { action.clearError(); setOverrides((old) => ({ ...old, [found.id]: found }));  }
      else { setEntityError('未找到当前版本，请返回列表重新选择。') }
    } catch (cause) { if (!signal.aborted && auth.isCurrent(expected)) { if (cause instanceof ApiError && cause.status === 401) auth.expire(); else setEntityError(errorText(cause)) } }
    finally { if (!signal.aborted && auth.isCurrent(expected)) setChecking(false) }
  }
  async function saveSharing() {
    if (!sharing.data) return
    const result = await shareAction.run<SharingView>('/admin/sharing', { revision: sharingDraft.revision, suggestion_ids: selectedShares }, 'PUT')
    if (result) { setSharingDraft({ ...result, original_ids: [...result.suggestion_ids] }); setShareDirty(false); sharing.reload(); suggestions.reload() }
  }
  if (managedId) return <PageFrame className="automations-page"><PageHeader title="受管自动化" description="审阅当前规则，再独立确认每一次管理操作。"/><AutomationManagement id={managedId} onChanged={() => { suggestions.reload(); automations.reload() }}/></PageFrame>
  return <PageFrame className="automations-page">
    <PageHeader title={manage ? '自动化建议与结果' : '自动化结果'} description={manage ? '先审阅结构，再明确确认。配置保存、运行加载和本地归档分别核实；安装记录不代表持续启用或设备动作成功。' : '这里只展示管理员明确共享的已确认结果；不提供控制或修改。'}/>
    {!id && !resultId && <><nav aria-label="自动化分类"><ActionGroup>{manage && <Button variant="secondary" aria-pressed={section==='review'} onClick={()=>setSection('review')}>待审阅（{reviewItems.length}）</Button>}<Button variant="secondary" aria-pressed={section==='shared'} onClick={()=>setSection('shared')}>{manage?'历史确认结果':'已共享结果'}（{sharedResults.length}）</Button><Button variant="secondary" aria-pressed={section==='installed'} onClick={()=>setSection('installed')}>已安装（{automationCounts?.installed ?? '…'}）</Button><Button variant="secondary" aria-pressed={section==='attention'} onClick={()=>setSection('attention')}>需关注（{automationCounts?.attention ?? '…'}）</Button>{manage && <Button variant="secondary" aria-pressed={section==='archive'} onClick={()=>setSection('archive')}>归档（{archivedItems.length + (automationCounts?.archived ?? 0)}）</Button>}</ActionGroup></nav>{(section==='installed'||section==='attention'||section==='archive') && <AutomationDiagnostics data={automations.data} error={automations.error} loading={automations.loading} reload={automations.reload} manage={manage} attention={section==='attention'} archived={section==='archive'}/>}</>}
    {!id && (resultId || section==='shared') && <Stack gap={24}>
      {resultId && <Breadcrumb><Link to="/app/automations" onClick={()=>setSection('shared')}>返回共享结果</Link></Breadcrumb>}
      {!suggestions.data ? suggestions.error ? <Alert tone="danger" live="assertive" actions={<Button variant="secondary" onClick={suggestions.reload}>重新读取结果</Button>}>{errorText(suggestions.error)}</Alert> : <LoadingState label="正在读取共享结果…"/> : <>
        {suggestions.error && <Alert tone="warning" live="assertive">{errorText(suggestions.error)} 共享结果可能已过期，请重新读取。</Alert>}
        {resultId ? !selectedResult ? <Alert tone="warning">未找到这个共享结果或没有读取权限。</Alert> : <Card heading={selectedResult.title}><Stack gap={16}>
          <p>管理员共享的历史确认结果；不代表当前配置、启用或执行状态。</p>
          <StatusBadge tone="neutral" label={sharedResultLabel(selectedResult.history_status)}/>
          {selectedResult.deleted_at && <p>删除记录时间：{dateLabel(selectedResult.deleted_at)}</p>}
          <KeyValueList items={[{id:'created',label:'原始结果产生时间',value:dateLabel(selectedResult.created_at)},{id:'zone',label:'时区',value:selectedResult.time_zone||'暂无记录'}]}/>
          <AutomationRuleView rule={selectedResult.rule} entities={new Map(selectedResult.entities.map(entity=>[entity.entity_id,entity]))}/>
          <SuggestionEvidence item={selectedResult}/>
        </Stack></Card> : sharedResults.length===0 ? <EmptyState icon="workflow" title="暂无可读取的共享历史结果"/> : <ul className="jx-plain-list">{sharedResults.map(item=><li key={item.id}><Link className="jx-suggestion-link" to={'/app/automations?result='+encodeURIComponent(item.id)}><ListRow leading={<Icon name="workflow"/>} title={<h2>{item.title}</h2>} meta={<StatusBadge tone="neutral" label={sharedResultLabel(item.history_status)}/>} trailing={<Icon name="arrow"/>}/></Link></li>)}</ul>}
      </>}
    </Stack>}
    {!resultId && (id || section==='review' || section==='archive') && <>
    {!suggestions.data ? suggestions.error ? <Alert tone="danger" live="assertive" actions={<Button variant="secondary" onClick={suggestions.reload}>重新读取结果</Button>}>{errorText(suggestions.error)}</Alert> : <LoadingState label="正在读取自动化结果…"/> : <>
      {suggestions.error && <Alert tone="warning" live="assertive">{errorText(suggestions.error)} 当前结果可能已过期，请重新读取。</Alert>}
      {id ? <><Breadcrumb><Link to="/app/automations"><Icon name="back"/>返回自动化结果</Link></Breadcrumb>{!selected ? <Alert tone="danger" live="assertive">未找到这个建议或没有读取权限。</Alert> : <Card heading={selected.title}>
        <Stack gap={24}>
          <StatusBadge tone={installationTone(selected)} label={installationLabel(selected)}/>
          {manage && selected.installation && <AutomationInstallation operation={selected.installation}/>}
          <KeyValueList items={[{ id: 'zone', label: '时区', value: selected.time_zone || '暂无记录' }, { id: 'created', label: '产生时间', value: dateLabel(selected.created_at) }]}/>
          <SuggestionEvidence item={selected}/>
          {manage && <Disclosure title="版本与来源"><KeyValueList items={[{ id: 'id', label: '新版本', value: selected.id }, { id: 'source', label: '来源版本', value: selected.source_suggestion_id || '暂无记录' }]}/>{selected.source_suggestion_id && <Link className="jx-action-link" to={'/app/automations?id=' + encodeURIComponent(selected.source_suggestion_id)}>查看原版本及证据</Link>}{selected.superseded_by && <Link className="jx-action-link" to={`/app/automations?id=${encodeURIComponent(selected.superseded_by)}`}>查看替代版本</Link>}</Disclosure>}
          {manage && selected.revision_diff?.length ? <AutomationRevisionDiff changes={selected.revision_diff} entities={new Map([...all.flatMap(item => item.entities ?? []), ...(entities ?? [])].map(entity => [entity.entity_id, entity]))}/> : null}
          <AutomationRuleView rule={selected.rule} entities={new Map((entities ?? selected.entities ?? []).map((entity) => [entity.entity_id, entity]))}/>
          {selected.unsupported_code && <Alert tone="warning">{selected.unsupported_code === 'missing_bindings' ? '尚未补齐有人在家条件，请明确选择后再审阅规则。' : '当前规则暂不可执行，请核对规则内容与关联设备。'}</Alert>}
          {manage && <>
            <ActionGroup>
              <Button variant="secondary" onClick={suggestions.reload} disabled={action.pending || suggestions.loading}>刷新规则与名称</Button>
              {!selected.installation && selected.can_revise && selected.intent && !revising && <Button variant="secondary" onClick={() => { setBinding(false); setRevising(true) }} disabled={action.pending}>修订建议</Button>}
              {!selected.installation && selected.can_bind && !revising && <Button variant="secondary" onClick={() => void readEntities()} disabled={action.pending}>补齐有人在家条件</Button>}
              {!selected.installation && !revising && selected.can_confirm && selected.status !== 'applying' && selected.status !== 'failed' && <Button disabled={action.pending || !selected.rule || Boolean(action.error)} onClick={() => void decide('confirm')}>确认创建自动化</Button>}
              {selected.installation?.can_reconcile && <Button variant="secondary" disabled={action.pending} onClick={() => void reconcile()}>只读核对安装状态</Button>}
              {!selected.installation && !revising && selected.can_ignore && <Button variant="secondary" disabled={action.pending || Boolean(action.error)} onClick={() => void decide('ignore')}>忽略未执行建议</Button>}
            </ActionGroup>
            <p className="jx-muted">忽略不等于关闭已安装的自动化。页面不会自动重试结果未知的安装。</p>
            {revising && !selected.installation && <AutomationRevisionEditor key={selected.id} suggestion={selected} onCancel={() => { setRevising(false); suggestions.reload() }} onSaved={next => { setOverrides(old => ({ ...old, [selected.id]: { ...selected, status: 'superseded', can_bind: false, can_revise: false, can_confirm: false, can_ignore: false, superseded_by: next.id }, [next.id]: next })); setRevising(false); setParams({ id: next.id }); suggestions.reload() }}/>}
            {binding && !selected.installation && <Card heading="明确绑定家庭占用条件"><p>请自行选择真正代表全家是否有人在家的聚合实体；实体名称不构成判断依据。</p><form onSubmit={(event) => { event.preventDefault(); void bind() }}><Stack gap={16}>
              {entityError ? <Alert tone="danger" live="assertive" actions={<Button variant="secondary" onClick={() => void readEntities()}>重新读取实体</Button>}>{entityError}</Alert> : entities === null ? <LoadingState label="正在读取可选择的安全实体…"/> : <>
                <PresenceEntityPicker entities={entities} value={entityId} onChange={value => { setEntityId(value); setPresenceState('') }} disabled={action.pending}/>
                <Field id="presence-state" label="有人在家时的状态" hint="请核实这个状态确实代表全家有人在家，房间归属与名称不能证明这一点。" required><Select id="presence-state" aria-describedby="presence-state-hint" value={presenceState} onChange={(event) => setPresenceState(event.target.value)} required disabled={action.pending || !presenceEntity}><option value="">请选择已核实的状态</option><option value="on">开启</option>{presenceEntity?.domain === 'group' && <option value="home">在家</option>}</Select></Field>
                {presenceEntity && <Disclosure title="所选实体的技术详情"><KeyValueList items={[{ id: 'entity', label: '实体标识', value: presenceEntity.entity_id }, { id: 'state', label: '当前原始状态', value: presenceEntity.state }, { id: 'binding-state', label: '选定原始状态', value: presenceState || '尚未选择' }]}/></Disclosure>}
                <Button type="submit" disabled={action.pending || !validPresenceState}>保存绑定并查看新版本</Button>
              </>}
            </Stack></form></Card>}
            {action.error && <><Alert tone="danger" live="assertive">{action.error}</Alert>{<Button variant="secondary" disabled={checking} onClick={() => void checkLatest()}>核对最新建议状态</Button>}</>}
            {entityError && !binding && <Alert tone="warning" live="assertive">{entityError}</Alert>}
          </>}
        </Stack>
      </Card>}</> : listItems.length === 0 ? <EmptyState icon="workflow" title={section==='archive'?'暂无归档建议':'暂无待审阅建议'}/> : <ul className="jx-plain-list">{listItems.map((item) => <li key={item.id}><Link className="jx-suggestion-link" to={`/app/automations?id=${encodeURIComponent(item.id)}`}><ListRow leading={<Icon name="workflow"/>} title={<h2>{item.title}</h2>} meta={<StatusBadge tone={installationTone(item)} label={installationLabel(item)}/>} trailing={<Icon name="arrow"/>}/></Link></li>)}</ul>}
      {manage && !id && section==='review' && <Card heading="向家庭成员共享确认结果" description="默认不共享。选择的是原始确认结果；后续更新不会扩大其共享内容，本操作不安装或停用规则。"><Stack gap={16}>
        {sharing.error ? <Alert tone="danger" live="assertive">{errorText(sharing.error)}</Alert> : !sharing.data ? <LoadingState label="正在读取共享设置…"/> : <>
          <fieldset disabled={shareAction.pending}><legend>明确选择要共享的结果</legend><Stack gap={16}>
            {shareable.map((item) => <Stack key={item.id} gap={8}><Checkbox label={item.title} aria-label={'共享：' + item.title} checked={selectedShares.includes(item.id)} onChange={(event) => { setShareDirty(true); setSharingDraft((old) => ({ ...old, suggestion_ids: event.target.checked ? [...old.suggestion_ids, item.id] : old.suggestion_ids.filter((value) => value !== item.id) })) }}/><Link to={'/app/automations?result='+encodeURIComponent(item.id)}>{'审阅将共享的原始结果：'+item.title}</Link></Stack>) }
            {unverifiableShares.map((sharedId) => <Card key={sharedId} tone="warning" heading="旧授权无法核对"><code>{sharedId}</code><p>这里只能撤销旧授权；保存后生效，不能重新授予无法核对的结果。</p><Button variant="secondary" aria-label={'撤销旧授权：' + sharedId} disabled={!selectedShares.includes(sharedId)} onClick={() => { setShareDirty(true); setSharingDraft((old) => ({ ...old, suggestion_ids: old.suggestion_ids.filter((value) => value !== sharedId) })) }}>{selectedShares.includes(sharedId) ? '撤销旧授权' : '已从本次选择撤销'}</Button></Card>)}
          </Stack></fieldset>
          <ActionGroup><Button disabled={shareAction.pending || !shareDirty} onClick={() => void saveSharing()}>保存结果共享</Button><Button variant="secondary" disabled={shareAction.pending} onClick={() => { setShareDirty(false); sharing.reload(); shareAction.clearError() }}>加载最新共享设置</Button></ActionGroup>
        </>}
        {shareAction.error && <Alert tone="danger" live="assertive">{shareAction.error} 请加载最新共享设置后重新选择。</Alert>}
      </Stack></Card>}
    </>}
    </>}
  </PageFrame>

}

function sharedResultLabel(status:string) { return status==='deleted'?'已删除，保留共享历史':status==='updated'?'当前规则已更新；这里只展示原始结果':status==='legacy_unverified'?'历史确认结果，安装归属与加载未核实':'原始确认结果，当前状态未核对' }
function SuggestionEvidence({ item }: { item: Pick<SuggestionView,'evidence'> & {confidence?:number} }) {
 const e = item.evidence
 const denominator = e.denominator
 const unit = e.denominator_type === 'requested_days' ? '请求天数' : e.denominator_type === 'source_transitions' ? '源状态变化次数' : e.denominator_type === 'numeric_samples' ? '数值样本' : ''
 const numerator = e.denominator_type === 'requested_days' ? e.hit_days : e.denominator_type === 'source_transitions' ? e.sample_size : null
 const validRatio = numerator != null && denominator != null && denominator > 0 && numerator >= 0 && numerator <= denominator
 return <Stack gap={16}><KeyValueList items={[
  { id: 'sample', label: '样本数', value: e.sample_size ?? '尚无法确认' },
  { id: 'hit', label: '命中日期', value: e.hit_days == null ? '尚无法确认' : e.hit_days + ' 天' },
  { id: 'eligible', label: '有效观测日期', value: e.eligible_days == null ? '尚无法确认' : e.eligible_days + ' 天' },
  { id: 'ratio', label: e.denominator_type === 'numeric_samples' ? '算法分数' : '历史命中率', value: validRatio ? Math.round(numerator! / denominator! * 100) + '%' : e.denominator_type === 'numeric_samples' && item.confidence!=null && Number.isFinite(item.confidence) ? item.confidence.toFixed(2) : '尚无法确认' },
  { id: 'denominator', label: '算法分母', value: validRatio ? numerator + ' / ' + denominator + ' ' + unit : denominator != null && unit ? denominator + ' ' + unit : '尚无法确认' },
 ]}/><p>历史观测描述过去规律，不能保证自动化执行或物理设备动作成功。有效观测日期不替代算法分母。</p>
 <Disclosure title="建议证据详情"><KeyValueList items={[{ id: 'window', label: '观测窗口', value: e.window ? dateLabel(e.window.start) + ' 至 ' + dateLabel(e.window.end) : '尚无法确认' }, { id: 'version', label: '算法版本', value: e.algorithm_version ?? '尚无法确认' }, { id: 'excluded', label: '无效或歧义状态排除数', value: e.excluded_counts?.invalid_or_ambiguous_state ?? '尚无法确认' }]}/>{e.event_refs?.length ? <ul>{e.event_refs.slice(0, 10).map((ref, index) => <li key={index}><code>{ref.entity_id}</code> · {dateLabel(ref.at)}</li>)}</ul> : <p>暂无本机事件引用。</p>}</Disclosure></Stack>
}
