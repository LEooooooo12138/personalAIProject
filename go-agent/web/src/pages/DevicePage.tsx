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
  return <main className="content-page family-page"><FamilyBreadcrumb area={result.data?.area} device={device?.name}/><div className="page-intro"><span className="eyebrow">DEVICE DETAILS</span><h1>{device?.name ?? '设备详情'}</h1><p>{device?.kind === 'entity' ? '独立实体' : '设备注册项'} · 仅展示当前区域下的实体</p></div>
    {!result.data || !device ? result.error ? <FamilyFailure error={result.error} reload={result.reload}/> : <FamilyPending/> : <>
      <FamilyStatus meta={result.data.meta} failed={Boolean(result.error)} reload={result.reload}/>
      <section className="device-context panel"><div><span className="membership-tag">{membershipLabel(device)}</span><span>{device.entity_count} 个实体</span></div>{device.kind === 'device' && <p className="load-location-note">控制器归属；负载地点未验证。区域关系不代表已确认各通道连接的房间或灯具。</p>}{device.membership === 'entity' && device.direct_area_id !== null && device.direct_area_id !== result.data.area.id && <p>设备直接归属另一区域，此处仅显示覆盖到当前区域的实体。</p>}</section>
      {device.entities.length === 0 ? <div className="empty-card"><strong>此区域没有该设备的实体，实体可能已覆盖到其他区域。</strong></div> : <>
        {main.length > 0 && <section className="device-group"><h2>实体状态</h2><div className="entities-grid">{main.map((entity) => <EntityCard key={entity.entity_id} entity={entity}/>)}</div></section>}
        {extras.length > 0 && <details className="configuration-entities"><summary>配置与诊断实体（{extras.length}）</summary><div className="entities-grid">{extras.map((entity) => <EntityCard key={entity.entity_id} entity={entity}/>)}</div></details>}
      </>}
    </>}
  </main>
}
