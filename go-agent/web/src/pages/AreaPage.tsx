import { EmptyState, Field, Grid, PageFrame, PageHeader, Section, Select } from '../ui'
import '../styles/family.css'
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
  return <PageFrame><FamilyBreadcrumb area={result.data?.area}/><PageHeader title={result.data?.area.name ?? '区域设备'} description="设备按注册项归组，多个通道仍属于同一设备。"/>
    {!result.data ? result.error ? <FamilyFailure error={result.error} reload={result.reload}/> : <FamilyPending/> : <>
      <FamilyStatus meta={result.data.meta} failed={Boolean(result.error)} reload={result.reload}/>
      {result.data.devices.length === 0 ? <EmptyState title="这个区域还没有设备或独立实体。" description="返回全部区域查看其他房间。"/> : <>
        <div className="jx-family-filter"><Field id="domain-filter" label="设备类型筛选"><Select id="domain-filter" value={domain} onChange={(event) => setDomain(event.target.value)}><option value="">全部类型</option>{domains.map((value) => <option key={value} value={value}>{domainLabel(value)}</option>)}</Select></Field><span>{visible.length} 个目录项</span></div>
        {devices.length > 0 && <Section title="设备注册项"><Grid columns={2}>{devices.map((device) => <DeviceCard key={device.id} device={device} areaId={areaID}/>)}</Grid></Section>}
        {standalone.length > 0 && <Section title="独立实体"><Grid columns={2}>{standalone.map((device) => <DeviceCard key={device.id} device={device} areaId={areaID}/>)}</Grid></Section>}
        {visible.length === 0 && <EmptyState title="此区域没有该类型的目录项。"/>}
      </>}
    </>}
  </PageFrame>
}
