import { useEffect, useRef, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { ApiError, requestConsole, errorText, type AreaDevices, type AreaList, type EntityView, type SharingView, type SuggestionList, type SuggestionRule, type SuggestionView } from '../api'
import { useAuth } from '../auth'
import { useFamilyQuery } from '../familyQuery'
import { useConsoleAction } from '../consoleActions'
import { dateLabel } from '../familyViews'
import { Icon } from '../icons'

const labels: Record<SuggestionView['status'], string> = { pending: '待审核', superseded: '已被新版本替代', applying: '结果待核对', failed: '安装失败', confirmed: '已确认安装并归档', ignored: '已忽略，未执行' }
function RuleView({ rule }: { rule: SuggestionRule | null }) {
  if (!rule) return <p>当前没有完整可执行规则，补齐条件后再审阅。</p>
  return <div className="rule-grid"><section><h3>触发</h3><ul>{rule.triggers.map((item, index) => <li key={index}>{item.platform === 'time' ? `每天 ${item.at ?? ''}` : <>{item.platform} · <code>{item.entity_id}</code> · {item.from ? `${item.from} → ` : ''}{item.to}</>}</li>)}</ul></section><section><h3>条件</h3>{rule.conditions.length ? <ul>{rule.conditions.map((item, index) => <li key={index}>{item.condition === 'sun' ? `太阳条件：${item.after === 'sunset' ? '日落后' : item.after}` : <>{item.condition} · <code>{item.entity_id}</code> = {item.state}</>}</li>)}</ul> : <p>无额外条件</p>}</section><section><h3>动作</h3><ul>{rule.actions.map((item, index) => <li key={index}><code>{item.service}</code><span>目标：<code>{item.target.entity_id}</code></span></li>)}</ul></section></div>
}

export default function AutomationsPage() {
  const auth = useAuth()
  const manage = auth.identity?.user.role === 'admin' && auth.identity.capabilities.includes('suggestions:manage')
  const [params, setParams] = useSearchParams()
  const id = params.get('id') ?? ''
  const idRef = useRef(id)
  idRef.current = id
  const suggestions = useFamilyQuery<SuggestionList>('/suggestions')
  const sharing = useFamilyQuery<SharingView>('/admin/sharing', manage)
  const action = useConsoleAction()
  const shareAction = useConsoleAction()
  const [overrides, setOverrides] = useState<Record<string, SuggestionView>>({})
  const [binding, setBinding] = useState(false)
  const [entities, setEntities] = useState<EntityView[] | null>(null)
  const [entityError, setEntityError] = useState('')
  const [entityId, setEntityId] = useState('')
  const [presenceState, setPresenceState] = useState('')
  const [verifiedRetry, setVerifiedRetry] = useState('')
  const [checking, setChecking] = useState(false)
  const [sharingDraft, setSharingDraft] = useState<SharingView & { original_ids: string[] }>({ revision: 0, suggestion_ids: [], original_ids: [] })
  const selectedShares = sharingDraft.suggestion_ids
  const [shareDirty, setShareDirty] = useState(false)
  const dirtyRef = useRef(false)
  dirtyRef.current = shareDirty
  const local = useRef(new AbortController())
  useEffect(() => { local.current = new AbortController(); const active = local.current; return () => active.abort() }, [])
  useEffect(() => { if (sharing.data && !dirtyRef.current) setSharingDraft({ ...sharing.data, original_ids: [...sharing.data.suggestion_ids] }) }, [sharing.data])
  useEffect(() => {
    if (!suggestions.data) return
    setOverrides((old) => { const next = { ...old }; for (const item of suggestions.data!.suggestions) delete next[item.id]; return next })
  }, [suggestions.data])
  useEffect(() => { setBinding(false); setVerifiedRetry(''); setEntityError(''); action.clearError() }, [id])
  const all = (suggestions.data?.suggestions ?? []).map((item) => overrides[item.id] ?? item)
  for (const item of Object.values(overrides)) if (!all.some((existing) => existing.id === item.id)) all.push(item)
  const shareable = all.filter((item) => item.status === 'confirmed' && item.rule && !item.unsupported_code)
  const unverifiableShares = [...new Set([...sharingDraft.original_ids, ...selectedShares])].filter((sharedId) => !shareable.some((item) => item.id === sharedId))
  const selected = all.find((item) => item.id === id)
  async function readEntities() {
    setBinding(true); setEntityError(''); setEntities(null)
    const expected = auth.epoch
    const signal = AbortSignal.any([auth.signal, local.current.signal])
    try {
      const directory = await requestConsole<AreaList>('/areas', { signal })
      if (directory.meta.freshness !== 'fresh' || directory.meta.connection !== 'connected') throw new ApiError(503, 'ha_unavailable')
      const lists = await Promise.all(directory.areas.map((area) => requestConsole<AreaDevices>(`/areas/${encodeURIComponent(area.id)}/devices`, { signal })))
      if (signal.aborted || !auth.isCurrent(expected)) return
      if (lists.some((list) => list.meta.freshness !== 'fresh' || list.meta.connection !== 'connected')) throw new ApiError(503, 'ha_unavailable')
      const unique = new Map<string, EntityView>()
      lists.forEach((list) => list.devices.forEach((device) => device.entities.forEach((entity) => { if (!entity.disabled) unique.set(entity.entity_id, entity) })))
      setEntities([...unique.values()].sort((a, b) => a.entity_id.localeCompare(b.entity_id)))
    } catch (cause) { if (!signal.aborted && auth.isCurrent(expected)) { if (cause instanceof ApiError && cause.status === 401) auth.expire(); else setEntityError(errorText(cause)) } }
  }
  async function bind() {
    if (!selected || !entityId || !presenceState.trim()) return
    const result = await action.run<{ suggestion: SuggestionView }>(`/admin/suggestions/${encodeURIComponent(selected.id)}/bindings`, { presence: { entity_id: entityId, state: presenceState } }, 'POST', () => idRef.current === selected.id)
    if (!result) return
    setOverrides((old) => ({ ...old, [selected.id]: { ...selected, status: 'superseded', can_bind: false, can_confirm: false, can_ignore: false, superseded_by: result.suggestion.id }, [result.suggestion.id]: result.suggestion }))
    setParams({ id: result.suggestion.id }); setBinding(false); suggestions.reload()
  }
  async function decide(kind: 'confirm' | 'ignore') {
    if (!selected) return
    const result = await action.run<{ suggestion: SuggestionView }>(`/admin/suggestions/${encodeURIComponent(selected.id)}/${kind}`, {}, 'POST', () => idRef.current === selected.id)
    if (result) { setOverrides((old) => ({ ...old, [result.suggestion.id]: result.suggestion })); setVerifiedRetry(''); suggestions.reload() }
  }
  async function checkLatest() {
    if (checking || !selected) return
    setChecking(true); setEntityError('')
    const expected = auth.epoch
    const signal = AbortSignal.any([auth.signal, local.current.signal])
    try {
      const latest = await requestConsole<SuggestionList>('/suggestions', { signal })
      if (signal.aborted || !auth.isCurrent(expected) || idRef.current !== selected.id) return
      const found = latest.suggestions.find((item) => item.id === selected.id)
      if (found) { action.clearError(); setOverrides((old) => ({ ...old, [found.id]: found })); setVerifiedRetry(found.id) }
      else { setVerifiedRetry(''); setEntityError('未找到当前版本，请返回列表重新选择。') }
    } catch (cause) { if (!signal.aborted && auth.isCurrent(expected)) { if (cause instanceof ApiError && cause.status === 401) auth.expire(); else setEntityError(errorText(cause)) } }
    finally { if (!signal.aborted && auth.isCurrent(expected)) setChecking(false) }
  }
  async function saveSharing() {
    if (!sharing.data) return
    const result = await shareAction.run<SharingView>('/admin/sharing', { revision: sharingDraft.revision, suggestion_ids: selectedShares }, 'PUT')
    if (result) { setSharingDraft({ ...result, original_ids: [...result.suggestion_ids] }); setShareDirty(false); sharing.reload(); suggestions.reload() }
  }
  return <main className="content-page automations-page"><div className="page-intro"><h1>{manage ? '自动化建议与结果' : '自动化结果'}</h1><p>{manage ? '先审阅结构，再明确确认。确认代表安装并归档，不代表持续监测规则仍启用。' : '这里只展示管理员明确共享的已确认结果；不提供控制或修改。'}</p></div>
    {!suggestions.data ? suggestions.error ? <div className="state-panel danger" role="alert"><p>{errorText(suggestions.error)}</p><button className="button button-secondary" onClick={suggestions.reload}>重新读取结果</button></div> : <p role="status">正在读取自动化结果…</p> : <>
      {suggestions.error && <p role="alert" className="form-error">{errorText(suggestions.error)} 当前结果可能已过期，请重新读取。</p>}
      {id ? <><Link className="return-link" to="/app/automations"><Icon name="back"/>返回自动化结果</Link>{!selected ? <p className="state-panel danger" role="alert">未找到这个建议或没有读取权限。</p> : <section className="panel suggestion-detail"><h2>{selected.title}</h2><p role="status" className={`suggestion-status status-${selected.status}`}>{labels[selected.status]}</p><dl className="suggestion-facts"><div><dt>时区</dt><dd>{selected.time_zone || '暂无记录'}</dd></div><div><dt>产生时间</dt><dd>{dateLabel(selected.created_at)}</dd></div><div><dt>样本数</dt><dd>{selected.evidence.sample_size ?? '尚无法确认'}</dd></div><div><dt>分析天数</dt><dd>{selected.evidence.period_days ?? '尚无法确认'}</dd></div><div><dt>置信度</dt><dd>{Number.isFinite(selected.confidence) ? `${Math.round(selected.confidence * 100)}%` : '尚无法确认'}</dd></div></dl>
        {manage && <div className="version-note"><p>新版本：{selected.id}</p>{selected.source_suggestion_id && <p>来源版本：{selected.source_suggestion_id}</p>}{selected.superseded_by && <Link to={`/app/automations?id=${encodeURIComponent(selected.superseded_by)}`}>查看替代版本</Link>}</div>}
        <RuleView rule={selected.rule}/>{selected.unsupported_code && <p className="state-panel warning">当前规则不可执行（{selected.unsupported_code}），请核对条件。</p>}
        {manage && <><div className="action-row">{selected.can_bind && <button className="button button-secondary" onClick={() => void readEntities()} disabled={action.pending}>补齐有人在家条件</button>}{selected.can_confirm && selected.status !== 'applying' && selected.status !== 'failed' && <button className="button button-primary" disabled={action.pending || !selected.rule || Boolean(action.error)} onClick={() => void decide('confirm')}>确认创建自动化</button>}{(selected.status === 'failed' || selected.status === 'applying') && <><button className="button button-secondary" onClick={() => void checkLatest()} disabled={checking || action.pending}>{checking ? '核对中…' : '核对最新建议状态'}</button><button className="button button-primary" onClick={() => void decide('confirm')} disabled={action.pending || checking || verifiedRetry !== selected.id || !selected.can_confirm || !selected.rule}>明确重试创建自动化</button></>}{selected.can_ignore && <button className="button button-secondary" disabled={action.pending || Boolean(action.error)} onClick={() => void decide('ignore')}>忽略未执行建议</button>}</div><p className="panel-description">忽略不等于关闭已安装的自动化。页面不会自动重试结果未知的安装。</p>
          {binding && <form className="binding-form" onSubmit={(event) => { event.preventDefault(); void bind() }}><h3>明确绑定家庭占用条件</h3><p>请自行选择真正代表全家是否有人在家的聚合实体；实体名称不构成判断依据。</p>{entityError ? <p role="alert" className="form-error">{entityError}</p> : entities === null ? <p role="status">正在读取可选择的安全实体…</p> : <><label htmlFor="presence-entity">代表全家的占用实体</label><select id="presence-entity" value={entityId} onChange={(event) => setEntityId(event.target.value)} required disabled={action.pending}><option value="">请明确选择实体</option>{entities.map((entity) => <option key={entity.entity_id} value={entity.entity_id}>{entity.name} · {entity.entity_id}</option>)}</select><label htmlFor="presence-state">有人在家时的状态</label><input id="presence-state" value={presenceState} onChange={(event) => setPresenceState(event.target.value)} placeholder="例如 on 或 home，请核实实际含义" required disabled={action.pending}/><button className="button button-primary" disabled={action.pending || !entityId || !presenceState.trim()}>保存绑定并查看新版本</button></>}</form>}{action.error && <><p role="alert" className="form-error">{action.error}</p>{selected.status !== 'failed' && selected.status !== 'applying' && <button className="button button-secondary" disabled={checking} onClick={() => void checkLatest()}>核对最新建议状态</button>}</>}{entityError && !binding && <p role="alert" className="form-error">{entityError}</p>}</>}
      </section>}</> : all.length === 0 ? <div className="empty-card"><Icon name="workflow" size={32}/><p>{manage ? '暂无自动化建议或结果' : '暂无已共享结果'}</p></div> : <ul className="suggestion-list">{all.map((item) => <li key={item.id}><Link to={`/app/automations?id=${encodeURIComponent(item.id)}`}><Icon name="workflow"/><div><h2>{item.title}</h2><p>{labels[item.status]}</p></div><Icon name="arrow"/></Link></li>)}</ul>}
      {manage && !id && <section className="panel sharing-panel"><h2>向家庭成员共享确认结果</h2><p>默认不共享。只共享结构有效且关联实体可验证的确认结果；本操作不安装或停用规则。</p>{sharing.error ? <p role="alert" className="form-error">{errorText(sharing.error)}</p> : !sharing.data ? <p role="status">正在读取共享设置…</p> : <><fieldset disabled={shareAction.pending}><legend>明确选择要共享的结果</legend>{shareable.map((item) => <label className="checkbox-row" key={item.id}><input type="checkbox" aria-label={`共享：${item.title}`} checked={selectedShares.includes(item.id)} onChange={(event) => { setShareDirty(true); setSharingDraft((old) => ({ ...old, suggestion_ids: event.target.checked ? [...old.suggestion_ids, item.id] : old.suggestion_ids.filter((value) => value !== item.id) })) }}/>{item.title}</label>)}{unverifiableShares.map((sharedId) => <div className="legacy-sharing-row" key={sharedId}><p>旧授权无法核对：<code>{sharedId}</code></p><button type="button" className="button button-secondary" aria-label={'撤销旧授权：' + sharedId} disabled={!selectedShares.includes(sharedId)} onClick={() => { setShareDirty(true); setSharingDraft((old) => ({ ...old, suggestion_ids: old.suggestion_ids.filter((value) => value !== sharedId) })) }}>{selectedShares.includes(sharedId) ? '撤销旧授权' : '已从本次选择撤销'}</button><p className="panel-description">这里只能撤销旧授权；保存后生效，不能重新授予无法核对的结果。</p></div>)}</fieldset><button className="button button-primary" disabled={shareAction.pending || !shareDirty} onClick={() => void saveSharing()}>保存结果共享</button><button className="button button-secondary" disabled={shareAction.pending} onClick={() => { setShareDirty(false); sharing.reload(); shareAction.clearError() }}>加载最新共享设置</button></>}{shareAction.error && <p role="alert" className="form-error">{shareAction.error} 请加载最新共享设置后重新选择。</p>}</section>}
    </>}
  </main>
}