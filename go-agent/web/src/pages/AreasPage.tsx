import { useEffect, useState, type FormEvent } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import type { AreaList } from '../api'
import { Icon } from '../icons'
import { useFamilyQuery } from '../familyQuery'
import { areaPath, devicePath, FamilyFailure, FamilyPending, FamilyStatus } from '../familyViews'

export default function AreasPage() {
  const [params, setParams] = useSearchParams()
  const query = params.get('q') ?? ''
  const [draft, setDraft] = useState(query)
  useEffect(() => setDraft(query), [query])
  const [validation, setValidation] = useState('')
  const result = useFamilyQuery<AreaList>(`/areas${query ? `?q=${encodeURIComponent(query)}` : ''}`)
  function search(event: FormEvent) {
    event.preventDefault()
    const value = draft.trim()
    if ([...value].length > 128) { setValidation('搜索内容最多 128 个字符。'); return }
    setValidation(''); setParams(value ? { q: value } : {})
  }
  return <main className="content-page family-page"><div className="page-intro"><span className="eyebrow">YOUR HOME, ROOM BY ROOM</span><h1>家庭区域</h1><p>按区域看看家中的设备和实体。这里仅展示状态。</p></div>
    <form className="family-search" onSubmit={search}><label className="visually-hidden" htmlFor="family-search">搜索区域、设备或实体</label><input id="family-search" type="search" placeholder="搜索区域、设备名称或实体 ID" value={draft} onChange={(event) => setDraft(event.target.value)} /><button className="button button-primary">搜索</button>{query && <button type="button" className="text-button" onClick={() => { setDraft(''); setParams({}); setValidation('') }}>清除搜索</button>}</form>{validation && <p role="alert" className="form-error">{validation}</p>}
    {!result.data ? result.error ? <FamilyFailure error={result.error} reload={result.reload}/> : <FamilyPending/> : <>
      <FamilyStatus meta={result.data.meta} failed={Boolean(result.error)} reload={result.reload}/>
      <div className="family-totals" aria-label="全屋去重统计"><strong>{result.data.totals.devices} 个设备注册项</strong><span>{result.data.totals.entities} 个实体</span><span>{result.data.totals.standalone_entities} 个独立实体</span></div><p className="family-count-note">全屋按注册项去重统计，通道不另算设备；设备注册项可能包含软件设备和场景。</p>
      {query && <p className="search-summary" role="status">匹配 {result.data.areas.length} 个区域</p>}
      {result.data.areas.length === 0 ? <div className="empty-card"><Icon name="family" size={28}/><strong>{query ? '没有匹配的区域或设备' : '尚无区域目录'}</strong><p>{query ? '试试其他名称或实体 ID。' : 'HA 返回目录后将在这里展示。'}</p></div> : <div className="areas-grid">{result.data.areas.map((area) => <section className="area-card" key={area.id}><Link className="area-card-link" to={areaPath(area.id)}><span className="area-card-symbol"><Icon name="home" size={24}/></span><h2>{area.name}</h2><div><span>{area.device_count} 个设备注册项</span><span>{area.entity_count} 个实体</span></div><small>{area.standalone_entity_count} 个独立实体</small><Icon name="arrow" size={18}/></Link>{query && area.matches.length > 0 && <div className="area-matches">{area.matches.map((match) => <Link key={match.id} to={devicePath(area.id, match.id)}>{match.name}<span>{match.kind === 'device' ? '设备' : '独立实体'}</span><Icon name="chevron" size={15}/></Link>)}</div>}</section>)}</div>}
    </>}
  </main>
}
