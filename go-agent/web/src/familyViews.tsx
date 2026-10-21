import { formatEntityState } from './entityState'
import { Link } from 'react-router-dom'
import { ApiError, errorText, type CatalogMeta, type DeviceView, type EntityView } from './api'
import { ActionGroup, Alert, Breadcrumb, Button, Card, Disclosure, Icon, KeyValueList, ListRow, LoadingState, Metric, Stack, StatusBadge } from './ui'
import './styles/family.css'

export function dateLabel(value: string | null): string {
  if (!value) return '暂无记录'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '暂无记录' : new Intl.DateTimeFormat('zh-CN', { month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit', second: '2-digit' }).format(date)
}
export function areaPath(id: string) { return `/app/areas/${encodeURIComponent(id)}` }
export function devicePath(areaId: string, id: string) { return `${areaPath(areaId)}/devices/${encodeURIComponent(id)}` }
export function haStatusText(meta: CatalogMeta, failed = false) {
  const causes: Record<string, string> = { ha_auth_required: '家庭设备服务授权失效，请联系管理员。', ha_forbidden: '家庭设备服务权限不足，请联系管理员。', ha_timeout: '家庭设备服务请求超时，请稍后重新检查。', ha_invalid_response: '家庭设备服务返回异常，请联系管理员。', ha_not_configured: '家庭设备服务尚未配置。', ha_unavailable: 'HA 暂时无法连接' }
  if (meta.error_code && causes[meta.error_code]) return causes[meta.error_code]
  return failed || meta.connection === 'unavailable' ? 'HA 暂时无法连接' : meta.connection === 'connected' ? 'HA 最近检查成功' : 'HA 连接尚未确认'
}
export function domainLabel(domain: string) { const names: Record<string, string> = { light: '灯', switch: '开关', fan: '风扇', binary_sensor: '二值传感器', sensor: '传感器', cover: '窗帘', climate: '温控', scene: '场景', group: '实体分组', person: '人员状态', device_tracker: '设备追踪', input_boolean: '布尔辅助项', automation: '自动化' }; return names[domain] ? `${names[domain]}（${domain}）` : domain }
export function FamilyStatus({ meta, failed = false, reload }: { meta: CatalogMeta; failed?: boolean; reload: () => void }) {
 const stale = failed || meta.freshness === 'stale'
 return <section aria-label="HA 与目录状态"><Alert tone={stale || meta.connection === 'unavailable' ? 'warning' : 'info'} title={haStatusText(meta, failed)} actions={<Button variant="secondary" onClick={reload}>重新检查</Button>}><Stack gap={12}>
  {stale ? <p role="status">正在显示旧数据</p> : meta.freshness === 'unknown' ? <p>尚无完整目录</p> : null}
  <KeyValueList items={[{id:'observed',label:'数据时间',value:<time>{dateLabel(meta.observed_at)}</time>},{id:'attempt',label:'检查时间',value:<time>{dateLabel(meta.last_attempt_at)}</time>},{id:'success',label:'最近成功',value:<time>{dateLabel(meta.last_success_at)}</time>}]}/>
 </Stack></Alert></section>
}
export function FamilyPending() { return <LoadingState label="正在读取家庭目录…"/> }
export function FamilyFailure({ error, reload }: { error: ApiError | null; reload: () => void }) {return <Alert tone="danger" live="assertive" title={error?.status === 404 ? '未找到这个区域或设备。' : '家庭目录暂时不可用。'} actions={<Button variant="secondary" onClick={reload}>重新加载</Button>}>{error ? errorText(error) : '请稍后重新加载。'}</Alert>}
export function FamilyBreadcrumb({ area, device }: { area?: { id: string; name: string }; device?: string }) {return <Breadcrumb label="页面路径"><Link to="/app/areas">全部区域</Link>{area && <><Icon name="chevron" size={16}/>{device ? <Link to={areaPath(area.id)}>{area.name}</Link> : <span aria-current="page">{area.name}</span>}</>}{device && <><Icon name="chevron" size={16}/><span aria-current="page">{device}</span></>}</Breadcrumb>}

export function membershipLabel(device: DeviceView) {
  return device.membership === 'entity' ? '实体覆盖到此区域' : device.membership === 'both' ? '直接归属与实体关系' : '直接归属此区域'
}
export function DeviceCard({ device, areaId }: { device: DeviceView; areaId: string }) {return <Link className="jx-family-device-link" to={devicePath(areaId, device.id)}><ListRow leading={<Icon name={device.kind === 'device' ? 'device' : 'sensor'} size={24}/>} title={<h3>{device.name}</h3>} description={`${device.kind === 'device' ? '设备注册项' : '独立实体'} · ${device.entity_count} 个实体`} meta={<><StatusBadge tone="neutral" label={membershipLabel(device)}/>{device.entity_count === 0 && <p className="jx-muted">此区域无该设备的实体</p>}</>} trailing={<Icon name="chevron"/>}/></Link>}

export function entityStateLabel(entity: EntityView) {
  return formatEntityState(entity)
}
export function EntityCard({ entity }: { entity: EntityView }) {
 const unit = entity.state !== null && entity.state !== 'unavailable' && entity.state !== 'unknown' && entity.unit !== null ? entity.unit : undefined
 return <div className="jx-family-entity"><Card heading={entity.name}><Stack><StatusBadge tone="neutral" label={domainLabel(entity.domain)}/><Metric label="当前状态" value={entityStateLabel(entity)} unit={unit}/><ActionGroup>{entity.disabled && <StatusBadge tone="neutral" label="已禁用"/>}{entity.hidden && <StatusBadge tone="neutral" label="已隐藏"/>}</ActionGroup><KeyValueList items={[{id:'changed',label:'状态变化',value:<time>{dateLabel(entity.last_changed)}</time>},{id:'updated',label:'最近更新',value:<time>{dateLabel(entity.last_updated)}</time>}]}/><Disclosure title="技术详情"><Stack gap={12}><code>{entity.entity_id}</code><p>原始状态：{entity.state ?? 'null'}</p><p>类别：{entity.device_class ?? '暂无类别'}</p></Stack></Disclosure></Stack></Card></div>
}
