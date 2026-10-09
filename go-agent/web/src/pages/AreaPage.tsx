import { useEffect, useState } from 'react'
import { useParams } from 'react-router-dom'
import type { AreaDevices } from '../api'
import { useFamilyQuery } from '../familyQuery'
import { domainLabel, DeviceCard, FamilyBreadcrumb, FamilyFailure, FamilyPending, FamilyStatus } from '../familyViews'

export default function AreaPage() {
  const { areaID = '' } = useParams()
  const [domain, setDomain] = useState('')
  useEffect(() => setDomain(''), [areaID])
  const result = useFamilyQuery<AreaDevices>(`/areas/${encodeURIComponent(areaID)}/devices`)
  const domains = [...new Set(result.data?.devices.flatMap((device) => device.domains) ?? [])].sort()
  const visible = result.data?.devices.filter((device) => !domain || device.domains.includes(domain)) ?? []
  const devices = visible.filter((device) => device.kind === 'device')
  const standalone = visible.filter((device) => device.kind === 'entity')
  return <main className="content-page family-page"><FamilyBreadcrumb area={result.data?.area}/><div className="page-intro"><span className="eyebrow">A CLOSER LOOK</span><h1>{result.data?.area.name ?? '区域设备'}</h1><p>设备按注册项归组，多个通道仍属于同一设备。</p></div>
    {!result.data ? result.error ? <FamilyFailure error={result.error} reload={result.reload}/> : <FamilyPending/> : <>
      <FamilyStatus meta={result.data.meta} failed={Boolean(result.error)} reload={result.reload}/>
      {result.data.devices.length === 0 ? <div className="empty-card"><strong>这个区域还没有设备或独立实体。</strong><p>返回全部区域查看其他房间。</p></div> : <>
        <div className="family-filter"><label htmlFor="domain-filter">设备类型筛选</label><select id="domain-filter" value={domain} onChange={(event) => setDomain(event.target.value)}><option value="">全部类型</option>{domains.map((value) => <option key={value} value={value}>{domainLabel(value)}</option>)}</select><span>{visible.length} 个目录项</span></div>
        {devices.length > 0 && <section className="device-group"><h2>设备注册项</h2><div className="devices-grid">{devices.map((device) => <DeviceCard key={device.id} device={device} areaId={areaID}/>)}</div></section>}
        {standalone.length > 0 && <section className="device-group"><h2>独立实体</h2><div className="devices-grid">{standalone.map((device) => <DeviceCard key={device.id} device={device} areaId={areaID}/>)}</div></section>}
        {visible.length === 0 && <div className="empty-card"><strong>此区域没有该类型的目录项。</strong></div>}
      </>}
    </>}
  </main>
}
