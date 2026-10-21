import { ActionGroup, Alert, Card, Disclosure, EmptyState, Grid, PageFrame, PageHeader, Section, Stack, StatusBadge } from '../ui'
import '../styles/family.css'
import { useParams } from 'react-router-dom'
import type { AreaDeviceDetail } from '../api'
import { useFamilyQuery } from '../familyQuery'
import { EntityCard, FamilyBreadcrumb, FamilyFailure, FamilyPending, FamilyStatus, membershipLabel } from '../familyViews'

export default function DevicePage() {
  const { areaID = '', deviceID = '' } = useParams()
  const result = useFamilyQuery<AreaDeviceDetail>(`/areas/${encodeURIComponent(areaID)}/devices/${encodeURIComponent(deviceID)}`)
  const device = result.data?.device
  const main = device?.entities.filter((entity) => entity.category === null) ?? []
  const extras = device?.entities.filter((entity) => entity.category !== null) ?? []
  return <PageFrame><FamilyBreadcrumb area={result.data?.area} device={device?.name}/><PageHeader title={device?.name ?? '设备详情'} description={`${device?.kind === 'entity' ? '独立实体' : '设备注册项'} · 仅展示当前区域下的实体`}/>
    {!result.data || !device ? result.error ? <FamilyFailure error={result.error} reload={result.reload}/> : <FamilyPending/> : <>
      <FamilyStatus meta={result.data.meta} failed={Boolean(result.error)} reload={result.reload}/>
      <div className="jx-family-device-context"><Card><Stack><ActionGroup><StatusBadge tone="neutral" label={membershipLabel(device)}/><span>{device.entity_count} 个实体</span></ActionGroup>{device.kind === 'device' && <Alert tone="warning">控制器归属；负载地点未验证。区域关系不代表已确认各通道连接的房间或灯具。</Alert>}{device.membership === 'entity' && device.direct_area_id !== null && device.direct_area_id !== result.data.area.id && <p>设备直接归属另一区域，此处仅显示覆盖到当前区域的实体。</p>}</Stack></Card></div>
      {device.entities.length === 0 ? <EmptyState title="此区域没有该设备的实体，实体可能已覆盖到其他区域。"/> : <>
        {main.length > 0 && <Section title="实体状态"><Grid columns={2}>{main.map((entity) => <EntityCard key={entity.entity_id} entity={entity}/>)}</Grid></Section>}
        {extras.length > 0 && <Disclosure title={`配置与诊断实体（${extras.length}）`}><Grid columns={2}>{extras.map((entity) => <EntityCard key={entity.entity_id} entity={entity}/>)}</Grid></Disclosure>}
      </>}
    </>}
  </PageFrame>
}
