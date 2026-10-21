import { Alert, Button, Card, EmptyState, Field, Grid, Icon, Input, ListRow, Metric, PageFrame, PageHeader, Stack, fieldDescriptionIds } from '../ui'
import '../styles/family.css'
import { useEffect, useState, type FormEvent } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import type { AreaList } from '../api'

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
  return <PageFrame><PageHeader title="家庭区域" description="按区域看看家中的设备和实体。这里仅展示状态。"/>
    <form onSubmit={search}><div className="jx-family-search"><Field id="family-search" label="搜索区域、设备或实体" hint="搜索内容最多 128 个字符。" error={validation || undefined}><Input id="family-search" type="search" placeholder="搜索区域、设备名称或实体 ID" value={draft} onChange={(event) => setDraft(event.target.value)} invalid={Boolean(validation)} aria-describedby={fieldDescriptionIds('family-search',true,Boolean(validation))}/></Field><Button type="submit">搜索</Button>{query && <Button variant="ghost" onClick={() => { setDraft(''); setParams({}); setValidation('') }}>清除搜索</Button>}</div></form>{validation && <Alert tone="danger" live="assertive">{validation}</Alert>}
    {!result.data ? result.error ? <FamilyFailure error={result.error} reload={result.reload}/> : <FamilyPending/> : <>
      <FamilyStatus meta={result.data.meta} failed={Boolean(result.error)} reload={result.reload}/>
      <section aria-label="全屋去重统计"><Grid columns={3}><Metric label="设备注册项" value={`${result.data.totals.devices} 个设备注册项`}/><Metric label="实体" value={`${result.data.totals.entities} 个实体`}/><Metric label="独立实体" value={`${result.data.totals.standalone_entities} 个独立实体`}/></Grid><p className="jx-muted">全屋按注册项去重统计，通道不另算设备；设备注册项可能包含软件设备和场景。</p></section>
      {query && <p role="status">匹配 {result.data.areas.length} 个区域</p>}
      {result.data.areas.length === 0 ? <EmptyState title={query ? '没有匹配的区域或设备' : '尚无区域目录'} description={query ? '试试其他名称或实体 ID。' : 'HA 返回目录后将在这里展示。'} icon="family"/> : <Grid columns={3}>{result.data.areas.map((area) => <Card key={area.id}><Link className="jx-family-area-link" to={areaPath(area.id)}><ListRow leading={<Icon name="home" size={24}/>} title={<h2>{area.name}</h2>} trailing={<Icon name="arrow"/>}/><Stack gap={8}><span>{area.device_count} 个设备注册项</span><span>{area.entity_count} 个实体</span><span className="jx-muted">{area.standalone_entity_count} 个独立实体</span></Stack></Link>{query && area.matches.length > 0 && <ul className="jx-plain-list jx-family-matches">{area.matches.map((match) => <li key={match.id}><Link to={devicePath(area.id, match.id)}><ListRow title={match.name} meta={match.kind === 'device' ? '设备' : '独立实体'} trailing={<Icon name="chevron" size={16}/>}/></Link></li>)}</ul>}</Card>)}</Grid>}
    </>}
  </PageFrame>
}
